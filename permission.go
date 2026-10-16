package mongoplus

import (
	"context"
	"fmt"

	"github.com/kuangshp/mongo-plus/plugin"
)

// DataPermissionInjectFn 按集合返回额外过滤条件。
type DataPermissionInjectFn = plugin.DataPermissionInjectFn

// DataPermissionConfig 配置不参与数据权限的集合。
type DataPermissionConfig = plugin.DataPermissionConfig

// ErrDataPermissionRequired 可用 errors.Is 判断严格模式中缺失的权限上下文。
var ErrDataPermissionRequired = plugin.ErrDataPermissionRequired

// RequireDataPermission 供鉴权中间件在进入业务层前检查权限注入是否完整。
// 返回 nil 仅表示存在注入函数；具体集合的权限条件仍在 Repository 执行时检查。
func RequireDataPermission(ctx context.Context) error {
	if ctx == nil || DataPermissionFromCtx(ctx) == nil {
		return ErrDataPermissionRequired
	}
	return nil
}

// RegisterDataPermission 启用数据权限插件。查询、更新和删除都会读取上下文中的注入函数。
func RegisterDataPermission(db *Database, cfg DataPermissionConfig) error {
	if db == nil {
		return fmt.Errorf("数据权限数据库不能为空")
	}
	if db.permission != nil {
		return fmt.Errorf("数据权限已经注册")
	}
	db.permission = plugin.NewDataPermission(cfg)
	return nil
}

// WithDataPermission 把注入函数写入上下文，通常在鉴权中间件中调用。
func WithDataPermission(ctx context.Context, fn DataPermissionInjectFn) context.Context {
	return plugin.WithDataPermission(resolveContext(ctx), fn)
}

// DataPermissionFromCtx 读取注入函数；没有注册时返回 nil。
func DataPermissionFromCtx(ctx context.Context) DataPermissionInjectFn {
	return plugin.DataPermissionFromCtx(resolveContext(ctx))
}

// SkipDataPermission 让当前上下文跳过数据权限，用于受控的全量查询。
func SkipDataPermission(ctx context.Context) context.Context {
	return plugin.SkipDataPermission(resolveContext(ctx))
}

// AddDataPermissionExcludeCollection 运行时追加不参与过滤的集合。
func AddDataPermissionExcludeCollection(db *Database, collections ...string) error {
	if db == nil || db.permission == nil {
		return fmt.Errorf("数据权限尚未注册")
	}
	db.permission.AddExclude(collections...)
	return nil
}

// RemoveDataPermissionExcludeCollection 取消集合排除。
func RemoveDataPermissionExcludeCollection(db *Database, collections ...string) error {
	if db == nil || db.permission == nil {
		return fmt.Errorf("数据权限尚未注册")
	}
	db.permission.RemoveExclude(collections...)
	return nil
}

// DataPermissionExcludedCollections 返回当前排除集合。
func DataPermissionExcludedCollections(db *Database) ([]string, error) {
	if db == nil || db.permission == nil {
		return nil, fmt.Errorf("数据权限尚未注册")
	}
	names := db.permission.Excluded()
	if names == nil {
		return []string{}, nil
	}
	return names, nil
}
