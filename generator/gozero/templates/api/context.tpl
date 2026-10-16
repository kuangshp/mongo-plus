package svc

import (
	"context"
	"fmt"
	"time"

	mongoplus "github.com/kuangshp/mongo-plus"
	{{.configImport}}
)

type ServiceContext struct {
	Config {{.config}}
	MongoDB *mongoplus.Database // 所有 Repository 共用此连接池；关闭 MongoConfig 时为 nil。
	// 在此注入生成的 Repository，具体写法见 mongo-plus 的 go-zero 文档。
	{{.middleware}}
}

func NewServiceContext(c {{.config}}) *ServiceContext {
	var db *mongoplus.Database
	if c.MongoConfig.Enabled {
		if c.MongoConfig.TimeoutSeconds <= 0 || c.MongoConfig.TimeoutSeconds > 3600 {
			panic("MongoConfig.TimeoutSeconds 必须在 1 到 3600 秒之间")
		}
		timeout := time.Duration(c.MongoConfig.TimeoutSeconds) * time.Second
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()
		var err error
		db, err = mongoplus.Connect(ctx, mongoplus.Config{
			URI: c.MongoConfig.URI, Database: c.MongoConfig.Database,
			MaxPoolSize: c.MongoConfig.MaxPoolSize, Timeout: timeout,
			Log: c.MongoConfig.Log,
		})
		if err != nil { panic(fmt.Errorf("初始化 MongoDB 失败: %w", err)) }
	}
	return &ServiceContext{
		Config: c,
		MongoDB: db,
		{{.middlewareAssignment}}
	}
}

// Close 由启动入口在服务停止接收请求后调用，释放共享的 MongoDB Client。
func (s *ServiceContext) Close() error {
	if s.MongoDB == nil { return nil }
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.MongoDB.Close(ctx)
}
