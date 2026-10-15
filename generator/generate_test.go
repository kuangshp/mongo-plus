package generator

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	manual "github.com/kuangshp/mongo-plus/examples/manual/model"
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
	if err = os.WriteFile(filepath.Join(dir, "mongo-plus.yaml"), []byte("module: example.com/fixture\nid:\n  type: int64\n  generation: increment\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := schema.LoadConfig(filepath.Join(dir, "mongo-plus.yaml"))
	if err != nil {
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

func TestHandwrittenEntityGeneration(t *testing.T) {
	c, dir := fixture(t)
	if _, err := GenerateModels(c, schema.Model{Value: manual.DocumentEntity{}, Collection: "documents"}); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(c.Output, "model", "document_entity.go")); !os.IsNotExist(err) {
		t.Fatal("生成器不应复制或覆盖手写 Entity")
	}
	compileFixture(t, dir)
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
