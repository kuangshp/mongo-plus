// 由 mongoplus 首次创建；后续生成不覆盖，可按业务调整字段和校验。
package vo

// TokenVO 业务传入字符串主键；不启用逻辑删除查询结果；可用 Scan/ScanByPage 直接填充，按需裁剪敏感字段。
type TokenVO struct {
	ID    string `bson:"_id" json:"id"`      // 主键
	Value string `bson:"value" json:"value"` // 业务内容
}
