package mongoplus

import (
	"context"
	"sync/atomic"
)

type contextResolver struct {
	resolve func(context.Context) context.Context
}

var registeredContextResolver atomic.Pointer[contextResolver]

// RegisterCtxResolver 注册框架 Context 到标准 Context 的解析器，通常在启动时调用。
// Gin 项目可将 *gin.Context 转为 c.Request.Context()，沿用 gorm-plus 的注册方式。
// fn 应保留标准 Context（特别是事务 Context），传 nil 恢复默认行为。
func RegisterCtxResolver(fn func(context.Context) context.Context) {
	if fn == nil {
		registeredContextResolver.Store(nil)
		return
	}
	registeredContextResolver.Store(&contextResolver{resolve: fn})
}

func resolveContext(ctx context.Context) context.Context {
	if resolver := registeredContextResolver.Load(); resolver != nil {
		if resolved := resolver.resolve(ctx); resolved != nil {
			return resolved
		}
	}
	return ctx
}
