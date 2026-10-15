package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	mongoplus "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/generator"
	"github.com/kuangshp/mongo-plus/generator/gozero"
	swaggergen "github.com/kuangshp/mongo-plus/generator/swagger"
	"github.com/kuangshp/mongo-plus/schema"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// run 将命令解析与进程退出分离，便于验证错误参数不会执行数据库操作。
func run(args []string, out, errOut io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" {
		fmt.Fprintln(out, "用法：mongoplus generate|check|schema plan|schema apply --config mongo-plus.yaml")
		fmt.Fprintln(out, "      mongoplus goctl <goctl 参数...> | export-goctl-templates --output <目录>")
		fmt.Fprintln(out, "      mongoplus swagger|swagger-open --config <服务.yaml> [--dir <服务目录>]")
		return nil
	}
	command := args[0]
	args = args[1:]
	if command == "goctl" {
		if len(args) == 0 {
			return fmt.Errorf("goctl 参数不能为空")
		}
		output, err := gozero.Run(context.Background(), gozero.RunOptions{}, args...)
		_, _ = out.Write(output)
		return err
	}
	if command == "swagger" || command == "swagger-open" || command == "export-goctl-templates" {
		flags := flag.NewFlagSet(command, flag.ContinueOnError)
		flags.SetOutput(errOut)
		if command == "export-goctl-templates" {
			output := flags.String("output", "", "模板导出目录")
			if err := flags.Parse(args); err != nil {
				if err == flag.ErrHelp {
					return nil
				}
				return err
			}
			if flags.NArg() != 0 {
				return fmt.Errorf("模板导出命令不接受位置参数")
			}
			return gozero.WriteTemplateHome(*output)
		}
		config := flags.String("config", "", "服务 YAML 配置路径")
		dir := flags.String("dir", ".", "服务目录")
		timeout := flags.Duration("timeout", 30*time.Second, "生成或等待文档的最长时间")
		if err := flags.Parse(args); err != nil {
			if err == flag.ErrHelp {
				return nil
			}
			return err
		}
		if flags.NArg() != 0 || *timeout <= 0 {
			return fmt.Errorf("Swagger 命令不接受位置参数，timeout 必须大于零")
		}
		ctx, cancel := context.WithTimeout(context.Background(), *timeout)
		defer cancel()
		options := swaggergen.Options{ConfigFile: *config, Dir: *dir}
		var output []byte
		var err error
		if command == "swagger-open" {
			output, err = swaggergen.Open(ctx, options)
		} else {
			output, err = swaggergen.Generate(ctx, options)
		}
		_, _ = out.Write(output)
		return err
	}
	if command == "schema" {
		if len(args) == 0 {
			return fmt.Errorf("schema 需要 plan 或 apply")
		}
		command = "schema " + args[0]
		args = args[1:]
	}
	switch command {
	case "generate", "check", "schema plan", "schema apply":
	default:
		return fmt.Errorf("未知命令 %q", command)
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(errOut)
	config := flags.String("config", "mongo-plus.yaml", "项目生成/连接配置")
	timeout := flags.Duration("timeout", 60*time.Second, "数据库同步超时")
	if err := flags.Parse(args); err != nil {
		if err == flag.ErrHelp {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("不支持的位置参数: %v", flags.Args())
	}
	if *timeout <= 0 {
		return fmt.Errorf("timeout 必须大于零")
	}
	c, err := schema.LoadConfig(*config)
	if err != nil {
		return err
	}
	if command == "generate" {
		files, err := generator.Generate(c)
		if err != nil {
			return err
		}
		for _, file := range files {
			fmt.Fprintln(out, "已生成:", file)
		}
		return nil
	}
	entities, err := schema.LoadEntities(c)
	if err != nil {
		return err
	}
	if command == "check" {
		fmt.Fprintf(out, "配置有效，共 %d 个实体\n", len(entities))
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	db, err := mongoplus.Connect(ctx, c.MongoDB)
	if err != nil {
		return err
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = db.Close(closeCtx)
	}()
	var changes []schema.Change
	if command == "schema plan" {
		changes, err = schema.Plan(ctx, db.Native(), entities)
	} else {
		changes, err = schema.Apply(ctx, db.Native(), entities)
	}
	if err != nil {
		return err
	}
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(changes)
}
