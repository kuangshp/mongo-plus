// 由 mongoplus 首次创建；后续生成不覆盖，可按业务调整字段和校验。
package dto

import (
	decimaljson "github.com/kuangshp/mongo-plus/decimaljson"
)

// CreateDepartmentDTO 部门；修改 DTO 表示完整业务字段提交，不是 PATCH。
type CreateDepartmentDTO struct {
	Name string `json:"name" validate:"required" binding:"required"` // 部门名称
}

// UnmarshalJSON 兼容数字和数字字符串，保留数值原文。
func (value *CreateDepartmentDTO) UnmarshalJSON(data []byte) error {
	type plain CreateDepartmentDTO
	return decimaljson.Unmarshal(data, (*plain)(value))
}

// ModifyDepartmentDTO 部门；修改 DTO 表示完整业务字段提交，不是 PATCH。
type ModifyDepartmentDTO struct {
	ID   int64  `json:"id" validate:"required" binding:"required"`   // 主键
	Name string `json:"name" validate:"required" binding:"required"` // 部门名称
}

// UnmarshalJSON 兼容数字和数字字符串，保留数值原文。
func (value *ModifyDepartmentDTO) UnmarshalJSON(data []byte) error {
	type plain ModifyDepartmentDTO
	return decimaljson.Unmarshal(data, (*plain)(value))
}
