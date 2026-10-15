package mongoplus

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// 与框架包装 Context 一样，Value/Done 不会自动委托给 request。
type frameworkContext struct {
	context.Context
	request context.Context
}

func TestContextResolverPreservesRequestContext(t *testing.T) {
	RegisterCtxResolver(func(ctx context.Context) context.Context {
		if wrapped, ok := ctx.(*frameworkContext); ok {
			return wrapped.request
		}
		return ctx
	})
	t.Cleanup(func() { RegisterCtxResolver(nil) })
	root, _, _ := querySources(t)
	root.config.TenantField = "company_id"
	request, cancel := context.WithCancel(WithActor(WithTenant(context.Background(), int64(7)), int64(11)))
	cancel()
	wrapped := &frameworkContext{Context: context.Background(), request: request}
	if got := WithTenant(wrapped, int64(8)); got.Err() != context.Canceled || got.Value(actorKey{}) != int64(11) {
		t.Fatal("WithTenant 丢失请求上下文")
	}
	if got := WithActor(wrapped, int64(12)); got.Err() != context.Canceled || got.Value(tenantKey{}) != int64(7) {
		t.Fatal("WithActor 丢失租户信息")
	}
	for name, fn := range map[string]func() error{
		"FindOne":   func() error { _, err := root.FindOne(wrapped); return err },
		"FindList":  func() error { _, err := root.FindList(wrapped); return err },
		"FindPage":  func() error { _, _, err := root.FindPage(wrapped, 1, 10); return err },
		"Count":     func() error { _, err := root.CountByWrapper(wrapped, nil); return err },
		"Exists":    func() error { _, err := root.ExistsByWrapper(wrapped, nil); return err },
		"Walk":      func() error { return root.Walk(wrapped, func(*queryEntity) error { return nil }) },
		"Aggregate": func() error { _, err := Aggregate[queryEntity](wrapped, root, nil); return err },
	} {
		t.Run(name, func(t *testing.T) {
			if err := fn(); !errors.Is(err, context.Canceled) {
				t.Fatalf("请求取消信号/租户丢失: %v", err)
			}
		})
	}
	if _, err := From(root).Build(wrapped); err != nil {
		t.Fatalf("Build 没有读取租户: %v", err)
	}
	if got := root.WithContext(wrapped).ctx; got != request {
		t.Fatal("WithContext 没有保留请求上下文")
	}
	var observed context.Context
	root.db.Observer = func(ctx context.Context, _ Observation) { observed = ctx }
	for name, fn := range map[string]func(){
		"Create":      func() { _ = root.Create(wrapped, nil) },
		"CreateBatch": func() { _ = root.CreateBatch(wrapped, nil) },
		"Update":      func() { _, _ = root.UpdateById(wrapped, 1, field.NewString("_id").Set("bad")) },
		"Delete":      func() { _, _ = root.DeleteByWrapper(wrapped, nil) },
		"Scan":        func() { _, _ = Scan[queryEntity](wrapped, From(root)) },
		"ScanByPage":  func() { _, _, _ = ScanByPage[queryEntity](wrapped, From(root), 1, 10) },
	} {
		t.Run(name, func(t *testing.T) {
			observed = nil
			fn()
			if observed != request {
				t.Fatal("执行/监控没有使用解析后的上下文")
			}
		})
	}
	session, err := root.db.native.Client().StartSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.EndSession(context.Background())
	wrapped.request = mongo.NewSessionContext(request, session)
	if got := resolveContext(wrapped); mongo.SessionFromContext(got) != session || got.Value(tenantKey{}) != int64(7) {
		t.Fatal("解析丢失事务 Session 或租户")
	}
	if err := Transaction(wrapped, root.db, func(context.Context) error { return nil }); err == nil || !strings.Contains(err.Error(), "嵌套") {
		t.Fatalf("事务未识别请求 Session: %v", err)
	}
}

func TestContextResolverResetAndNilResult(t *testing.T) {
	t.Cleanup(func() { RegisterCtxResolver(nil) })
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	RegisterCtxResolver(func(context.Context) context.Context { return nil })
	if resolveContext(ctx) != ctx {
		t.Fatal("nil 解析结果丢弃了原始上下文")
	}
	RegisterCtxResolver(nil)
	if resolveContext(ctx) != ctx {
		t.Fatal("重置解析器没有恢复默认行为")
	}
}
