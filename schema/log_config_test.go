package schema

import (
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
	c = Config{ProjectDir: t.TempDir(), Package: "example.com/shop", MongoDB: settings.Config{URI: "mongodb://localhost:27017", Database: "shop", Log: settings.LogConfig{Level: settings.LogInfo, Format: "shell", Replayable: true, SlowThresholdMS: 500, Colorful: true}}}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	if c.MongoDB.Log.Level != settings.LogInfo || !c.MongoDB.Log.Replayable {
		t.Fatal("Go 配置丢失日志参数")
	}
}
