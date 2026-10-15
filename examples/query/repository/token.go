package repository

import (
	mongoplus "github.com/kuangshp/mongo-plus"
)

// TokenRepository 是业务扩展接口；此文件首次创建后不再覆盖。
type TokenRepository interface {
	iDefaultTokenRepository
}

type customTokenRepository struct{ *defaultTokenRepository }

var _ TokenRepository = (*customTokenRepository)(nil)

// NewTokenRepository 创建仓储并复用传入的数据库连接。
func NewTokenRepository(db *mongoplus.Database) TokenRepository {
	return &customTokenRepository{newDefaultTokenRepository(db)}
}
