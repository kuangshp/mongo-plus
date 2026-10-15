// Package model 展示手写 Entity 路线；生成器不会修改本文件。
package model

import "time"

// Address 是文档中的嵌套对象。
type Address struct {
	City string `bson:"city" comment:"城市"`
}

// Title 展示手写模型中的命名字符串类型。
type Title string

// DocumentEntity 是手写实体，主键类型须匹配全局配置或显式实体覆盖。
type DocumentEntity struct {
	ID        int64      `bson:"_id,omitempty" json:"id"`
	Title     Title      `bson:"title" json:"title" mongoplus:"required" comment:"标题"`
	Address   Address    `bson:"address" json:"address"`
	CreatedAt time.Time  `bson:"created_at" json:"createdAt"`
	UpdatedAt time.Time  `bson:"updated_at" json:"updatedAt"`
	DeletedAt *time.Time `bson:"deleted_at" json:"deletedAt"`
}
