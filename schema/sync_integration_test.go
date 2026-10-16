package schema

import (
	"context"
	"errors"
	"testing"

	"github.com/kuangshp/mongo-plus/internal/testutil"
	"github.com/kuangshp/mongo-plus/migration"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
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
	users := db.Collection(entities[0].Collection)
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

func TestSchemaApplySharesMigrationLock(t *testing.T) {
	db := testutil.Database(t)
	ctx := context.Background()
	c := testConfig(t, map[string]string{"user": `{"fields":[{"name":"name","type":"string"}]}`})
	entities, err := LoadEntities(c)
	if err != nil {
		t.Fatal(err)
	}
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	var saved context.Context
	steps := []migration.Migration{{Version: "2026101601", Checksum: migration.Checksum([]byte("schema v1")), Up: func(lockedCtx context.Context, _ *mongo.Database) error {
		saved = lockedCtx
		if _, err := Apply(lockedCtx, db, entities); err != nil {
			return err
		}
		close(started)
		<-release
		return nil
	}}}
	go func() { done <- migration.Run(ctx, db, steps, migration.Options{}) }()
	select {
	case <-started:
	case err := <-done:
		t.Fatal("版本迁移未能复用锁", err)
	}
	if _, err := Apply(ctx, db, entities); !errors.Is(err, migration.ErrLocked) {
		t.Error("旧 schema.Apply 绕过了迁移锁", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(saved, db, entities); !errors.Is(err, migration.ErrLockLost) {
		t.Fatal("复用了已退出的迁移上下文", err)
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
	users := db.Collection(entities[0].Collection)
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

func TestSchemaRepairsValidationModeDrift(t *testing.T) {
	ctx := context.Background()
	db := testutil.Database(t)
	c := testConfig(t, map[string]string{"user": `{"timestamps":false,"fields":[{"name":"name","type":"string","required":true}]}`})
	entities, err := LoadEntities(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, mode := range []struct{ level, action string }{{"off", "error"}, {"strict", "warn"}, {"moderate", "error"}, {"off", "warn"}} {
		t.Run(mode.level+"-"+mode.action, func(t *testing.T) {
			e := entities[0]
			e.Collection = "drift_" + mode.level + "_" + mode.action
			if err := db.CreateCollection(ctx, e.Collection, options.CreateCollection().SetValidator(e.Validator()).SetValidationLevel(mode.level).SetValidationAction(mode.action)); err != nil {
				t.Fatal(err)
			}
			changes, err := Plan(ctx, db, []Entity{e})
			if err != nil || len(changes) != 1 || !changes[0].UpdateValidator {
				t.Fatalf("未检测校验状态变化 %+v %v", changes, err)
			}
			if _, err := Apply(ctx, db, []Entity{e}); err != nil {
				t.Fatal(err)
			}
			if changes, err := Plan(ctx, db, []Entity{e}); err != nil || len(changes) != 0 {
				t.Fatalf("同步不幂等 %+v %v", changes, err)
			}
			if _, err := db.Collection(e.Collection).InsertOne(ctx, bson.M{"_id": int64(1), "name": 123}); err == nil {
				t.Fatal("校验仍然未生效")
			}
		})
	}
}
