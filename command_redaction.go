package mongoplus

import (
	"fmt"
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
	result, err := redactedCommand(raw, cfg)
	if err != nil {
		return "[command unavailable]"
	}
	encoded, err := bson.MarshalExtJSON(result, false, false)
	if err != nil {
		return "[command unavailable]"
	}
	return truncateCommand(singleLine(string(encoded)), cfg.MaxLength)
}

// 两种展示格式共用同一份脱敏结果；shell 格式不能绕过脱敏重新读取业务值。
func redactedCommand(raw bson.Raw, cfg LogConfig) (bson.D, error) {
	remaining := 2048
	var walk func(bson.RawValue, string, int, bool) any
	walk = func(value bson.RawValue, path string, depth int, expression bool) any {
		remaining--
		if remaining < 0 || depth > 32 {
			return "[truncated]"
		}
		if sensitiveLogPath(path, cfg.SensitiveFields) {
			return "[REDACTED]"
		}
		// 表达式参数不一定以敏感字段名作为键，不能靠键名判断常量是否安全。
		// 保留操作符结构，保守隐藏表达式、命令变量和脚本中的全部标量。
		expression = expression || expressionLogPath(path)
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
				result = append(result, bson.E{Key: e.Key(), Value: walk(e.Value(), path+"."+e.Key(), depth+1, expression)})
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
				result = append(result, walk(item, path, depth+1, expression))
			}
			return result
		case bson.TypeBinary:
			return "[REDACTED]"
		}
		if expression || cfg.HideParameters && !logCommandTarget(path) {
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
		return nil, fmt.Errorf("命令 BSON 无法解析: %w", err)
	}
	result := bson.D{}
	for _, e := range elements {
		if remaining <= 0 {
			result = append(result, bson.E{Key: "...", Value: "[truncated]"})
			break
		}
		result = append(result, bson.E{Key: e.Key(), Value: walk(e.Value(), e.Key(), 0, false)})
	}
	return result, nil
}

func expressionLogPath(path string) bool {
	parts := strings.Split(path, ".")
	switch parts[len(parts)-1] {
	case "$expr", "let", "$let", "$literal", "$const", "$where", "$function", "$accumulator",
		"$project", "$group", "$addFields", "$set", "$replaceRoot", "$replaceWith", "$redact", "$bucket", "$bucketAuto", "$setWindowFields":
		return true
	}
	return false
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
