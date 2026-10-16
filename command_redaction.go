package mongoplus

import (
	"strings"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func sensitiveLogPath(path string, additional []string) bool {
	parts := strings.Split(path, ".")
	leaf := strings.ToLower(strings.ReplaceAll(parts[len(parts)-1], "_", ""))
	for _, name := range []string{"password", "passwd", "pwd", "secret", "token", "authorization", "phone", "mobile", "email", "idcard", "cipher", "apikey", "privatekey", "identitynumber"} {
		if strings.Contains(leaf, name) {
			return true
		}
	}
	for _, name := range additional {
		if strings.EqualFold(path, name) || len(path) > len(name) && strings.EqualFold(path[len(path)-len(name)-1:], "."+name) {
			return true
		}
	}
	return false
}

// safeCommand 只序列化脱敏后的副本；解析失败不回退到原始命令。
// 控制递归深度及节点数，避免大文档在日志处理时无限展开。
func safeCommand(raw bson.Raw, cfg LogConfig) string {
	remaining := 2048
	var walk func(bson.RawValue, string, int) any
	walk = func(value bson.RawValue, path string, depth int) any {
		remaining--
		if remaining < 0 || depth > 32 {
			return "[truncated]"
		}
		if sensitiveLogPath(path, cfg.SensitiveFields) {
			return "[REDACTED]"
		}
		switch value.Type {
		case bson.TypeEmbeddedDocument:
			elements, err := value.Document().Elements()
			if err != nil {
				return "[unavailable]"
			}
			result := bson.D{}
			for _, e := range elements {
				if remaining <= 0 {
					result = append(result, bson.E{Key: "...", Value: "[truncated]"})
					break
				}
				result = append(result, bson.E{Key: e.Key(), Value: walk(e.Value(), path+"."+e.Key(), depth+1)})
			}
			return result
		case bson.TypeArray:
			values, err := value.Array().Values()
			if err != nil {
				return "[unavailable]"
			}
			result := bson.A{}
			for _, item := range values {
				if remaining <= 0 {
					result = append(result, "[truncated]")
					break
				}
				result = append(result, walk(item, path, depth+1))
			}
			return result
		case bson.TypeBinary:
			return "[REDACTED]"
		}
		if cfg.HideParameters && !logCommandTarget(path) {
			return "?"
		}
		if text, ok := value.StringValueOK(); ok {
			return truncateCommand(text, cfg.MaxLength)
		}
		var decoded any
		if err := value.Unmarshal(&decoded); err != nil {
			return "[unavailable]"
		}
		return decoded
	}
	elements, err := raw.Elements()
	if err != nil {
		return "[command unavailable]"
	}
	result := bson.D{}
	for _, e := range elements {
		if remaining <= 0 {
			result = append(result, bson.E{Key: "...", Value: "[truncated]"})
			break
		}
		result = append(result, bson.E{Key: e.Key(), Value: walk(e.Value(), e.Key(), 0)})
	}
	encoded, err := bson.MarshalExtJSON(result, false, false)
	if err != nil {
		return "[command unavailable]"
	}
	return truncateCommand(singleLine(string(encoded)), cfg.MaxLength)
}

func logCommandTarget(path string) bool {
	switch path {
	case "find", "insert", "update", "delete", "aggregate", "findAndModify", "count", "distinct", "$db":
		return true
	}
	return false
}

func truncateCommand(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	const suffix = "...[truncated]"
	end := max(0, limit-len(suffix))
	for end > 0 && !utf8.RuneStart(value[end]) {
		end--
	}
	return value[:end] + suffix
}
