package schema

import (
	"context"
	"testing"

	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestSchemaApplyAndIdempotency(t *testing.T) {
	db := testutil.Database(t)
	ctx := context.Background()
	c := testConfig(t, map[string]string{"user": `{"timestamps":false,"fields":[{"name":"username","type":"string","required":true,"unique":true},{"name":"status","type":"int64"}]}`})
	entities, err := LoadEntities(c)
	if err != nil {
		t.Fatal(err)
	}
	changes, err := Plan(ctx, db, entities)
	if err != nil || len(changes) != 1 || !changes[0].Create {
		t.Fatalf("计划 %+v %v", changes, err)
	}
	if exists, _ := db.ListCollectionNames(ctx, bson.D{}); len(exists) != 0 {
		t.Fatal("Plan 修改了数据库")
	}
	if _, err = Apply(ctx, db, entities); err != nil {
		t.Fatal(err)
	}
	if changes, err = Plan(ctx, db, entities); err != nil || len(changes) != 0 {
		t.Fatalf("同步不幂等 %+v %v", changes, err)
	}
	users := db.Collection("users")
	if _, err = users.InsertOne(ctx, bson.M{"_id": int64(1), "username": "one", "status": int64(0)}); err != nil {
		t.Fatal(err)
	}
	if _, err = users.InsertOne(ctx, bson.M{"_id": int64(2), "username": "one"}); !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("唯一索引未生效 %v", err)
	}
	if _, err = users.InsertOne(ctx, bson.M{"_id": int64(3), "username": 123}); err == nil {
		t.Fatal("字段类型校验未生效")
	}
	if _, err = users.InsertOne(ctx, bson.M{"_id": int64(4)}); err == nil {
		t.Fatal("required 未生效")
	}
	if _, err = users.InsertOne(ctx, bson.M{"_id": int64(5), "username": ""}); err != nil {
		t.Fatalf("required 不应等价于非空 %v", err)
	}
	// 结构同步更新规则，不会自动为历史文档补字段。
	entities[0].Fields = append(entities[0].Fields, Field{Name: "mobile", GoName: "Mobile", Type: "string", Required: true})
	if _, err = Apply(ctx, db, entities); err != nil {
		t.Fatal(err)
	}
	var old bson.M
	if err = users.FindOne(ctx, bson.M{"_id": int64(1)}).Decode(&old); err != nil {
		t.Fatal(err)
	}
	if _, ok := old["mobile"]; ok {
		t.Fatal("同步修改了历史数据")
	}
}

func TestIndexConflictAndActiveOnly(t *testing.T) {
	db := testutil.Database(t)
	ctx := context.Background()
	c := testConfig(t, map[string]string{"user": `{"timestamps":false,"fields":[{"name":"username","type":"string"}],"indexes":[{"name":"uk_active_name","fields":[{"name":"username","order":1}],"unique":true,"activeOnly":true}]}`})
	entities, err := LoadEntities(c)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = Apply(ctx, db, entities); err != nil {
		t.Fatal(err)
	}
	users := db.Collection("users")
	if _, err = users.InsertOne(ctx, bson.M{"_id": int64(1), "username": "one", "deleted_at": nil}); err != nil {
		t.Fatal(err)
	}
	if _, err = users.InsertOne(ctx, bson.M{"_id": int64(2), "username": "one", "deleted_at": bson.DateTime(1)}); err != nil {
		t.Fatal(err)
	}
	entities[0].Indexes[0].Unique = false
	if _, err = Plan(ctx, db, entities); err == nil {
		t.Fatal("未发现索引定义冲突")
	}
}
