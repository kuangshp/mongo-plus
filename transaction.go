package mongoplus

import (
	"context"
	"errors"
	"fmt"
	"sync/atomic"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	// ErrTxDone 表示事务回调已经退出；重试前一轮绑定的仓储也不能再使用。
	ErrTxDone = errors.New("事务已结束，绑定仓储不可继续使用")
	// ErrUnmanagedTransaction 表示 WithTx 没有收到本库事务回调提供的 Context。
	ErrUnmanagedTransaction = errors.New("请使用本库事务入口提供的事务 Context")
	// ErrTransactionMismatch 表示混用了不同 Client、Session 或事务轮次。
	ErrTransactionMismatch = errors.New("仓储与上下文不属于同一事务或 MongoDB Client")
)

type transactionScopeKey struct{}

// 每轮驱动重试都有独立作用域，防止前一轮仓储及缓存登记混入下一轮。
type transactionScope struct {
	session *mongo.Session
	active  atomic.Bool
	caches  txCaches
	success bool
}

func (s *transactionScope) check(db *Database) error {
	if !s.active.Load() || !s.session.TransactionRunning() {
		return ErrTxDone
	}
	if db == nil || db.native == nil || db.native.Client() != s.session.Client() {
		return ErrTransactionMismatch
	}
	return nil
}

// Transaction 执行 Session 事务；返回 nil 提交，返回 error 或 panic 回滚。
// 回调可能被驱动重试，每轮必须重新 WithTx(txCtx)，且不要并行使用同一 Session。
// 可继续向普通仓储传 txCtx，也可 WithTx(txCtx) 后向普通方法传请求 ctx。
func Transaction(ctx context.Context, db *Database, fn func(txCtx context.Context) error, opts ...options.Lister[options.TransactionOptions]) error {
	if ctx == nil || db == nil || db.native == nil || fn == nil {
		return fmt.Errorf("事务上下文、数据库和回调不能为空")
	}
	ctx = resolveContext(ctx)
	if mongo.SessionFromContext(ctx) != nil {
		return fmt.Errorf("不支持嵌套 Session 事务或保存点")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	session, err := db.native.Client().StartSession()
	if err != nil {
		return err
	}
	// EndSession 会中止仍在运行的事务，亦覆盖 panic 路径；清理不受请求取消影响。
	defer session.EndSession(context.WithoutCancel(ctx))
	var last *transactionScope
	_, err = session.WithTransaction(ctx, func(txCtx context.Context) (any, error) {
		scope := &transactionScope{session: session, caches: txCaches{}}
		last = scope
		scope.active.Store(true)
		defer scope.active.Store(false)
		txCtx = context.WithValue(txCtx, transactionScopeKey{}, scope)
		txCtx = context.WithValue(txCtx, txCacheKey{}, scope.caches)
		callbackErr := fn(txCtx)
		// 回调取消请求后即使返回 nil，驱动也会在提交前中止事务。
		scope.success = callbackErr == nil && txCtx.Err() == nil
		return nil, callbackErr
	}, opts...)
	// 回调失败/回滚不发布失效；提交成功或提交结果不确定时保守失效。
	// 只发布最终一轮的登记，失败重试轮次的写入已经由驱动回滚。
	if last != nil && last.success {
		for database, collections := range last.caches {
			for collection := range collections {
				database.InvalidateCache(collection)
			}
		}
	}
	return err
}

// TransactionCtx 是 Transaction 的同义入口，便于沿用 gorm-plus 的命名。
// MongoDB 始终显式传 Context，因此两个入口的参数和行为相同。
func TransactionCtx(ctx context.Context, db *Database, fn func(context.Context) error, opts ...options.Lister[options.TransactionOptions]) error {
	return Transaction(ctx, db, fn, opts...)
}

// TransactionAs 在每轮事务中构造业务仓储集合；use 应调用各仓储的 WithTx(txCtx)。
// 公共库不依赖业务 DAO，Q 由业务的 use 函数推导。
func TransactionAs[Q any](ctx context.Context, db *Database, use func(context.Context) Q, fn func(Q) error, opts ...options.Lister[options.TransactionOptions]) error {
	if use == nil || fn == nil {
		return fmt.Errorf("事务仓储构造函数和回调不能为空")
	}
	return Transaction(ctx, db, func(txCtx context.Context) error { return fn(use(txCtx)) }, opts...)
}

// TransactionAsCtx 是 TransactionAs 的同义入口，自动保留请求 Context。
func TransactionAsCtx[Q any](ctx context.Context, db *Database, use func(context.Context) Q, fn func(Q) error, opts ...options.Lister[options.TransactionOptions]) error {
	return TransactionAs(ctx, db, use, fn, opts...)
}

type transactionBinding struct {
	ctx   context.Context
	scope *transactionScope
	err   error
}

// WithTx 返回绑定事务的仓储副本，不修改原仓储，也不开启或提交事务。
// txCtx 必须来自本库事务入口；无效绑定在执行时返回错误，绝不回退到事务外。
// 生成仓储的 WithTx 返回完整业务接口；手写扩展应同样复制自身并替换内嵌执行器。
func (r *Repository[T, K]) WithTx(txCtx context.Context) *Repository[T, K] {
	copy := *r
	binding := &transactionBinding{err: ErrUnmanagedTransaction}
	if txCtx != nil {
		binding.ctx = resolveContext(txCtx)
		binding.scope, _ = binding.ctx.Value(transactionScopeKey{}).(*transactionScope)
		if binding.scope != nil && mongo.SessionFromContext(binding.ctx) == binding.scope.session {
			binding.err = nil
		}
	}
	copy.tx = binding
	return &copy
}

// transactionValues 保留当前请求值，并用事务入口 Context 补齐租户、操作人等值。
// Session 与作用域单独强制绑定，不能被调用方的另一个事务覆盖。
type transactionValues struct {
	context.Context
	fallback context.Context
}

func (c transactionValues) Value(key any) any {
	if value := c.Context.Value(key); value != nil {
		return value
	}
	return c.fallback.Value(key)
}

func noContextCleanup() {}

// operationContext 在每个实际读写入口解析绑定；cleanup 释放合并取消信号的资源。
// 不使用全局事务变量，因此不同请求与仓储副本不会互相切换连接。
func operationContext(ctx context.Context, db *Database, binding *transactionBinding) (context.Context, func(), error) {
	if ctx == nil {
		return nil, noContextCleanup, fmt.Errorf("操作上下文不能为空")
	}
	ctx = resolveContext(ctx)
	if binding == nil {
		if scope, _ := ctx.Value(transactionScopeKey{}).(*transactionScope); scope != nil {
			if err := scope.check(db); err != nil {
				return nil, noContextCleanup, err
			}
			if mongo.SessionFromContext(ctx) != scope.session {
				return nil, noContextCleanup, ErrTransactionMismatch
			}
		}
		return ctx, noContextCleanup, nil
	}
	if binding.err != nil {
		return nil, noContextCleanup, binding.err
	}
	if err := binding.scope.check(db); err != nil {
		return nil, noContextCleanup, err
	}
	if session := mongo.SessionFromContext(ctx); session != nil && session != binding.scope.session {
		return nil, noContextCleanup, ErrTransactionMismatch
	}
	if scope, _ := ctx.Value(transactionScopeKey{}).(*transactionScope); scope != nil && scope != binding.scope {
		return nil, noContextCleanup, ErrTransactionMismatch
	}
	if err := binding.ctx.Err(); err != nil {
		return nil, noContextCleanup, err
	}
	if ctx.Value(transactionScopeKey{}) == binding.scope {
		return ctx, noContextCleanup, nil
	}
	merged := context.Context(transactionValues{Context: ctx, fallback: binding.ctx})
	var cancel context.CancelFunc
	if deadline, ok := binding.ctx.Deadline(); ok {
		merged, cancel = context.WithDeadline(merged, deadline)
	} else {
		merged, cancel = context.WithCancel(merged)
	}
	stop := context.AfterFunc(binding.ctx, cancel)
	merged = mongo.NewSessionContext(merged, binding.scope.session)
	merged = context.WithValue(merged, transactionScopeKey{}, binding.scope)
	merged = context.WithValue(merged, txCacheKey{}, binding.scope.caches)
	return merged, func() { stop(); cancel() }, nil
}

func (r *Repository[T, K]) operationContext(ctx context.Context) (context.Context, func(), error) {
	return operationContext(ctx, r.db, r.tx)
}

func (q *Aggregation) operationContext(ctx context.Context) (context.Context, func(), error) {
	if q == nil {
		return nil, noContextCleanup, fmt.Errorf("关联查询不能为空")
	}
	return operationContext(ctx, q.source.db, q.source.tx)
}
