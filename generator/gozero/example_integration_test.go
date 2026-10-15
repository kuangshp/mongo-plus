package gozero

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// 验证仓库示例可按文档生成、更新文档和构建；不安装全局工具，也不改写源示例。
func TestGoctlExampleMakefileRoundTrip(t *testing.T) {
	if os.Getenv("MONGOPLUS_GOCTL_INTEGRATION") != "1" {
		t.Skip("设置 MONGOPLUS_GOCTL_INTEGRATION=1 运行示例构建")
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	source := filepath.Join(root, "examples/gozero")
	for _, name := range []string{"go.mod", "generator.go", "shop.api", "mongo-plus.yaml", "Makefile", "schema/user.json"} {
		data, err := os.ReadFile(filepath.Join(source, name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "go.mod" {
			data = []byte(strings.Replace(string(data), "=> ../..", "=> "+strconv.Quote(root), 1))
		}
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	toolDir := t.TempDir()
	binary := filepath.Join(toolDir, "mongoplus")
	run := func(directory string, arguments ...string) []byte {
		t.Helper()
		command := exec.Command(arguments[0], arguments[1:]...)
		command.Dir = directory
		command.Env = append(os.Environ(), "GOWORK=", "PATH="+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))
		output, err := command.CombinedOutput()
		if err != nil {
			t.Fatalf("%v: %v\n%s", arguments, err, output)
		}
		return output
	}
	// 与示例 install-tools 使用同一包路径，-o 放入临时目录，不安装到用户 GOBIN。
	run(dir, "go", "build", "-mod=mod", "-o", binary, "github.com/kuangshp/mongo-plus/cmd/mongoplus")
	run(dir, "make", "-o", "install-tools", "gen")
	run(dir, "go", "mod", "tidy")
	config := filepath.Join(dir, "etc/shop-api.yaml")
	yaml, err := os.ReadFile(config)
	if err != nil {
		t.Fatal(err)
	}
	yaml = []byte(strings.Replace(string(yaml), "SwaggerConfig:\n  Enabled: false", "SwaggerConfig:\n  Enabled: true", 1))
	if err := os.WriteFile(config, yaml, 0600); err != nil {
		t.Fatal(err)
	}
	run(dir, "make", "build")
	if _, err := os.Stat(filepath.Join(dir, "bin/shop")); err != nil {
		t.Fatal(err)
	}
	// 不重新生成时也能直接构建；生成错误必须使 make build 停止，而不是打包旧文档。
	api := filepath.Join(dir, "shop.api")
	if err := os.WriteFile(api, []byte("invalid api"), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("make", "build")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=", "PATH="+toolDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	if output, err := command.CombinedOutput(); err == nil {
		t.Fatalf("无效 API 仍成功构建: %s", output)
	}
}
