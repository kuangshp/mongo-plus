// 由 mongoplus 首次创建；后续生成不覆盖，可按业务调整字段和校验。
package dto

import (
	decimaljson "github.com/kuangshp/mongo-plus/decimaljson"
)

// CreateUserDTO 用户；修改 DTO 表示完整业务字段提交，不是 PATCH。
type CreateUserDTO struct {
	Username string `json:"username" validate:"required"` // 用户名
	Status   int64  `json:"status" validate:"omitempty"`  // 状态：1正常，2禁用
	DeptID   int64  `json:"dept_id" validate:"omitempty"` // 所属部门 ID
}

// UnmarshalJSON 兼容数字和数字字符串，保留数值原文。
func (value *CreateUserDTO) UnmarshalJSON(data []byte) error {
	type plain CreateUserDTO
	return decimaljson.Unmarshal(data, (*plain)(value))
}

// ModifyUserDTO 用户；修改 DTO 表示完整业务字段提交，不是 PATCH。
type ModifyUserDTO struct {
	ID       int64  `json:"id" validate:"required"`       // 主键
	Username string `json:"username" validate:"required"` // 用户名
	Status   int64  `json:"status" validate:"omitempty"`  // 状态：1正常，2禁用
	DeptID   int64  `json:"dept_id" validate:"omitempty"` // 所属部门 ID
}

// UnmarshalJSON 兼容数字和数字字符串，保留数值原文。
func (value *ModifyUserDTO) UnmarshalJSON(data []byte) error {
	type plain ModifyUserDTO
	return decimaljson.Unmarshal(data, (*plain)(value))
}
