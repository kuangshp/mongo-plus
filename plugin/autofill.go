package plugin

import (
	"context"
	"reflect"
	"strings"
)

type contextKey string

const (
	CtxOperatorKey1  contextKey = "operator1"
	CtxOperatorKey2  contextKey = "operator2"
	CtxOperatorKey3  contextKey = "operator3"
	CtxOperatorKey4  contextKey = "operator4"
	CtxOperatorKey5  contextKey = "operator5"
	CtxOperatorKey6  contextKey = "operator6"
	CtxOperatorKey7  contextKey = "operator7"
	CtxOperatorKey8  contextKey = "operator8"
	CtxOperatorKey9  contextKey = "operator9"
	CtxOperatorKey10 contextKey = "operator10"
)

// FieldGetter 从上下文读取自动填充的值。
type FieldGetter func(context.Context) any

// FieldConfig 描述一个自动填充字段。Name 可以是 BSON 路径，也可以是 CreatedBy 这样的 Go 字段名。
type FieldConfig struct {
	Name     string
	Getter   FieldGetter
	OnCreate bool
	OnUpdate bool
}

// AutoFillConfig 是操作人自动填充插件的配置。
type AutoFillConfig struct {
	Fields []FieldConfig
}

// Assignment 是一次要写入文档的字段赋值。
type Assignment struct {
	Path  string
	Value any
}

// AutoFillPlugin 在创建和更新时写入操作人及其他上下文字段。
type AutoFillPlugin struct {
	fields []FieldConfig
}

// NewAutoFillPlugin 创建自动填充插件。
func NewAutoFillPlugin(cfg AutoFillConfig) (*AutoFillPlugin, error) {
	fields := make([]FieldConfig, 0, len(cfg.Fields))
	for _, item := range cfg.Fields {
		item.Name = bsonPath(item.Name)
		if item.Name == "" || item.Getter == nil || (!item.OnCreate && !item.OnUpdate) {
			return nil, errAutoFillConfig
		}
		fields = append(fields, item)
	}
	if len(fields) == 0 {
		return nil, errAutoFillConfig
	}
	return &AutoFillPlugin{fields: fields}, nil
}

var errAutoFillConfig = configError("自动填充字段需要名称、取值函数，以及 OnCreate 或 OnUpdate")

type configError string

func (e configError) Error() string { return string(e) }

// WithOperator 把操作人写入默认的 operator1。
func WithOperator(ctx context.Context, id any) context.Context {
	return context.WithValue(ctx, CtxOperatorKey1, id)
}

// OperatorGetter 读取 WithOperator 或 WithActor 写入的操作人。
func OperatorGetter[T any]() FieldGetter {
	return CtxGetter[T](CtxOperatorKey1)
}

// CtxGetter 按 key 读取上下文，类型不匹配时返回 nil。
func CtxGetter[T any](key any) FieldGetter {
	return func(ctx context.Context) any {
		if ctx == nil {
			return nil
		}
		value, ok := ctx.Value(key).(T)
		if !ok || reflect.ValueOf(value).IsZero() {
			return nil
		}
		return value
	}
}

// OnCreate 返回创建时要写入的字段。
func (p *AutoFillPlugin) OnCreate(ctx context.Context) []Assignment {
	return p.assignments(ctx, true)
}

// OnUpdate 返回更新时要写入的字段。
func (p *AutoFillPlugin) OnUpdate(ctx context.Context) []Assignment {
	return p.assignments(ctx, false)
}

func (p *AutoFillPlugin) assignments(ctx context.Context, create bool) []Assignment {
	if p == nil {
		return nil
	}
	values := make([]Assignment, 0, len(p.fields))
	for _, item := range p.fields {
		if create && !item.OnCreate || !create && !item.OnUpdate {
			continue
		}
		value := item.Getter(ctx)
		if value == nil || reflect.ValueOf(value).IsZero() {
			continue
		}
		values = append(values, Assignment{Path: item.Name, Value: value})
	}
	return values
}

func bsonPath(name string) string {
	name = strings.TrimSpace(name)
	if strings.Contains(name, ".") {
		parts := strings.Split(name, ".")
		for i, part := range parts {
			parts[i] = bsonPath(part)
		}
		return strings.Join(parts, ".")
	}
	if name == "" || strings.Contains(name, "_") || name == strings.ToLower(name) {
		return name
	}
	var result strings.Builder
	for i, char := range name {
		if i > 0 && char >= 'A' && char <= 'Z' {
			result.WriteByte('_')
		}
		result.WriteRune(char)
	}
	return strings.ToLower(result.String())
}
