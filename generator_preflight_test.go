package mongoplus_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/generator"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"github.com/kuangshp/mongo-plus/schema"
	"go.mongodb.org/mongo-driver/v2/bson"
)

const conflictingDAOJSON = `{"timestamps":false,"fields":[{"name":"username","type":"string","unique":true},{"name":"profile","type":"object","fields":[{"name":"city","type":"string"}]},{"name":"profile_city","type":"string","required":true}]}`

func TestAllGeneratorEntrypointsPreflightBeforeConnecting(t *testing.T) {
	for _, action := range []string{"check", "generate", "plan", "apply", "Generate", "SyncSchema"} {
		t.Run(action, func(t *testing.T) {
			c := syncConfig(t)
			c.MongoDB = mp.Config{URI: "invalid-uri", Database: "shop"}
			if err := os.WriteFile(filepath.Join(c.SchemaDir, "sysUser.json"), []byte(conflictingDAOJSON), 0600); err != nil {
				t.Fatal(err)
			}
			var err error
			switch action {
			case "Generate":
				err = mp.Generate(c)
			case "SyncSchema":
				_, err = mp.SyncSchema(context.Background(), c)
			default:
				_, err = mp.RunGenerator(context.Background(), c, action)
			}
			if err == nil || !strings.Contains(err.Error(), "DAO 字段名称冲突") {
				t.Fatalf("应先返回生成冲突，不能尝试解析连接: %v", err)
			}
			if _, err := os.Stat(c.ModelPkgPath); !os.IsNotExist(err) {
				t.Fatal("预检失败产生了输出")
			}
		})
	}
}

func TestFailedGenerationLeavesExistingValidatorAndWritesUnchanged(t *testing.T) {
	db := testutil.Database(t)
	c := syncConfig(t)
	c.MongoDB = mp.Config{URI: os.Getenv("MONGOPLUS_TEST_URI"), Database: db.Name()}
	ctx := context.Background()
	if _, err := mp.SyncSchema(ctx, c); err != nil {
		t.Fatal(err)
	}
	before, err := schema.LoadEntities(*c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(c.SchemaDir, "sysUser.json"), []byte(conflictingDAOJSON), 0600); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"generate", "apply"} {
		if _, err := mp.RunGenerator(ctx, c, action); err == nil {
			t.Fatal("命名冲突未报错")
		}
	}
	if changes, err := schema.Plan(ctx, db, before); err != nil || len(changes) != 0 {
		t.Fatalf("失败生成修改了数据库结构: %v %v", changes, err)
	}
	if _, err := db.Collection("sys_users").InsertOne(ctx, bson.M{"_id": int64(1), "username": "old-service"}); err != nil {
		t.Fatalf("失败生成后旧服务写入不再合法: %v", err)
	}
	if _, err := os.Stat(c.ModelPkgPath); !os.IsNotExist(err) {
		t.Fatal("失败生成写入了模型")
	}
}

func TestPreparedSchemaAndFilesUseSameSnapshot(t *testing.T) {
	db := testutil.Database(t)
	c := syncConfig(t)
	prepared, err := generator.Prepare(*c)
	if err != nil {
		t.Fatal(err)
	}
	// 模拟准备与同步之间源文件发生变化：数据库与代码必须使用原来的同一快照。
	if err := os.WriteFile(filepath.Join(c.SchemaDir, "sysUser.json"), []byte(`{"timestamps":false,"fields":[{"name":"username","type":"int64","required":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.ApplySchema(context.Background(), db); err != nil {
		t.Fatal(err)
	}
	if _, err := prepared.Write(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(c.ModelPkgPath, "sysuser_entity.go"))
	if err != nil || !strings.Contains(strings.Join(strings.Fields(string(data)), " "), "Username string") {
		t.Fatalf("输出不是准备时的模型: %v\n%s", err, data)
	}
	if _, err := db.Collection("sys_users").InsertOne(context.Background(), bson.M{"_id": int64(1), "username": "snapshot"}); err != nil {
		t.Fatalf("数据库没有使用与代码相同的 string 快照: %v", err)
	}
}
