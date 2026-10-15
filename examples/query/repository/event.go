package repository

import (
	mongoplus "github.com/kuangshp/mongo-plus"
)

// customerEventRepository 是业务自定义仓储；此文件首次创建后不再覆盖。
// 自定义方法写在此结构体上；覆盖基础方法时保持相同的方法签名。
type customerEventRepository struct {
	*defaultEventRepository
}

// EventRepository 对外暴露基础能力与业务扩展方法。
// 新增自定义方法时在此接口声明。
type EventRepository interface {
	iDefaultEventRepository
}

var _ EventRepository = (*customerEventRepository)(nil)

// NewEventRepository 创建仓储。
// 不传连接时使用 mongoplus.Use 注册的默认数据库；传入连接时优先使用参数。
func NewEventRepository(db ...*mongoplus.Database) EventRepository {
	return &customerEventRepository{
		defaultEventRepository: newDefaultEventRepository(db...),
	}
}

// 自定义查询示例。取消注释后补上 context 与 dao 的导入，再按实际字段调整 Like/WhereIf。
//
// func (r *customerEventRepository) FindRecent(ctx context.Context, page, size int64) ([]*model.EventEntity, int64, error) {
// 	return r.FindPageByWrapper(ctx, page, size, func(w *mongoplus.Wrapper) {
// 		w.OrderDefault(dao.EventEntity.ID.Desc())
// 	}, mongoplus.QueryOpt().WithSingleFlight(0).Build())
// }
