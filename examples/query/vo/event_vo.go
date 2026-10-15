// 由 mongoplus 首次创建；后续生成不覆盖，可按业务调整字段和校验。
package vo

import (
	bson "go.mongodb.org/mongo-driver/v2/bson"
	time "time"
)

// EventVO 事件记录：覆盖全局主键为 ObjectID查询结果；可用 Scan/ScanByPage 直接填充，按需裁剪敏感字段。
type EventVO struct {
	ID        bson.ObjectID `bson:"_id" json:"id"`                // 主键
	Message   string        `bson:"message" json:"message"`       // 事件内容
	CreatedAt time.Time     `bson:"created_at" json:"created_at"` // 创建时间
	UpdatedAt time.Time     `bson:"updated_at" json:"updated_at"` // 更新时间
}
