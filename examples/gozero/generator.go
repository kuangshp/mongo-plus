//go:build ignore

// 在当前业务项目根目录运行 go run generator.go；无需先安装 mongoplus CLI。
package main

import (
	"log"

	mongoplus "github.com/kuangshp/mongo-plus"
)

func main() {
	if err := mongoplus.Generate(&mongoplus.GeneratorConfig{
		Package:      "github.com/kuangshp/mongo-plus/examples/gozero",
		SchemaDir:    "schema",
		ModelPkgPath: "internal/dal/entity",
		OutPath:      "internal/dal/dao",
		RepoPath:     "internal/dal/repository",
		DtoPath:      "internal/dal/dto", // 新增/修改 DTO，首次创建后不覆盖。
		VoPath:       "internal/dal/vo",  // 查询 VO，首次创建后不覆盖。
		ID:           mongoplus.IDConfig{Type: "int64", Generation: "increment"},
		SoftDelete:   mongoplus.Bool(true),
		Timestamps:   mongoplus.Bool(true),
	}); err != nil {
		log.Fatal(err)
	}
}
