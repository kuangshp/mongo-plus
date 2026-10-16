package mongoplus

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// DataPermissionInjectFn 由中间件按集合返回额外过滤条件。
// 返回空文档表示当前集合不加限制；返回错误会让本次查询失败。
type DataPermissionInjectFn func(collection string) (bson.D, error)

// DataPermissionConfig 配置不参与数据权限的集合。
type DataPermissionConfig struct {
	ExcludeCollections []string
}

type dataPermission struct {
	mu       sync.Mutex
	excluded map[string]struct{}
}

type dataPermissionKey struct{}
type dataPermissionSkipKey struct{}

// RegisterDataPermission 在数据库上启用数据权限。查询、更新和删除都会读取上下文中的注入函数。
func RegisterDataPermission(db *Database, cfg DataPermissionConfig) error {
	if db == nil {
		return fmt.Errorf("数据权限数据库不能为空")
	}
	if db.permission != nil {
		return fmt.Errorf("数据权限已经注册")
	}
	state := &dataPermission{excluded: map[string]struct{}{}}
	for _, name := range cfg.ExcludeCollections {
		if name = strings.TrimSpace(name); name != "" {
			state.excluded[name] = struct{}{}
		}
	}
	db.permission = state
	return nil
}

// WithDataPermission 把注入函数写入上下文，通常在鉴权中间件中调用。
func WithDataPermission(ctx context.Context, fn DataPermissionInjectFn) context.Context {
	return context.WithValue(resolveContext(ctx), dataPermissionKey{}, fn)
}

// DataPermissionFromCtx 读取注入函数；没有注册时返回 nil。
func DataPermissionFromCtx(ctx context.Context) DataPermissionInjectFn {
	fn, _ := resolveContext(ctx).Value(dataPermissionKey{}).(DataPermissionInjectFn)
	return fn
}

// SkipDataPermission 让当前上下文跳过数据权限，用于受控的全量查询。
func SkipDataPermission(ctx context.Context) context.Context {
	return context.WithValue(resolveContext(ctx), dataPermissionSkipKey{}, true)
}

// AddDataPermissionExcludeCollection 运行时追加不参与过滤的集合。
func AddDataPermissionExcludeCollection(db *Database, collections ...string) error {
	state, err := permissionState(db)
	if err != nil {
		return err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, name := range collections {
		if name = strings.TrimSpace(name); name != "" {
			state.excluded[name] = struct{}{}
		}
	}
	return nil
}

// RemoveDataPermissionExcludeCollection 取消集合排除。
func RemoveDataPermissionExcludeCollection(db *Database, collections ...string) error {
	state, err := permissionState(db)
	if err != nil {
		return err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	for _, name := range collections {
		delete(state.excluded, strings.TrimSpace(name))
	}
	return nil
}

// DataPermissionExcludedCollections 返回当前排除集合。
func DataPermissionExcludedCollections(db *Database) ([]string, error) {
	state, err := permissionState(db)
	if err != nil {
		return nil, err
	}
	state.mu.Lock()
	defer state.mu.Unlock()
	names := make([]string, 0, len(state.excluded))
	for name := range state.excluded {
		names = append(names, name)
	}
	return names, nil
}

func permissionState(db *Database) (*dataPermission, error) {
	if db == nil || db.permission == nil {
		return nil, fmt.Errorf("数据权限尚未注册")
	}
	return db.permission, nil
}

func (p *dataPermission) apply(ctx context.Context, collection string) (bson.D, error) {
	if p == nil || ctx == nil {
		return nil, nil
	}
	if skipped, _ := ctx.Value(dataPermissionSkipKey{}).(bool); skipped {
		return nil, nil
	}
	p.mu.Lock()
	_, excluded := p.excluded[collection]
	p.mu.Unlock()
	if excluded {
		return nil, nil
	}
	fn, _ := ctx.Value(dataPermissionKey{}).(DataPermissionInjectFn)
	if fn == nil {
		return nil, nil
	}
	return fn(collection)
}
