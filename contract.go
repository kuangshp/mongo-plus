package mongoplus

import (
	"context"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Contract 定义生成 Repository 的基础能力；业务接口可嵌入后增加自定义方法。
type Contract[T any, K ID] interface {
	Source
	WithContext(context.Context) *Aggregation
	Create(context.Context, *T) error
	CreateBatch(context.Context, []*T) error
	FindById(context.Context, K, ...QueryOption) (*T, error)
	FindByIdList(context.Context, []K, ...QueryOption) ([]*T, error)
	FindOne(context.Context, ...QueryOption) (*T, error)
	FindOneWrapper(context.Context, func(*Wrapper), ...QueryOption) (*T, error)
	FindList(context.Context, ...QueryOption) ([]*T, error)
	FindListByWrapper(context.Context, func(*Wrapper), ...QueryOption) ([]*T, error)
	FindPage(context.Context, int64, int64, ...QueryOption) ([]*T, int64, error)
	FindPageByWrapper(context.Context, int64, int64, func(*Wrapper), ...QueryOption) ([]*T, int64, error)
	FindCursorPage(context.Context, *K, int64, ...QueryOption) (CursorPage[T, K], error)
	Walk(context.Context, func(*T) error, ...QueryOption) error
	Count(context.Context, ...QueryOption) (int64, error)
	CountByWrapper(context.Context, func(*Wrapper)) (int64, error)
	Exists(context.Context, ...QueryOption) (bool, error)
	ExistsByWrapper(context.Context, func(*Wrapper)) (bool, error)
	UpdateById(context.Context, K, ...UpdateOp) (*mongo.UpdateResult, error)
	UpdateMapById(context.Context, K, map[string]any) (*mongo.UpdateResult, error)
	UpdateByWrapper(context.Context, func(*Wrapper), ...UpdateOp) (*mongo.UpdateResult, error)
	DeleteById(context.Context, K) (int64, error)
	DeleteByWrapper(context.Context, func(*Wrapper)) (int64, error)
	RestoreById(context.Context, K) (int64, error)
	HardDeleteById(context.Context, K) (int64, error)
	HardDeleteByWrapper(context.Context, func(*Wrapper)) (int64, error)
	Native() *mongo.Collection
}
