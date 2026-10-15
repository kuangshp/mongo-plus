package mongoplus

import (
	"reflect"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func (w *Wrapper) orCondition(c Condition) *Wrapper {
	if len(c.Document) == 0 && c.Err == nil {
		return w
	}
	if len(w.option.conditions) == 0 {
		return w.Where(c)
	}
	w.option.conditions = []Condition{field.Or(field.And(w.option.conditions...), c)}
	return w
}

// OrGroup 将组内 AND 条件整体以 OR 接入主查询。
func (w *Wrapper) OrGroup(conditions ...Condition) *Wrapper {
	return w.orCondition(field.And(conditions...))
}

// OrGroupIf 条件为真时追加 OR (a AND b) 分组。
func (w *Wrapper) OrGroupIf(ok bool, conditions ...Condition) *Wrapper {
	if ok {
		w.OrGroup(conditions...)
	}
	return w
}

// WhereGroupFn 将回调中的条件整体以 AND 接入；空回调或无有效条件时跳过。
// 子构造器仅贡献过滤条件，不向外传播排序、分页或 AllowAll。
func (w *Wrapper) WhereGroupFn(fn func(*Wrapper)) *Wrapper {
	return w.groupFn(fn, false)
}

// OrGroupFn 将回调中的条件整体以 OR 接入；空回调或无有效条件时跳过。
func (w *Wrapper) OrGroupFn(fn func(*Wrapper)) *Wrapper {
	return w.groupFn(fn, true)
}

func (w *Wrapper) groupFn(fn func(*Wrapper), or bool) *Wrapper {
	child := Query()
	if fn != nil {
		fn(child)
	}
	if child.option.err != nil {
		w.option.err = child.option.err
	}
	if len(child.option.conditions) == 0 {
		return w
	}
	condition := field.And(child.option.conditions...)
	if or {
		return w.orCondition(condition)
	}
	return w.Where(condition)
}

// WhereRaw 与 RawWhere 等价，接收可信仓储代码构造的 BSON。
func (w *Wrapper) WhereRaw(filter bson.D) *Wrapper { return w.RawWhere(filter) }

// WhereRawIf 条件为真时追加原生 BSON 条件。
func (w *Wrapper) WhereRawIf(ok bool, filter bson.D) *Wrapper {
	if ok {
		w.WhereRaw(filter)
	}
	return w
}

// OrWhereRaw 以 OR 追加原生 BSON 条件。
func (w *Wrapper) OrWhereRaw(filter bson.D) *Wrapper {
	return w.orCondition(Condition{Document: filter})
}

// WithUnscoped 沿用 gorm-plus 命名，包含逻辑删除记录，仍保留租户隔离。
func (w *Wrapper) WithUnscoped() *Wrapper { return w.WithDeleted() }

// LLikeIf 条件为真且文本非空时追加后缀匹配。
func (w *Wrapper) LLikeIf(ok bool, f field.Named, value string) *Wrapper {
	if ok {
		w.LLike(f, value)
	}
	return w
}

// RLikeIf 条件为真且文本非空时追加前缀匹配。
func (w *Wrapper) RLikeIf(ok bool, f field.Named, value string) *Wrapper {
	if ok {
		w.RLike(f, value)
	}
	return w
}

// OrLike 以 OR 追加字面量包含匹配，空文本跳过。
func (w *Wrapper) OrLike(f field.Named, value string) *Wrapper {
	if value != "" {
		w.orCondition(field.NewString(f.Name()).Contains(value))
	}
	return w
}

// OrLikeIf 条件为真时以 OR 追加包含匹配。
func (w *Wrapper) OrLikeIf(ok bool, f field.Named, value string) *Wrapper {
	if ok {
		w.OrLike(f, value)
	}
	return w
}

// OrLLike 以 OR 追加后缀匹配，空文本跳过。
func (w *Wrapper) OrLLike(f field.Named, value string) *Wrapper {
	if value != "" {
		w.orCondition(field.NewString(f.Name()).EndsWith(value))
	}
	return w
}

// OrLLikeIf 条件为真时以 OR 追加后缀匹配。
func (w *Wrapper) OrLLikeIf(ok bool, f field.Named, value string) *Wrapper {
	if ok {
		w.OrLLike(f, value)
	}
	return w
}

// OrRLike 以 OR 追加前缀匹配，空文本跳过。
func (w *Wrapper) OrRLike(f field.Named, value string) *Wrapper {
	if value != "" {
		w.orCondition(field.NewString(f.Name()).StartsWith(value))
	}
	return w
}

// OrRLikeIf 条件为真时以 OR 追加前缀匹配。
func (w *Wrapper) OrRLikeIf(ok bool, f field.Named, value string) *Wrapper {
	if ok {
		w.OrRLike(f, value)
	}
	return w
}

// OrderIf 条件为真使用 truthy，否则追加 falsy 排序。
func (w *Wrapper) OrderIf(ok bool, truthy bson.E, falsy ...bson.E) *Wrapper {
	if ok {
		return w.Order(truthy)
	}
	return w.Order(falsy...)
}

// OrderTriState 1/asc 为升序，2/desc 为降序，其余跳过。
// 支持 gorm-plus 接受的数值类型和字符串。
func (w *Wrapper) OrderTriState(state any, asc, desc bson.E) *Wrapper {
	var order int64
	switch value := state.(type) {
	case string:
		switch value {
		case "1", "asc", "ASC":
			order = 1
		case "2", "desc", "DESC":
			order = 2
		}
	default:
		if state != nil {
			v := reflect.ValueOf(state)
			switch v.Kind() {
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				order = v.Int()
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				if n := v.Uint(); n == 1 || n == 2 {
					order = int64(n)
				}
			case reflect.Float32, reflect.Float64:
				order = int64(v.Float())
			}
		}
	}
	switch order {
	case 1:
		return w.Order(asc)
	case 2:
		return w.Order(desc)
	}
	return w
}

// OrderDefault 在没有显式排序时提供默认排序，行为与 gorm-plus GenWrapper 一致。
// 显式排序无论在它之前还是之后调用，均优先于默认排序。
func (w *Wrapper) OrderDefault(order ...bson.E) *Wrapper {
	if len(w.defaultOrders) == 0 {
		w.defaultOrders = append(bson.D(nil), order...)
	}
	return w
}

// WithColumns 追加字段赋值，沿用 gorm-plus.Update().WithColumns(...) 命名。
func (u *UpdateBuilder) WithColumns(ops ...UpdateOp) *UpdateBuilder { return u.Apply(ops...) }

// Columns 是 WithColumns 的短别名。
func (u *UpdateBuilder) Columns(ops ...UpdateOp) *UpdateBuilder { return u.WithColumns(ops...) }

// MergeQueryOptions 合并查询选项，条件始终以 AND 累积。
func MergeQueryOptions(opts ...QueryOption) QueryOption { return mergeOptions(opts) }
