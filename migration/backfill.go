package migration

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Backfill 按 _id 有序分批执行可重入的 $set/$unset 更新，每批重新检查 filter。
// 推荐用 {field:{$exists:false}} 等条件，仅处理尚未完成的历史数据。
// 不在长事务中执行；出错返回此前成功批次的修改数，本批可能部分写入。
// 修复原因后可从头安全重跑。
// 回填期间仍需保证旧服务不会继续创建缺失新字段的数据。
func Backfill(ctx context.Context, collection *mongo.Collection, filter bson.D, update bson.D, batchSize int) (int64, error) {
	if collection == nil {
		return 0, fmt.Errorf("回填集合不能为空")
	}
	var modified int64
	err := WithLock(ctx, collection.Database(), func(lockedCtx context.Context) error {
		var backfillErr error
		modified, backfillErr = backfill(lockedCtx, collection, filter, update, batchSize)
		return backfillErr
	})
	return modified, err
}

func backfill(ctx context.Context, collection *mongo.Collection, filter bson.D, update bson.D, batchSize int) (int64, error) {
	if collection == nil || len(filter) == 0 || len(update) == 0 {
		return 0, fmt.Errorf("回填需要集合、显式过滤条件和更新内容")
	}
	if batchSize == 0 {
		batchSize = 500
	}
	if batchSize < 1 || batchSize > 10000 {
		return 0, fmt.Errorf("回填 batchSize 必须在 1 到 10000 之间")
	}
	for _, op := range update {
		if op.Key != "$set" && op.Key != "$unset" {
			return 0, fmt.Errorf("回填仅支持可重入的 $set/$unset")
		}
	}
	if mongo.SessionFromContext(ctx) != nil {
		return 0, fmt.Errorf("分批回填必须在独立发布命令中执行，不能传入 Session Context")
	}
	// MongoDB 范围查询存在 BSON 类型边界；混合类型主键不能直接用 _id > 游标。
	// 先检查类型，避免回填工具在跨类型边界时静默漏掉历史数据。
	types, err := collection.Aggregate(ctx, mongo.Pipeline{
		bson.D{{Key: "$match", Value: filter}},
		bson.D{{Key: "$group", Value: bson.D{{Key: "_id", Value: bson.D{{Key: "$type", Value: "$_id"}}}}}},
		bson.D{{Key: "$limit", Value: 3}},
	})
	if err != nil {
		return 0, err
	}
	var kinds []struct {
		Kind string `bson:"_id"`
	}
	err = types.All(ctx, &kinds)
	_ = types.Close(ctx)
	if err != nil {
		return 0, err
	}
	category := ""
	for _, kind := range kinds {
		current := kind.Kind
		if current == "int" || current == "long" {
			current = "integer"
		}
		if current != "integer" && current != "string" && current != "objectId" {
			return 0, fmt.Errorf("回填不支持 %s 类型的主键", kind.Kind)
		}
		if category != "" && category != current {
			return 0, fmt.Errorf("回填要求统一的整数、string 或 ObjectID 主键，不能混合 BSON 类型")
		}
		category = current
	}
	var after any
	var modified int64
	for {
		parts := bson.A{filter}
		if after != nil {
			parts = append(parts, bson.D{{Key: "_id", Value: bson.D{{Key: "$gt", Value: after}}}})
		}
		cursor, err := collection.Find(ctx, bson.D{{Key: "$and", Value: parts}}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetLimit(int64(batchSize)).SetProjection(bson.D{{Key: "_id", Value: 1}}))
		if err != nil {
			return modified, err
		}
		var rows []struct {
			ID any `bson:"_id"`
		}
		err = cursor.All(ctx, &rows)
		_ = cursor.Close(ctx)
		if err != nil {
			return modified, err
		}
		if len(rows) == 0 {
			return modified, nil
		}
		ids := make(bson.A, 0, len(rows))
		for _, row := range rows {
			if row.ID == nil {
				return modified, fmt.Errorf("回填要求非 null 的 _id")
			}
			ids = append(ids, row.ID)
		}
		result, err := collection.UpdateMany(ctx, bson.D{{Key: "$and", Value: bson.A{filter, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}}}}}, update)
		if err != nil {
			return modified, err
		}
		if result == nil || !result.Acknowledged {
			return modified, fmt.Errorf("回填写入未获得确认")
		}
		modified += result.ModifiedCount
		after = rows[len(rows)-1].ID
	}
}
