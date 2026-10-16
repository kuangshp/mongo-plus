package {{.pkgName}}

import (
	"net/http"

	{{.imports}}
)

// {{.logic}} 保存当前请求的上下文及业务依赖。
type {{.logic}} struct {
	logx.Logger
	ctx     context.Context // 保留取消信号、租户和链路信息，查询仓储时继续传递。
	svcCtx  *svc.ServiceContext // 复用服务启动时初始化的数据库和仓储。
	Request *http.Request // 当前 HTTP 请求，供鉴权或读取请求元数据使用。
	W       http.ResponseWriter // 当前 HTTP 响应，流式输出等场景可按需使用。
}

// New{{.logic}} 创建接口逻辑。
func New{{.logic}}(ctx context.Context, svcCtx *svc.ServiceContext, request *http.Request, w http.ResponseWriter) *{{.logic}} {
	return &{{.logic}}{
		Logger:  logx.WithContext(ctx),
		ctx:     ctx,
		svcCtx:  svcCtx,
		Request: request,
		W:       w,
	}
}

// {{.function}} 处理接口业务。
func (l *{{.logic}}) {{.function}}({{.request}}) {{.responseType}} {
	// todo: add your logic here and delete this line

	{{.returnString}}
}
