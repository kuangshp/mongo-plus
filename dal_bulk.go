package mongoplus

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// DALBulkWrite 执行单集合混合批量文件，返回官方计数、UpsertedIDs 和可能的部分成功结果。
// error 非 nil 时仍应检查 result；有序和无序批量均不自动提供整体原子性。
func DALBulkWrite(ctx context.Context, file string, args ...any) (*mongo.BulkWriteResult, error) {
	return dalBulkWrite(ctx, file, args, nil)
}

// DALBulkWriteNamed 使用命名参数执行批量文件。
func DALBulkWriteNamed(ctx context.Context, file string, params map[string]any) (*mongo.BulkWriteResult, error) {
	return dalBulkWrite(ctx, file, nil, nonNilDALParams(params))
}

func dalBulkWrite(ctx context.Context, file string, args []any, params map[string]any) (*mongo.BulkWriteResult, error) {
	ctx = resolveContext(ctx)
	dal, err := resolveDAL(ctx)
	if err != nil {
		return nil, err
	}
	var result *mongo.BulkWriteResult
	err = dal.run(ctx, file, dalHookArgs(args, params), func(ctx context.Context) error {
		command, err := dal.command(file, args, params)
		if err != nil {
			return err
		}
		result, err = runDALBulk(ctx, dal, command)
		return err
	})
	return result, err
}

func runDALBulk(ctx context.Context, dal *DAL, command dalCommand) (*mongo.BulkWriteResult, error) {
	if command.op != "bulkWrite" {
		return nil, fmt.Errorf("DALBulkWrite 需要 op=bulkWrite")
	}
	// 在追加系统条件前验证业务过滤，AllowAll 不得改变租户/权限范围。
	if _, err := dalBulkModels(command); err != nil {
		return nil, err
	}
	command, err := applyDALScope(ctx, dal, command)
	if err != nil {
		return nil, err
	}
	models, err := dalBulkModels(command)
	if err != nil {
		return nil, err
	}
	opts := options.BulkWrite().SetOrdered(true)
	for _, item := range command.options {
		switch item.Key {
		case "ordered":
			value, err := dalBool(item.Value)
			if err != nil {
				return nil, err
			}
			opts.SetOrdered(value)
		case "let":
			value, err := dalDocument(item.Value)
			if err != nil {
				return nil, err
			}
			opts.SetLet(value)
		case "comment":
			opts.SetComment(item.Value)
		default:
			return nil, fmt.Errorf("不支持的 bulkWrite options.%s；其他选项使用 DALNative", item.Key)
		}
	}
	db, err := dalDatabase(ctx, dal)
	if err != nil {
		return nil, err
	}
	defer invalidateDALWrites(ctx, db, append([]string{command.collection}, command.invalidates...))
	result, err := db.native.Collection(command.collection).BulkWrite(ctx, models, opts)
	if err == nil && (result == nil || !result.Acknowledged) {
		return result, ErrUnacknowledgedWrite
	}
	return result, err
}

func dalBulkModels(command dalCommand) ([]mongo.WriteModel, error) {
	if len(command.operations) == 0 {
		return nil, fmt.Errorf("operations 不能为空")
	}
	models := make([]mongo.WriteModel, 0, len(command.operations))
	for i, raw := range command.operations {
		model, err := dalBulkModel(raw, command.allowAll)
		if err != nil {
			return nil, fmt.Errorf("operations[%d]: %w", i, err)
		}
		models = append(models, model)
	}
	return models, nil
}

func dalBulkModel(raw any, inheritedAllowAll bool) (mongo.WriteModel, error) {
	doc, err := dalDocument(raw)
	if err != nil {
		return nil, err
	}
	var op string
	var filter, document, replacement bson.D
	var update any
	var upsert bool
	var collation *options.Collation
	var sort bson.D
	var hint any
	var arrayFilters []any
	allowAll := inheritedAllowAll
	seen := make(map[string]bool)
	for _, item := range doc {
		if seen[item.Key] {
			return nil, fmt.Errorf("重复属性 %s", item.Key)
		}
		seen[item.Key] = true
		switch item.Key {
		case "op":
			op, _ = item.Value.(string)
		case "filter":
			filter, err = dalDocument(item.Value)
		case "document":
			document, err = dalDocument(item.Value)
		case "replacement":
			replacement, err = dalDocument(item.Value)
		case "update":
			switch value := item.Value.(type) {
			case bson.D:
				update = value
			case bson.A:
				update, err = dalPipeline(dalCommand{pipeline: value}, true)
			default:
				err = fmt.Errorf("update 必须是 BSON 文档或更新管道数组")
			}
		case "upsert":
			upsert, err = dalBool(item.Value)
		case "allowAll":
			allowAll, err = dalBool(item.Value)
		case "collation":
			collation, err = dalCollation(item.Value)
		case "sort":
			sort, err = dalDocument(item.Value)
		case "hint":
			hint = item.Value
		case "arrayFilters":
			value, ok := item.Value.(bson.A)
			if !ok {
				err = fmt.Errorf("arrayFilters 必须是数组")
			} else {
				arrayFilters = []any(value)
			}
		default:
			return nil, fmt.Errorf("不支持的模型属性 %s", item.Key)
		}
		if err != nil {
			return nil, err
		}
	}
	if op != "insertOne" && len(filter) == 0 && !allowAll {
		return nil, fmt.Errorf("批量写入模型必须提供 filter 或 allowAll=true")
	}
	// 拒绝与模型无关的配置，防止把拼错的参数静默当成已生效。
	allowed := map[string]bool{"op": true, "allowAll": true}
	switch op {
	case "insertOne":
		allowed["document"] = true
	case "updateOne", "updateMany":
		for _, key := range []string{"filter", "update", "upsert", "arrayFilters", "hint", "collation"} {
			allowed[key] = true
		}
	case "replaceOne":
		for _, key := range []string{"filter", "replacement", "upsert", "hint", "collation", "sort"} {
			allowed[key] = true
		}
	case "deleteOne", "deleteMany":
		allowed["filter"], allowed["hint"], allowed["collation"] = true, true, true
	default:
		return nil, fmt.Errorf("不支持的批量操作 %s", op)
	}
	if op == "updateOne" {
		allowed["sort"] = true
	}
	for key := range seen {
		if !allowed[key] {
			return nil, fmt.Errorf("%s 不支持属性 %s", op, key)
		}
	}
	switch op {
	case "insertOne":
		if document == nil {
			return nil, fmt.Errorf("insertOne 缺少 document")
		}
		return mongo.NewInsertOneModel().SetDocument(document), nil
	case "updateOne", "updateMany":
		if update == nil {
			return nil, fmt.Errorf("%s 缺少 update", op)
		}
		if op == "updateOne" {
			model := mongo.NewUpdateOneModel().SetFilter(filterOrEmpty(filter)).SetUpdate(update).SetUpsert(upsert)
			if collation != nil {
				model.SetCollation(collation)
			}
			if sort != nil {
				model.SetSort(sort)
			}
			if arrayFilters != nil {
				model.SetArrayFilters(arrayFilters)
			}
			if hint != nil {
				model.SetHint(hint)
			}
			return model, nil
		}
		model := mongo.NewUpdateManyModel().SetFilter(filterOrEmpty(filter)).SetUpdate(update).SetUpsert(upsert)
		if collation != nil {
			model.SetCollation(collation)
		}
		if arrayFilters != nil {
			model.SetArrayFilters(arrayFilters)
		}
		if hint != nil {
			model.SetHint(hint)
		}
		return model, nil
	case "replaceOne":
		if replacement == nil {
			return nil, fmt.Errorf("replaceOne 缺少 replacement")
		}
		model := mongo.NewReplaceOneModel().SetFilter(filterOrEmpty(filter)).SetReplacement(replacement).SetUpsert(upsert)
		if sort != nil {
			model.SetSort(sort)
		}
		if collation != nil {
			model.SetCollation(collation)
		}
		if hint != nil {
			model.SetHint(hint)
		}
		return model, nil
	case "deleteOne":
		model := mongo.NewDeleteOneModel().SetFilter(filterOrEmpty(filter))
		if collation != nil {
			model.SetCollation(collation)
		}
		if hint != nil {
			model.SetHint(hint)
		}
		return model, nil
	case "deleteMany":
		model := mongo.NewDeleteManyModel().SetFilter(filterOrEmpty(filter))
		if collation != nil {
			model.SetCollation(collation)
		}
		if hint != nil {
			model.SetHint(hint)
		}
		return model, nil
	}
	return nil, fmt.Errorf("无效批量操作")
}
