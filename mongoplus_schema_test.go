package mongoplus_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mp "github.com/kuangshp/mongo-plus"
	manual "github.com/kuangshp/mongo-plus/examples/manual/model"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func syncConfig(t *testing.T) *mp.GeneratorConfig {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "generator.yaml")
	if err := os.WriteFile(path, []byte("package: example.com/shop\npluralize: true\nid:\n  type: int64\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(dir, "schema"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "schema/sysUser.json"), []byte(`{"timestamps":false,"fields":[{"name":"username","type":"string","required":true,"unique":true}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := mp.LoadGeneratorConfig(path)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestPublicSchemaSyncErrors(t *testing.T) {
	if _, err := mp.SyncSchema(context.Background(), nil); err == nil {
		t.Fatal("nil 配置未报错")
	}
	if _, err := mp.SyncModels(context.Background(), nil); err == nil {
		t.Fatal("nil 模型配置未报错")
	}
	c := syncConfig(t)
	if !c.Pluralize {
		t.Fatal("YAML 未读取集合后缀")
	}
	if _, err := mp.SyncSchema(nil, c); err == nil {
		t.Fatal("nil context 未报错")
	}
	if _, err := mp.SyncSchema(context.Background(), c); err == nil || !strings.Contains(err.Error(), "uri、database") {
		t.Fatalf("缺少数据源时未报错: %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := mp.SyncSchema(ctx, c); !errors.Is(err, context.Canceled) {
		t.Fatalf("未遵循调用方取消: %v", err)
	}
	if _, err := os.Stat(c.ModelPkgPath); !os.IsNotExist(err) {
		t.Fatal("同步入口不应生成文件")
	}
}

func TestPublicSchemaSyncCreatesEmptyCollections(t *testing.T) {
	db := testutil.Database(t)
	c := syncConfig(t)
	c.MongoDB = mp.Config{URI: os.Getenv("MONGOPLUS_TEST_URI"), Database: db.Name()}
	ctx := context.Background()
	changes, err := mp.SyncSchema(ctx, c)
	if err != nil || len(changes) != 1 || changes[0].Collection != "sys_users" || !changes[0].Create {
		t.Fatalf("同步失败 %+v %v", changes, err)
	}
	names, err := db.ListCollectionNames(ctx, bson.M{"name": "sys_users"})
	if err != nil || len(names) != 1 {
		t.Fatalf("插入数据前集合未创建: %v %v", names, err)
	}
	collection := db.Collection("sys_users")
	if count, err := collection.CountDocuments(ctx, bson.D{}); err != nil || count != 0 {
		t.Fatalf("同步插入了业务数据: %d %v", count, err)
	}
	if changes, err := mp.SyncSchema(ctx, c); err != nil || len(changes) != 0 {
		t.Fatalf("重复同步非幂等 %+v %v", changes, err)
	}
	if _, err := collection.InsertOne(ctx, bson.M{"_id": int64(1), "username": "one"}); err != nil {
		t.Fatal(err)
	}
	if _, err := collection.InsertOne(ctx, bson.M{"_id": int64(2), "username": "one"}); !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("唯一索引未创建: %v", err)
	}
	if _, err := collection.InsertOne(ctx, bson.M{"_id": int64(3), "username": 123}); err == nil {
		t.Fatal("数据库校验器未创建")
	}
	models := []mp.GeneratorModel{{Value: manual.DocumentEntity{}}}
	changes, err = mp.SyncModels(ctx, c, models...)
	if err != nil || len(changes) != 1 || changes[0].Collection != "documents" {
		t.Fatalf("手写实体同步失败 %+v %v", changes, err)
	}
	if changes, err := mp.SyncModels(ctx, c, models...); err != nil || len(changes) != 0 {
		t.Fatalf("手写实体重复同步非幂等 %+v %v", changes, err)
	}
}

func TestPublicGenerateWithOptionalDataSource(t *testing.T) {
	db := testutil.Database(t)
	c := syncConfig(t)
	ctx := context.Background()
	if err := mp.Generate(c); err != nil {
		t.Fatal(err)
	}
	if names, err := db.ListCollectionNames(ctx, bson.D{}); err != nil || len(names) != 0 {
		t.Fatalf("不配置 MongoDB 却创建了集合: %v %v", names, err)
	}
	c.MongoDB = mp.Config{URI: os.Getenv("MONGOPLUS_TEST_URI"), Database: db.Name()}
	if err := mp.Generate(c); err != nil {
		t.Fatal(err)
	}
	if names, err := db.ListCollectionNames(ctx, bson.M{"name": "sys_users"}); err != nil || len(names) != 1 {
		t.Fatalf("Generate 未自动同步: %v %v", names, err)
	}
	if count, err := db.Collection("sys_users").CountDocuments(ctx, bson.D{}); err != nil || count != 0 {
		t.Fatalf("Generate 应创建空集合: %d %v", count, err)
	}
	if err := mp.GenerateModels(c, mp.GeneratorModel{Value: manual.DocumentEntity{}}); err != nil {
		t.Fatal(err)
	}
	if names, err := db.ListCollectionNames(ctx, bson.M{"name": "documents"}); err != nil || len(names) != 1 {
		t.Fatalf("GenerateModels 未自动同步: %v %v", names, err)
	}
}

func TestPublicGenerateStopsOnInvalidDataSource(t *testing.T) {
	for _, manualModel := range []bool{false, true} {
		c := syncConfig(t)
		c.MongoDB = mp.Config{URI: "invalid-uri", Database: "shop"}
		var err error
		if manualModel {
			err = mp.GenerateModels(c, mp.GeneratorModel{Value: manual.DocumentEntity{}})
		} else {
			err = mp.Generate(c)
		}
		if err == nil {
			t.Fatal("数据源错误仍继续生成")
		}
		if _, err := os.Stat(c.RepoPath); !os.IsNotExist(err) {
			t.Fatal("同步失败时生成了文件")
		}
	}
}
