package plugin

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// ErrTenantRequired 表示已启用租户隔离但上下文没有可用的租户 ID。
var ErrTenantRequired = fmt.Errorf("缺少租户信息")

type tenantValueKey struct{}
type tenantSkipKey struct{}
type tenantGlobalKey struct{}

type tenantIDKey[T comparable] struct{}
type tenantOverrideKey[T comparable] struct{}

// TenantFieldConfig 是一个租户字段及其取值方式。
type TenantFieldConfig[T comparable] struct {
	Field       string
	GetTenantID func(context.Context) (T, bool)
}

// TenantConfig 是多租户插件配置。
// 字段优先级：CollectionFields > TenantFields > TenantField。
// CollectionFields 中的空切片表示该集合不注入。
type TenantConfig[T comparable] struct {
	TenantField           string
	TenantFields          []TenantFieldConfig[T]
	CollectionFields      map[string][]TenantFieldConfig[T]
	ExcludeCollections    []string
	GetTenantID           func(context.Context) (T, bool)
	AllowOverrideTenantID bool
}

// TenantPlugin 在仓储查询、创建、更新和删除时注入租户条件。
type TenantPlugin struct {
	mu            sync.Mutex
	excluded      map[string]struct{}
	defaults      []tenantField
	byCollection  map[string]tenantCollection
	allowOverride bool
}

type tenantField struct {
	name string
	get  func(context.Context) (any, bool)
}

type tenantCollection struct {
	skip   bool
	fields []tenantField
}

// WithTenantValue 把租户 ID 写入上下文。类型化的 WithTenantID 也会写入这里。
func WithTenantValue(ctx context.Context, id any) context.Context {
	return context.WithValue(ctx, tenantValueKey{}, id)
}

// TenantValue 读取 WithTenantValue 写入的租户 ID。
func TenantValue(ctx context.Context) (any, bool) {
	if ctx == nil {
		return nil, false
	}
	value := ctx.Value(tenantValueKey{})
	if value == nil || reflect.ValueOf(value).IsZero() {
		return nil, false
	}
	return value, true
}

// WithTenantID 按具体类型写入租户 ID。
func WithTenantID[T comparable](ctx context.Context, id T) context.Context {
	ctx = context.WithValue(ctx, tenantIDKey[T]{}, id)
	return WithTenantValue(ctx, id)
}

// TenantIDFromCtx 读取与 T 对应的租户 ID。
func TenantIDFromCtx[T comparable](ctx context.Context) (T, bool) {
	if ctx == nil {
		var zero T
		return zero, false
	}
	value, ok := ctx.Value(tenantIDKey[T]{}).(T)
	if !ok || isZero(value) {
		var zero T
		return zero, false
	}
	return value, true
}

// SkipTenant 让当前上下文跳过租户条件。
func SkipTenant(ctx context.Context) context.Context {
	return context.WithValue(ctx, tenantSkipKey{}, true)
}

// TenantSkipped 判断当前上下文是否跳过租户。
func TenantSkipped(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	skipped, _ := ctx.Value(tenantSkipKey{}).(bool)
	return skipped
}

// AllowGlobalOperation 允许当前上下文在只有租户条件时执行批量更新或删除。
func AllowGlobalOperation(ctx context.Context) context.Context {
	return context.WithValue(ctx, tenantGlobalKey{}, true)
}

// GlobalAllowed 判断是否允许无业务条件的批量写。
func GlobalAllowed(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	allowed, _ := ctx.Value(tenantGlobalKey{}).(bool)
	return allowed
}

// WithOverrideTenantID 在允许覆盖时切换到另一个租户。
func WithOverrideTenantID[T comparable](ctx context.Context, id T) context.Context {
	return context.WithValue(ctx, tenantOverrideKey[T]{}, id)
}

// NewTenantPlugin 创建多租户插件。TenantField 和 TenantFields 不能同时为空。
func NewTenantPlugin[T comparable](cfg TenantConfig[T]) (*TenantPlugin, error) {
	defaults, err := tenantSpecs[T](cfg, cfg.TenantFields, cfg.TenantField, cfg.GetTenantID)
	if err != nil {
		return nil, err
	}
	if len(defaults) == 0 {
		return nil, fmt.Errorf("TenantField 和 TenantFields 不能同时为空")
	}
	plugin := &TenantPlugin{
		excluded:      map[string]struct{}{},
		defaults:      defaults,
		byCollection:  map[string]tenantCollection{},
		allowOverride: cfg.AllowOverrideTenantID,
	}
	for _, name := range cfg.ExcludeCollections {
		if name = strings.TrimSpace(name); name != "" {
			plugin.excluded[name] = struct{}{}
		}
	}
	for name, fields := range cfg.CollectionFields {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if len(fields) == 0 {
			plugin.byCollection[name] = tenantCollection{skip: true}
			continue
		}
		specs, err := tenantSpecs[T](cfg, fields, "", cfg.GetTenantID)
		if err != nil {
			return nil, err
		}
		plugin.byCollection[name] = tenantCollection{fields: specs}
	}
	return plugin, nil
}

func tenantSpecs[T comparable](cfg TenantConfig[T], fields []TenantFieldConfig[T], single string, fallback func(context.Context) (T, bool)) ([]tenantField, error) {
	if len(fields) == 0 && single != "" {
		fields = []TenantFieldConfig[T]{{Field: single}}
	}
	specs := make([]tenantField, 0, len(fields))
	seen := map[string]struct{}{}
	for _, item := range fields {
		name := strings.TrimSpace(item.Field)
		if name == "" {
			return nil, fmt.Errorf("租户字段名不能为空")
		}
		if _, ok := seen[name]; ok {
			continue
		}
		seen[name] = struct{}{}
		getter := item.GetTenantID
		if getter == nil {
			getter = fallback
		}
		specs = append(specs, tenantField{name: name, get: tenantGetter(cfg.AllowOverrideTenantID, getter)})
	}
	return specs, nil
}

func tenantGetter[T comparable](allowOverride bool, getter func(context.Context) (T, bool)) func(context.Context) (any, bool) {
	return func(ctx context.Context) (any, bool) {
		if allowOverride {
			if value, ok := ctx.Value(tenantOverrideKey[T]{}).(T); ok && !isZero(value) {
				return value, true
			}
		}
		if getter != nil {
			if value, ok := getter(ctx); ok && !isZero(value) {
				return value, true
			}
		}
		if value, ok := TenantIDFromCtx[T](ctx); ok {
			return value, true
		}
		if value, ok := TenantValue(ctx); ok {
			typed, isTyped := value.(T)
			if isTyped && !isZero(typed) {
				return typed, true
			}
		}
		return nil, false
	}
}

// AddExclude 追加不参与租户隔离的集合。
func (p *TenantPlugin) AddExclude(collections ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, name := range collections {
		if name = strings.TrimSpace(name); name != "" {
			p.excluded[name] = struct{}{}
		}
	}
}

// RemoveExclude 让集合重新参与租户隔离。
func (p *TenantPlugin) RemoveExclude(collections ...string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, name := range collections {
		delete(p.excluded, strings.TrimSpace(name))
	}
}

// Excluded 返回当前排除的集合。
func (p *TenantPlugin) Excluded() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.excluded))
	for name := range p.excluded {
		names = append(names, name)
	}
	return names
}

func (p *TenantPlugin) excludedCollection(name string) bool {
	p.mu.Lock()
	_, ok := p.excluded[name]
	p.mu.Unlock()
	return ok
}

func (p *TenantPlugin) fieldsFor(collection, fallback string) ([]tenantField, bool) {
	if p.excludedCollection(collection) {
		return nil, true
	}
	if rule, ok := p.byCollection[collection]; ok {
		if rule.skip {
			return nil, true
		}
		return append([]tenantField(nil), rule.fields...), false
	}
	fields := append([]tenantField(nil), p.defaults...)
	if fallback != "" {
		found := false
		for _, item := range fields {
			if item.name == fallback {
				found = true
				break
			}
		}
		if !found {
			fields = append(fields, p.defaults[0])
			fields[len(fields)-1].name = fallback
		}
	}
	return fields, false
}

// Conditions 返回当前集合要强制加上的租户条件。第二个返回值表示该集合被排除。
func (p *TenantPlugin) Conditions(ctx context.Context, collection, fallback string) ([]bson.D, bool, error) {
	if p == nil || TenantSkipped(ctx) {
		return nil, false, nil
	}
	fields, skip := p.fieldsFor(collection, fallback)
	if skip {
		return nil, true, nil
	}
	documents := make([]bson.D, 0, len(fields))
	for _, item := range fields {
		value, ok := item.get(ctx)
		if !ok {
			return nil, false, ErrTenantRequired
		}
		condition := field.New[any](item.name).Eq(value)
		if condition.Err != nil {
			return nil, false, condition.Err
		}
		documents = append(documents, condition.Document)
	}
	return documents, false, nil
}

// ProtectedFields 返回当前集合实际生效的租户字段；更新时同时保护父子路径。
// 即使本次 Context 使用 SkipTenant，也不能通过普通更新迁移记录的租户归属。
func (p *TenantPlugin) ProtectedFields(collection, fallback string) []string {
	if p == nil {
		return nil
	}
	fields, skip := p.fieldsFor(collection, fallback)
	if skip {
		return nil
	}
	names := make([]string, 0, len(fields))
	for _, item := range fields {
		names = append(names, item.name)
	}
	return names
}

// CreateValues 返回创建文档时要写入的租户字段。
func (p *TenantPlugin) CreateValues(ctx context.Context, collection, fallback string) (map[string]any, bool, error) {
	documents, skip, err := p.Conditions(ctx, collection, fallback)
	if err != nil || skip || len(documents) == 0 {
		return nil, skip, err
	}
	values := make(map[string]any, len(documents))
	for _, document := range documents {
		value := document[0].Value
		if eq, ok := value.(bson.D); ok && len(eq) == 1 {
			value = eq[0].Value
		}
		values[document[0].Key] = value
	}
	return values, false, nil
}

func isZero[T comparable](value T) bool {
	return reflect.ValueOf(value).IsZero()
}
