package mongoplus

import (
	"context"
	"fmt"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Aggregate 执行只读单集合聚合，自动在入口合并租户和逻辑删除条件。
// 返回类型由结果结构体决定；跨集合管道请在仓储扩展中显式使用 Native 并处理隔离。
func Aggregate[R any, T any, K ID](ctx context.Context, repo *Repository[T, K], pipeline mongo.Pipeline, opts ...QueryOption) ([]R, error) {
	if repo == nil {
		return nil, fmt.Errorf("聚合仓储不能为空")
	}
	ctx, cleanup, err := repo.operationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	o := mergeOptions(opts)
	if err = aggregateOption(o); err != nil {
		return nil, err
	}
	filter, err := repo.filter(ctx, o, false)
	if err != nil {
		return nil, err
	}
	if err = validatePipeline(pipeline); err != nil {
		return nil, err
	}
	stages := mongo.Pipeline{}
	if len(filter) > 0 {
		stages = append(stages, bson.D{{Key: "$match", Value: filter}})
	}
	stages = append(stages, pipeline...)
	// QueryOption 的结果选项作用于用户管道最终输出，与链式聚合保持一致。
	stages = (&Aggregation{option: o}).finalStages(stages, o.sort, o.skip, o.limit)
	cursor, err := repo.collection.Aggregate(ctx, stages, aggregateDriverOptions(o))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := []R{}
	err = cursor.All(ctx, &result)
	if err == nil {
		err = repo.reveal(ctx, &result)
	}
	return result, err
}

func validatePipeline(pipeline mongo.Pipeline) error {
	for _, stage := range pipeline {
		if len(stage) != 1 {
			return fmt.Errorf("聚合阶段必须只有一个操作符")
		}
		switch stage[0].Key {
		case "$match", "$project", "$group", "$sort", "$limit", "$skip", "$unwind", "$addFields", "$set", "$unset", "$count", "$replaceRoot", "$replaceWith", "$sortByCount", "$sample":
		default:
			return fmt.Errorf("聚合阶段 %q 需要在自定义仓储通过 Native 显式处理", stage[0].Key)
		}
	}
	return nil
}

func aggregateDriverOptions(o QueryOption) *options.AggregateOptionsBuilder {
	opts := options.Aggregate().SetAllowDiskUse(true)
	if o.collation != nil {
		opts.SetCollation(o.collation)
	}
	if o.hint != nil {
		opts.SetHint(o.hint)
	}
	if o.let != nil {
		opts.SetLet(o.let)
	}
	if o.comment != nil {
		opts.SetComment(o.comment)
	}
	if o.batchSize > 0 {
		opts.SetBatchSize(o.batchSize)
	}
	if o.allowDiskUse != nil {
		opts.SetAllowDiskUse(*o.allowDiskUse)
	}
	return opts
}
