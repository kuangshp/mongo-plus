# go-zero 模板示例

先写 schema/*.json，再修改根目录 generator.go 的生成参数。在此目录运行 `make gen` 会安装工具、生成数据层，再根据 shop.api 生成 HTTP 服务。无需专用 mongo-plus.yaml，不能通过手写 Entity 代替 JSON。

首次运行可使用 `-mod=mod` 补齐 go.mod / go.sum；Makefile 已包含该参数。

生成参数包括输出目录、主键、时间、软删除、Pluralize，统一写在 generator.go；字段与索引只写 JSON。默认只生成代码，不连接数据库：

```bash
go run -mod=mod generator.go -action check
make gen
go mod tidy
```

服务生成后，根据环境填写 etc/shop-api.yaml 的 MongoConfig。启用且 URI/Database 完整、TimeoutSeconds 在 1～3600 之间时，可以复用该运行连接：

```bash
go run generator.go -action plan -config etc/shop-api.yaml
go run generator.go -action apply -config etc/shop-api.yaml
# 或通过 Makefile 执行相同操作。
make schema-plan
make schema-apply
```

默认 generate 在提供连接时先同步数据库再生成，未提供连接时只生成。check 不连接/写库、不生成；plan 只预览；apply 只同步、不生成文件。HTTP 服务启动不会自动迁移集合。首次写入不能替代 JSON 声明的索引和校验器同步。

生成 Entity、DAO 和仓储基础文件会更新；业务仓储扩展和 DTO/VO 首次生成后保留业务修改。运行 `make run` / `make build` 使用服务自身配置，Swagger 与业务运行 YAML 继续保留。

本例 generator.go 使用 go-zero 的 `conf.Load` 读取已有 MongoConfig，与服务保持相同的键名大小写兼容和默认值：省略 MaxPoolSize 时为 100，省略 TimeoutSeconds 时为 10；显式无效超时仍报错。不导入尚未生成的 internal/config，首次生成不会依赖生成结果。已有服务可改为调用自己的配置加载器。

完整入口与旧 API 迁移见 [JSON 模型生成文档](../../docs/generator.md)，HTTP 模板见 [go-zero 文档](../../docs/gozero.md)。
