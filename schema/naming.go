package schema

import (
	"strings"
	"unicode"
)

// SnakeName 统一文件名和手写 Entity 名称，例如 sysUser/sys_user 均为 sys_user。
// 连续大写缩写按单词处理，例如 HTTPUser 为 http_user，UserID 为 user_id。
func SnakeName(name string) string {
	runes := []rune(name)
	var result strings.Builder
	for i, r := range runes {
		if r == '_' || r == '-' {
			result.WriteRune('_')
			continue
		}
		if unicode.IsUpper(r) && i > 0 {
			prev := runes[i-1]
			if unicode.IsLower(prev) || unicode.IsDigit(prev) || unicode.IsUpper(prev) && i+1 < len(runes) && unicode.IsLower(runes[i+1]) {
				result.WriteRune('_')
			}
		}
		result.WriteRune(unicode.ToLower(r))
	}
	return result.String()
}
