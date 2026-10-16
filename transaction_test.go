package mongoplus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// 不连接服务器，用官方 Session 检查绑定及 Context 的边界行为。
func managedTestContext(t *testing.T, db *Database, ctx context.Context) (context.Context, *transactionScope) {
	t.Helper()
	session, err := db.native.Client().StartSession()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.EndSession(context.Background()) })
	if err := session.StartTransaction(); err != nil {
		t.Fatal(err)
	}
	scope := &transactionScope{session: session, caches: txCaches{}}
	scope.active.Store(true)
	ctx = mongo.NewSessionContext(ctx, session)
	ctx = context.WithValue(ctx, transactionScopeKey{}, scope)
	ctx = context.WithValue(ctx, txCacheKey{}, scope.caches)
	return ctx, scope
}

func TestWithTxRejectsInvalidAndExpiredBindings(t *testing.T) {
	r, _, _ := querySources(t)
	ctx := context.Background()
	txCtx, scope := managedTestContext(t, r.db, ctx)
	unmanaged := mongo.NewSessionContext(ctx, scope.session)
	expired := r.WithTx(txCtx)
	scope.active.Store(false)
	for name, tc := range map[string]struct {
		repo *Repository[queryEntity, int64]
		want error
	}{
		"nil":         {r.WithTx(nil), ErrUnmanagedTransaction},
		"plain":       {r.WithTx(ctx), ErrUnmanagedTransaction},
		"unmanaged":   {r.WithTx(unmanaged), ErrUnmanagedTransaction},
		"expired":     {expired, ErrTxDone},
		"expired ctx": {r, ErrTxDone},
	} {
		t.Run(name, func(t *testing.T) {
			callCtx := ctx
			if name == "expired ctx" {
				callCtx = txCtx
			}
			bound := tc.repo
			for operation, run := range map[string]func() error{
				"create":    func() error { return bound.Create(callCtx, &queryEntity{ID: 1}) },
				"batch":     func() error { return bound.CreateBatch(callCtx, []*queryEntity{{ID: 1}}) },
				"find":      func() error { _, err := bound.FindById(callCtx, 1); return err },
				"list":      func() error { _, err := bound.FindList(callCtx); return err },
				"page":      func() error { _, _, err := bound.FindPage(callCtx, 1, 10); return err },
				"cursor":    func() error { _, err := bound.FindCursorPage(callCtx, nil, 10); return err },
				"count":     func() error { _, err := bound.Count(callCtx); return err },
				"exists":    func() error { _, err := bound.Exists(callCtx); return err },
				"walk":      func() error { return bound.Walk(callCtx, func(*queryEntity) error { return nil }) },
				"update":    func() error { _, err := bound.UpdateById(callCtx, 1, field.NewString("name").Set("x")); return err },
				"delete":    func() error { _, err := bound.DeleteById(callCtx, 1); return err },
				"restore":   func() error { _, err := bound.RestoreById(callCtx, 1); return err },
				"aggregate": func() error { _, err := Aggregate[queryEntity](callCtx, bound, nil); return err },
				"join":      func() error { _, err := Scan[queryEntity](callCtx, From(bound)); return err },
				"join page": func() error { _, _, err := ScanByPage[queryEntity](callCtx, From(bound), 1, 10); return err },
			} {
				if err := run(); !errors.Is(err, tc.want) {
					t.Errorf("%s: want %v, got %v", operation, tc.want, err)
				}
			}
		})
	}
	if r.tx != nil {
		t.Fatal("WithTx 修改了原仓储")
	}
}

func TestBoundTransactionContextAndClientIsolation(t *testing.T) {
	r, _, _ := querySources(t)
	other, _, _ := querySources(t)
	type requestKey struct{}
	parent, cancelParent := context.WithCancel(WithActor(WithTenant(context.Background(), int64(7)), int64(11)))
	defer cancelParent()
	txCtx, _ := managedTestContext(t, r.db, parent)
	bound := r.WithTx(txCtx)
	request := context.WithValue(context.Background(), requestKey{}, "trace")
	request, cancel := context.WithTimeout(request, time.Minute)
	defer cancel()
	ctx, cleanup, err := bound.operationContext(request)
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	if ctx.Value(requestKey{}) != "trace" || ctx.Value(tenantKey{}) != int64(7) || ctx.Value(actorKey{}) != int64(11) || mongo.SessionFromContext(ctx) != mongo.SessionFromContext(txCtx) {
		t.Fatal("事务绑定丢失请求值、租户、操作人或 Session")
	}
	if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > time.Minute {
		t.Fatal("事务绑定丢失请求超时")
	}
	if _, _, err := other.WithTx(txCtx).operationContext(request); !errors.Is(err, ErrTransactionMismatch) {
		t.Fatalf("允许不同 Client: %v", err)
	}
	otherTx, _ := managedTestContext(t, r.db, parent)
	if _, _, err := bound.operationContext(otherTx); !errors.Is(err, ErrTransactionMismatch) {
		t.Fatalf("允许混用不同 Session: %v", err)
	}
	cancelParent()
	select {
	case <-ctx.Done():
	case <-time.After(time.Second):
		t.Fatal("丢失事务入口的取消信号")
	}
}
