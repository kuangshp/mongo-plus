package repository

import (
	mongoplus "github.com/kuangshp/mongo-plus"
)

// OrderRepository 是业务扩展接口；此文件首次创建后不再覆盖。
type OrderRepository interface {
	iDefaultOrderRepository
}

type customOrderRepository struct{ *defaultOrderRepository }

var _ OrderRepository = (*customOrderRepository)(nil)

// NewOrderRepository 创建仓储并复用传入的数据库连接。
func NewOrderRepository(db *mongoplus.Database) OrderRepository {
	return &customOrderRepository{newDefaultOrderRepository(db)}
}
