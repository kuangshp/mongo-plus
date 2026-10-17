# DAL 业务仓储示例

本目录是可编译、可集成测试的参考实现，不硬编码数据库连接，也不创建 HTTP 接口。

启动入口从项目配置创建 `*mongoplus.Database`，再调用 `NewReportRepository(db)` 注入到业务层。仓储关闭时调用 `Close()`；数据库由拥有 Client 的入口统一关闭。

## 先定义 JSON，再生成模型

| 文件 | 职责 |
| --- | --- |
| [schema/user.json](schema/user.json) | 用户集合字段的唯一来源；主键、时间、软删除、租户由生成配置补齐 |
| [generator.go](generator.go) | 配置全局生成规则，读取 JSON 并生成 model、dao、repository |
| `generated/model/user_entity.go` | 自动生成的 `UserEntity`；业务中的 `User` 是其类型别名，禁止另写一份模型 |
| `query/user/*.json` | 聚合、导出、批量与订阅命令，不是建表定义 |

从本目录执行：

```bash
go run generator.go
```

本示例默认只生成代码，不连接数据库。连接由 generator.go 从 `MONGOPLUS_URI` / `MONGOPLUS_DATABASE` 读取；提供目标测试库后执行 `go run generator.go -action plan` / `-action apply` 预览和同步 users 集合。查询依赖的 `departments` 集合在业务项目中也应先有自己的 JSON 定义及生成模型；测试用数据由隔离集成测试负责准备。

后续字段变化只修改模型 JSON 并重新生成；自定义查询写进 `repository.go` / `advanced.go`，聚合结果结构体按返回内容维护。生成步骤和覆盖策略见 [JSON 模型生成文档](../../docs/generator.md)。

## 业务方法

| 方法 | 文件 / 入口 | 说明 |
| --- | --- | --- |
| `Summary` | summary.json / count_summary.json | 主集合及 lookup 子集合都显式过滤租户和软删除，再按部门统计分页 |
| `Export` | export.json / DALWalkNamed | 流式导出，回调报错时结束并关闭游标 |
| `BulkStatus` | bulk_status.json / DALBulkWriteNamed | 两个同集合状态更新；时间字段显式传入 |
| `WatchActive` | watch.json / DALWatchNamed | 本租户可见文档新增/更新/替换，返回官方变更流 |
| `FindNameIgnoringCase` | DALNative | 官方 Collation 选项，不把原生查询散到业务层 |
| `WithTx` | Repository.WithTx + WithDAL | 扩展方法沿用事务绑定和数据源 |

```go
report, err := dalexample.NewReportRepository(db)
if err != nil {
    return err
}
defer report.Close()

// companyID 来自已验证的服务端鉴权上下文。
ctx = mongoplus.WithTenantID(ctx, companyID)
page, err := report.Summary(ctx, 1, 20)

err = mongoplus.Transaction(ctx, db, func(txCtx context.Context) error {
    _, err := report.WithTx(txCtx).BulkStatus(ctx, enabledID, disabledID)
    return err
})
```

查询使用 `users` / `departments`，用户字段以 [schema/user.json](schema/user.json) 和生成的 Entity 为准。接入项目时复用其 JSON 定义、生成模型及迁移流程；`NewReportRepository` 只装配仓储和 DAL，不自动建表。生产索引按真实过滤、排序与执行计划设计。

复制到业务项目时，将 query 放到 `internal/dal/query`，将 VO 放到 `internal/dal/vo`，将方法合并进非生成 Repository 文件，并保留启动时的 DAL 注入。完整属性表、参数、订阅恢复和缓存/权限边界见 [DAL 文档](../../docs/dal.md)。

使用隔离副本集验证：

```bash
MONGOPLUS_TEST_URI='测试专用副本集 URI' go test -race -count=1 ./examples/dal
```
