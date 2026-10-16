// Package mongoplus 为官方 MongoDB v2 驱动提供类型安全查询和 Repository。
package mongoplus

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kuangshp/mongo-plus/internal/settings"
	"github.com/kuangshp/mongo-plus/plugin"
	"github.com/kuangshp/mongo-plus/sf"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

var (
	ErrNotFound            = mongo.ErrNoDocuments
	ErrUnsafeWrite         = errors.New("批量写入必须指定业务条件，或显式调用 AllowAll")
	ErrTenantRequired      = errors.New("缺少租户信息")
	ErrInvalidID           = errors.New("主键类型或值无效")
	ErrUnacknowledgedWrite = errors.New("写入未获得确认，不能确定是否成功")
)

// IDConfig 通常只配置 Type；集合的 _id 映射为 Go 的 ID 字段。
// Generation 省略时：ObjectID 自动生成、int64 自增、string 由业务提供。
type IDConfig = settings.IDConfig

// IDType 是主键类型，推荐使用 IDTypeObjectID、IDTypeInt64、IDTypeString。
type IDType = settings.IDType

// IDGeneration 是主键生成方式，推荐使用对应的 IDGeneration 常量。
type IDGeneration = settings.IDGeneration

const (
	IDTypeObjectID IDType = settings.IDTypeObjectID // bson.ObjectID 主键。
	IDTypeInt64    IDType = settings.IDTypeInt64    // int64 主键。
	IDTypeString   IDType = settings.IDTypeString   // string 主键。

	IDGenerationAuto      IDGeneration = settings.IDGenerationAuto      // ObjectID 自动生成。
	IDGenerationIncrement IDGeneration = settings.IDGenerationIncrement // int64 自增。
	IDGenerationManual    IDGeneration = settings.IDGenerationManual    // 业务提供。
)

// CollectionConfig 是生成 Repository 携带的集合规则。
type CollectionConfig = settings.CollectionConfig

// Config 是运行时 MongoDB 连接配置，与 GeneratorConfig 分开。
type Config = settings.Config

// LogConfig 配置控制台命令日志，LogLevel 零值默认关闭。
type LogConfig = settings.LogConfig
type LogLevel = settings.LogLevel

const (
	LogSilent = settings.LogSilent
	LogError  = settings.LogError
	LogWarn   = settings.LogWarn
	LogInfo   = settings.LogInfo
)

// Observation 描述一次数据库操作，默认不记录文档和敏感参数。
type Observation struct {
	Collection string
	Operation  string
	Duration   time.Duration
	Err        error
}

// Observer 可用于接入项目的日志、慢查询或监控。
type Observer func(context.Context, Observation)

// Database 持有可复用的驱动数据库和可选监控回调。
type Database struct {
	native     *mongo.Database
	cache      readCache
	cacheStore sf.SFCache
	sequences  sync.Map // 集合名 -> 本包装器共享的预分配号码区间。
	Observer   Observer
	tenant     *plugin.TenantPlugin
	autofill   *plugin.AutoFillPlugin
	permission *plugin.DataPermission
	sensitive  *SensitivePlugin
}

// Connect 建立连接并 Ping；连接池由官方 Client 管理。
func Connect(ctx context.Context, c Config) (*Database, error) {
	if strings.TrimSpace(c.URI) == "" || strings.TrimSpace(c.Database) == "" {
		return nil, fmt.Errorf("uri、database 不能为空")
	}
	opts := options.Client().ApplyURI(c.URI)
	monitor, err := NewCommandLogger(c.Log)
	if err != nil {
		return nil, err
	}
	if monitor != nil {
		opts.SetMonitor(monitor)
	}
	if c.MaxPoolSize > 0 {
		opts.SetMaxPoolSize(c.MaxPoolSize)
	}
	if c.Timeout > 0 {
		opts.SetTimeout(c.Timeout)
	}
	client, err := mongo.Connect(opts)
	if err != nil {
		return nil, err
	}
	pingCtx := ctx
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		pingCtx, cancel = context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
	}
	if err = client.Ping(pingCtx, nil); err != nil {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Disconnect(closeCtx)
		return nil, err
	}
	return NewDatabase(client.Database(c.Database)), nil
}

// NewDatabase 使用业务项目已有的官方驱动数据库；不创建连接。
func NewDatabase(db *mongo.Database) *Database {
	d := &Database{native: db}
	registerCache(d)
	return d
}

var defaultDatabase atomic.Pointer[Database]

// Use 注册进程内默认数据库。之后生成的 NewXRepository() 可以不传连接，用法对应 gorm-plus 注册数据源后直接构造仓储。
// 传入 nil 会清除默认连接。显式 NewXRepository(db) 仍优先使用参数中的连接。
func Use(db *Database) {
	if db == nil {
		defaultDatabase.Store(nil)
		return
	}
	defaultDatabase.Store(db)
}

// DefaultDB 返回 Use 注册的数据库；尚未注册时为 nil。
func DefaultDB() *Database { return defaultDatabase.Load() }

// Native 返回原生数据库，供明确需要官方 API 的仓储扩展使用。
func (d *Database) Native() *mongo.Database { return d.native }

// Close 关闭 Client；由创建并拥有连接的启动入口调用。
func (d *Database) Close(ctx context.Context) error { return d.native.Client().Disconnect(ctx) }

type tenantKey struct{}
type actorKey struct{}

// WithTenant 为请求上下文设置租户；仓储从上下文读取，业务过滤不能覆盖它。
func WithTenant(ctx context.Context, id any) context.Context {
	ctx = resolveContext(ctx)
	ctx = context.WithValue(ctx, tenantKey{}, id)
	return plugin.WithTenantValue(ctx, id)
}

// WithActor 设置审计操作人，同时写入自动填充插件读取的操作人。
func WithActor(ctx context.Context, id any) context.Context {
	ctx = resolveContext(ctx)
	ctx = context.WithValue(ctx, actorKey{}, id)
	return plugin.WithOperator(ctx, id)
}

// Transaction 执行官方 Session 事务；回调必须使用 txCtx，且可能被驱动重试。
func Transaction(ctx context.Context, db *Database, fn func(txCtx context.Context) error, opts ...options.Lister[options.TransactionOptions]) error {
	ctx = resolveContext(ctx)
	if db == nil || db.native == nil || fn == nil {
		return fmt.Errorf("事务数据库和回调不能为空")
	}
	if mongo.SessionFromContext(ctx) != nil {
		return fmt.Errorf("不支持嵌套 Session 事务")
	}
	session, err := db.native.Client().StartSession()
	if err != nil {
		return err
	}
	defer session.EndSession(ctx)
	state := txCaches{}
	ctx = context.WithValue(ctx, txCacheKey{}, state)
	_, err = session.WithTransaction(ctx, func(txCtx context.Context) (any, error) { return nil, fn(txCtx) }, opts...)
	for database, collections := range state {
		for collection := range collections {
			database.InvalidateCache(collection)
		}
	}
	return err
}

// TransactionAs 在事务中构建业务项目自己的仓储集合，保持与 gorm-plus 类似的用法。
func TransactionAs[Q any](ctx context.Context, db *Database, use func(context.Context) Q, fn func(Q) error, opts ...options.Lister[options.TransactionOptions]) error {
	if use == nil || fn == nil {
		return fmt.Errorf("事务仓储构造函数和回调不能为空")
	}
	return Transaction(ctx, db, func(txCtx context.Context) error { return fn(use(txCtx)) }, opts...)
}

// DbPage 计算分页范围，与 gorm-plus 一样默认每页 10 条、最多 500 条。
func DbPage(page, size int64) (offset, limit int64) {
	if page < 1 {
		page = 1
	}
	if size <= 0 {
		size = 10
	}
	if size > 500 {
		size = 500
	}
	if page-1 > int64(^uint64(0)>>1)/size {
		page = 1
	}
	return (page - 1) * size, size
}
