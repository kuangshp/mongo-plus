package schema

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// 测试也先写真实 JSON，再走与业务相同的加载入口。
func loadEntityDefinition(t *testing.T, c Config, name, definition string) Entity {
	t.Helper()
	c.SchemaDir = t.TempDir()
	if err := c.Normalize(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.SchemaDir, name+".json"), []byte(definition), 0600); err != nil {
		t.Fatal(err)
	}
	entities, err := LoadEntities(c)
	if err != nil {
		t.Fatal(err)
	}
	return entities[0]
}

func TestSchemaRequiresUnmodifiedJSONSource(t *testing.T) {
	c := testConfig(t, nil)
	load := func() Entity {
		return loadEntityDefinition(t, c, "user", `{"fields":[{"name":"name","type":"string","rules":{"minLength":1}}],"indexes":[{"name":"name_index","fields":[{"name":"name","order":1}]}]}`)
	}
	if err := validateLoadedEntities([]Entity{load()}); err != nil {
		t.Fatal("JSON 元数据未通过校验", err)
	}
	for name, change := range map[string]func(*Entity){
		"collection": func(e *Entity) { e.Collection = "other" },
		"source":     func(e *Entity) { e.Source = "fake.json" },
		"field":      func(e *Entity) { e.Fields[0].Type = "int64" },
		"nested map": func(e *Entity) { e.Fields[0].Rules["minLength"] = 9 },
		"index":      func(e *Entity) { e.Indexes[0].Fields[0].Order = -1 },
		"defaults":   func(e *Entity) { *e.SoftDelete = !*e.SoftDelete },
	} {
		t.Run(name, func(t *testing.T) {
			e := load()
			change(&e)
			assertSchemaSourceRejected(t, []Entity{e})
		})
	}
	manual := Entity{Collection: "manual", Source: "schema/user.json", Fields: []Field{{Name: "name", Type: "string", Rules: bson.M{}}}}
	if err := manual.Normalize(c, "manual"); err != nil {
		t.Fatal(err)
	}
	assertSchemaSourceRejected(t, []Entity{manual})
	assertSchemaSourceRejected(t, nil)
	valid := load()
	assertSchemaSourceRejected(t, []Entity{valid, valid})
}

func assertSchemaSourceRejected(t *testing.T, entities []Entity) {
	t.Helper()
	// 零值数据库一旦执行驱动操作就会 panic；拒绝必须发生在读取/迁移锁之前。
	for name, operation := range map[string]func(context.Context, *mongo.Database, []Entity) ([]Change, error){"plan": Plan, "apply": Apply} {
		if _, err := operation(context.Background(), &mongo.Database{}, entities); err == nil || (!strings.Contains(err.Error(), "JSON") && !strings.Contains(err.Error(), "集合重复")) {
			t.Fatalf("%s 未拒绝无效 JSON 来源: %v", name, err)
		}
	}
}
