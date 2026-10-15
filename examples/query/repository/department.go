package repository

import (
	mongoplus "github.com/kuangshp/mongo-plus"
)

// customerDepartmentRepository 是业务自定义仓储；此文件首次创建后不再覆盖。
// 自定义方法写在此结构体上；覆盖基础方法时保持相同的方法签名。
type customerDepartmentRepository struct {
	*defaultDepartmentRepository
}

// DepartmentRepository 对外暴露基础能力与业务扩展方法。
// 新增自定义方法时在此接口声明。
type DepartmentRepository interface {
	iDefaultDepartmentRepository
}

var _ DepartmentRepository = (*customerDepartmentRepository)(nil)

// NewDepartmentRepository 创建仓储。
// 不传连接时使用 mongoplus.Use 注册的默认数据库；传入连接时优先使用参数。
func NewDepartmentRepository(db ...*mongoplus.Database) DepartmentRepository {
	return &customerDepartmentRepository{
		defaultDepartmentRepository: newDefaultDepartmentRepository(db...),
	}
}

// 自定义查询示例。取消注释后补上 context 与 dao 的导入，再按实际字段调整 Like/WhereIf。
//
// func (r *customerDepartmentRepository) FindRecent(ctx context.Context, page, size int64) ([]*model.DepartmentEntity, int64, error) {
// 	return r.FindPageByWrapper(ctx, page, size, func(w *mongoplus.Wrapper) {
// 		w.OrderDefault(dao.DepartmentEntity.ID.Desc())
// 	}, mongoplus.QueryOpt().WithSingleFlight(0).Build())
// }
