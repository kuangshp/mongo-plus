package repository

import (
	mongoplus "github.com/kuangshp/mongo-plus"
)

// UserRepository 是业务扩展接口；此文件首次创建后不再覆盖。
// 基础读写在 iDefaultUserRepository。自定义查询加在 customUserRepository 上。
type UserRepository interface {
	iDefaultUserRepository
}

type customUserRepository struct {
	*defaultUserRepository
}

var _ UserRepository = (*customUserRepository)(nil)

// NewUserRepository 创建仓储并复用传入的数据库连接。
func NewUserRepository(db *mongoplus.Database) UserRepository {
	return &customUserRepository{newDefaultUserRepository(db)}
}

// 自定义查询示例。取消注释后按实际字段调整，并补上 context、time 与 dao 的导入。
// 生成的 Find/Count 已处理租户、逻辑删除；WithCache 只缓存这一次查询。
// 直接使用 Native 写入时，需要自行调用 db.InvalidateCache。
//
// func (r *customUserRepository) FindRecent(ctx context.Context, limit int64) ([]*model.UserEntity, error) {
// 	return r.FindList(ctx, mongoplus.Query().
// 		Order(dao.UserEntity.ID.Desc()).
// 		Limit(limit).
// 		WithCache(30*time.Second).
// 		Build())
// }
