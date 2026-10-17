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
)

func main() {
	action := flag.String("action", "generate", "generate、check、plan 或 apply")
	timeout := flag.Duration("timeout", 60*time.Second, "本次生成或同步的总超时")
	flag.Parse()
	if flag.NArg() != 0 || *timeout <= 0 {
		log.Fatal("不接受位置参数，timeout 必须大于零")
	}
	cfg := &mongoplus.GeneratorConfig{
		Package:   "github.com/kuangshp/mongo-plus/examples/dal",
		Output:    "generated",
		SchemaDir: "schema",
		ID: mongoplus.IDConfig{
			Type:       mongoplus.IDTypeInt64,
			Generation: mongoplus.IDGenerationManual,
		},
		SoftDelete: mongoplus.Bool(true),
		Timestamps: mongoplus.Bool(true),
		Tenant: &mongoplus.GeneratorTenant{
			Name: "company_id",
			Type: "int64",
		},
		MongoDB: mongoplus.Config{
			URI:      os.Getenv("MONGOPLUS_URI"),
			Database: os.Getenv("MONGOPLUS_DATABASE"),
		},
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
