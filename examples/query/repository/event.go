package repository

import (
	mongoplus "github.com/kuangshp/mongo-plus"
)

// EventRepository 是业务扩展接口；此文件首次创建后不再覆盖。
type EventRepository interface {
	iDefaultEventRepository
}

type customEventRepository struct{ *defaultEventRepository }

var _ EventRepository = (*customEventRepository)(nil)

// NewEventRepository 创建仓储并复用传入的数据库连接。
func NewEventRepository(db *mongoplus.Database) EventRepository {
	return &customEventRepository{newDefaultEventRepository(db)}
}
