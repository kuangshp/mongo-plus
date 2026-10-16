package mongoplus

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type dalDatabaseKey struct{}
type dalDatabaseBinding struct {
	dal       *DAL
	db        *Database
	selection *dalDatabaseSelection
}

// 一次 DAL 操作（包括分页的查询和计数）只允许 Provider 选择一次数据源。
// 延迟选择保留文件解析错误的优先级，不在调用方 Context 中留下跨操作的选择结果。
type dalDatabaseSelection struct {
	once sync.Once
	db   *Database
	err  error
}

// WithDAL 在业务仓储扩展中绑定 DAL，沿用该仓储的数据源和 WithTx 事务。
// 回调中把 queryCtx 传给 DAL 函数；不要保留回调 Context 或换回请求原 Context。
// DAL 执行时追加该仓储所属数据库上的租户、数据权限和逻辑删除。文件里的条件继续生效。
func (r *Repository[T, K]) WithDAL(ctx context.Context, dal *DAL, fn func(queryCtx context.Context) error) error {
	if dal == nil || fn == nil {
		return fmt.Errorf("DAL 和仓储回调不能为空")
	}
	queryCtx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	queryCtx = WithDALDB(queryCtx, dal)
	queryCtx = context.WithValue(queryCtx, dalDatabaseKey{}, dalDatabaseBinding{dal: dal, db: r.db})
	return fn(queryCtx)
}

// dalDatabase 在操作和事务内固定数据库，隔离检查、执行和缓存失效必须使用同一实例。
func dalDatabase(ctx context.Context, dal *DAL) (*Database, error) {
	var db *Database
	var err error
	if binding, ok := ctx.Value(dalDatabaseKey{}).(dalDatabaseBinding); ok && binding.dal == dal {
		db = binding.db
		if binding.selection != nil {
			binding.selection.once.Do(func() {
				binding.selection.db, binding.selection.err = dal.provider.Get(ctx)
			})
			db, err = binding.selection.db, binding.selection.err
		}
	} else {
		db, err = dal.provider.Get(ctx)
	}
	if err != nil {
		return nil, err
	}
	if db == nil || db.native == nil {
		return nil, fmt.Errorf("DAL 数据库不能为空")
	}
	_, cleanup, err := operationContext(ctx, db, nil)
	defer cleanup()
	return db, err
}

// DALNative 在 DAL 所选数据库上调用官方 API，供文件格式未覆盖的选项和操作使用。
// writes 必须列出当前数据库中可能写入的集合；即使回调部分失败，也会保守失效。
// 普通调用立即失效，本库事务在提交后失效；空 writes 表示只读，须由业务保证。
func DALNative(ctx context.Context, writes []string, fn func(context.Context, *mongo.Database) error) error {
	ctx = resolveContext(ctx)
	if fn == nil {
		return fmt.Errorf("DAL 原生回调不能为空")
	}
	for _, name := range writes {
		if !validDALCollection(name) {
			return fmt.Errorf("无效的写入集合 %q", name)
		}
	}
	dal, err := resolveDAL(ctx)
	if err != nil {
		return err
	}
	return dal.run(ctx, "<native>", nil, func(ctx context.Context) error {
		db, err := dalDatabase(ctx, dal)
		if err != nil {
			return err
		}
		defer invalidateDALWrites(ctx, db, writes)
		return fn(ctx, db.native)
	})
}

func invalidateDALWrites(ctx context.Context, db *Database, names []string) {
	seen := make(map[string]bool, len(names))
	for _, name := range names {
		if !seen[name] {
			seen[name] = true
			db.invalidateWrite(ctx, name)
		}
	}
}

// closeDALCursor 在请求取消后仍尝试释放服务端游标，但限制清理等待时间。
func closeDALCursor(ctx context.Context, cursor *mongo.Cursor) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	_ = cursor.Close(cleanupCtx)
}

// DALWalk 按游标逐条解码 find/aggregate 文件，避免将大结果集整体放入内存。
// 回调返回错误即停止；该函数负责关闭游标，不能用于写入聚合或 watch。
func DALWalk[T any](ctx context.Context, file string, consume func(T) error, args ...any) error {
	return dalWalk(ctx, file, consume, args, nil)
}

// DALWalkNamed 是 DALWalk 的命名参数入口。
func DALWalkNamed[T any](ctx context.Context, file string, params map[string]any, consume func(T) error) error {
	return dalWalk(ctx, file, consume, nil, nonNilDALParams(params))
}

func dalWalk[T any](ctx context.Context, file string, consume func(T) error, args []any, params map[string]any) error {
	ctx = resolveContext(ctx)
	if consume == nil {
		return fmt.Errorf("DAL 流式回调不能为空")
	}
	dal, err := resolveDAL(ctx)
	if err != nil {
		return err
	}
	return dal.run(ctx, file, dalHookArgs(args, params), func(ctx context.Context) error {
		command, err := dal.command(file, args, params)
		if err != nil {
			return err
		}
		cursor, err := dalReadCursor(ctx, dal, command)
		if err != nil {
			return err
		}
		defer closeDALCursor(ctx, cursor)
		for cursor.Next(ctx) {
			var row T
			if err := cursor.Decode(&row); err != nil {
				return err
			}
			if err := consume(row); err != nil {
				return err
			}
		}
		return cursor.Err()
	})
}

// DALWatch 打开集合级变更流；调用方负责 Next/Decode/Err/Close 和持久化 ResumeToken。
// Hook 仅覆盖打开订阅；不自动确认事件，也不在当前 Session/事务内打开订阅。
func DALWatch(ctx context.Context, file string, args ...any) (*mongo.ChangeStream, error) {
	return dalWatch(ctx, file, args, nil)
}

// DALWatchNamed 支持将持久化的 BSON ResumeToken 作为命名参数传入文件。
func DALWatchNamed(ctx context.Context, file string, params map[string]any) (*mongo.ChangeStream, error) {
	return dalWatch(ctx, file, nil, nonNilDALParams(params))
}

func dalWatch(ctx context.Context, file string, args []any, params map[string]any) (*mongo.ChangeStream, error) {
	ctx = resolveContext(ctx)
	dal, err := resolveDAL(ctx)
	if err != nil {
		return nil, err
	}
	var stream *mongo.ChangeStream
	err = dal.run(ctx, file, dalHookArgs(args, params), func(ctx context.Context) error {
		if mongo.SessionFromContext(ctx) != nil {
			return fmt.Errorf("DALWatch 不支持 Session 或事务上下文")
		}
		command, err := dal.command(file, args, params)
		if err != nil {
			return err
		}
		command, err = applyDALScope(ctx, dal, command)
		if err != nil {
			return err
		}
		if command.op != "watch" {
			return fmt.Errorf("DALWatch 需要 op=watch")
		}
		pipeline, err := dalPipeline(command, false)
		if err != nil {
			return err
		}
		opts, err := dalWatchOptions(command.options)
		if err != nil {
			return err
		}
		collection, err := dalCollection(ctx, dal, command)
		if err != nil {
			return err
		}
		stream, err = collection.Watch(ctx, pipeline, opts)
		return err
	})
	return stream, err
}

func nonNilDALParams(params map[string]any) map[string]any {
	if params == nil {
		return map[string]any{}
	}
	return params
}

func dalPipeline(command dalCommand, writes bool) (mongo.Pipeline, error) {
	pipeline := make(mongo.Pipeline, 0, len(command.pipeline))
	for _, stage := range command.pipeline {
		document, err := dalDocument(stage)
		if err != nil || len(document) != 1 {
			return nil, fmt.Errorf("pipeline 阶段必须是单操作符文档")
		}
		if !writes && (document[0].Key == "$out" || document[0].Key == "$merge") {
			return nil, fmt.Errorf("写入聚合请通过 DALExec 执行")
		}
		pipeline = append(pipeline, document)
	}
	return pipeline, nil
}

func dalReadCursor(ctx context.Context, dal *DAL, command dalCommand) (*mongo.Cursor, error) {
	command, err := applyDALScope(ctx, dal, command)
	if err != nil {
		return nil, err
	}
	collection, err := dalCollection(ctx, dal, command)
	if err != nil {
		return nil, err
	}
	switch command.op {
	case "aggregate":
		pipeline, err := dalPipeline(command, false)
		if err != nil {
			return nil, err
		}
		opts, err := dalAggregateOptions(command.options)
		if err != nil {
			return nil, err
		}
		return collection.Aggregate(ctx, pipeline, opts)
	case "find", "findOne":
		opts := options.Find()
		if len(command.sort) > 0 {
			opts.SetSort(command.sort)
		}
		if len(command.projection) > 0 {
			opts.SetProjection(command.projection)
		}
		if command.limit != nil {
			if *command.limit < 0 {
				return nil, fmt.Errorf("limit 不能为负数")
			}
			opts.SetLimit(*command.limit)
		}
		if command.skip != nil {
			if *command.skip < 0 {
				return nil, fmt.Errorf("skip 不能为负数")
			}
			opts.SetSkip(*command.skip)
		}
		if command.op == "findOne" {
			opts.SetLimit(1)
		}
		for _, item := range command.options {
			switch item.Key {
			case "batchSize":
				value, err := dalBatchSize(item.Value)
				if err != nil {
					return nil, err
				}
				opts.SetBatchSize(value)
			case "collation":
				value, err := dalCollation(item.Value)
				if err != nil {
					return nil, err
				}
				opts.SetCollation(value)
			case "let":
				value, err := dalDocument(item.Value)
				if err != nil {
					return nil, err
				}
				opts.SetLet(value)
			case "hint":
				opts.SetHint(item.Value)
			case "comment":
				opts.SetComment(item.Value)
			case "allowDiskUse":
				value, err := dalBool(item.Value)
				if err != nil {
					return nil, err
				}
				opts.SetAllowDiskUse(value)
			default:
				return nil, fmt.Errorf("不支持的 find options.%s；其他选项使用 DALNative", item.Key)
			}
		}
		return collection.Find(ctx, filterOrEmpty(command.filter), opts)
	default:
		return nil, fmt.Errorf("操作 %s 不能通过查询入口执行", command.op)
	}
}

func dalBool(value any) (bool, error) {
	v, ok := value.(bool)
	if !ok {
		return false, fmt.Errorf("选项需要 bool，实际 %T", value)
	}
	return v, nil
}

func dalBatchSize(value any) (int32, error) {
	n, err := dalInt(value)
	if err != nil || n < 1 || n > math.MaxInt32 {
		return 0, fmt.Errorf("batchSize 必须在 1 到 %d 之间", math.MaxInt32)
	}
	return int32(n), nil
}

func dalAggregateOptions(doc bson.D) (*options.AggregateOptionsBuilder, error) {
	opts := options.Aggregate().SetAllowDiskUse(true)
	for _, item := range doc {
		switch item.Key {
		case "allowDiskUse":
			value, err := dalBool(item.Value)
			if err != nil {
				return nil, err
			}
			opts.SetAllowDiskUse(value)
		case "batchSize":
			value, err := dalBatchSize(item.Value)
			if err != nil {
				return nil, err
			}
			opts.SetBatchSize(value)
		case "collation":
			value, err := dalCollation(item.Value)
			if err != nil {
				return nil, err
			}
			opts.SetCollation(value)
		case "let":
			value, err := dalDocument(item.Value)
			if err != nil {
				return nil, err
			}
			opts.SetLet(value)
		case "hint":
			opts.SetHint(item.Value)
		case "comment":
			opts.SetComment(item.Value)
		default:
			return nil, fmt.Errorf("不支持的 aggregate options.%s；其他选项使用 DALNative", item.Key)
		}
	}
	return opts, nil
}

func dalWatchOptions(doc bson.D) (*options.ChangeStreamOptionsBuilder, error) {
	opts := options.ChangeStream()
	var resumeCount int
	for _, item := range doc {
		switch item.Key {
		case "collation":
			value, err := dalCollation(item.Value)
			if err != nil {
				return nil, err
			}
			opts.SetCollation(*value)
		case "showExpandedEvents":
			value, err := dalBool(item.Value)
			if err != nil {
				return nil, err
			}
			opts.SetShowExpandedEvents(value)
		case "batchSize":
			value, err := dalBatchSize(item.Value)
			if err != nil {
				return nil, err
			}
			opts.SetBatchSize(value)
		case "comment":
			opts.SetComment(item.Value)
		case "fullDocument", "fullDocumentBeforeChange":
			value, ok := item.Value.(string)
			valid := value == "whenAvailable" || value == "required"
			if item.Key == "fullDocument" {
				valid = valid || value == "default" || value == "updateLookup"
			} else {
				valid = valid || value == "off"
			}
			if !ok || !valid {
				return nil, fmt.Errorf("无效的 %s", item.Key)
			}
			if item.Key == "fullDocument" {
				opts.SetFullDocument(options.FullDocument(value))
			} else {
				opts.SetFullDocumentBeforeChange(options.FullDocument(value))
			}
		case "maxAwaitTimeMS":
			value, err := dalInt(item.Value)
			if err != nil || value < 1 || value > 3600000 {
				return nil, fmt.Errorf("maxAwaitTimeMS 必须在 1 到 3600000 之间")
			}
			opts.SetMaxAwaitTime(time.Duration(value) * time.Millisecond)
		case "resumeAfter", "startAfter":
			if item.Value == nil {
				continue
			}
			value, err := dalDocument(item.Value)
			if err != nil || len(value) == 0 {
				return nil, fmt.Errorf("%s 必须是非空 BSON 文档", item.Key)
			}
			resumeCount++
			if item.Key == "resumeAfter" {
				opts.SetResumeAfter(value)
			} else {
				opts.SetStartAfter(value)
			}
		case "startAtOperationTime":
			if item.Value == nil {
				continue
			}
			value, ok := item.Value.(bson.Timestamp)
			if !ok {
				return nil, fmt.Errorf("startAtOperationTime 需要 BSON Timestamp")
			}
			resumeCount++
			opts.SetStartAtOperationTime(&value)
		default:
			return nil, fmt.Errorf("不支持的 watch options.%s；其他选项使用 DALNative", item.Key)
		}
	}
	if resumeCount > 1 {
		return nil, fmt.Errorf("resumeAfter、startAfter、startAtOperationTime 不能同时设置")
	}
	return opts, nil
}

// runDALWriteAggregate 通过 Exec 显式执行 $out/$merge，并追踪实际的同库目标集合。
func runDALWriteAggregate(ctx context.Context, dal *DAL, command dalCommand) (*DALExecResult, error) {
	if mongo.SessionFromContext(ctx) != nil {
		return nil, fmt.Errorf("$out/$merge 写入聚合不能在事务或显式 Session 中执行")
	}
	db, err := dalDatabase(ctx, dal)
	if err != nil {
		return nil, err
	}
	pipeline, err := dalPipeline(command, true)
	if err != nil {
		return nil, err
	}
	if len(pipeline) == 0 {
		return nil, fmt.Errorf("写入聚合不能为空")
	}
	last := pipeline[len(pipeline)-1][0]
	if last.Key != "$out" && last.Key != "$merge" {
		return nil, fmt.Errorf("DALExec 聚合必须以 $out/$merge 结束")
	}
	target := last.Value
	if last.Key == "$merge" {
		if doc, ok := target.(bson.D); ok {
			target = nil
			for _, item := range doc {
				if item.Key == "into" {
					target = item.Value
				}
			}
		}
	}
	name, ok := target.(string)
	if !ok {
		doc, err := dalDocument(target)
		if err != nil {
			return nil, fmt.Errorf("无效写入聚合目标")
		}
		for _, item := range doc {
			switch item.Key {
			case "db":
				if value, ok := item.Value.(string); !ok || value != db.native.Name() {
					return nil, fmt.Errorf("跨数据库写入聚合请使用 DALNative 并处理目标库缓存")
				}
			case "coll":
				name, _ = item.Value.(string)
			}
		}
	}
	if !validDALCollection(name) {
		return nil, fmt.Errorf("无效写入聚合目标集合")
	}
	opts, err := dalAggregateOptions(command.options)
	if err != nil {
		return nil, err
	}
	defer invalidateDALWrites(ctx, db, append([]string{name}, command.invalidates...))
	// Aggregate 不返回 Acknowledged，显式 majority 避免 w=0 被误报为写入成功。
	collection := db.native.Collection(command.collection).Clone(options.Collection().SetWriteConcern(writeconcern.Majority()))
	cursor, err := collection.Aggregate(ctx, pipeline, opts)
	if err != nil {
		return nil, err
	}
	defer closeDALCursor(ctx, cursor)
	for cursor.Next(ctx) {
	} // 完成驱动游标，不能把服务端部分执行当作成功。
	if err := cursor.Err(); err != nil {
		return nil, err
	}
	return &DALExecResult{}, nil // MongoDB 不返回 $out/$merge 的影响行数。
}

// dalCollation 使用官方字段名，拒绝未知属性，避免拼写错误被静默忽略。
func dalCollation(value any) (*options.Collation, error) {
	doc, err := dalDocument(value)
	if err != nil {
		return nil, err
	}
	raw, err := bson.MarshalExtJSON(doc, false, false)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var c options.Collation
	if err = decoder.Decode(&c); err != nil {
		return nil, fmt.Errorf("collation: %w", err)
	}
	if c.Locale == "" {
		return nil, fmt.Errorf("collation.locale 不能为空")
	}
	return &c, nil
}
