# go-zero 模板示例

在此目录运行 `make gen`，会从本地源码安装工具，通过根目录 generator.go 调用 mongo-plus 公共包，根据 schema/*.json 生成数据层，再根据 shop.api 生成 HTTP 服务。

生成后执行 `go mod tidy`，再执行 `make run` 或 `make build`。默认关闭 MongoDB 和 Swagger，可以先验证 HTTP 参数解析与中文校验。

`/demo/check` 展示请求契约；goctl 生成的 Logic 为待实现的业务入口。按 `docs/gozero.md` 完成返回值、Repository 注入和数据库配置后再添加真实业务逻辑。

开启 Swagger：修改生成的 `etc/shop-api.yaml` 中 `SwaggerConfig.Enabled: true`；后续 run/build 会先更新并内嵌文档。

开启 MongoDB：修改同一文件的 `MongoConfig`，填写 URI、数据库并启用。同步集合与索引使用可选的 `mongo-plus.yaml`，通过 `make schema-plan`、`make schema-apply` 显式执行。

完整说明见仓库的 [go-zero 使用文档](../../docs/gozero.md)。
