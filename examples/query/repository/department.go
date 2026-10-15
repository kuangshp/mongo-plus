package repository

import (
	mongoplus "github.com/kuangshp/mongo-plus"
)

// DepartmentRepository 是业务扩展接口；此文件首次创建后不再覆盖。
// 基础读写在 iDefaultDepartmentRepository。自定义查询加在 customDepartmentRepository 上。
type DepartmentRepository interface {
	iDefaultDepartmentRepository
}

type customDepartmentRepository struct {
	*defaultDepartmentRepository
}

var _ DepartmentRepository = (*customDepartmentRepository)(nil)

// NewDepartmentRepository 创建仓储并复用传入的数据库连接。
func NewDepartmentRepository(db *mongoplus.Database) DepartmentRepository {
	return &customDepartmentRepository{newDefaultDepartmentRepository(db)}
}

// 自定义查询示例。取消注释后按实际字段调整，并补上 context、time 与 dao 的导入。
// 生成的 Find/Count 已处理租户、逻辑删除；WithCache 只缓存这一次查询。
// 直接使用 Native 写入时，需要自行调用 db.InvalidateCache。
//
// func (r *customDepartmentRepository) FindRecent(ctx context.Context, limit int64) ([]*model.DepartmentEntity, error) {
// 	return r.FindList(ctx, mongoplus.Query().
// 		Order(dao.DepartmentEntity.ID.Desc()).
// 		Limit(limit).
// 		WithCache(30*time.Second).
// 		Build())
// }
