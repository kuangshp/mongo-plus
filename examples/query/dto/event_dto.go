// 由 mongoplus 首次创建；后续生成不覆盖，可按业务调整字段和校验。
package dto

import (
	decimaljson "github.com/kuangshp/mongo-plus/decimaljson"
)

// CreateEventDTO 事件记录：覆盖全局主键为 ObjectID；修改 DTO 表示完整业务字段提交，不是 PATCH。
type CreateEventDTO struct {
	Message string `json:"message" validate:"omitempty" binding:"omitempty"` // 事件内容
}

// UnmarshalJSON 兼容数字和数字字符串，保留数值原文。
func (value *CreateEventDTO) UnmarshalJSON(data []byte) error {
	type plain CreateEventDTO
	return decimaljson.Unmarshal(data, (*plain)(value))
}

// ModifyEventDTO 事件记录：覆盖全局主键为 ObjectID；修改 DTO 表示完整业务字段提交，不是 PATCH。
type ModifyEventDTO struct {
	ID      string `json:"id" validate:"required,objectId" binding:"required,objectId"` // 主键；24 位十六进制字符串，入库前用 bson.ObjectIDFromHex 解析
	Message string `json:"message" validate:"omitempty" binding:"omitempty"`            // 事件内容
}

// UnmarshalJSON 兼容数字和数字字符串，保留数值原文。
func (value *ModifyEventDTO) UnmarshalJSON(data []byte) error {
	type plain ModifyEventDTO
	return decimaljson.Unmarshal(data, (*plain)(value))
}
