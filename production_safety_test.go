package mongoplus

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestPluginTenantUpdateProtection(t *testing.T) {
	for _, tc := range []struct {
		name     string
		config   TenantConfig[int64]
		fallback string
		paths    []string
	}{
		{"single", TenantConfig[int64]{TenantField: "company_id"}, "", []string{"company_id", "company_id.child"}},
		{"multiple", TenantConfig[int64]{TenantFields: []TenantFieldConfig[int64]{{Field: "company_id"}, {Field: "org_id"}}}, "", []string{"company_id", "org_id"}},
		{"collection override", TenantConfig[int64]{TenantField: "default_id", CollectionFields: map[string][]TenantFieldConfig[int64]{"main": {{Field: "org_id"}}}}, "", []string{"org_id"}},
		{"nested parent", TenantConfig[int64]{TenantField: "scope.company_id"}, "", []string{"scope", "scope.company_id"}},
		{"fallback", TenantConfig[int64]{TenantField: "company_id"}, "tenant_id", []string{"company_id", "tenant_id"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r, _, _ := querySources(t)
			r.config.TenantField = tc.fallback
			if err := RegisterTenant(r.db, tc.config); err != nil {
				t.Fatal(err)
			}
			ctx := WithTenantID(context.Background(), int64(1))
			for _, callCtx := range []context.Context{ctx, SkipTenant(ctx)} {
				for _, path := range tc.paths {
					for _, op := range []UpdateOp{field.New[int64](path).Set(2), {Operator: "$unset", Path: path, Value: ""}, {Operator: "$inc", Path: path, Value: 1}} {
						_, err := r.UpdateById(callCtx, 1, op)
						if err == nil || !strings.Contains(err.Error(), "受保护") {
							t.Fatalf("未在写入前保护 %s: %v", path, err)
						}
					}
				}
			}
		})
	}
}

func TestTenantOwnershipProtectionInDatabase(t *testing.T) {
	db := NewDatabase(testutil.Database(t))
	if err := RegisterTenant(db, TenantConfig[int64]{TenantField: "company_id"}); err != nil {
		t.Fatal(err)
	}
	r := MustRepository[queryEntity, int64](db, CollectionConfig{Name: "users", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	first := WithTenantID(context.Background(), int64(1))
	second := WithTenantID(context.Background(), int64(2))
	if err := r.Create(first, &queryEntity{ID: 1}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpdateMapById(first, 1, map[string]any{"company_id": int64(2)}); err == nil {
		t.Fatal("复现：普通更新迁移了租户")
	}
	if n, err := r.Count(first); err != nil || n != 1 {
		t.Fatal("原租户丢失文档", n, err)
	}
	if n, err := r.Count(second); err != nil || n != 0 {
		t.Fatal("其他租户看到了文档", n, err)
	}
	if err := RegisterDataPermission(db, DataPermissionConfig{Strict: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.Count(first); !errors.Is(err, ErrDataPermissionRequired) {
		t.Fatal("数据库请求缺少权限仍放行", err)
	}
}

func TestStrictDataPermissionAndMiddleware(t *testing.T) {
	r, _, _ := querySources(t)
	if err := RegisterDataPermission(r.db, DataPermissionConfig{Strict: true, ExcludeCollections: []string{"public"}}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := r.FindList(ctx); !errors.Is(err, ErrDataPermissionRequired) {
		t.Fatal(err)
	}
	if _, err := r.UpdateById(ctx, 1, field.NewString("name").Set("x")); !errors.Is(err, ErrDataPermissionRequired) {
		t.Fatal(err)
	}
	if err := RequireDataPermission(ctx); !errors.Is(err, ErrDataPermissionRequired) {
		t.Fatal(err)
	}
	allowed := WithDataPermission(ctx, func(string) (bson.D, error) { return bson.D{{Key: "owner_id", Value: 1}}, nil })
	if err := RequireDataPermission(allowed); err != nil {
		t.Fatal(err)
	}
	filter, err := r.filter(allowed, QueryOption{}, false)
	if err != nil || !strings.Contains(filter.String(), "owner_id") {
		t.Fatalf("权限条件丢失: %v %v", filter, err)
	}
	if _, err := r.filter(SkipDataPermission(ctx), QueryOption{}, false); err != nil {
		t.Fatal(err)
	}
	r.config.Name = "public"
	if _, err := r.filter(ctx, QueryOption{}, false); err != nil {
		t.Fatal(err)
	}
}

func TestAutoFillCannotOverrideTenantOwnership(t *testing.T) {
	r, _, _ := querySources(t)
	if err := RegisterTenant(r.db, TenantConfig[int64]{TenantField: "company_id"}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterAutoFill(r.db, AutoFillConfig{Fields: []FieldConfig{{Name: "company_id", Getter: func(context.Context) any { return int64(2) }, OnCreate: true, OnUpdate: true}}}); err != nil {
		t.Fatal(err)
	}
	ctx := WithTenantID(context.Background(), int64(1))
	if _, err := r.auditUpdate(ctx, bson.D{}); err == nil || !strings.Contains(err.Error(), "租户字段") {
		t.Fatalf("自动填充绕过更新租户保护: %v", err)
	}
	if _, err := r.prepareCreate(ctx, &queryEntity{ID: 1}, int64(1)); err == nil || !strings.Contains(err.Error(), "租户字段") {
		t.Fatalf("自动填充绕过新增租户保护: %v", err)
	}
}
