//go:build ignore

// 从本目录执行 go run generator.go；模型字段只在 schema JSON 中维护。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	mongoplus "github.com/kuangshp/mongo-plus"
	"github.com/zeromicro/go-zero/core/conf"
)

func main() {
	action := flag.String("action", "generate", "generate、check、plan 或 apply")
	timeout := flag.Duration("timeout", 60*time.Second, "本次生成或同步的总超时")
	configFile := flag.String("config", "", "可选：已有服务 YAML；只读取 MongoConfig 数据源")
	flag.Parse()
	if flag.NArg() != 0 || *timeout <= 0 {
		log.Fatal("不接受位置参数，timeout 必须大于零")
	}
	var source mongoplus.Config
	if *configFile != "" {
		// 使用服务相同的加载器，保留默认值和配置键大小写兼容。
		// 不导入尚未生成的 internal/config，也不读取模型生成参数。
		var app struct {
			MongoConfig struct {
				Enabled        bool                `json:",default=false"`
				URI            string              `json:",optional"`
				Database       string              `json:",optional"`
				MaxPoolSize    uint64              `json:",default=100"`
				TimeoutSeconds int64               `json:",default=10"`
				Log            mongoplus.LogConfig `json:",optional"`
			} `json:",optional"`
		}
		if err := conf.Load(*configFile, &app); err != nil {
			log.Fatal(err)
		}
		if !app.MongoConfig.Enabled {
			log.Fatal("需要启用业务 MongoConfig")
		}
		m := app.MongoConfig
		if m.URI == "" || m.Database == "" || m.TimeoutSeconds <= 0 || m.TimeoutSeconds > 3600 {
			log.Fatal("MongoConfig 需要完整数据源，TimeoutSeconds 必须在 1 到 3600 之间")
		}
		source = mongoplus.Config{
			URI:         m.URI,
			Database:    m.Database,
			MaxPoolSize: m.MaxPoolSize,
			Timeout:     time.Duration(m.TimeoutSeconds) * time.Second,
			Log:         m.Log,
		}
	}
	cfg := &mongoplus.GeneratorConfig{
		Package:      "github.com/kuangshp/mongo-plus/examples/gozero",
		SchemaDir:    "schema",
		ModelPkgPath: "internal/dal/entity",
		OutPath:      "internal/dal/dao",
		RepoPath:     "internal/dal/repository",
		DtoPath:      "internal/dal/dto",
		VoPath:       "internal/dal/vo",
		Pluralize:    true,
		ID:           mongoplus.IDConfig{Type: mongoplus.IDTypeInt64},
		SoftDelete:   mongoplus.Bool(true),
		Timestamps:   mongoplus.Bool(true),
		MongoDB:      source,
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	changes, err := mongoplus.RunGenerator(ctx, cfg, *action)
	if err != nil {
		log.Fatal(err)
	}
	if changes != nil {
		if err := json.NewEncoder(os.Stdout).Encode(changes); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("%s 完成", *action)
}
