//go:build ignore

// 在项目根目录执行 go run generator.go；也可通过 -config 指定 YAML 文件。
package main

import (
	"flag"
	"log"

	mongoplus "github.com/kuangshp/mongo-plus"
)

func main() {
	configFile := flag.String("config", "examples/mongo-plus.yaml", "生成器 YAML 配置文件")
	flag.Parse()
	cfg, err := mongoplus.LoadGeneratorConfig(*configFile)
	if err != nil {
		log.Fatal(err)
	}
	// 配置了 mongodb 就先同步集合、索引、校验器；省略则只生成文件。
	if err := mongoplus.Generate(cfg); err != nil {
		log.Fatal(err)
	}
	log.Print("Entity、DAO、Repository、DTO、VO 生成完成")
}
