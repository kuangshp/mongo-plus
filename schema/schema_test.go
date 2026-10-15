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
