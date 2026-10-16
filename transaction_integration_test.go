package mongoplus_test

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestBoundTransactionCommitRollbackAndPanic(t *testing.T) {
	db, users := userRepo(t)
	details := mp.MustRepository[testUser, int64](db, mp.CollectionConfig{
		Name: "details", ID: mp.IDConfig{Type: mp.IDTypeInt64}, TenantField: "company_id",
	})
	ctx := mp.WithTenant(context.Background(), int64(7))
	// 提前建立集合，事务只验证业务写入和原子性。
	if err := users.Create(ctx, &testUser{Name: "seed"}); err != nil {
		t.Fatal(err)
	}
	if err := details.Create(ctx, &testUser{Name: "seed"}); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback second write")
	for _, mode := range []string{"rollback", "panic", "commit"} {
		t.Run(mode, func(t *testing.T) {
			var saved *mp.Repository[testUser, int64]
			var escaped context.Context
			var gotPanic any
			err := func() (err error) {
				defer func() { gotPanic = recover() }()
				return mp.TransactionCtx(ctx, db, func(txCtx context.Context) error {
					escaped = txCtx
					saved = users.WithTx(txCtx)
					if err := saved.Create(context.Background(), &testUser{Name: mode}); err != nil {
						return err
					}
					if err := details.WithTx(txCtx).Create(ctx, &testUser{Name: mode}); err != nil {
						return err
					}
					if n, err := saved.Count(ctx); err != nil || n != 2 {
						return fmt.Errorf("绑定仓储未读到事务写入: %d %v", n, err)
					}
					if n, err := users.Count(ctx); err != nil || n != 1 {
						return fmt.Errorf("原仓储被绑定修改: %d %v", n, err)
					}
					if mode == "panic" {
						panic("business panic")
					}
					if mode == "rollback" {
						return rollback
					}
					return nil
				})
			}()
			if mode == "panic" && gotPanic != "business panic" || mode != "panic" && gotPanic != nil {
				t.Fatalf("panic 传播异常: %v", gotPanic)
			}
			if mode == "rollback" && !errors.Is(err, rollback) || mode == "commit" && err != nil {
				t.Fatalf("事务返回错误: %v", err)
			}
			want := int64(1)
			if mode == "commit" {
				want = 2
			}
			for _, repo := range []*mp.Repository[testUser, int64]{users, details} {
				if n, err := repo.Count(ctx); err != nil || n != want {
					t.Fatalf("%s 原子性失败: count=%d err=%v", mode, n, err)
				}
			}
			if _, err := saved.Count(ctx); !errors.Is(err, mp.ErrTxDone) {
				t.Fatalf("允许复用已退出仓储: %v", err)
			}
			if _, err := users.Count(escaped); !errors.Is(err, mp.ErrTxDone) {
				t.Fatalf("允许复用已退出 txCtx: %v", err)
			}
		})
	}
}

func TestBoundTransactionRetryAndFactory(t *testing.T) {
	db, users := userRepo(t)
	ctx := mp.WithTenant(context.Background(), int64(1))
	if err := users.Create(ctx, &testUser{Name: "seed"}); err != nil {
		t.Fatal(err)
	}
	attempts := 0
	var previous *mp.Repository[testUser, int64]
	use := func(tx context.Context) *mp.Repository[testUser, int64] {
		return users.WithTx(tx)
	}
	err := mp.TransactionAsCtx(ctx, db, use, func(bound *mp.Repository[testUser, int64]) error {
		attempts++
		if previous != nil {
			if _, err := previous.Count(ctx); !errors.Is(err, mp.ErrTxDone) {
				return fmt.Errorf("重试后旧绑定仍有效: %v", err)
			}
		}
		previous = bound
		if err := bound.Create(ctx, &testUser{Name: "retry"}); err != nil {
			return err
		}
		if attempts == 1 {
			return mongo.CommandError{Code: 112, Message: "retry", Labels: []string{"TransientTransactionError"}}
		}
		return nil
	})
	if err != nil || attempts != 2 {
		t.Fatalf("工厂或重试失败: attempts=%d err=%v", attempts, err)
	}
	if n, err := users.Count(ctx); err != nil || n != 2 {
		t.Fatalf("重试写入未正确回滚: %d %v", n, err)
	}
}

func TestBoundTransactionCachePublication(t *testing.T) {
	db, users := userRepo(t)
	ctx := mp.WithTenant(context.Background(), int64(1))
	row := &testUser{Name: "original"}
	if err := users.Create(ctx, row); err != nil {
		t.Fatal(err)
	}
	cache := mp.Query().WithCache(time.Minute).WithSingleFlight().Build()
	loads := 0
	db.Observer = func(_ context.Context, o mp.Observation) {
		if o.Operation == "FindOne" {
			loads++
		}
	}
	check := func(repo *mp.Repository[testUser, int64], want string) {
		t.Helper()
		got, err := repo.FindById(ctx, row.ID, cache)
		if err != nil || got.Name != want {
			t.Fatalf("缓存/事务读取期望 %s: %+v %v", want, got, err)
		}
	}
	check(users, "original")
	rollback := errors.New("rollback")
	err := mp.Transaction(ctx, db, func(tx context.Context) error {
		bound := users.WithTx(tx)
		if _, err := bound.UpdateById(ctx, row.ID, field.NewString("name").Set("pending")); err != nil {
			return err
		}
		check(bound, "pending")
		before := loads
		check(users, "original")
		if loads != before {
			return fmt.Errorf("提交前提前失效了缓存")
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	before := loads
	check(users, "original")
	if loads != before {
		t.Fatal("回滚不应发布缓存失效")
	}
	cancelCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	err = mp.Transaction(cancelCtx, db, func(tx context.Context) error {
		if _, err := users.WithTx(tx).UpdateById(cancelCtx, row.ID, field.NewString("name").Set("canceled")); err != nil {
			return err
		}
		cancel()
		return nil // 驱动识别请求取消，执行回滚而非提交。
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("取消未回滚: %v", err)
	}
	check(users, "original")
	if loads != before {
		t.Fatal("取消回滚不应发布缓存失效")
	}
	if err := mp.Transaction(ctx, db, func(tx context.Context) error {
		_, err := users.WithTx(tx).UpdateById(ctx, row.ID, field.NewString("name").Set("committed"))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	check(users, "committed")
	if loads != before+1 {
		t.Fatal("提交没有失效缓存")
	}
}

func TestBoundTransactionCRUDAndAggregation(t *testing.T) {
	db, users := userRepo(t)
	ctx := mp.WithTenant(context.Background(), int64(1))
	if err := users.Create(ctx, &testUser{Name: "seed"}); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback")
	err := mp.Transaction(ctx, db, func(tx context.Context) error {
		bound := users.WithTx(tx)
		rows := []*testUser{{Name: "first"}, {Name: "second"}}
		if err := bound.CreateBatch(ctx, rows); err != nil {
			return err
		}
		if _, err := bound.UpdateMapById(ctx, rows[0].ID, map[string]any{"name": "updated"}); err != nil {
			return err
		}
		if got, err := bound.FindById(ctx, rows[0].ID); err != nil || got.Name != "updated" {
			return fmt.Errorf("更新不在事务内: %+v %v", got, err)
		}
		if ok, err := bound.ExistsByWrapper(ctx, func(w *mp.Wrapper) { w.Where(field.NewString("name").Eq("updated")) }); err != nil || !ok {
			return fmt.Errorf("Exists 不在事务内: %v %v", ok, err)
		}
		if list, count, err := bound.FindPage(ctx, 1, 10); err != nil || count != 3 || len(list) != 3 {
			return fmt.Errorf("分页不在事务内: count=%d len=%d %v", count, len(list), err)
		}
		if page, err := bound.FindCursorPage(ctx, nil, 10); err != nil || len(page.List) != 3 {
			return fmt.Errorf("游标不在事务内: len=%d %v", len(page.List), err)
		}
		walked := 0
		if err := bound.Walk(ctx, func(*testUser) error { walked++; return nil }); err != nil || walked != 3 {
			return fmt.Errorf("Walk 不在事务内: %d %v", walked, err)
		}
		if list, err := mp.Aggregate[testUser](ctx, bound, nil); err != nil || len(list) != 3 {
			return fmt.Errorf("聚合不在事务内: len=%d %v", len(list), err)
		}
		if _, err := bound.DeleteById(ctx, rows[0].ID); err != nil {
			return err
		}
		if _, err := bound.FindById(ctx, rows[0].ID); !errors.Is(err, mp.ErrNotFound) {
			return fmt.Errorf("逻辑删除不在事务内: %v", err)
		}
		if _, err := bound.RestoreById(ctx, rows[0].ID); err != nil {
			return err
		}
		if _, err := bound.HardDeleteById(ctx, rows[1].ID); err != nil {
			return err
		}
		if n, err := bound.Count(ctx); err != nil || n != 2 {
			return fmt.Errorf("恢复/物理删除不在事务内: %d %v", n, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if n, err := users.Count(ctx); err != nil || n != 1 {
		t.Fatalf("CRUD 未回滚: %d %v", n, err)
	}
}

func TestBoundTransactionJoins(t *testing.T) {
	f := joinedFixture(t)
	rollback := errors.New("rollback")
	err := mp.Transaction(f.ctx, f.db, func(tx context.Context) error {
		users, departments := f.users.WithTx(tx), f.departments.WithTx(tx)
		if _, err := users.UpdateById(f.ctx, 1, field.NewString("name").Set("pending-user")); err != nil {
			return err
		}
		if _, err := departments.UpdateById(f.ctx, 1, field.NewString("name").Set("pending-dept")); err != nil {
			return err
		}
		query := users.WithContext(f.ctx).
			LeftJoin(departments, field.New[int64]("dept_id"), field.New[int64]("_id"), "department").
			Where(field.New[int64]("_id").Eq(1))
		var row joinedUser
		if err := query.ScanOne(&row); err != nil || row.Name != "pending-user" || row.Department == nil || row.Department.Name != "pending-dept" {
			return fmt.Errorf("绑定关联不在事务内: %+v %v", row, err)
		}
		var page []joinedUser
		if total, err := query.ScanByPage(&page, 1, 10); err != nil || total != 1 || len(page) != 1 || page[0].Name != "pending-user" {
			return fmt.Errorf("绑定关联分页不在事务内: total=%d len=%d %v", total, len(page), err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if got, err := f.users.FindById(f.ctx, 1); err != nil || got.Name != "user1" {
		t.Fatalf("关联事务未回滚: %+v %v", got, err)
	}
}
