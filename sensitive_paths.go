package mongoplus

import (
	"fmt"
	"reflect"
	"strings"
)

// 优先匹配完整 Go/BSON 路径；短字段名只有唯一时才可用，避免多个嵌套对象串字段。
func findSensitiveField(fields []SensitiveFieldConfig, logical string) (SensitiveFieldConfig, bool, error) {
	for _, exact := range []bool{true, false} {
		var result SensitiveFieldConfig
		found := false
		for _, item := range fields {
			match := false
			for _, name := range []string{item.PlainField, item.PlainPath, item.IndexField, item.IndexPath} {
				if exact {
					match = match || logical == name
				} else {
					parts := strings.Split(name, ".")
					match = match || logical == parts[len(parts)-1]
				}
			}
			if !match {
				continue
			}
			if found {
				return result, false, fmt.Errorf("敏感字段 %s 有多个匹配，请使用完整路径", logical)
			}
			result, found = item, true
		}
		if found {
			return result, true, nil
		}
	}
	return SensitiveFieldConfig{}, false, nil
}

func sensitiveArrayIndex(part string) bool {
	if part == "" {
		return false
	}
	for _, char := range part {
		if char < '0' || char > '9' {
			return false
		}
	}
	return true
}

func matchSensitivePattern(path string, pattern []string) ([]string, bool) {
	parts := strings.Split(path, ".")
	if len(parts) != len(pattern) {
		return nil, false
	}
	indexes := []string{}
	for i, token := range pattern {
		if token == sensitiveArrayToken {
			if !sensitiveArrayIndex(parts[i]) {
				return nil, false
			}
			indexes = append(indexes, parts[i])
		} else if token != parts[i] {
			return nil, false
		}
	}
	return indexes, true
}

func fillSensitivePattern(pattern, indexes []string) string {
	parts := append([]string(nil), pattern...)
	j := 0
	for i := range parts {
		if parts[i] == sensitiveArrayToken {
			parts[i] = indexes[j]
			j++
		}
	}
	return strings.Join(parts, ".")
}

// 识别整个父对象替换、敏感字符串子路径及缺少下标的数组赋值。
// 这些操作不能原样放行，否则可能绕过加密或留下旧密文和索引。
func relatedSensitivePath(path string, pattern []string) bool {
	parts := strings.Split(path, ".")
	position := 0
	for _, token := range pattern {
		if position == len(parts) {
			return true
		}
		if token == sensitiveArrayToken {
			if sensitiveArrayIndex(parts[position]) {
				position++
			}
			continue
		}
		if parts[position] != token {
			return false
		}
		position++
	}
	return true
}

func sensitiveUpdateBinding(bindings []sensitiveBinding, path string) (sensitiveBinding, []string, bool, error) {
	found := -1
	var indexes []string
	for i, binding := range bindings {
		for _, pattern := range [][]string{binding.plainPattern, binding.goPattern} {
			if captured, ok := matchSensitivePattern(path, pattern); ok {
				if found >= 0 && found != i {
					return sensitiveBinding{}, nil, false, fmt.Errorf("敏感字段 %s 路径有歧义", path)
				}
				found, indexes = i, captured
			}
		}
	}
	if found >= 0 {
		return bindings[found], indexes, true, nil
	}
	// 保留历史 Go 字段短名称入口，嵌套字段要求唯一；数组必须显式带数字下标。
	if !strings.Contains(path, ".") {
		for i, binding := range bindings {
			parts := strings.Split(binding.field.PlainField, ".")
			if path != parts[len(parts)-1] {
				continue
			}
			if found >= 0 {
				return sensitiveBinding{}, nil, false, fmt.Errorf("敏感字段 %s 有多个匹配，请使用完整路径", path)
			}
			found = i
		}
		if found >= 0 {
			binding := bindings[found]
			for _, token := range binding.goPattern {
				if token == sensitiveArrayToken {
					return binding, nil, false, fmt.Errorf("敏感数组字段 %s 更新必须指定数字下标", path)
				}
			}
			return binding, nil, true, nil
		}
	}
	for _, binding := range bindings {
		for _, pattern := range [][]string{binding.plainPattern, binding.goPattern} {
			if relatedSensitivePath(path, pattern) {
				return binding, nil, false, fmt.Errorf("敏感字段 %s 不支持父子对象替换或缺少数组下标的更新", path)
			}
		}
	}
	return sensitiveBinding{}, nil, false, nil
}

// assignCreatePlain 将 AutoFill 的逻辑敏感值写入实体副本，再由 seal 统一生成密文/索引。
func (p *SensitivePlugin) assignCreatePlain(entity any, path string, value any) (bool, error) {
	root := reflect.ValueOf(entity).Elem()
	model := p.modelForType(root.Type())
	if model.err != nil {
		return false, model.err
	}
	binding, indexes, found, err := sensitiveUpdateBinding(model.bindings, path)
	if err != nil || !found {
		return found, err
	}
	if len(indexes) > 0 {
		return true, fmt.Errorf("创建自动填充不支持敏感数组元素，请在实体中提供 %s", path)
	}
	text, ok := value.(string)
	if !ok {
		return true, fmt.Errorf("敏感自动字段 %s 需要字符串", path)
	}
	assigned := false
	err = visitSensitiveOwners(root, binding.owner, func(owner reflect.Value) error {
		plain, ok := sensitiveValueAt(owner, binding.plain)
		if ok {
			plain.SetString(text)
			assigned = true
		}
		return nil
	})
	if err == nil && !assigned {
		err = fmt.Errorf("敏感自动字段 %s 的父对象不存在", path)
	}
	return true, err
}
