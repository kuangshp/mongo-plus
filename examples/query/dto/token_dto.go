// 由 mongoplus 首次创建；后续生成不覆盖，可按业务调整字段和校验。
package dto

import (
	decimaljson "github.com/kuangshp/mongo-plus/decimaljson"
)

// CreateTokenDTO 业务传入字符串主键；不启用逻辑删除；修改 DTO 表示完整业务字段提交，不是 PATCH。
type CreateTokenDTO struct {
	ID    string `json:"id" validate:"required"`     // 主键
	Value string `json:"value" validate:"omitempty"` // 业务内容
}

// UnmarshalJSON 兼容数字和数字字符串，保留数值原文。
func (value *CreateTokenDTO) UnmarshalJSON(data []byte) error {
	type plain CreateTokenDTO
	return decimaljson.Unmarshal(data, (*plain)(value))
}

// ModifyTokenDTO 业务传入字符串主键；不启用逻辑删除；修改 DTO 表示完整业务字段提交，不是 PATCH。
type ModifyTokenDTO struct {
	ID    string `json:"id" validate:"required"`     // 主键
	Value string `json:"value" validate:"omitempty"` // 业务内容
}

// UnmarshalJSON 兼容数字和数字字符串，保留数值原文。
func (value *ModifyTokenDTO) UnmarshalJSON(data []byte) error {
	type plain ModifyTokenDTO
	return decimaljson.Unmarshal(data, (*plain)(value))
}
