package repository

import (
	mongoplus "github.com/kuangshp/mongo-plus"
)

// UserRepository 是业务扩展接口；此文件首次创建后不再覆盖。
type UserRepository interface {
	iDefaultUserRepository
}

type customUserRepository struct{ *defaultUserRepository }

var _ UserRepository = (*customUserRepository)(nil)

// NewUserRepository 创建仓储并复用传入的数据库连接。
func NewUserRepository(db *mongoplus.Database) UserRepository {
	return &customUserRepository{newDefaultUserRepository(db)}
}
