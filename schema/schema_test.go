package schema

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/kuangshp/mongo-plus/internal/settings"
)

func testConfig(t *testing.T, definitions map[string]string) Config {
	t.Helper()
	dir := t.TempDir()
	schemaDir := filepath.Join(dir, "schema")
	if err := os.Mkdir(schemaDir, 0755); err != nil {
		t.Fatal(err)
	}
	for name, data := range definitions {
		if err := os.WriteFile(filepath.Join(schemaDir, name+".json"), []byte(data), 0600); err != nil {
			t.Fatal(err)
		}
	}
	c := Config{ProjectDir: dir, Module: "example.com/shop", ID: settings.IDConfig{Type: settings.IDTypeInt64}}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	return c
}

func TestGlobalDefaultsAndOverrides(t *testing.T) {
	c := testConfig(t, map[string]string{
		"user":  `{"fields":[{"name":"username","type":"string","required":true,"unique":true}]}`,
		"event": `{"id":{"type":"objectId"},"softDelete":false,"fields":[{"name":"value","type":"string"}]}`,
		"token": `{"id":{"type":"string","generation":"manual"},"fields":[]}`,
	})
	entities, err := LoadEntities(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entities {
		switch e.Entity {
		case "UserEntity":
			if e.ID.Type != "int64" || e.ID.Generation != "increment" || !*e.SoftDelete || len(e.Indexes) != 1 {
				t.Fatalf("全局继承错误 %+v", e)
			}
		case "EventEntity":
			if e.ID.Type != "objectId" || e.ID.Generation != "auto" || *e.SoftDelete {
				t.Fatalf("实体覆盖错误 %+v", e)
			}
		case "TokenEntity":
			if e.ID.Type != "string" || e.ID.Generation != "manual" {
				t.Fatalf("手动主键错误 %+v", e)
			}
		}
	}
}

func TestRejectInvalidSchemasBeforeGeneration(t *testing.T) {
	for name, body := range map[string]string{
		"unknown":         `{"fields":[],"typo":true}`,
		"duplicate-json":  `{"fields":[],"fields":[]}`,
		"duplicate-field": `{"fields":[{"name":"x","type":"string"},{"name":"x","type":"int64"}]}`,
		"reserved":        `{"fields":[{"name":"deleted_at","type":"int64"}]}`,
		"go-collision":    `{"fields":[{"name":"user_id","type":"int64"},{"name":"user_ID","type":"int64"}]}`,
		"unsupported":     `{"fields":[{"name":"x","type":"int128"}]}`,
		"id":              `{"id":{"type":"string","generation":"increment"},"fields":[]}`,
		"index":           `{"fields":[],"indexes":[{"name":"missing","fields":[{"name":"unknown","order":1}]}]}`,
		"array":           `{"fields":[{"name":"items","type":"[]object"}]}`,
	} {
		t.Run(name, func(t *testing.T) {
			c := testConfig(t, map[string]string{"invalid": body})
			if _, err := LoadEntities(c); err == nil {
				t.Fatal("允许无效定义")
			}
		})
	}
}

func TestValidationNestedAndOrderedIndexes(t *testing.T) {
	c := testConfig(t, map[string]string{"user": `{"fields":[{"name":"profile","type":"object","fields":[{"name":"city","type":"string","required":true}]},{"name":"tags","type":"[]string"}],"indexes":[{"name":"city_id","fields":[{"name":"profile.city","order":1},{"name":"_id","order":-1}]}]}`})
	entities, err := LoadEntities(c)
	if err != nil {
		t.Fatal(err)
	}
	if !entities[0].HasField("profile.city") {
		t.Fatal("缺少嵌套路径")
	}
	index := expectedIndex(entities[0].Indexes[0])
	if index.Key[0].Key != "profile.city" || index.Key[1].Key != "_id" {
		t.Fatal("索引顺序改变")
	}
	if len(entities[0].Validator()) == 0 {
		t.Fatal("缺少校验规则")
	}
}

func TestProjectPathEscape(t *testing.T) {
	c := testConfig(t, map[string]string{"user": `{"fields":[]}`})
	c.Output = filepath.Join(c.BaseDir(), "..", "outside")
	if err := c.Normalize(); err == nil {
		t.Fatal("允许逃离项目目录")
	}

}

func TestIndependentOutputConfigValidation(t *testing.T) {
	for name, change := range map[string]func(*Config){
		"module conflict":       func(c *Config) { c.Package = "example.com/other" },
		"model outside":         func(c *Config) { c.ModelPkgPath = filepath.Join(c.BaseDir(), "..", "outside") },
		"dao outside":           func(c *Config) { c.OutPath = filepath.Join(c.BaseDir(), "..", "outside") },
		"repository outside":    func(c *Config) { c.RepoPath = filepath.Join(c.BaseDir(), "..", "outside") },
		"dto outside":           func(c *Config) { c.DtoPath = filepath.Join(c.BaseDir(), "..", "outside") },
		"vo outside":            func(c *Config) { c.VoPath = filepath.Join(c.BaseDir(), "..", "outside") },
		"dto duplicate":         func(c *Config) { c.DtoPath = c.OutPath },
		"dto vo duplicate":      func(c *Config) { c.DtoPath, c.VoPath = "transport", "transport" },
		"vo keyword":            func(c *Config) { c.VoPath = "type" },
		"duplicate directories": func(c *Config) { c.RepoPath = c.OutPath },
		"keyword package":       func(c *Config) { c.ModelPkgPath = filepath.Join(c.BaseDir(), "type") },
		"invalid package":       func(c *Config) { c.OutPath = filepath.Join(c.BaseDir(), "my-dao") },
	} {
		t.Run(name, func(t *testing.T) {
			c := testConfig(t, map[string]string{"user": `{"fields":[]}`})
			change(&c)
			if err := c.Normalize(); err == nil {
				t.Fatal("接受了无效生成配置")
			}
		})
	}
}

func TestOptionalMongoDBConfig(t *testing.T) {
	for _, item := range []struct {
		name                string
		source              settings.Config
		configured, invalid bool
	}{
		{"omitted", settings.Config{}, false, false},
		{"complete", settings.Config{URI: "mongodb://127.0.0.1:27017", Database: "shop"}, true, false},
		{"uri only", settings.Config{URI: "mongodb://127.0.0.1:27017"}, true, true},
		{"database only", settings.Config{Database: "shop"}, true, true},
		{"options only", settings.Config{MaxPoolSize: 100}, true, true},
	} {
		t.Run(item.name, func(t *testing.T) {
			c := Config{ProjectDir: t.TempDir(), Package: "example.com/shop", MongoDB: item.source}
			if c.HasMongoDB() != item.configured {
				t.Fatal("数据源判断错误")
			}
			if err := c.Normalize(); (err != nil) != item.invalid {
				t.Fatalf("数据源校验错误: %v", err)
			}
		})
	}
}

func TestProjectDirAndStablePaths(t *testing.T) {
	c := Config{ProjectDir: t.TempDir(), Package: "example.com/shop", ModelPkgPath: "internal/dal/entity"}
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(c.ProjectDir, "internal/dal/entity")
	if c.ModelPkgPath != want || c.ImportPath("model") != "example.com/shop/internal/dal/entity" {
		t.Fatalf("项目路径错误: %+v", c)
	}
	if err := c.Normalize(); err != nil || c.ModelPkgPath != want {
		t.Fatalf("重复归一化改变路径: %v", err)
	}
}

func TestSequenceBlockInheritance(t *testing.T) {
	c := testConfig(t, map[string]string{
		"user":     `{"fields":[]}`,
		"event":    `{"id":{"type":"objectId"},"fields":[]}`,
		"manual":   `{"id":{"generation":"manual"},"fields":[]}`,
		"disabled": `{"id":{"blockSize":1},"fields":[]}`,
	})
	c.ID.BlockSize = 100
	entities, err := LoadEntities(c)
	if err != nil {
		t.Fatal(err)
	}
	wants := map[string]int64{"UserEntity": 100, "EventEntity": 0, "ManualEntity": 0, "DisabledEntity": 1}
	for _, e := range entities {
		if e.ID.BlockSize != wants[e.Entity] {
			t.Fatalf("实体预分配继承错误 %s %+v", e.Entity, e.ID)
		}
	}
}
