package repository

import (
	mongoplus "github.com/kuangshp/mongo-plus"
)

// EventRepository 是业务扩展接口；此文件首次创建后不再覆盖。
// 基础读写在 iDefaultEventRepository。自定义查询加在 customEventRepository 上。
type EventRepository interface {
	iDefaultEventRepository
}

type customEventRepository struct {
	*defaultEventRepository
}

var _ EventRepository = (*customEventRepository)(nil)

// NewEventRepository 创建仓储并复用传入的数据库连接。
func NewEventRepository(db *mongoplus.Database) EventRepository {
	return &customEventRepository{newDefaultEventRepository(db)}
}

// 自定义查询示例。取消注释后按实际字段调整，并补上 context、time 与 dao 的导入。
// 生成的 Find/Count 已处理租户、逻辑删除；WithCache 只缓存这一次查询。
// 直接使用 Native 写入时，需要自行调用 db.InvalidateCache。
//
// func (r *customEventRepository) FindRecent(ctx context.Context, limit int64) ([]*model.EventEntity, error) {
// 	return r.FindList(ctx, mongoplus.Query().
// 		Order(dao.EventEntity.ID.Desc()).
// 		Limit(limit).
// 		WithCache(30*time.Second).
// 		Build())
// }
