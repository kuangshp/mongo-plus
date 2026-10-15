package repository

import (
	mongoplus "github.com/kuangshp/mongo-plus"
)

// DepartmentRepository 是业务扩展接口；此文件首次创建后不再覆盖。
type DepartmentRepository interface {
	iDefaultDepartmentRepository
}

type customDepartmentRepository struct{ *defaultDepartmentRepository }

var _ DepartmentRepository = (*customDepartmentRepository)(nil)

// NewDepartmentRepository 创建仓储并复用传入的数据库连接。
func NewDepartmentRepository(db *mongoplus.Database) DepartmentRepository {
	return &customDepartmentRepository{newDefaultDepartmentRepository(db)}
}
