package plugin

import (
	"context"
	"errors"
	"strings"
	"sync"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// DataPermissionInjectFn 按集合返回额外过滤条件。空文档表示不加限制。
type DataPermissionInjectFn func(collection string) (bson.D, error)

// DataPermissionConfig 配置不参与数据权限的集合。
type DataPermissionConfig struct {
	ExcludeCollections []string
	// Strict 开启后，未排除的集合缺少注入函数时返回 ErrDataPermissionRequired。
	// 默认 false 兼容旧项目；面向严格权限的服务应显式开启。
	Strict bool
}

// DataPermission 在查询、更新和删除时追加调用方给出的权限条件。
type DataPermission struct {
	mu       sync.Mutex
	excluded map[string]struct{}
	strict   bool
}

var ErrDataPermissionRequired = errors.New("缺少数据权限上下文")

type dataPermissionKey struct{}
type dataPermissionSkipKey struct{}

// NewDataPermission 创建数据权限插件。
func NewDataPermission(cfg DataPermissionConfig) *DataPermission {
	state := &DataPermission{excluded: map[string]struct{}{}, strict: cfg.Strict}
	state.AddExclude(cfg.ExcludeCollections...)
	return state
}

// WithDataPermission 把注入函数写入上下文。
func WithDataPermission(ctx context.Context, fn DataPermissionInjectFn) context.Context {
	return context.WithValue(ctx, dataPermissionKey{}, fn)
}

// DataPermissionFromCtx 读取注入函数。
func DataPermissionFromCtx(ctx context.Context) DataPermissionInjectFn {
	if ctx == nil {
		return nil
	}
	fn, _ := ctx.Value(dataPermissionKey{}).(DataPermissionInjectFn)
	return fn
}

// SkipDataPermission 让当前上下文跳过数据权限。
func SkipDataPermission(ctx context.Context) context.Context {
	return context.WithValue(ctx, dataPermissionSkipKey{}, true)
}

// AddExclude 追加排除集合。
func (p *DataPermission) AddExclude(collections ...string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, name := range collections {
		if name = strings.TrimSpace(name); name != "" {
			p.excluded[name] = struct{}{}
		}
	}
}

// RemoveExclude 取消排除。
func (p *DataPermission) RemoveExclude(collections ...string) {
	if p == nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, name := range collections {
		delete(p.excluded, strings.TrimSpace(name))
	}
}

// Excluded 返回排除集合。
func (p *DataPermission) Excluded() []string {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	names := make([]string, 0, len(p.excluded))
	for name := range p.excluded {
		names = append(names, name)
	}
	return names
}

// Apply 返回要追加的权限条件。
func (p *DataPermission) Apply(ctx context.Context, collection string) (bson.D, error) {
	if p == nil {
		return nil, nil
	}
	if ctx != nil {
		if skipped, _ := ctx.Value(dataPermissionSkipKey{}).(bool); skipped {
			return nil, nil
		}
	}
	p.mu.Lock()
	_, excluded := p.excluded[collection]
	p.mu.Unlock()
	if excluded {
		return nil, nil
	}
	if ctx == nil {
		if p.strict {
			return nil, ErrDataPermissionRequired
		}
		return nil, nil
	}
	fn := DataPermissionFromCtx(ctx)
	if fn == nil {
		if p.strict {
			return nil, ErrDataPermissionRequired
		}
		return nil, nil
	}
	return fn(collection)
}
