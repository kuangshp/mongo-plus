package mongoplus_test

import (
	"context"
	"errors"
	"testing"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestPeerCacheWritesAndTransactions(t *testing.T) {
	db, writer := userRepo(t)
	peerDB := mp.NewDatabase(db.Native().Client().Database(db.Native().Name()))
	reader := mp.MustRepository[testUser, int64](peerDB, mp.CollectionConfig{Name: "users", ID: mp.IDConfig{Type: mp.IDTypeInt64}, SoftDelete: true, Timestamps: true, TenantField: "company_id"})
	ctx := mp.WithTenant(context.Background(), int64(1))
	row := &testUser{Name: "original"}
	if err := writer.Create(ctx, row); err != nil {
		t.Fatal(err)
	}
	cache := mp.Query().WithCache(time.Minute).Build()
	check := func(want string) {
		t.Helper()
		got, err := reader.FindById(ctx, row.ID, cache)
		if err != nil || got.Name != want {
			t.Fatalf("期望 %s 得到 %+v %v", want, got, err)
		}
	}
	check("original")
	if _, err := writer.UpdateById(ctx, row.ID, field.NewString("name").Set("updated")); err != nil {
		t.Fatal(err)
	}
	check("updated")
	rollback := errors.New("rollback")
	err := mp.Transaction(ctx, db, func(tx context.Context) error {
		if _, err := writer.UpdateById(tx, row.ID, field.NewString("name").Set("rollback")); err != nil {
			return err
		}
		got, err := reader.FindById(tx, row.ID, cache)
		if err != nil || got.Name != "rollback" {
			t.Fatalf("另一个包装器的事务读取未绕过缓存: %+v %v", got, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	check("updated")
	if err := mp.Transaction(ctx, db, func(tx context.Context) error {
		_, err := writer.UpdateById(tx, row.ID, field.NewString("name").Set("committed"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	check("committed")
	// 强制直读模式对调用方仍传 WithCache 的旧代码也生效，原生写入无需失效通知。
	if err := peerDB.ConfigureCache(mp.CacheConfig{Disabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := writer.Native().UpdateOne(ctx, bson.M{"_id": row.ID}, bson.M{"$set": bson.M{"name": "external"}}); err != nil {
		t.Fatal(err)
	}
	check("external")
}
