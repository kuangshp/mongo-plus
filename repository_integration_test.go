package mongoplus_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type testUser struct {
	ID        int64      `bson:"_id,omitempty"`
	Name      string     `bson:"name"`
	Status    int64      `bson:"status"`
	CompanyID int64      `bson:"company_id"`
	CreatedAt time.Time  `bson:"created_at"`
	UpdatedAt time.Time  `bson:"updated_at"`
	DeletedAt *time.Time `bson:"deleted_at"`
}

func userRepo(t *testing.T) (*mp.Database, *mp.Repository[testUser, int64]) {
	t.Helper()
	db := mp.NewDatabase(testutil.Database(t))
	r, err := mp.NewRepository[testUser, int64](db, mp.CollectionConfig{Name: "users", ID: mp.IDConfig{Type: "int64", Generation: "increment"}, SoftDelete: true, Timestamps: true, TenantField: "company_id"})
	if err != nil {
		t.Fatal(err)
	}
	return db, r
}

func TestRepositoryIntegration(t *testing.T) {
	_, r := userRepo(t)
	ctx := mp.WithTenant(context.Background(), int64(1))
	other := mp.WithTenant(ctx, int64(2))
	user := &testUser{Name: "one", Status: 1, CompanyID: 999}
	if err := r.Create(ctx, user); err != nil {
		t.Fatal(err)
	}
	if user.ID != 1 || user.CompanyID != 1 || user.CreatedAt.IsZero() || user.DeletedAt != nil {
		t.Fatalf("自动填充错误 %+v", user)
	}
	if err := r.Create(other, &testUser{Name: "two"}); err != nil {
		t.Fatal(err)
	}
	if _, err := r.FindById(other, user.ID); !errors.Is(err, mp.ErrNotFound) {
		t.Fatalf("跨租户读取 %v", err)
	}
	if _, err := r.Count(context.Background()); !errors.Is(err, mp.ErrTenantRequired) {
		t.Fatalf("未校验租户 %v", err)
	}
	list, err := r.FindListByWrapper(ctx, func(w *mp.Wrapper) {
		w.WhereOrGroup(field.New[int64]("company_id").Eq(2), field.NewString("name").Eq("one"))
	})
	if err != nil || len(list) != 1 || list[0].CompanyID != 1 {
		t.Fatalf("OR 绕过租户 %+v %v", list, err)
	}
	result, err := r.UpdateById(ctx, user.ID, field.NewNumeric[int64]("status").Set(0), field.NewString("name").Set(""))
	if err != nil || result.ModifiedCount != 1 {
		t.Fatalf("零值更新 %+v %v", result, err)
	}
	got, err := r.FindById(ctx, user.ID)
	if err != nil || got.Status != 0 || got.Name != "" {
		t.Fatalf("零值丢失 %+v %v", got, err)
	}
	if _, err = r.UpdateById(ctx, user.ID, field.New[int64]("company_id").Set(2)); err == nil {
		t.Fatal("允许修改租户")
	}
	if _, err = r.DeleteByWrapper(ctx, func(w *mp.Wrapper) {}); !errors.Is(err, mp.ErrUnsafeWrite) {
		t.Fatalf("空条件删除 %v", err)
	}
	if n, err := r.DeleteById(ctx, user.ID); err != nil || n != 1 {
		t.Fatalf("逻辑删除 %d %v", n, err)
	}
	if _, err = r.FindById(ctx, user.ID); !errors.Is(err, mp.ErrNotFound) {
		t.Fatalf("删除后仍可见 %v", err)
	}
	if n, err := r.Count(ctx); err != nil || n != 0 {
		t.Fatalf("删除后统计 %d %v", n, err)
	}
	if ok, err := r.Exists(ctx); err != nil || ok {
		t.Fatalf("删除后仍存在 %v %v", ok, err)
	}
	if result, err = r.UpdateById(ctx, user.ID, field.NewString("name").Set("hidden")); err != nil || result.MatchedCount != 0 {
		t.Fatalf("普通更新命中删除文档 %+v %v", result, err)
	}
	got, err = r.FindById(ctx, user.ID, mp.Query().WithDeleted().Build())
	if err != nil || got.DeletedAt == nil {
		t.Fatalf("WithDeleted %+v %v", got, err)
	}
	if n, err := r.RestoreById(other, user.ID); err != nil || n != 0 {
		t.Fatalf("跨租户恢复 %d %v", n, err)
	}
	if n, err := r.RestoreById(ctx, user.ID); err != nil || n != 1 {
		t.Fatalf("恢复 %d %v", n, err)
	}
	if n, err := r.HardDeleteById(ctx, user.ID); err != nil || n != 1 {
		t.Fatalf("物理删除 %d %v", n, err)
	}
	if _, err = r.FindById(ctx, user.ID, mp.Query().WithDeleted().Build()); !errors.Is(err, mp.ErrNotFound) {
		t.Fatalf("物理删除后仍存在 %v", err)
	}
}

func TestConcurrentSequenceAndPagination(t *testing.T) {
	_, r := userRepo(t)
	ctx := mp.WithTenant(context.Background(), int64(1))
	const count = 32
	ids := make(chan int64, count)
	errs := make(chan error, count)
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			u := &testUser{Name: fmt.Sprint(i), Status: 1}
			if err := r.Create(ctx, u); err != nil {
				errs <- err
				return
			}
			ids <- u.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for id := range ids {
		if seen[id] || id <= 0 {
			t.Fatalf("重复主键 %d", id)
		}
		seen[id] = true
	}
	if len(seen) != count {
		t.Fatalf("缺少主键 %d", len(seen))
	}
	list, total, err := r.FindPage(ctx, 2, 5, mp.Query().Order(field.NewNumeric[int64]("status").Asc()).Build())
	if err != nil || len(list) != 5 || total != count || list[0].ID != 6 {
		t.Fatalf("分页 %+v %d %v", list, total, err)
	}
	var after *int64
	all := []int64{}
	for {
		page, err := r.FindCursorPage(ctx, after, 7)
		if err != nil {
			t.Fatal(err)
		}
		for _, v := range page.List {
			all = append(all, v.ID)
		}
		if !page.HasMore {
			break
		}
		after = page.Next
	}
	if len(all) != count {
		t.Fatalf("游标分页丢失 %v", all)
	}
	for i, id := range all {
		if id != int64(i+1) {
			t.Fatalf("游标分页乱序 %v", all)
		}
	}
}

func TestTransactionRollbackAndCommit(t *testing.T) {
	db, r := userRepo(t)
	ctx := mp.WithTenant(context.Background(), int64(1))
	rollback := errors.New("rollback")
	err := mp.Transaction(ctx, db, func(txCtx context.Context) error {
		if err := r.Create(txCtx, &testUser{Name: "abort"}); err != nil {
			return err
		}
		count, err := r.Count(txCtx)
		if err != nil {
			return err
		}
		if count != 1 {
			return fmt.Errorf("事务内不可见")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if n, err := r.Count(ctx); err != nil || n != 0 {
		t.Fatalf("事务未回滚 %d %v", n, err)
	}
	u := &testUser{Name: "commit"}
	if err = mp.Transaction(ctx, db, func(txCtx context.Context) error { return r.Create(txCtx, u) }); err != nil {
		t.Fatal(err)
	}
	if u.ID != 2 {
		t.Fatalf("回滚序列不应复用 %d", u.ID)
	}
	if n, err := r.Count(ctx); err != nil || n != 1 {
		t.Fatalf("事务未提交 %d %v", n, err)
	}
}

func TestObjectIDAndManualIDs(t *testing.T) {
	db := mp.NewDatabase(testutil.Database(t))
	ctx := context.Background()
	type object struct {
		ID   bson.ObjectID `bson:"_id,omitempty"`
		Name string        `bson:"name"`
	}
	r, err := mp.NewRepository[object, bson.ObjectID](db, mp.CollectionConfig{Name: "objects"})
	if err != nil {
		t.Fatal(err)
	}
	u := &object{Name: "auto"}
	if err = r.Create(ctx, u); err != nil || u.ID.IsZero() {
		t.Fatalf("ObjectID %+v %v", u, err)
	}
	type manual struct {
		ID string `bson:"_id,omitempty"`
	}
	m, err := mp.NewRepository[manual, string](db, mp.CollectionConfig{Name: "manual", ID: mp.IDConfig{Type: "string", Generation: "manual"}})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.Create(ctx, &manual{}); !errors.Is(err, mp.ErrInvalidID) {
		t.Fatalf("手动 ID 允许空值 %v", err)
	}
	if err = m.Create(ctx, &manual{ID: "business-id"}); err != nil {
		t.Fatal(err)
	}
	if _, err = m.FindById(ctx, "business-id"); err != nil {
		t.Fatal(err)
	}
	if err = m.Create(ctx, &manual{ID: "business-id"}); !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("重复 ID 未报错 %v", err)
	}
}

func TestAggregateIsolation(t *testing.T) {
	_, r := userRepo(t)
	ctx := mp.WithTenant(context.Background(), int64(1))
	if err := r.Create(ctx, &testUser{Name: "visible"}); err != nil {
		t.Fatal(err)
	}
	if err := r.Create(mp.WithTenant(ctx, int64(2)), &testUser{Name: "other"}); err != nil {
		t.Fatal(err)
	}
	type result struct {
		Count int64 `bson:"count"`
	}
	rows, err := mp.Aggregate[result](ctx, r, mongo.Pipeline{bson.D{{Key: "$count", Value: "count"}}})
	if err != nil || len(rows) != 1 || rows[0].Count != 1 {
		t.Fatalf("聚合隔离 %+v %v", rows, err)
	}
	if _, err = mp.Aggregate[result](ctx, r, mongo.Pipeline{bson.D{{Key: "$unionWith", Value: "users"}}}); err == nil {
		t.Fatal("允许未审查的跨集合聚合")
	}
}

func TestReadCacheInvalidationAndTransactions(t *testing.T) {
	db, r := userRepo(t)
	ctx := mp.WithTenant(context.Background(), int64(1))
	cached := mp.Query().WithCache(time.Minute).Build()
	u := &testUser{Name: "original"}
	if err := r.Create(ctx, u); err != nil {
		t.Fatal(err)
	}
	if _, err := r.FindById(ctx, u.ID, cached); err != nil {
		t.Fatal(err)
	}
	if _, err := r.UpdateById(ctx, u.ID, field.NewString("name").Set("updated")); err != nil {
		t.Fatal(err)
	}
	got, err := r.FindById(ctx, u.ID, cached)
	if err != nil || got.Name != "updated" {
		t.Fatalf("写入未清除缓存 %+v %v", got, err)
	}
	rollback := errors.New("rollback")
	err = mp.Transaction(ctx, db, func(txCtx context.Context) error {
		if _, err := r.UpdateById(txCtx, u.ID, field.NewString("name").Set("rollback")); err != nil {
			return err
		}
		inside, err := r.FindById(txCtx, u.ID, cached)
		if err != nil {
			return err
		}
		if inside.Name != "rollback" {
			return fmt.Errorf("事务读取了旧缓存")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	got, err = r.FindById(ctx, u.ID, cached)
	if err != nil || got.Name != "updated" {
		t.Fatalf("回滚污染缓存 %+v %v", got, err)
	}
	if err = mp.Transaction(ctx, db, func(txCtx context.Context) error {
		_, err := r.UpdateById(txCtx, u.ID, field.NewString("name").Set("commit"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	got, err = r.FindById(ctx, u.ID, cached)
	if err != nil || got.Name != "commit" {
		t.Fatalf("提交未清除缓存 %+v %v", got, err)
	}
}

func TestTransactionCallbackRetry(t *testing.T) {
	db, r := userRepo(t)
	ctx := mp.WithTenant(context.Background(), int64(1))
	u := &testUser{Name: "retry"}
	attempts := 0
	err := mp.Transaction(ctx, db, func(txCtx context.Context) error {
		attempts++
		if err := r.Create(txCtx, u); err != nil {
			return err
		}
		if attempts == 1 {
			return mongo.CommandError{Code: 112, Message: "test retry", Labels: []string{"TransientTransactionError"}}
		}
		return nil
	})
	if err != nil || attempts != 2 {
		t.Fatalf("事务重试失败 attempts=%d err=%v", attempts, err)
	}
	if n, err := r.Count(ctx); err != nil || n != 1 {
		t.Fatalf("重试产生重复数据 %d %v", n, err)
	}
}
