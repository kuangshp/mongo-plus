# DAL 业务仓储示例

本目录是可编译、可集成测试的参考实现，不硬编码数据库连接，也不创建 HTTP 接口。

启动入口从项目配置创建 `*mongoplus.Database`，再调用 `NewReportRepository(db)` 注入到业务层。仓储关闭时调用 `Close()`；数据库由拥有 Client 的入口统一关闭。

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

查询使用 `users` / `departments`，字段以 [User](repository.go) 和 JSON 为准。本示例不迁移业务数据库；接入项目时复用其 schema、Entity 和迁移流程。生产索引按真实过滤、排序与执行计划设计。

复制到业务项目时，将 query 放到 `internal/dal/query`，将 VO 放到 `internal/dal/vo`，将方法合并进非生成 Repository 文件，并保留启动时的 DAL 注入。完整属性表、参数、订阅恢复和缓存/权限边界见 [DAL 文档](../../docs/dal.md)。

使用隔离副本集验证：

```bash
MONGOPLUS_TEST_URI='测试专用副本集 URI' go test -race -count=1 ./examples/dal
```
