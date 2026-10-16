package mongoplus

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// DALQuery 按位置参数执行 find/aggregate 文件，返回多条记录；大结果集使用 DALWalk。
func DALQuery[T any](ctx context.Context, file string, args ...any) ([]T, error) {
	ctx = resolveContext(ctx)
	dal, err := resolveDAL(ctx)
	if err != nil {
		return nil, err
	}
	var rows []T
	err = dal.run(ctx, file, args, func() error {
		command, err := dal.command(file, args, nil)
		if err != nil {
			return err
		}
		rows, err = runDALFind[T](ctx, dal, command)
		return err
	})
	return rows, err
}

// DALQueryOne 查询一条记录。不存在时返回 (nil, nil)。
func DALQueryOne[T any](ctx context.Context, file string, args ...any) (*T, error) {
	ctx = resolveContext(ctx)
	dal, err := resolveDAL(ctx)
	if err != nil {
		return nil, err
	}
	var row *T
	err = dal.run(ctx, file, args, func() error {
		command, err := dal.command(file, args, nil)
		if err != nil {
			return err
		}
		if command.limit == nil && (command.op == "find" || command.op == "findOne") {
			limit := int64(1)
			command.limit = &limit
		}
		row, err = runDALFindOne[T](ctx, dal, command)
		return err
	})
	return row, err
}

// DALQueryNamed 按命名参数执行 find/aggregate 文件。
func DALQueryNamed[T any](ctx context.Context, file string, params map[string]any) ([]T, error) {
	params = nonNilDALParams(params)
	ctx = resolveContext(ctx)
	dal, err := resolveDAL(ctx)
	if err != nil {
		return nil, err
	}
	var rows []T
	err = dal.run(ctx, file, namedArgs(params), func() error {
		command, err := dal.command(file, nil, params)
		if err != nil {
			return err
		}
		rows, err = runDALFind[T](ctx, dal, command)
		return err
	})
	return rows, err
}

// DALQueryOneNamed 按命名参数查询一条记录。不存在时返回 (nil, nil)。
func DALQueryOneNamed[T any](ctx context.Context, file string, params map[string]any) (*T, error) {
	params = nonNilDALParams(params)
	ctx = resolveContext(ctx)
	dal, err := resolveDAL(ctx)
	if err != nil {
		return nil, err
	}
	var row *T
	err = dal.run(ctx, file, namedArgs(params), func() error {
		command, err := dal.command(file, nil, params)
		if err != nil {
			return err
		}
		row, err = runDALFindOne[T](ctx, dal, command)
		return err
	})
	return row, err
}

// DALCount 执行 count 文件。位置参数和命名参数都可以传，命名参数使用 map。
func DALCount(ctx context.Context, file string, args ...any) (int64, error) {
	ctx = resolveContext(ctx)
	dal, err := resolveDAL(ctx)
	if err != nil {
		return 0, err
	}
	var total int64
	err = dal.run(ctx, file, args, func() error {
		var command dalCommand
		if len(args) == 1 {
			if params, ok := args[0].(map[string]any); ok {
				command, err = dal.command(file, nil, nonNilDALParams(params))
			}
		}
		if err == nil && command.collection == "" {
			command, err = dal.command(file, args, nil)
		}
		if err != nil {
			return err
		}
		total, err = runDALCount(ctx, dal, command)
		return err
	})
	return total, err
}

// DALQueryPage 执行数据文件和自动推导的 count_ 文件。
// filterArgs 同时传给两边，pageArgs 只传给数据文件，顺序在过滤参数之后。
func DALQueryPage[T any](ctx context.Context, file string, filterArgs []any, pageArgs []any) (DALPageResult[T], error) {
	ctx = resolveContext(ctx)
	dal, err := resolveDAL(ctx)
	if err != nil {
		return DALPageResult[T]{}, err
	}
	var result DALPageResult[T]
	err = dal.run(ctx, file, append(append([]any{}, filterArgs...), pageArgs...), func() error {
		command, err := dal.command(file, append(append([]any{}, filterArgs...), pageArgs...), nil)
		if err != nil {
			return err
		}
		countName := countFileName(file)
		countCommand, err := dal.command(countName, filterArgs, nil)
		if err != nil {
			return err
		}
		result.List, err = runDALFind[T](ctx, dal, command)
		if err != nil {
			return err
		}
		result.Total, err = runDALCount(ctx, dal, countCommand)
		return err
	})
	return result, err
}

// DALQueryPageNamed 执行带命名参数的分页文件。计数文件不引用 limit 和 offset 即可。
func DALQueryPageNamed[T any](ctx context.Context, file string, params map[string]any) (DALPageResult[T], error) {
	params = nonNilDALParams(params)
	ctx = resolveContext(ctx)
	dal, err := resolveDAL(ctx)
	if err != nil {
		return DALPageResult[T]{}, err
	}
	var result DALPageResult[T]
	err = dal.run(ctx, file, namedArgs(params), func() error {
		command, err := dal.command(file, nil, params)
		if err != nil {
			return err
		}
		countCommand, err := dal.command(countFileName(file), nil, params)
		if err != nil {
			return err
		}
		result.List, err = runDALFind[T](ctx, dal, command)
		if err != nil {
			return err
		}
		result.Total, err = runDALCount(ctx, dal, countCommand)
		return err
	})
	return result, err
}

// DALExec 执行单条写入、bulkWrite 或以 $out/$merge 结束的聚合文件。
func DALExec(ctx context.Context, file string, args ...any) error {
	_, err := DALExecAffected(ctx, file, args...)
	return err
}

// DALExecAffected 执行写文件并返回命中数量。
func DALExecAffected(ctx context.Context, file string, args ...any) (*DALExecResult, error) {
	ctx = resolveContext(ctx)
	dal, err := resolveDAL(ctx)
	if err != nil {
		return nil, err
	}
	var result *DALExecResult
	err = dal.run(ctx, file, args, func() error {
		command, err := dal.command(file, args, nil)
		if err != nil {
			return err
		}
		result, err = runDALWrite(ctx, dal, command)
		return err
	})
	return result, err
}

// DALExecNamed 是 DALExec 的命名参数入口。
func DALExecNamed(ctx context.Context, file string, params map[string]any) error {
	_, err := DALExecAffectedNamed(ctx, file, params)
	return err
}

// DALExecAffectedNamed 执行命名参数写文件；批量明细及部分成功信息使用 DALBulkWriteNamed。
func DALExecAffectedNamed(ctx context.Context, file string, params map[string]any) (*DALExecResult, error) {
	ctx = resolveContext(ctx)
	params = nonNilDALParams(params)
	dal, err := resolveDAL(ctx)
	if err != nil {
		return nil, err
	}
	var result *DALExecResult
	err = dal.run(ctx, file, namedArgs(params), func() error {
		command, err := dal.command(file, nil, params)
		if err != nil {
			return err
		}
		result, err = runDALWrite(ctx, dal, command)
		return err
	})
	return result, err
}

// DALWithTx 在当前 DAL 的数据库上开启事务。回调里的 DAL 查询会使用 txCtx。
func DALWithTx(ctx context.Context, fn func(txCtx context.Context) error) error {
	ctx = resolveContext(ctx)
	if fn == nil {
		return fmt.Errorf("DAL 事务回调不能为空")
	}
	dal, err := resolveDAL(ctx)
	if err != nil {
		return err
	}
	db, err := dalDatabase(ctx, dal)
	if err != nil {
		return err
	}
	return Transaction(ctx, db, func(txCtx context.Context) error {
		txCtx = context.WithValue(txCtx, dalDatabaseKey{}, dalDatabaseBinding{dal: dal, db: db})
		return fn(WithDALDB(txCtx, dal))
	})
}

// DALMustExec 在失败时 panic，只适合启动阶段。
func DALMustExec(ctx context.Context, file string, args ...any) {
	if err := DALExec(ctx, file, args...); err != nil {
		panic(err)
	}
}

// DALMustQueryOne 在失败或不存在时 panic。
func DALMustQueryOne[T any](ctx context.Context, file string, args ...any) *T {
	row, err := DALQueryOne[T](ctx, file, args...)
	if err != nil || row == nil {
		panic(fmt.Errorf("查询 %s 失败: %w", file, err))
	}
	return row
}

func namedArgs(params map[string]any) []any {
	args := make([]any, 0, len(params)*2)
	for key, value := range params {
		args = append(args, key, value)
	}
	return args
}

func dalHookArgs(args []any, params map[string]any) []any {
	if params != nil {
		return namedArgs(params)
	}
	return args
}

func dalCollection(ctx context.Context, dal *DAL, command dalCommand) (*mongo.Collection, error) {
	db, err := dalDatabase(ctx, dal)
	if err != nil {
		return nil, err
	}
	return db.native.Collection(command.collection), nil
}

func runDALFind[T any](ctx context.Context, dal *DAL, command dalCommand) ([]T, error) {
	cursor, err := dalReadCursor(ctx, dal, command)
	if err != nil {
		return nil, err
	}
	defer closeDALCursor(ctx, cursor)
	rows := []T{}
	if err = cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	return rows, nil
}

func runDALFindOne[T any](ctx context.Context, dal *DAL, command dalCommand) (*T, error) {
	if command.op == "find" || command.op == "findOne" {
		value := int64(1)
		command.limit = &value
	}
	cursor, err := dalReadCursor(ctx, dal, command)
	if err != nil {
		return nil, err
	}
	defer closeDALCursor(ctx, cursor)
	if !cursor.Next(ctx) {
		return nil, cursor.Err()
	}
	var row T
	if err := cursor.Decode(&row); err != nil {
		return nil, err
	}
	return &row, nil
}

func runDALCount(ctx context.Context, dal *DAL, command dalCommand) (int64, error) {
	if command.op == "aggregate" {
		cursor, err := dalReadCursor(ctx, dal, command)
		if err != nil {
			return 0, err
		}
		defer closeDALCursor(ctx, cursor)
		if !cursor.Next(ctx) {
			return 0, cursor.Err()
		}
		var row bson.D
		if err := cursor.Decode(&row); err != nil {
			return 0, err
		}
		if cursor.Next(ctx) {
			return 0, fmt.Errorf("聚合计数必须返回一行 total")
		}
		if err := cursor.Err(); err != nil {
			return 0, err
		}
		for _, item := range row {
			if item.Key == "total" {
				n, err := dalInt(item.Value)
				if err != nil || n < 0 {
					return 0, fmt.Errorf("total 必须是非负整数")
				}
				return n, nil
			}
		}
		return 0, fmt.Errorf("聚合计数缺少 total 字段")
	}
	if command.op != "count" {
		return 0, fmt.Errorf("DALCount 需要 op=count 或返回 total 的 aggregate")
	}
	if len(command.options) != 0 {
		return 0, fmt.Errorf("count options 请通过 DALNative 配置")
	}
	collection, err := dalCollection(ctx, dal, command)
	if err != nil {
		return 0, err
	}
	return collection.CountDocuments(ctx, filterOrEmpty(command.filter))
}

func runDALWrite(ctx context.Context, dal *DAL, command dalCommand) (*DALExecResult, error) {
	if command.op == "aggregate" {
		return runDALWriteAggregate(ctx, dal, command)
	}
	if command.op == "bulkWrite" {
		result, err := runDALBulk(ctx, dal, command)
		if result == nil {
			return nil, err
		}
		return &DALExecResult{RowsAffected: result.InsertedCount + result.MatchedCount + result.DeletedCount + result.UpsertedCount}, err
	}
	if len(command.options) != 0 {
		return nil, fmt.Errorf("单条写入 options 请通过 DALBulkWrite 或 DALNative 配置")
	}
	db, err := dalDatabase(ctx, dal)
	if err != nil {
		return nil, err
	}
	collection := db.native.Collection(command.collection)
	defer invalidateDALWrites(ctx, db, append([]string{command.collection}, command.invalidates...))
	switch command.op {
	case "insertOne":
		result, err := collection.InsertOne(ctx, command.document)
		if err != nil {
			return nil, err
		}
		if result == nil || !result.Acknowledged {
			return nil, ErrUnacknowledgedWrite
		}
		return &DALExecResult{RowsAffected: 1}, nil
	case "update", "updateOne", "updateMany":
		var result *mongo.UpdateResult
		if command.op == "updateOne" {
			result, err = collection.UpdateOne(ctx, filterOrEmpty(command.filter), command.update)
		} else {
			result, err = collection.UpdateMany(ctx, filterOrEmpty(command.filter), command.update)
		}
		if err != nil {
			return nil, err
		}
		if result == nil || !result.Acknowledged {
			return nil, ErrUnacknowledgedWrite
		}
		return &DALExecResult{RowsAffected: result.MatchedCount}, nil
	case "delete", "deleteOne", "deleteMany":
		var result *mongo.DeleteResult
		if command.op == "deleteOne" {
			result, err = collection.DeleteOne(ctx, filterOrEmpty(command.filter))
		} else {
			result, err = collection.DeleteMany(ctx, filterOrEmpty(command.filter))
		}
		if err != nil {
			return nil, err
		}
		if result == nil || !result.Acknowledged {
			return nil, ErrUnacknowledgedWrite
		}
		return &DALExecResult{RowsAffected: result.DeletedCount}, nil
	default:
		return nil, fmt.Errorf("文件操作 %s 不是写操作", command.op)
	}
}

func filterOrEmpty(filter bson.D) bson.D {
	if filter == nil {
		return bson.D{}
	}
	return filter
}
