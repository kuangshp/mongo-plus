package mongoplus

import (
	"context"
	"fmt"

	"github.com/kuangshp/mongo-plus/plugin"
)

// TenantConfig 是多租户插件配置，用法对应 gorm-plus 的 TenantConfig。
type TenantConfig[T comparable] = plugin.TenantConfig[T]

// TenantFieldConfig 是单个租户字段的取值配置。
type TenantFieldConfig[T comparable] = plugin.TenantFieldConfig[T]

// AutoFillConfig 是操作人自动填充插件配置。
type AutoFillConfig = plugin.AutoFillConfig

// FieldConfig 是一个自动填充字段。
type FieldConfig = plugin.FieldConfig

// FieldGetter 从上下文读取自动填充的值。
type FieldGetter = plugin.FieldGetter

const (
	CtxOperatorKey1  = plugin.CtxOperatorKey1
	CtxOperatorKey2  = plugin.CtxOperatorKey2
	CtxOperatorKey3  = plugin.CtxOperatorKey3
	CtxOperatorKey4  = plugin.CtxOperatorKey4
	CtxOperatorKey5  = plugin.CtxOperatorKey5
	CtxOperatorKey6  = plugin.CtxOperatorKey6
	CtxOperatorKey7  = plugin.CtxOperatorKey7
	CtxOperatorKey8  = plugin.CtxOperatorKey8
	CtxOperatorKey9  = plugin.CtxOperatorKey9
	CtxOperatorKey10 = plugin.CtxOperatorKey10
)

// RegisterTenant 启用多租户插件。之后仓储的查询、创建、更新和删除都会注入租户字段。
func RegisterTenant[T comparable](db *Database, cfg TenantConfig[T]) error {
	if db == nil {
		return fmt.Errorf("租户数据库不能为空")
	}
	if db.tenant != nil {
		return fmt.Errorf("租户插件已经注册")
	}
	tenant, err := plugin.NewTenantPlugin(cfg)
	if err != nil {
		return err
	}
	db.tenant = tenant
	return nil
}

// WithTenantID 按具体类型写入租户 ID，同时保留 WithTenant 的读取方式。
func WithTenantID[T comparable](ctx context.Context, id T) context.Context {
	ctx = plugin.WithTenantID(resolveContext(ctx), id)
	return context.WithValue(ctx, tenantKey{}, id)
}

// TenantIDFromCtx 读取 WithTenantID 写入的租户 ID。
func TenantIDFromCtx[T comparable](ctx context.Context) (T, bool) {
	return plugin.TenantIDFromCtx[T](resolveContext(ctx))
}

// SkipTenant 让当前上下文跳过租户条件，只用于受控的跨租户操作。
func SkipTenant(ctx context.Context) context.Context {
	return plugin.SkipTenant(resolveContext(ctx))
}

// AllowGlobalOperation 允许当前上下文在仅有租户条件时做批量更新或删除。
func AllowGlobalOperation(ctx context.Context) context.Context {
	return plugin.AllowGlobalOperation(resolveContext(ctx))
}

// WithOverrideTenantID 在插件开启 AllowOverrideTenantID 后切换到指定租户。
func WithOverrideTenantID[T comparable](ctx context.Context, id T) context.Context {
	return plugin.WithOverrideTenantID(resolveContext(ctx), id)
}

// AddTenantExcludeCollection 让集合不再参与租户插件。
func AddTenantExcludeCollection(db *Database, collections ...string) error {
	if db == nil || db.tenant == nil {
		return fmt.Errorf("租户插件尚未注册")
	}
	db.tenant.AddExclude(collections...)
	return nil
}

// RemoveTenantExcludeCollection 让集合重新参与租户插件。
func RemoveTenantExcludeCollection(db *Database, collections ...string) error {
	if db == nil || db.tenant == nil {
		return fmt.Errorf("租户插件尚未注册")
	}
	db.tenant.RemoveExclude(collections...)
	return nil
}

// RegisterAutoFill 启用操作人自动填充插件。
func RegisterAutoFill(db *Database, cfg AutoFillConfig) error {
	if db == nil {
		return fmt.Errorf("自动填充数据库不能为空")
	}
	if db.autofill != nil {
		return fmt.Errorf("自动填充插件已经注册")
	}
	fill, err := plugin.NewAutoFillPlugin(cfg)
	if err != nil {
		return err
	}
	db.autofill = fill
	return nil
}

// NewAutoFillPlugin 创建自动填充插件，可由 RegisterAutoFill 注册。
func NewAutoFillPlugin(cfg AutoFillConfig) (*plugin.AutoFillPlugin, error) {
	return plugin.NewAutoFillPlugin(cfg)
}

// CtxGetter 按上下文 key 读取自动填充的值。
func CtxGetter[T any](key any) FieldGetter { return plugin.CtxGetter[T](key) }

// OperatorGetter 读取 WithActor 或 WithOperator 写入的操作人。
func OperatorGetter[T any]() FieldGetter { return plugin.OperatorGetter[T]() }
