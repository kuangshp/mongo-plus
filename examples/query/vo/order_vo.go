// 由 mongoplus 首次创建；后续生成不覆盖，可按业务调整字段和校验。
package vo

import (
	bson "go.mongodb.org/mongo-driver/v2/bson"
	time "time"
)

// OrderVO 订单，演示金额和分组统计查询结果；可用 Scan/ScanByPage 直接填充，按需裁剪敏感字段。
type OrderVO struct {
	ID        int64           `bson:"_id" json:"id"`                // 主键
	Category  string          `bson:"category" json:"category"`     // 订单分类
	Status    int64           `bson:"status" json:"status"`         // 状态：1待支付，2已支付
	Quantity  int64           `bson:"quantity" json:"quantity"`     // 数量
	Amount    bson.Decimal128 `bson:"amount" json:"amount"`         // 金额，使用 Decimal128 保留十进制精度
	Tags      []string        `bson:"tags" json:"tags"`             // 订单标签
	CreatedAt time.Time       `bson:"created_at" json:"created_at"` // 创建时间
	UpdatedAt time.Time       `bson:"updated_at" json:"updated_at"` // 更新时间
}
