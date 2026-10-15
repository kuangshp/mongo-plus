package mongoplus_test

import (
	"context"
	"errors"
	"testing"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/field"
)

func TestGormPlusMigrationIntegration(t *testing.T) {
	db, r := userRepo(t)
	ctx := mp.WithTenant(context.Background(), int64(1))
	other := mp.WithTenant(ctx, int64(2))
	users := []*testUser{{Name: "admin", Status: 1}, {Name: "test_one", Status: 1}, {Name: "ordinary", Status: 0}}
	if err := r.CreateBatch(ctx, users); err != nil {
		t.Fatal(err)
	}
	foreign := &testUser{Name: "admin", Status: 1}
	if err := r.Create(other, foreign); err != nil {
		t.Fatal(err)
	}
	list, total, err := r.FindPageByWrapper(ctx, 1, 10, func(w *mp.Wrapper) {
		w.Where(field.New[int64]("status").Eq(1)).WhereGroupFn(func(group *mp.Wrapper) {
			group.Like(field.NewString("name"), "admin").OrRLike(field.NewString("name"), "test_")
		}).OrderTriState("desc", field.New[int64]("_id").Asc(), field.New[int64]("_id").Desc())
	}, mp.QueryOpt().WithSingleFlight(0).Build())
	if err != nil || total != 2 || len(list) != 2 || list[0].ID != users[1].ID {
		t.Fatalf("迁移后的条件/分页: %+v %d %v", list, total, err)
	}
	// 任一零边界应整体跳过范围，保留所有当前租户的记录。
	list, err = r.FindListByWrapper(ctx, func(w *mp.Wrapper) { w.BetweenIfNotZero(field.New[int64]("status"), int64(1), int64(0)) })
	if err != nil || len(list) != 3 {
		t.Fatalf("范围语义不兼容: %+v %v", list, err)
	}
	// 混入其他租户主键仍受隔离约束。
	if n, err := r.DeleteByIdList(ctx, []int64{users[0].ID, users[1].ID, foreign.ID}); err != nil || n != 2 {
		t.Fatalf("批量软删除: %d %v", n, err)
	}
	all := mp.QueryOpt().WithUnscoped().WithSingleFlight(time.Minute).Build()
	if n, err := r.CountByWrapper(ctx, nil, all); err != nil || n != 3 {
		t.Fatalf("Count 选项丢失: %d %v", n, err)
	}
	if ok, err := r.ExistsByWrapper(ctx, func(w *mp.Wrapper) { w.Where(field.New[int64]("_id").Eq(users[0].ID)) }, all); err != nil || !ok {
		t.Fatalf("Exists 选项丢失: %v %v", ok, err)
	}
	rollback := errors.New("rollback")
	err = mp.Transaction(ctx, db, func(txCtx context.Context) error {
		if _, err := r.DeleteByCondition(txCtx, field.New[int64]("_id").Eq(users[2].ID)); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if _, err := r.FindById(ctx, users[2].ID); err != nil {
		t.Fatalf("条件删除没有随事务回滚: %v", err)
	}
	if n, err := r.DeleteByIdList(ctx, []int64{users[0].ID, users[1].ID, foreign.ID}, mp.Delete().WithPhysicalDelete().Build()); err != nil || n != 2 {
		t.Fatalf("批量物理删除: %d %v", n, err)
	}
	if n, err := r.CountByWrapper(ctx, nil, all); err != nil || n != 1 {
		t.Fatalf("写后缓存未失效: %d %v", n, err)
	}
	if _, err := r.FindById(other, foreign.ID); err != nil {
		t.Fatalf("物理删除影响了其他租户: %v", err)
	}
	if n, err := r.DeleteByCondition(ctx, field.New[int64]("status").Eq(0)); err != nil || n != 1 {
		t.Fatal(n, err)
	}
	if n, err := r.DeleteByWrapper(ctx, func(w *mp.Wrapper) { w.Where(field.New[int64]("_id").Eq(users[2].ID)) }, mp.WithPhysicalDelete()); err != nil || n != 1 {
		t.Fatalf("Wrapper 物理删除没有命中软删除记录: %d %v", n, err)
	}
}
