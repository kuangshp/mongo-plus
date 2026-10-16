package middleware

import "net/http"

// {{.name}} 保存当前中间件需要的共享依赖。
type {{.name}} struct {
}

// New{{.name}} 在服务启动时创建中间件，按需注入依赖。
func New{{.name}}() *{{.name}} {
	return &{{.name}}{}
}

// Handle 在业务 Handler 前后执行中间件逻辑。
func (m *{{.name}}) Handle(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO：在此实现鉴权等请求前置处理；写入 Context 后需传递更新后的 Request。

		// 检查通过后调用后续 Handler；需要拦截请求时先写入响应并返回。
		next(w, r)
	}
}
