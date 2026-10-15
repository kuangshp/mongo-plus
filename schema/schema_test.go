package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	path := filepath.Join(dir, "mongo-plus.yaml")
	if err := os.WriteFile(path, []byte("module: example.com/shop\nid:\n  type: int64\n  generation: increment\n"), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadConfig(path)
	if err != nil {
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

func TestUnknownConfigAndPathEscape(t *testing.T) {
	c := testConfig(t, map[string]string{"user": `{"fields":[]}`})
	c.Output = filepath.Join(c.BaseDir(), "..", "outside")
	if err := c.Normalize(); err == nil {
		t.Fatal("允许逃离项目目录")
	}
	path := filepath.Join(c.BaseDir(), "bad.yaml")
	_ = os.WriteFile(path, []byte("module: example.com/shop\nsoftDelet: true\n"), 0600)
	if _, err := LoadConfig(path); err == nil || !strings.Contains(err.Error(), "softDelet") {
		t.Fatalf("未知配置被忽略 %v", err)
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
	t.Setenv("MONGO_PLUS_TEST_MISSING_URI", "")
	t.Setenv("MONGO_PLUS_TEST_MISSING_DB", "")
	t.Setenv("MONGO_PLUS_TEST_CONFIG_URI", "mongodb://127.0.0.1:27017")
	t.Setenv("MONGO_PLUS_TEST_CONFIG_DB", "shop")
	for _, item := range []struct {
		name, yaml          string
		configured, invalid bool
	}{
		{"omitted", "", false, false},
		{"null", "mongodb: null\n", false, false},
		{"complete", "mongodb:\n  uri: mongodb://127.0.0.1:27017\n  database: shop\n", true, false},
		{"env", "mongodb:\n  uri: ${MONGO_PLUS_TEST_CONFIG_URI}\n  database: ${MONGO_PLUS_TEST_CONFIG_DB}\n", true, false},
		{"empty", "mongodb: {}\n", false, true},
		{"uri only", "mongodb:\n  uri: mongodb://127.0.0.1:27017\n", true, true},
		{"database only", "mongodb:\n  database: shop\n", true, true},
		{"options only", "mongodb:\n  maxPoolSize: 100\n", true, true},
		{"missing env", "mongodb:\n  uri: ${MONGO_PLUS_TEST_MISSING_URI}\n  database: ${MONGO_PLUS_TEST_MISSING_DB}\n", false, true},
	} {
		t.Run(item.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "generator.yml")
			if err := os.WriteFile(path, []byte("package: example.com/shop\n"+item.yaml), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := LoadConfig(path)
			if item.invalid {
				if err == nil || !strings.Contains(err.Error(), "uri、database") {
					t.Fatalf("不完整数据源未报错: %v", err)
				}
				return
			}
			if err != nil || c.HasMongoDB() != item.configured {
				t.Fatalf("可选数据源判断不正确 %+v %v", c, err)
			}
			if item.configured && (c.MongoDB.URI != "mongodb://127.0.0.1:27017" || c.MongoDB.Database != "shop") {
				t.Fatal("数据源读取或环境变量展开失败")
			}
		})
	}
}
