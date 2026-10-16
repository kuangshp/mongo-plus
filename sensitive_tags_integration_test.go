package mongoplus

import (
	"context"
	"testing"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestSensitiveTagOnlyUpdatePersistsNewCipherAndIndex(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "local"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			db := NewDatabase(testutil.Database(t))
			if shared {
				cache := NewMemoryCache()
				t.Cleanup(func() { _ = cache.Close() })
				db.UseCache(cache)
			}
			if _, err := RegisterSensitive(db, SensitiveConfig{Key: []byte("0123456789abcdef")}); err != nil {
				t.Fatal(err)
			}
			r := MustRepository[customTagSensitiveEntity, int64](db, CollectionConfig{Name: "users", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
			ctx := context.Background()
			if err := r.Create(ctx, &customTagSensitiveEntity{ID: 1, Phone: "13800008000"}); err != nil {
				t.Fatal(err)
			}
			cached := Query().WithCache(time.Minute).Build()
			old, err := r.FindById(WithSensitivePlaintext(ctx), 1, cached)
			if err != nil || old.Phone != "13800008000" {
				t.Fatalf("创建/预热: %+v %v", old, err)
			}
			if _, err := r.UpdateById(ctx, 1, field.New[string]("phone").Set("139-0000-9000")); err != nil {
				t.Fatal(err)
			}
			var raw bson.M
			if err := r.Native().FindOne(ctx, bson.M{"_id": int64(1)}).Decode(&raw); err != nil {
				t.Fatal(err)
			}
			if _, exists := raw["phone"]; exists {
				t.Fatalf("数据库出现明文字段: %+v", raw)
			}
			if raw["encrypted_mobile"] == "13900009000" || raw["encrypted_mobile"] == old.PhoneCipher || raw["mobile_lookup"] == old.PhoneIndex {
				t.Fatalf("密文/索引未刷新: %+v", raw)
			}
			got, err := r.FindById(WithSensitivePlaintext(ctx), 1, cached)
			if err != nil || got.Phone != "13900009000" {
				t.Fatalf("更新后读取仍是旧值: %+v %v", got, err)
			}
			got, err = r.FindById(ctx, 1, cached)
			if err != nil || got.Phone != "139****9000" {
				t.Fatalf("缓存没有遵守脱敏模式: %+v %v", got, err)
			}
			for _, phone := range []string{"13800008000", "13900009000"} {
				rows, err := r.FindList(ctx, Query().Where(r.SensitiveEq("Phone", phone)).Build())
				want := 0
				if phone == "13900009000" {
					want = 1
				}
				if err != nil || len(rows) != want {
					t.Fatalf("新旧盲索引查询 phone=%s rows=%d err=%v", phone, len(rows), err)
				}
			}
			if _, err := r.UpdateMapById(ctx, 1, map[string]any{"phone": "13700007000"}); err != nil {
				t.Fatal(err)
			}
			got, err = r.FindById(WithSensitivePlaintext(ctx), 1, cached)
			if err != nil || got.Phone != "13700007000" {
				t.Fatalf("map 更新: %+v %v", got, err)
			}
			if _, err := r.UpdateByWrapper(ctx, func(w *Wrapper) { w.Where(r.SensitiveEq("phone", "13700007000")) }, field.New[string]("phone").Unset()); err != nil {
				t.Fatal(err)
			}
			raw = nil
			if err := r.Native().FindOne(ctx, bson.M{"_id": int64(1)}).Decode(&raw); err != nil {
				t.Fatal(err)
			}
			for _, path := range []string{"phone", "encrypted_mobile", "mobile_lookup"} {
				if _, exists := raw[path]; exists {
					t.Fatalf("Unset 后字段仍存在: %s", path)
				}
			}
		})
	}
}
