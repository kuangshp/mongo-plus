// 由 mongoplus 首次创建；后续生成不覆盖，可按业务调整字段和校验。
package vo

import (
	time "time"
)

// DepartmentVO 部门查询结果；可用 Scan/ScanByPage 直接填充，按需裁剪敏感字段。
type DepartmentVO struct {
	ID        int64     `bson:"_id" json:"id"`                // 主键
	Name      string    `bson:"name" json:"name"`             // 部门名称
	CreatedAt time.Time `bson:"created_at" json:"created_at"` // 创建时间
	UpdatedAt time.Time `bson:"updated_at" json:"updated_at"` // 更新时间
}
