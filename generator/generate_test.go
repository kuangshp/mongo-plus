package generator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuangshp/mongo-plus/internal/settings"
	"github.com/kuangshp/mongo-plus/schema"
)

func fixture(t *testing.T) (schema.Config, string) {
	t.Helper()
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mod := fmt.Sprintf("module example.com/fixture\n\ngo 1.25.5\n\nrequire github.com/kuangshp/mongo-plus v0.0.0\nreplace github.com/kuangshp/mongo-plus => %s\n", filepath.ToSlash(root))
	if err = os.WriteFile(filepath.Join(dir, "go.mod"), []byte(mod), 0600); err != nil {
		t.Fatal(err)
	}
	if err = os.Mkdir(filepath.Join(dir, "schema"), 0755); err != nil {
		t.Fatal(err)
	}
	c := schema.Config{ProjectDir: dir, Package: "example.com/fixture", ID: settings.IDConfig{Type: settings.IDTypeInt64}}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	return c, dir
}

func compileFixture(t *testing.T, dir string) {
	t.Helper()
	cmd := exec.Command("go", "test", "-mod=mod", "./...")
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GOWORK=off")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("生成项目编译失败: %v\n%s", err, out)
	}
}

func TestGeneratedCodeAndPreservedExtensions(t *testing.T) {
	c, dir := fixture(t)
	c.ID.BlockSize = 100
	definitions := map[string]string{
		"user":   `{"fields":[{"name":"username","type":"string"},{"name":"status","type":"int64"},{"name":"profile","type":"object","fields":[{"name":"city","type":"string"}]},{"name":"items","type":"[]object","fields":[{"name":"price","type":"decimal128"}]},{"name":"tags","type":"[]string"},{"name":"extra","type":"json"},{"name":"enabled","type":"bool","nullable":true}]}`,
		"event":  `{"id":{"type":"objectId"},"fields":[]}`,
		"manual": `{"id":{"type":"string"},"softDelete":false,"timestamps":false,"fields":[]}`,
	}
	for name, data := range definitions {
		if err := os.WriteFile(filepath.Join(c.SchemaDir, name+".json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := Generate(c)
	if err != nil {
		t.Fatal(err)
	}
	generatedRepo, readErr := os.ReadFile(filepath.Join(c.RepoPath, "user_gen.go"))
	compact := strings.Join(strings.Fields(string(generatedRepo)), "")
	if readErr != nil || !strings.Contains(compact, "BlockSize:100") {
		t.Fatalf("生成仓储丢失预分配配置: %v\n%s", readErr, generatedRepo)
	}
	before := map[string]string{}
	for _, file := range files {
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		before[file] = string(data)
	}
	ext := filepath.Join(c.Output, "repository", "user.go")
	original, err := os.ReadFile(ext)
	if err != nil {
		t.Fatal(err)
	}
	custom := append(original, []byte("\n// 保留业务扩展。\n")...)
	if err = os.WriteFile(ext, custom, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Generate(c); err != nil {
		t.Fatal(err)
	}
	for file, want := range before {
		got, _ := os.ReadFile(file)
		if file == ext {
			want = string(custom)
		}
		if string(got) != want {
			t.Fatalf("重新生成不稳定或覆盖扩展: %s", file)
		}
	}
	compileFixture(t, dir)
	// 修改实体主键后，基础接口随生成更新；保留的业务扩展不会固定旧主键类型。
	changed := strings.Replace(definitions["user"], `{"fields":`, `{"id":{"type":"objectId"},"fields":`, 1)
	if err = os.WriteFile(filepath.Join(c.SchemaDir, "user.json"), []byte(changed), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err = Generate(c); err != nil {
		t.Fatal(err)
	}
	if got, _ := os.ReadFile(ext); string(got) != string(custom) {
		t.Fatal("修改主键时覆盖了业务扩展")
	}
	compileFixture(t, dir)
}

// 即使输出目录里已有手写 Entity，也不能代替 schema/*.json。
func TestGenerationRequiresJSONDefinitions(t *testing.T) {
	c, _ := fixture(t)
	if err := os.MkdirAll(c.ModelPkgPath, 0755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.ModelPkgPath, "user_entity.go")
	original := []byte("package model\ntype UserEntity struct { ID int64 }\n")
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	if files, err := Generate(c); err == nil || !strings.Contains(err.Error(), "没有 JSON 实体定义") || len(files) != 0 {
		t.Fatalf("无 JSON 仍生成文件: %v %v", files, err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(original) {
		t.Fatal("修改了原有文件", err)
	}
	if _, err := os.Stat(c.RepoPath); !os.IsNotExist(err) {
		t.Fatal("无 JSON 时生成了仓储")
	}
}

func TestRejectOverwritingHandwrittenFile(t *testing.T) {
	c, _ := fixture(t)
	if err := os.WriteFile(filepath.Join(c.SchemaDir, "user.json"), []byte(`{"fields":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.Output, "model")
	if err := os.MkdirAll(path, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(path, "user_entity.go"), []byte("package model\n// 用户自己的文件\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(c); err == nil || !strings.Contains(err.Error(), "拒绝覆盖") {
		t.Fatalf("手写文件被覆盖 %v", err)
	}
}
