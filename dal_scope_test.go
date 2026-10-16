package mongoplus

import (
	"context"
	"errors"
	"testing"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDataPermissionDefaultsToStrict(t *testing.T) {
	root, _, _ := querySources(t)
	if err := RegisterDataPermission(root.db, DataPermissionConfig{}); err != nil {
		t.Fatal(err)
	}
	if _, err := root.filter(context.Background(), QueryOption{}, false); !errors.Is(err, ErrDataPermissionRequired) {
		t.Fatalf("默认应要求权限上下文: %v", err)
	}
	loose := root.db.permission
	root.db.permission = nil
	if err := RegisterDataPermission(root.db, DataPermissionConfig{AllowMissing: true}); err != nil {
		t.Fatal(err)
	}
	if loose == root.db.permission {
		t.Fatal("应替换为允许缺失的插件")
	}
	filter, err := root.filter(context.Background(), QueryOption{}, false)
	if err != nil || len(filter) == 0 {
		t.Fatalf("AllowMissing = %v, %v", filter, err)
	}
}

func TestDALScopeAddsTenantPermissionAndSoftDelete(t *testing.T) {
	root, _, _ := querySources(t)
	if err := RegisterTenant(root.db, TenantConfig[int64]{TenantField: "company_id"}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterDataPermission(root.db, DataPermissionConfig{}); err != nil {
		t.Fatal(err)
	}
	dal := &DAL{provider: fixedDALProvider{root.db}}
	ctx := WithTenantID(context.Background(), int64(7))
	ctx = WithDataPermission(ctx, func(string) (bson.D, error) {
		return bson.D{{Key: "owner_id", Value: int64(3)}}, nil
	})
	command, err := applyDALScope(ctx, dal, dalCommand{collection: "main", op: "find", filter: bson.D{{Key: "name", Value: "a"}}})
	if err != nil {
		t.Fatal(err)
	}
	want := mergeDALFilter(bson.D{{Key: "name", Value: "a"}}, []bson.D{
		field.New[any]("company_id").Eq(int64(7)).Document,
		field.New[any]("deleted_at").IsNull().Document,
		{{Key: "owner_id", Value: int64(3)}},
	})
	if !bsonEqual(command.filter, want) {
		t.Fatalf("filter = %v\nwant %v", command.filter, want)
	}
	deleted, err := applyDALScope(ctx, dal, dalCommand{collection: "main", op: "deleteOne", filter: bson.D{{Key: "_id", Value: int64(1)}}})
	if err != nil || deleted.op != "updateOne" || len(deleted.update) == 0 {
		t.Fatalf("逻辑删除 = %+v, %v", deleted, err)
	}
	if _, err = applyDALScope(context.Background(), dal, dalCommand{collection: "main", op: "find"}); !errors.Is(err, ErrTenantRequired) {
		t.Fatal(err)
	}
}
