# go-zero 模板示例

在此目录运行 `make gen`，会安装工具，从 mongo-plus.yaml 读取生成配置，根据 schema/*.json 生成数据层，再根据 shop.api 生成 HTTP 服务。无需在 generator.go 内维护数据源或输出路径。

默认 YAML 注释了 mongodb 段，因此生成时不连接数据库。需要在生成时创建集合、索引、校验器，填写该段的 uri、database 后仍执行 `make gen` 或 `go run generator.go`。不配置时，集合由服务中的首次业务写入创建；唯一索引和校验器需要显式同步。

pluralize: true 表示示例使用 users 等复数名称；false 或省略则使用 user、sys_user。数据层输出在 internal/dal/entity、dao、repository、dto、vo，DTO/VO 首次创建后保留业务修改。

生成后执行 `go mod tidy`，再执行 `make run` 或 `make build`。服务运行配置默认关闭 MongoDB 和 Swagger。`/demo/check` 展示请求契约，Logic 的业务实现、Repository 注入参考主文档。

开启 Swagger：修改生成的 etc/shop-api.yaml 中 SwaggerConfig.Enabled: true；后续 run/build 会先更新并内嵌文档。

开启服务的 MongoDB：配置 etc/shop-api.yaml 的 MongoConfig 并启用，与生成器 YAML 使用同一数据源。需要单独预览或同步集合时，可执行 `make schema-plan`、`make schema-apply`。

完整说明见 [go-zero 使用文档](../../docs/gozero.md)。
