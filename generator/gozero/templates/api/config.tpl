package config

import (
    {{.authImport}}
    mongoplus "github.com/kuangshp/mongo-plus"
    "github.com/kuangshp/mongo-plus/httpcors"
    "github.com/kuangshp/mongo-plus/httpresponse"
    "github.com/kuangshp/mongo-plus/httpstatic"
    "github.com/kuangshp/mongo-plus/httpswagger"
    "github.com/kuangshp/mongo-plus/logrotate"
)

// Config 汇总 HTTP 服务及各可选组件的运行配置。
type Config struct {
	rest.RestConf
	{{.auth}}
	{{.jwtTrans}}
    LogRotation logrotate.Config // 按天/小时写日志、延迟压缩及过期清理。
    ResponseConfig httpresponse.Config // 统一响应及跳过包装的路径。
    CorsConfig httpcors.Config // 跨域来源、额外请求头和暴露的响应头。
    StaticConfig httpstatic.Config // 可选的本机公开文件目录与 URL 映射。
    SwaggerConfig httpswagger.Config // 可选的内嵌 Swagger UI 与 API 文档。
	MongoConfig MongoConfig `json:",optional"` // MongoDB 连接；关闭时可先独立开发和验证 HTTP 接口。
}

// MongoConfig 是服务运行时连接；generator.go 可复用此连接，生成规则只在 Go 中维护。
type MongoConfig struct {
	Enabled        bool                `json:",default=false"` // 是否初始化 MongoDB；省略时关闭。
	URI            string              `json:",optional"`      // 开启时必填，认证和副本集参数也放在 URI 中。
	Database       string              `json:",optional"`      // 开启时必填，指定业务数据库名。
	MaxPoolSize    uint64              `json:",default=100"`   // Client 最大连接数；所有 Repository 共用连接池。
	TimeoutSeconds int64               `json:",default=10"`    // 初始化及驱动操作的超时秒数，范围 1～3600。
	Log            mongoplus.LogConfig `json:",optional"`      // 命令日志独立于服务 Log，level=info 开启全部命令输出。
}
