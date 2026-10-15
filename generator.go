//go:build ignore

// 在本库根目录执行 go run generator.go，重新生成 examples 中的数据层。
// 业务项目可复制 README 的同名文件，只导入 mongo-plus 公共包即可生成代码。
package main

import (
	"flag"
	"log"

	mongoplus "github.com/kuangshp/mongo-plus"
)

func main() {
	configFile := flag.String("config", "", "可选的生成器 YAML；不传则使用下方 Go 配置")
	flag.Parse()
	cfg := &mongoplus.GeneratorConfig{
		Package:      "github.com/kuangshp/mongo-plus",                           // 与本库 go.mod 的 module 一致。
		SchemaDir:    "examples/schema",                                          // JSON 实体定义目录。
		ModelPkgPath: "examples/query/model",                                     // Entity 输出目录。
		OutPath:      "examples/query/dao",                                       // DAO 输出目录。
		RepoPath:     "examples/query/repository",                                // Repository 输出目录。
		DtoPath:      "examples/query/dto",                                       // 新增/修改 DTO，首次创建后不覆盖。
		VoPath:       "examples/query/vo",                                        // 查询 VO，首次创建后不覆盖。
		ID:           mongoplus.IDConfig{Type: "int64", Generation: "increment"}, // 全局主键，实体可覆盖。
		SoftDelete:   mongoplus.Bool(true),
		Timestamps:   mongoplus.Bool(true),
	}
	if *configFile != "" {
		var err error
		cfg, err = mongoplus.LoadGeneratorConfig(*configFile)
		if err != nil {
			log.Fatal(err)
		}
	}
	if err := mongoplus.Generate(cfg); err != nil {
		log.Fatal(err)
	}
	log.Print("Entity、DAO、Repository、DTO、VO 生成完成")
}
