// Package field 提供生成 DAO 使用的类型安全字段和 BSON 条件。
package field

import (
	"fmt"
	"regexp"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// Condition 表示一个 MongoDB 过滤条件；错误保留到执行时返回。
type Condition struct {
	Document bson.D
	Err      error
}

// Named 表示具有 BSON 路径的字段。
type Named interface{ Name() string }

// Aliased 为分组键或结果投影指定输出字段名；Name 仍返回原始 BSON 路径。
type Aliased struct {
	path  string
	alias string
}

func (f Aliased) Name() string  { return f.path }
func (f Aliased) Alias() string { return f.alias }

// Field 是字段类型；Eq、In、Set 的参数类型与实体字段保持一致。
type Field[T any] struct{ path string }

// New 创建字段；生成代码中固定声明路径，业务输入不得充当字段路径。
func New[T any](path string) Field[T] { return Field[T]{path: path} }

// Name 返回 BSON 字段路径。
func (f Field[T]) Name() string { return f.path }

// As 指定链式聚合 GroupBy/Select 的结果字段名，如 Status.As("status")。
func (f Field[T]) As(alias string) Aliased { return Aliased{path: f.path, alias: alias} }

// At 为关联结果中的字段加上别名前缀，例如 Username.At("owner")。
func (f Field[T]) At(alias string) Field[T] { return New[T](alias + "." + f.path) }

// ValidPath 校验字段路径，拒绝操作符及空路径片段。
func ValidPath(path string) bool {
	for _, part := range strings.Split(path, ".") {
		if part == "" || strings.HasPrefix(part, "$") || strings.ContainsRune(part, '\x00') {
			return false
		}
	}
	return true
}

func (f Field[T]) condition(op string, value any) Condition {
	if !ValidPath(f.path) {
		return Condition{Err: fmt.Errorf("无效字段路径 %q", f.path)}
	}
	return Condition{Document: bson.D{{Key: f.path, Value: bson.D{{Key: op, Value: value}}}}}
}

// Eq 构造等于条件。
func (f Field[T]) Eq(value T) Condition { return f.condition("$eq", value) }

// Neq 构造不等于条件。
func (f Field[T]) Neq(value T) Condition { return f.condition("$ne", value) }

// Gt 构造大于条件。
func (f Field[T]) Gt(value T) Condition { return f.condition("$gt", value) }

// Gte 构造大于等于条件。
func (f Field[T]) Gte(value T) Condition { return f.condition("$gte", value) }

// Lt 构造小于条件。
func (f Field[T]) Lt(value T) Condition { return f.condition("$lt", value) }

// Lte 构造小于等于条件。
func (f Field[T]) Lte(value T) Condition { return f.condition("$lte", value) }

// In 构造集合包含条件；空参数得到不会命中的条件。
func (f Field[T]) In(values ...T) Condition {
	if values == nil {
		values = []T{}
	}
	return f.condition("$in", values)
}

// NotIn 构造集合排除条件。
func (f Field[T]) NotIn(values ...T) Condition {
	if values == nil {
		values = []T{}
	}
	return f.condition("$nin", values)
}

// Exists 判断字段是否存在，与字段为 null 的条件不同。
func (f Field[T]) Exists(value bool) Condition { return f.condition("$exists", value) }

// IsNull 匹配缺失或值为 null 的字段。
func (f Field[T]) IsNull() Condition { return f.condition("$eq", nil) }

// Asc 按字段升序排序。
func (f Field[T]) Asc() bson.E { return bson.E{Key: f.path, Value: 1} }

// Desc 按字段降序排序。
func (f Field[T]) Desc() bson.E { return bson.E{Key: f.path, Value: -1} }

// UpdateOp 表示一个显式字段更新，不会忽略零值。
type UpdateOp struct {
	Operator string
	Path     string
	Value    any
}

// Operations 让单个赋值可以直接传给仓储更新方法。
func (op UpdateOp) Operations() []UpdateOp { return []UpdateOp{op} }

// Set 显式设置字段值，支持零值和空字符串。
func (f Field[T]) Set(value T) UpdateOp { return UpdateOp{"$set", f.path, value} }

// Value 与 Set 相同，对应 gorm-plus 字段赋值 dao.Field.Value。
func (f Field[T]) Value(value T) UpdateOp { return f.Set(value) }

// Unset 删除字段。
func (f Field[T]) Unset() UpdateOp { return UpdateOp{"$unset", f.path, ""} }

// Number 是支持 MongoDB 数值递增的 Go 数值类型。
type Number interface {
	~int | ~int32 | ~int64 | ~float32 | ~float64
}

// Numeric 提供数值字段的额外操作。
type Numeric[T Number] struct{ Field[T] }

// NewNumeric 创建数值字段。
func NewNumeric[T Number](path string) Numeric[T] { return Numeric[T]{New[T](path)} }

// At 返回带关联别名前缀的数值字段。
func (f Numeric[T]) At(alias string) Numeric[T] { return NewNumeric[T](alias + "." + f.path) }

// Inc 对数值字段执行原子递增。
func (f Numeric[T]) Inc(value T) UpdateOp { return UpdateOp{"$inc", f.path, value} }

// String 提供字符串的字面量匹配；不会将用户文本直接解释为正则表达式。
type String struct{ Field[string] }

// NewString 创建字符串字段。
func NewString(path string) String { return String{New[string](path)} }

// At 返回带关联别名前缀的字符串字段，保留 Contains 等方法。
func (f String) At(alias string) String { return NewString(alias + "." + f.path) }

// Contains 匹配包含文本的字符串。
func (f String) Contains(value string) Condition {
	return f.condition("$regex", bson.Regex{Pattern: regexp.QuoteMeta(value)})
}

// StartsWith 匹配以文本开头的字符串。
func (f String) StartsWith(value string) Condition {
	return f.condition("$regex", bson.Regex{Pattern: "^" + regexp.QuoteMeta(value)})
}

// EndsWith 匹配以文本结尾的字符串。
func (f String) EndsWith(value string) Condition {
	return f.condition("$regex", bson.Regex{Pattern: regexp.QuoteMeta(value) + "$"})
}

// Array 提供数组字段查询和修改。
type Array[T any] struct{ Field[[]T] }

// NewArray 创建数组字段。
func NewArray[T any](path string) Array[T] { return Array[T]{New[[]T](path)} }

// At 返回带关联别名前缀的数组字段。
func (f Array[T]) At(alias string) Array[T] { return NewArray[T](alias + "." + f.path) }

// Has 匹配包含指定元素的数组。
func (f Array[T]) Has(value T) Condition { return f.condition("$eq", value) }

// Push 追加数组元素。
func (f Array[T]) Push(values ...T) UpdateOp {
	return UpdateOp{"$push", f.path, bson.D{{Key: "$each", Value: values}}}
}

// AddToSet 追加数组元素，保留 MongoDB 集合去重语义。
func (f Array[T]) AddToSet(values ...T) UpdateOp {
	return UpdateOp{"$addToSet", f.path, bson.D{{Key: "$each", Value: values}}}
}

// Pull 删除数组中的指定元素。
func (f Array[T]) Pull(value T) UpdateOp { return UpdateOp{"$pull", f.path, value} }

// ElemMatch 要求数组的同一个元素满足所有给定条件。
func (f Array[T]) ElemMatch(conditions ...Condition) Condition {
	c := And(conditions...)
	if c.Err != nil {
		return c
	}
	return f.condition("$elemMatch", c.Document)
}

// And 使用显式 $and 合并条件，避免相同字段的条件互相覆盖。
func And(conditions ...Condition) Condition { return group("$and", conditions) }

// Or 构造 OR 分组。
func Or(conditions ...Condition) Condition { return group("$or", conditions) }

func group(op string, conditions []Condition) Condition {
	docs := bson.A{}
	for _, c := range conditions {
		if c.Err != nil {
			return c
		}
		if len(c.Document) != 0 {
			docs = append(docs, c.Document)
		}
	}
	if len(docs) == 0 {
		return Condition{Err: fmt.Errorf("%s 分组不能为空", op)}
	}
	if len(docs) == 1 {
		return Condition{Document: docs[0].(bson.D)}
	}
	return Condition{Document: bson.D{{Key: op, Value: docs}}}
}
