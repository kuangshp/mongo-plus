package config

import (
    {{.authImport}}
    "github.com/kuangshp/mongo-plus/httpcors"
    "github.com/kuangshp/mongo-plus/httpresponse"
    "github.com/kuangshp/mongo-plus/httpstatic"
    "github.com/kuangshp/mongo-plus/httpswagger"
    "github.com/kuangshp/mongo-plus/logrotate"
)

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

// MongoConfig 是服务的运行时配置；schema 生成与同步使用独立的 mongo-plus.yaml。
type MongoConfig struct {
	Enabled bool `json:",default=false"`
	URI string `json:",optional"`
	Database string `json:",optional"`
	MaxPoolSize uint64 `json:",default=100"`
	TimeoutSeconds int64 `json:",default=10"`
}
