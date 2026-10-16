package schema

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kuangshp/mongo-plus/internal/settings"
)

// 验证配置检测不会比较 Writer 接口中不可比较的动态值。
type sliceLogWriter []byte

func (sliceLogWriter) Write(p []byte) (int, error) { return len(p), nil }

func TestMongoLogConfig(t *testing.T) {
	c := Config{MongoDB: settings.Config{Log: settings.LogConfig{Writer: sliceLogWriter{}}}}
	if !c.HasMongoDB() {
		t.Fatal("log writer must count as supplied configuration")
	}
	if !(Config{MongoDB: settings.Config{Log: settings.LogConfig{Format: "shell"}}}).HasMongoDB() {
		t.Fatal("log format must count as supplied configuration")
	}
	if !(Config{MongoDB: settings.Config{Log: settings.LogConfig{Replayable: true}}}).HasMongoDB() {
		t.Fatal("replayable mode must count as supplied configuration")
	}
	file := filepath.Join(t.TempDir(), "mongo-plus.yaml")
	err := os.WriteFile(file, []byte("module: example.com/shop\nmongodb:\n  uri: mongodb://localhost:27017\n  database: shop\n  log:\n    level: info\n    format: shell\n    replayable: true\n    slowThresholdMs: 500\n    colorful: true\n"), 0600)
	if err != nil {
		t.Fatal(err)
	}
	c, err = LoadConfig(file)
	if err != nil {
		t.Fatal(err)
	}
	if c.MongoDB.Log.Level != settings.LogInfo || c.MongoDB.Log.Format != "shell" || !c.MongoDB.Log.Replayable || c.MongoDB.Log.SlowThresholdMS != 500 || !c.MongoDB.Log.Colorful {
		t.Fatalf("log config not loaded: %+v", c.MongoDB.Log)
	}
}
