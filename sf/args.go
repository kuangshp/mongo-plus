package sf

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
)

// typedArgs 保留每一层的 Go 类型；map 规范排序，数组与 BSON D 的顺序保持不变。
// 不比较指针地址。自定义 JSON 类型以其公开编码契约作为值，但类型身份仍参与键。
func typedArgs(value reflect.Value, depth int) (any, error) {
	if depth > 64 {
		return nil, fmt.Errorf("参数嵌套过深或包含循环")
	}
	if !value.IsValid() {
		return nil, nil
	}
	typ := value.Type()
	identity := typeIdentity(typ)
	box := func(v any) any { return []any{identity, v} }
	switch value.Kind() {
	case reflect.Pointer, reflect.Interface:
		if value.IsNil() {
			return box(nil), nil
		}
		if value.Kind() == reflect.Pointer && value.CanInterface() && typ.Implements(reflect.TypeFor[json.Marshaler]()) {
			raw, err := json.Marshal(value.Interface())
			return box(json.RawMessage(raw)), err
		}
		inner, err := typedArgs(value.Elem(), depth+1)
		return box(inner), err
	}
	if value.CanInterface() && typ.Implements(reflect.TypeFor[json.Marshaler]()) {
		raw, err := json.Marshal(value.Interface())
		return box(json.RawMessage(raw)), err
	}
	switch value.Kind() {
	case reflect.Map:
		if value.IsNil() {
			return box(nil), nil
		}
		type entry struct {
			key  []byte
			pair []any
		}
		entries := make([]entry, 0, value.Len())
		iter := value.MapRange()
		for iter.Next() {
			key, err := typedArgs(iter.Key(), depth+1)
			if err != nil {
				return nil, err
			}
			item, err := typedArgs(iter.Value(), depth+1)
			if err != nil {
				return nil, err
			}
			encoded, err := json.Marshal(key)
			if err != nil {
				return nil, err
			}
			entries = append(entries, entry{encoded, []any{key, item}})
		}
		sort.Slice(entries, func(i, j int) bool { return bytes.Compare(entries[i].key, entries[j].key) < 0 })
		for i := 1; i < len(entries); i++ {
			if bytes.Equal(entries[i-1].key, entries[i].key) {
				return nil, fmt.Errorf("map 键的自定义编码发生碰撞")
			}
		}
		pairs := make([]any, len(entries))
		for i, e := range entries {
			pairs[i] = e.pair
		}
		return box(pairs), nil
	case reflect.Slice, reflect.Array:
		if value.Kind() == reflect.Slice && value.IsNil() {
			return box(nil), nil
		}
		items := make([]any, value.Len())
		for i := range items {
			item, err := typedArgs(value.Index(i), depth+1)
			if err != nil {
				return nil, err
			}
			items[i] = item
		}
		return box(items), nil
	case reflect.Struct:
		fields := []any{}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				if !value.Field(i).IsZero() {
					return nil, fmt.Errorf("参数 %s 含有非零私有字段，请提供明确的键参数或 JSON 编码", typ)
				}
				continue
			}
			item, err := typedArgs(value.Field(i), depth+1)
			if err != nil {
				return nil, err
			}
			fields = append(fields, []any{f.Name, item})
		}
		return box(fields), nil
	case reflect.String:
		// JSON 会把非法 UTF-8 替换为相同的替代字符；原始字节必须进入键。
		return box(base64.StdEncoding.EncodeToString([]byte(value.String()))), nil
	case reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64, reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
		return box(value.Interface()), nil
	default:
		return nil, fmt.Errorf("参数类型 %s 不能安全生成缓存键", typ)
	}
}

// Type.String 在指针和匿名组合类型中仅保留短包名，不能作为跨包身份。
// 命名类型使用完整导入路径，组合类型递归展开其成员。
func typeIdentity(typ reflect.Type) string {
	if typ.Name() != "" {
		return strconv.Quote(typ.PkgPath()) + ":" + strconv.Quote(typ.Name())
	}
	switch typ.Kind() {
	case reflect.Pointer:
		return "*" + typeIdentity(typ.Elem())
	case reflect.Slice:
		return "[]" + typeIdentity(typ.Elem())
	case reflect.Array:
		return "[" + strconv.Itoa(typ.Len()) + "]" + typeIdentity(typ.Elem())
	case reflect.Map:
		return "map[" + typeIdentity(typ.Key()) + "]" + typeIdentity(typ.Elem())
	case reflect.Chan:
		return "chan" + strconv.Itoa(int(typ.ChanDir())) + "[" + typeIdentity(typ.Elem()) + "]"
	case reflect.Struct:
		parts := []string{"{struct"}
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			parts = append(parts, strconv.Quote(f.PkgPath), strconv.Quote(f.Name), strconv.FormatBool(f.Anonymous), strconv.Quote(string(f.Tag)), typeIdentity(f.Type))
		}
		return strings.Join(parts, ";") + "}"
	case reflect.Func:
		parts := []string{"{func", strconv.FormatBool(typ.IsVariadic())}
		for i := 0; i < typ.NumIn(); i++ {
			parts = append(parts, typeIdentity(typ.In(i)))
		}
		parts = append(parts, "returns")
		for i := 0; i < typ.NumOut(); i++ {
			parts = append(parts, typeIdentity(typ.Out(i)))
		}
		return strings.Join(parts, ";") + "}"
	case reflect.Interface:
		parts := []string{"{interface"}
		for i := 0; i < typ.NumMethod(); i++ {
			m := typ.Method(i)
			parts = append(parts, strconv.Quote(m.PkgPath), strconv.Quote(m.Name), typeIdentity(m.Type))
		}
		return strings.Join(parts, ";") + "}"
	}
	return typ.String()
}
