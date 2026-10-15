package repository

import (
	mongoplus "github.com/kuangshp/mongo-plus"
)

// EventRepository 是业务扩展接口；此文件首次创建后不再覆盖。
// 基础读写在 iDefaultEventRepository。自定义查询加在 customerEventRepository 上。
type EventRepository interface {
	iDefaultEventRepository
}

type customerEventRepository struct {
	*defaultEventRepository
}

var _ EventRepository = (*customerEventRepository)(nil)

// NewEventRepository 创建仓储。
// 不传连接时使用 mongoplus.Use 注册的默认数据库；传入连接时优先使用参数。
func NewEventRepository(db ...*mongoplus.Database) EventRepository {
	database := mongoplus.DefaultDB()
	if len(db) > 0 {
		database = db[0]
	}
	return &customerEventRepository{newDefaultEventRepository(database)}
}

// 自定义查询示例。取消注释后补上 context 与 dao 的导入，再按实际字段调整 Like/WhereIf。
//
// func (r *customerEventRepository) FindRecent(ctx context.Context, page, size int64) ([]*model.EventEntity, int64, error) {
// 	return r.FindPageByWrapper(ctx, page, size, func(w *mongoplus.Wrapper) {
// 		w.OrderDefault(dao.EventEntity.ID.Desc())
// 	}, mongoplus.QueryOpt().WithSingleFlight(0).Build())
// }
