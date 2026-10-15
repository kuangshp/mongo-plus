package mongoplus

import (
	"context"
	"fmt"
	"math"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type sequenceBlock struct {
	gate chan struct{}
	next int64
	end  int64
}

func (r *Repository[T, K]) sequenceRange(ctx context.Context, count int64) (int64, error) {
	if r.config.ID.BlockSize <= 1 {
		return r.reserveSequence(ctx, count)
	}
	state, ok := r.db.sequences.Load(r.config.Name)
	if !ok {
		state, _ = r.db.sequences.LoadOrStore(r.config.Name, &sequenceBlock{gate: make(chan struct{}, 1)})
	}
	block := state.(*sequenceBlock)
	select {
	case block.gate <- struct{}{}:
		defer func() { <-block.gate }()
	case <-ctx.Done():
		return 0, ctx.Err()
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if block.next > 0 && count <= block.end-block.next+1 {
		first := block.next
		block.next += count
		return first, nil
	}
	size := max(count, r.config.ID.BlockSize)
	first, err := r.reserveSequence(ctx, size)
	if err != nil {
		return 0, err
	}
	block.next, block.end = first+count, first+size-1
	return first, nil
}

func (r *Repository[T, K]) reserveSequence(ctx context.Context, count int64) (int64, error) {
	if count <= 0 {
		return 0, fmt.Errorf("自增号码申请数量必须大于零")
	}
	// 序列独立于业务事务；失败、重试、重启可能跳号，保留 majority 确认。
	ctx = mongo.NewSessionContext(ctx, nil)
	var seq struct {
		Value int64 `bson:"value"`
	}
	for attempt := 0; attempt < 3; attempt++ {
		err := r.db.native.Collection("__mongo_plus_sequences", options.Collection().SetWriteConcern(writeconcern.Majority())).FindOneAndUpdate(ctx,
			bson.D{{Key: "_id", Value: r.config.Name}, {Key: "value", Value: bson.D{{Key: "$lte", Value: int64(math.MaxInt64) - count}}}},
			bson.D{{Key: "$inc", Value: bson.D{{Key: "value", Value: count}}}},
			options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After)).Decode(&seq)
		if mongo.IsDuplicateKeyError(err) {
			continue // 初始化并发冲突；达到上限时也不会修改现有序列。
		}
		if err != nil {
			return 0, err
		}
		if seq.Value < count {
			return 0, fmt.Errorf("自增主键序列无效")
		}
		return seq.Value - count + 1, nil
	}
	return 0, fmt.Errorf("自增序列达到上限或初始化冲突，请检查序列后重试")
}
