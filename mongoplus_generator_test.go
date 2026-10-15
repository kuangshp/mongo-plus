package mongoplus_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	mp "github.com/kuangshp/mongo-plus"
	manual "github.com/kuangshp/mongo-plus/examples/manual/model"
)

// 实际执行 README 中的业务 generator.go，验证仅导入公共包即可生成并编译。
func TestPublicGeneratorFromConsumerModule(t *testing.T) {
	root, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	document, err := os.ReadFile("README.md")
	if err != nil {
		t.Fatal(err)
	}
	_, block, ok := strings.Cut(string(document), "<!-- business-generator -->")
	if !ok {
		t.Fatal("缺少业务生成器示例")
	}
	_, block, ok = strings.Cut(block, "```go\n")
	if !ok {
		t.Fatal("缺少 Go 代码块")
	}
	code, _, ok := strings.Cut(block, "\n```")
	if !ok {
		t.Fatal("不完整的 Go 代码块")
	}
	code = strings.ReplaceAll(code, "example.com/shop", "example.com/consumer")
	mod := fmt.Sprintf("module example.com/consumer\n\ngo 1.25.5\nrequire github.com/kuangshp/mongo-plus v0.0.0\nreplace github.com/kuangshp/mongo-plus => %q\n", root)
	for name, data := range map[string]string{
		"go.mod":            mod,
		"generator.go":      code,
		"schema/user.json":  `{"fields":[{"name":"username","type":"string","required":true}]}`,
		"schema/event.json": `{"id":{"type":"objectId"},"fields":[]}`,
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	run := func(args ...string) {
		t.Helper()
		command := exec.Command("go", args...)
		command.Dir = dir
		command.Env = append(os.Environ(), "GOWORK=off")
		if output, err := command.CombinedOutput(); err != nil {
			t.Fatalf("业务项目 go %v 失败: %v\n%s", args, err, output)
		}
	}
	run("get", "github.com/kuangshp/mongo-plus@v0.0.0")
	run("run", "generator.go")
	for name, fragment := range map[string]string{
		"internal/dal/entity/user_entity.go":  "package entity",
		"internal/dal/dao/user_fields_gen.go": "package dao",
		"internal/dal/repository/user_gen.go": "example.com/consumer/internal/dal/entity",
		"internal/dal/dto/user_dto.go":        "type ModifyUserDTO struct",
		"internal/dal/vo/event_vo.go":         "bson.ObjectID",
	} {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil || !strings.Contains(string(data), fragment) {
			t.Fatalf("独立路径/包名不正确 %s: %s %v", name, data, err)
		}
	}
	custom := filepath.Join(dir, "internal/dal/repository/user.go")
	data, err := os.ReadFile(custom)
	if err != nil {
		t.Fatal(err)
	}
	data = append(data, []byte("\n// 业务扩展必须保留。\n")...)
	if err := os.WriteFile(custom, data, 0600); err != nil {
		t.Fatal(err)
	}
	run("run", "generator.go")
	got, err := os.ReadFile(custom)
	if err != nil || string(got) != string(data) {
		t.Fatalf("覆盖了业务扩展: %v", err)
	}
	run("test", "-mod=mod", "./...")
	resolved, err := os.ReadFile(filepath.Join(dir, "go.mod"))
	if err != nil {
		t.Fatal(err)
	}
	for _, dependency := range []string{"github.com/kuangshp/gorm-plus", "github.com/kuangshp/go-utils", "gorm.io/", "github.com/zeromicro/go-zero"} {
		if strings.Contains(string(resolved), dependency) {
			t.Fatalf("公共包引入了额外框架: %s", dependency)
		}
	}
}

func TestPublicGeneratorConfigAndNilErrors(t *testing.T) {
	if err := mp.Generate(nil); err == nil {
		t.Fatal("nil 配置未报错")
	}
	if err := mp.GenerateModels(nil); err == nil {
		t.Fatal("nil 手写模型配置未报错")
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "generator.yaml")
	yaml := "package: example.com/project\nschemaDir: schema\nmodel_pkg_path: internal/dal/entity\nout_path: internal/dal/dao\nrepo_path: internal/dal/repository\nid:\n  type: int64\n  generation: increment\n"
	if err := os.WriteFile(path, []byte(yaml), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := mp.LoadGeneratorConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.ImportPath("model") != "example.com/project/internal/dal/entity" || c.PackageName("model") != "entity" {
		t.Fatalf("YAML 路径错误 %+v", c)
	}
	var id mp.IDConfig = c.ID
	if normalized, err := id.Normalize(); err != nil || normalized.Type != "int64" {
		t.Fatalf("共享配置类型不兼容 %+v %v", normalized, err)
	}
	// 手写 Entity 路线同样从公共包调用；不会再生成一份实体定义。
	c.ID = mp.IDConfig{Type: "objectId", Generation: "auto"}
	if err := mp.GenerateModels(c, mp.GeneratorModel{Value: manual.DocumentEntity{}, Collection: "documents", ID: &mp.IDConfig{Type: "int64", Generation: "increment"}}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.ModelPkgPath, "document_entity.go")); !os.IsNotExist(err) {
		t.Fatal("手写 Entity 被重复生成")
	}
	if _, err := os.Stat(filepath.Join(c.RepoPath, "document_gen.go")); err != nil {
		t.Fatal(err)
	}
	if err := mp.WriteGoctlTemplateHome(filepath.Join(dir, "templates")); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "templates/api/handler.tpl")); err != nil {
		t.Fatal(err)
	}
}
