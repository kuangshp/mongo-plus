package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestCLIRejectsInvalidCommands(t *testing.T) {
	for _, args := range [][]string{{"unknown"}, {"schema"}, {"schema", "drop"}, {"generate", "--typo"}, {"generate", "extra"}, {"check", "--timeout=-1s"}, {"goctl"}, {"export-goctl-templates"}, {"export-goctl-templates", "--output", t.TempDir(), "extra"}, {"swagger", "--timeout=-1s"}, {"swagger-open", "extra"}} {
		var out bytes.Buffer
		if err := run(args, &out, &out); err == nil {
			t.Fatalf("允许无效命令 %v", args)
		}
	}
	var out bytes.Buffer
	if err := run([]string{"help"}, &out, &out); err != nil || out.Len() == 0 {
		t.Fatal("缺少帮助")
	}
}

func TestGenerateUsesOptionalYAMLDataSource(t *testing.T) {
	for _, item := range []struct {
		name, source string
		invalid      bool
	}{
		{"omitted", "", false},
		{"partial", "mongodb:\n  database: shop\n", true},
		{"invalid", "mongodb:\n  uri: invalid-uri\n  database: shop\n", true},
	} {
		t.Run(item.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.Mkdir(filepath.Join(dir, "schema"), 0755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, "schema/user.json"), []byte(`{"fields":[]}`), 0600); err != nil {
				t.Fatal(err)
			}
			path := filepath.Join(dir, "generator.yml")
			if err := os.WriteFile(path, []byte("package: example.com/shop\n"+item.source), 0600); err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			err := run([]string{"generate", "--config", path}, &output, &output)
			if (err != nil) != item.invalid {
				t.Fatalf("结果不正确: %v\n%s", err, output.String())
			}
			_, stat := os.Stat(filepath.Join(dir, "query/model/user_entity.go"))
			if item.invalid && !os.IsNotExist(stat) {
				t.Fatal("数据源错误后仍生成文件")
			}
			if !item.invalid && stat != nil {
				t.Fatalf("省略数据源没有生成文件: %v", stat)
			}
		})
	}
}
