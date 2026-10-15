// 由 mongoplus 首次创建；后续生成不覆盖，可按业务调整字段和校验。
package vo

import (
	time "time"
)

// UserVO 用户查询结果；可用 Scan/ScanByPage 直接填充，按需裁剪敏感字段。
type UserVO struct {
	ID        int64     `bson:"_id" json:"id"`                // 主键
	Username  string    `bson:"username" json:"username"`     // 用户名
	Status    int64     `bson:"status" json:"status"`         // 状态：1正常，2禁用
	DeptID    int64     `bson:"dept_id" json:"dept_id"`       // 所属部门 ID
	CreatedAt time.Time `bson:"created_at" json:"created_at"` // 创建时间
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"` // 更新时间
}
