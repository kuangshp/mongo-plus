package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/kuangshp/mongo-plus/generator/gozero"
	swaggergen "github.com/kuangshp/mongo-plus/generator/swagger"
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
		fmt.Fprintln(out, "用法：mongoplus goctl <goctl 参数...> | export-goctl-templates --output <目录>")
		fmt.Fprintln(out, "      mongoplus swagger|swagger-open --config <服务.yaml> [--dir <服务目录>]")
		fmt.Fprintln(out, "模型生成与结构同步：go run generator.go [-action generate|check|plan|apply]")
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
	if command == "generate" || command == "check" || command == "schema" {
		return fmt.Errorf("已移除基于生成 YAML 的 %s 命令，请使用 go run generator.go -action generate|check|plan|apply", command)
	}
	return fmt.Errorf("未知命令 %q", command)
}
