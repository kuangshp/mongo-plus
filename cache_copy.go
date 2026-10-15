package mongoplus

import (
	"reflect"
	"strings"
	"time"
)

// 缓存复制已解码的 Go 值，避免用默认 BSON 编解码器破坏 Client 的自定义类型。
// bytes 是保留对象的保守估算，包含切片、字符串、map 与指针；不包含在途请求的内存。
func copyCacheValue[V any](value V) (V, int64, bool) {
	var copy V
	var size int64
	cloned, ok := cloneCacheValue(reflect.ValueOf(&value).Elem(), &size, 0)
	if ok {
		reflect.ValueOf(&copy).Elem().Set(cloned)
	}
	return copy, size + 256, ok
}

var timeType = reflect.TypeOf(time.Time{})

func cloneCacheValue(v reflect.Value, size *int64, depth int) (reflect.Value, bool) {
	if depth > 64 { // 循环引用、自定义资源类型直接绕过缓存。
		return reflect.Value{}, false
	}
	*size += int64(v.Type().Size())
	switch v.Kind() {
	case reflect.Interface:
		copy := reflect.New(v.Type()).Elem()
		if !v.IsNil() {
			inner, ok := cloneCacheValue(v.Elem(), size, depth+1)
			if !ok {
				return reflect.Value{}, false
			}
			copy.Set(inner)
		}
		return copy, true
	case reflect.Pointer:
		if v.IsNil() {
			return reflect.Zero(v.Type()), true
		}
		inner, ok := cloneCacheValue(v.Elem(), size, depth+1)
		if !ok {
			return reflect.Value{}, false
		}
		copy := reflect.New(v.Type().Elem())
		copy.Elem().Set(inner)
		return copy, true
	case reflect.Slice:
		if v.IsNil() {
			return reflect.Zero(v.Type()), true
		}
		copy := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			inner, ok := cloneCacheValue(v.Index(i), size, depth+1)
			if !ok {
				return reflect.Value{}, false
			}
			copy.Index(i).Set(inner)
		}
		return copy, true
	case reflect.Map:
		if v.IsNil() {
			return reflect.Zero(v.Type()), true
		}
		*size += 64 + int64(v.Len())*48
		copy := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			key, ok := cloneCacheValue(iter.Key(), size, depth+1)
			if !ok {
				return reflect.Value{}, false
			}
			inner, ok := cloneCacheValue(iter.Value(), size, depth+1)
			if !ok {
				return reflect.Value{}, false
			}
			copy.SetMapIndex(key, inner)
		}
		return copy, true
	case reflect.Struct:
		// time.Location 由标准库共享且不可变，无需复制内部缓存。
		if v.Type() == timeType {
			return v, true
		}
		copy := reflect.New(v.Type()).Elem()
		copy.Set(v)
		for i := 0; i < v.NumField(); i++ {
			if !v.Type().Field(i).IsExported() {
				if cacheHasReferences(v.Field(i).Type()) {
					return reflect.Value{}, false
				}
				continue
			}
			inner, ok := cloneCacheValue(v.Field(i), size, depth+1)
			if !ok {
				return reflect.Value{}, false
			}
			copy.Field(i).Set(inner)
		}
		return copy, true
	case reflect.Array:
		copy := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			inner, ok := cloneCacheValue(v.Index(i), size, depth+1)
			if !ok {
				return reflect.Value{}, false
			}
			copy.Index(i).Set(inner)
		}
		return copy, true
	case reflect.String:
		*size += int64(v.Len())
		copy := reflect.New(v.Type()).Elem()
		copy.SetString(strings.Clone(v.String())) // 不保留大字符串的共享底层缓冲区。
		return copy, true
	case reflect.Chan, reflect.Func, reflect.UnsafePointer:
		return reflect.Value{}, false
	default:
		return v, true
	}
}

func cacheHasReferences(t reflect.Type) bool {
	switch t.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Slice, reflect.Map, reflect.Chan, reflect.Func, reflect.UnsafePointer, reflect.String:
		return true
	case reflect.Struct:
		for i := 0; i < t.NumField(); i++ {
			if cacheHasReferences(t.Field(i).Type) {
				return true
			}
		}
	case reflect.Array:
		return cacheHasReferences(t.Elem())
	}
	return false
}
