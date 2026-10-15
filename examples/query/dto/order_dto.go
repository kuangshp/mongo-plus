// 由 mongoplus 首次创建；后续生成不覆盖，可按业务调整字段和校验。
package dto

import (
	decimaljson "github.com/kuangshp/mongo-plus/decimaljson"
)

// CreateOrderDTO 订单，演示金额和分组统计；修改 DTO 表示完整业务字段提交，不是 PATCH。
type CreateOrderDTO struct {
	Category string   `json:"category" validate:"required"`   // 订单分类
	Status   int64    `json:"status" validate:"omitempty"`    // 状态：1待支付，2已支付
	Quantity int64    `json:"quantity" validate:"omitempty"`  // 数量
	Amount   string   `json:"amount" validate:"omitempty"`    // 金额，使用 Decimal128 保留十进制精度；数字文本，入库前用 bson.ParseDecimal128 解析并校验业务精度
	Tags     []string `json:"tags" validate:"omitempty,dive"` // 订单标签
}

// UnmarshalJSON 兼容数字和数字字符串，保留数值原文。
func (value *CreateOrderDTO) UnmarshalJSON(data []byte) error {
	type plain CreateOrderDTO
	return decimaljson.Unmarshal(data, (*plain)(value))
}

// ModifyOrderDTO 订单，演示金额和分组统计；修改 DTO 表示完整业务字段提交，不是 PATCH。
type ModifyOrderDTO struct {
	ID       int64    `json:"id" validate:"required"`         // 主键
	Category string   `json:"category" validate:"required"`   // 订单分类
	Status   int64    `json:"status" validate:"omitempty"`    // 状态：1待支付，2已支付
	Quantity int64    `json:"quantity" validate:"omitempty"`  // 数量
	Amount   string   `json:"amount" validate:"omitempty"`    // 金额，使用 Decimal128 保留十进制精度；数字文本，入库前用 bson.ParseDecimal128 解析并校验业务精度
	Tags     []string `json:"tags" validate:"omitempty,dive"` // 订单标签
}

// UnmarshalJSON 兼容数字和数字字符串，保留数值原文。
func (value *ModifyOrderDTO) UnmarshalJSON(data []byte) error {
	type plain ModifyOrderDTO
	return decimaljson.Unmarshal(data, (*plain)(value))
}
