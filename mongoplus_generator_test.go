package mongoplus_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// 直接执行 README 唯一 generator.go 示例，确保无专用生成 YAML 也可检查、生成和编译。
func TestPublicGeneratorFromConsumerModule(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	document, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(string(document), "<!-- business-generator-go -->")
	if !ok {
		t.Fatal("缺少生成器示例")
	}
	_, block, ok = strings.Cut(block, "```go\n")
	if !ok {
		t.Fatal("缺少 Go 代码")
	}
	code, _, ok := strings.Cut(block, "\n```")
	if !ok {
		t.Fatal("代码块不完整")
	}
	code = strings.ReplaceAll(code, "example.com/shop", "example.com/consumer")
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":              fmt.Sprintf("module example.com/consumer\n\ngo 1.25.5\nrequire github.com/kuangshp/mongo-plus v0.0.0\nreplace github.com/kuangshp/mongo-plus => %q\n", root),
		"generator.go":        code,
		"schema/user.json":    `{"fields":[{"name":"username","type":"string","required":true}]}`,
		"schema/event.json":   `{"id":{"type":"objectId"},"fields":[]}`,
		"schema/sysUser.json": `{"fields":[]}`,
	}
	for name, data := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) []byte {
		t.Helper()
		command := exec.Command("go", args...)
		command.Dir = dir
		command.Env = append(os.Environ(), "GOWORK=off")
		out, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("go %v: %v\n%s", args, err, out)
		}
		return out
	}
	run("get", "github.com/kuangshp/mongo-plus@v0.0.0", "gopkg.in/yaml.v3@v3.0.1")
	run("run", "generator.go", "-action", "check")
	if _, err := os.Stat(filepath.Join(dir, "internal")); !os.IsNotExist(err) {
		t.Fatal("check 不应生成文件")
	}
	// 显式传入业务配置时必须报出连接配置错误，不能悄悄退回只生成代码。
	for _, item := range []struct {
		name, config, message string
	}{
		{"missing-source", "Name: shop\n", "缺少 mongodb"},
		{"empty-source", "mongodb: {}\n", "uri、database 不能为空"},
		{"partial-source", "mongodb:\n  uri: mongodb://127.0.0.1:1\n", "uri、database 不能为空"},
		{"invalid-yaml", "mongodb: [\n", "yaml:"},
	} {
		t.Run(item.name, func(t *testing.T) {
			path := filepath.Join(dir, item.name+".yml")
			if err := os.WriteFile(path, []byte(item.config), 0600); err != nil {
				t.Fatal(err)
			}
			command := exec.Command("go", "run", "generator.go", "-config", path)
			command.Dir = dir
			command.Env = append(os.Environ(), "GOWORK=off")
			output, err := command.CombinedOutput()
			if err == nil || !strings.Contains(string(output), item.message) {
				t.Fatalf("错误业务配置未拒绝: %v\n%s", err, output)
			}
			if _, err := os.Stat(filepath.Join(dir, "internal")); !os.IsNotExist(err) {
				t.Fatal("业务配置错误后仍生成文件")
			}
		})
	}
	run("run", "generator.go")
	for name, fragment := range map[string]string{
		"internal/dal/entity/user_entity.go":     "package entity",
		"internal/dal/dao/user_fields_gen.go":    "package dao",
		"internal/dal/repository/user_gen.go":    "example.com/consumer/internal/dal/entity",
		"internal/dal/dto/user_dto.go":           "type ModifyUserDTO struct",
		"internal/dal/vo/event_vo.go":            "bson.ObjectID",
		"internal/dal/repository/sysuser_gen.go": `Name: "sys_user"`,
	} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !strings.Contains(string(data), fragment) {
			t.Fatalf("输出错误 %s: %v", name, err)
		}
	}
	custom := filepath.Join(dir, "internal/dal/repository/user.go")
	data, err := os.ReadFile(custom)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("\n// 保留业务扩展。\n")...)
	if err := os.WriteFile(custom, data, 0600); err != nil {
		t.Fatal(err)
	}
	run("run", "generator.go")
	if got, err := os.ReadFile(custom); err != nil || string(got) != string(data) {
		t.Fatal("覆盖业务扩展")
	}
	run("test", "-mod=mod", "./...")
	// 有数据源时验证业务 YAML 仅提供连接、plan 不写库、apply 不生成文件。
	if uri := os.Getenv("MONGOPLUS_TEST_URI"); uri != "" {
		db := testutil.Database(t)
		appYAML := fmt.Sprintf("Name: shop\nmongodb:\n  uri: %q\n  database: %q\n", uri, db.Name())
		if err := os.WriteFile(filepath.Join(dir, "application.dev.yml"), []byte(appYAML), 0600); err != nil {
			t.Fatal(err)
		}
		run("run", "generator.go", "-action", "plan", "-config", "application.dev.yml")
		if names, err := db.ListCollectionNames(context.Background(), bson.D{}); err != nil || len(names) != 0 {
			t.Fatalf("plan 写入数据库: %v %v", names, err)
		}
		run("run", "generator.go", "-action", "apply", "-config", "application.dev.yml")
		for _, name := range []string{"user", "event", "sys_user"} {
			names, err := db.ListCollectionNames(context.Background(), bson.M{"name": name})
			if err != nil || len(names) != 1 {
				t.Fatalf("未创建 %s: %v %v", name, names, err)
			}
		}
	}
}

func TestPublicGeneratorConfigAndNilErrors(t *testing.T) {
	if err := mp.Generate(nil); err == nil {
		t.Fatal("nil 配置未报错")
	}
	dir := t.TempDir()
	c := &mp.GeneratorConfig{ProjectDir: dir, Package: "example.com/project", ModelPkgPath: "internal/dal/entity", OutPath: "internal/dal/dao", RepoPath: "internal/dal/repository", ID: mp.IDConfig{Type: mp.IDTypeInt64}}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	if c.ImportPath("model") != "example.com/project/internal/dal/entity" || c.PackageName("model") != "entity" {
		t.Fatalf("Go 配置路径错误 %+v", c)
	}
	var id mp.IDConfig = c.ID
	if normalized, err := id.Normalize(); err != nil || normalized.Type != "int64" {
		t.Fatalf("共享配置类型不兼容 %+v %v", normalized, err)
	}
	// 配置了输出目录也必须先准备 JSON；随后生成模型、DAO 和仓储。
	if err := mp.Generate(c); err == nil {
		t.Fatal("未编写 JSON 仍生成成功")
	}
	if err := os.MkdirAll(c.SchemaDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.SchemaDir, "document.json"), []byte(`{"fields":[{"name":"title","type":"string","required":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if err := mp.Generate(c); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{filepath.Join(c.ModelPkgPath, "document_entity.go"), filepath.Join(c.RepoPath, "document_gen.go")} {
		if _, err := os.Stat(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := mp.WriteGoctlTemplateHome(filepath.Join(dir, "templates")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "templates/api/handler.tpl")); err != nil {
		t.Fatal(err)
	}
}
