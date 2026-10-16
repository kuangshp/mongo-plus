package mongoplus

import (
	"context"
	"errors"
	"testing"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestTenantPluginInjectsSkipsAndOverrides(t *testing.T) {
	root, _, _ := querySources(t)
	if err := RegisterTenant(root.db, TenantConfig[int64]{
		TenantField:           "tenant_id",
		ExcludeCollections:    []string{"dict"},
		AllowOverrideTenantID: true,
		CollectionFields: map[string][]TenantFieldConfig[int64]{
			"secret": {},
			"order":  {{Field: "company_id"}, {Field: "org_id", GetTenantID: func(context.Context) (int64, bool) { return 5, true }}},
		},
	}); err != nil {
		t.Fatal(err)
	}
	root.config.TenantField = ""
	ctx := WithTenantID(context.Background(), int64(9))
	filter, err := root.filter(ctx, Query().Build(), false)
	want := field.And(field.New[any]("tenant_id").Eq(int64(9)), field.New[any]("deleted_at").IsNull())
	if err != nil || !bsonEqual(filter, want.Document) {
		t.Fatalf("租户条件 = %v, %v", filter, err)
	}
	skipped, err := root.filter(SkipTenant(ctx), Query().Build(), false)
	if err != nil || !bsonEqual(skipped, field.New[any]("deleted_at").IsNull().Document) {
		t.Fatalf("跳过租户 = %v, %v", skipped, err)
	}
	overridden, err := root.filter(WithOverrideTenantID(ctx, int64(2)), Query().Build(), false)
	wantOverride := field.And(field.New[any]("tenant_id").Eq(int64(2)), field.New[any]("deleted_at").IsNull())
	if err != nil || !bsonEqual(overridden, wantOverride.Document) {
		t.Fatalf("覆盖租户 = %v, %v", overridden, err)
	}
	root.config.Name = "dict"
	if _, err = root.filter(context.Background(), Query().Build(), false); err != nil {
		t.Fatalf("排除集合不应要求租户: %v", err)
	}
	root.config.Name = "secret"
	if _, err = root.filter(context.Background(), Query().Build(), false); err != nil {
		t.Fatalf("空集合配置不应要求租户: %v", err)
	}
	root.config.Name = "order"
	orderFilter, err := root.filter(ctx, Query().Build(), false)
	wantOrder := field.And(field.New[any]("company_id").Eq(int64(9)), field.New[any]("org_id").Eq(int64(5)), field.New[any]("deleted_at").IsNull())
	if err != nil || !bsonEqual(orderFilter, wantOrder.Document) {
		t.Fatalf("多字段租户 = %v, %v", orderFilter, err)
	}
	root.config.Name = "main"
	if _, err = root.filter(AllowGlobalOperation(ctx), Query().Build(), true); err != nil {
		t.Fatalf("允许全量写 = %v", err)
	}
	if _, err = root.filter(ctx, Query().Build(), true); !errors.Is(err, ErrUnsafeWrite) {
		t.Fatalf("未放开的全量写 = %v", err)
	}
}

func TestAutoFillWritesOperatorOnCreateAndUpdate(t *testing.T) {
	root, _, _ := querySources(t)
	if err := RegisterAutoFill(root.db, AutoFillConfig{Fields: []FieldConfig{
		{Name: "CreatedBy", Getter: OperatorGetter[int64](), OnCreate: true},
		{Name: "UpdatedBy", Getter: OperatorGetter[int64](), OnCreate: true, OnUpdate: true},
		{Name: "created_name", Getter: CtxGetter[string](CtxOperatorKey2), OnCreate: true},
	}}); err != nil {
		t.Fatal(err)
	}
	type actorDoc struct {
		ID          int64  `bson:"_id"`
		CreatedBy   int64  `bson:"created_by"`
		UpdatedBy   int64  `bson:"updated_by"`
		CreatedName string `bson:"created_name"`
	}
	repo := &Repository[actorDoc, int64]{db: root.db, config: CollectionConfig{Name: "main"}}
	ctx := WithActor(context.Background(), int64(4))
	ctx = context.WithValue(ctx, CtxOperatorKey2, "ann")
	prepared, err := repo.prepareCreate(ctx, &actorDoc{ID: 1}, int64(1))
	if err != nil {
		t.Fatal(err)
	}
	if prepared.entity.CreatedBy != 4 || prepared.entity.UpdatedBy != 4 || prepared.entity.CreatedName != "ann" {
		t.Fatalf("自动填充 = %+v", prepared.entity)
	}
	update, err := repo.auditUpdate(ctx, bson.D{})
	if err != nil {
		t.Fatal(err)
	}
	set, _ := update[0].Value.(bson.D)
	if update[0].Key != "$set" || len(set) != 1 || set[0].Key != "updated_by" || set[0].Value != int64(4) {
		t.Fatalf("更新操作人 = %v", update)
	}
}

func bsonEqual(got, want bson.D) bool {
	rawGot, errGot := bson.Marshal(got)
	rawWant, errWant := bson.Marshal(want)
	return errGot == nil && errWant == nil && string(rawGot) == string(rawWant)
}
