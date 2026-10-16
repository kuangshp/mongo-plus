package svc

import (
	"context"
	"fmt"
	"time"

	mongoplus "github.com/kuangshp/mongo-plus"
	{{.configImport}}
)

// ServiceContext 保存服务配置、共享数据库连接和业务依赖，在服务启动时创建一次。
type ServiceContext struct {
	Config {{.config}} // 当前服务加载的完整运行配置。
	MongoDB *mongoplus.Database // 所有 Repository 复用此连接池；关闭 MongoConfig 时为 nil。
	// 在此注入生成的 Repository，具体写法见 mongo-plus 的 go-zero 文档。
	{{.middleware}}
}

// NewServiceContext 初始化共享依赖；MongoDB 连接成功后注册默认数据库。
// 这里只建立连接，不同步集合或索引；结构同步由 generator.go 或 schema apply 显式执行。
func NewServiceContext(c {{.config}}) *ServiceContext {
	var db *mongoplus.Database
	if c.MongoConfig.Enabled {
		// YAML 使用秒数，先校验范围，再转换为 Go 的 time.Duration。
		if c.MongoConfig.TimeoutSeconds <= 0 || c.MongoConfig.TimeoutSeconds > 3600 {
			panic("MongoConfig.TimeoutSeconds 必须在 1 到 3600 秒之间")
		}
		timeout := time.Duration(c.MongoConfig.TimeoutSeconds) * time.Second

		// 此 Context 只约束启动连接和 Ping，业务查询使用各自请求的 Context。
		ctx, cancel := context.WithTimeout(context.Background(), timeout)
		defer cancel()

		var err error
		db, err = mongoplus.Connect(ctx, mongoplus.Config{
			URI:         c.MongoConfig.URI,         // 连接地址；认证、副本集和读偏好通过 URI 配置。
			Database:    c.MongoConfig.Database,    // 业务数据库名，集合名由生成的 Repository 决定。
			MaxPoolSize: c.MongoConfig.MaxPoolSize, // 所有 Repository 共用的 Client 最大连接数。
			Timeout:     timeout,                   // 驱动操作的默认超时，请求可设置更早的截止时间。
			Log:         c.MongoConfig.Log,         // level=info 输出命令和调用位置；format=shell 多行显示。
		})
		if err != nil {
			panic(fmt.Errorf("初始化 MongoDB 失败: %w", err))
		}

		// 注册一次默认连接，之后生成的 NewXRepository() 均可无参构造。
		mongoplus.Use(db)
	}

	return &ServiceContext{
		Config: c,
		MongoDB: db,
		{{.middlewareAssignment}}
	}
}

// Close 由启动入口在服务停止接收请求后调用，释放共享的 MongoDB Client。
func (s *ServiceContext) Close() error {
	if s.MongoDB == nil {
		return nil
	}

	// 使用独立的清理 Context，避免服务停止时请求已取消而无法释放连接。
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.MongoDB.Close(ctx)
}
