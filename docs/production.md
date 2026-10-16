# 生产接入与安全配置

本页对应 v0.0.2。先在独立模块和预发布环境验收，再按业务容量灰度。库中的回归测试和本机基线不能替代项目的授权策略、数据迁移审核、备份恢复和真实流量验证。

## 租户与严格权限

普通更新始终保护实际生效的租户字段，包括 `RegisterTenant` 的单字段、多字段、集合覆盖配置及父子路径。`SkipTenant` 只影响过滤，不允许通过普通更新改写租户归属。集合显式排除租户插件时不会额外增加插件字段保护；`CollectionConfig.TenantField` 仍受保护。跨租户数据迁移应作为受控的独立迁移处理。

```go
err := mongoplus.RegisterDataPermission(db, mongoplus.DataPermissionConfig{
    Strict: true,
    ExcludeCollections: []string{"public_dictionary"},
})
```

Strict 默认 false 兼容旧项目；开启后，未排除的集合缺少权限注入函数就返回 `ErrDataPermissionRequired`，不会命中已有缓存。鉴权中间件先将服务端生成的权限函数放入标准请求 Context，再调用 `mongoplus.RequireDataPermission(ctx)` 检查，并将该 Context 传入后续 Handler/Logic。缺失时中间件应中止请求并返回 403，Repository 是第二道检查。

`WithDataPermission(ctx, fn)` 中，`fn` 返回空条件仍表示显式允许该集合的全部数据；没有权限的用户应返回错误或永不匹配的条件。`SkipDataPermission` 仍是受控管理操作入口，不能由请求参数直接决定。该检查不替代业务鉴权，也不验证创建文档是否符合业务写入权限。

## 共享缓存错误与恢复

旧 `SFCache` 接口继续兼容。生产共享适配器应额外实现：

```go
type SFCacheWithErrors interface {
    GetWithError(context.Context, string) (any, bool, error)
    SetWithError(context.Context, string, any, time.Duration) error
    DelWithError(context.Context, string) error
}
```

完整 Redis 示例见 [cache.go](../examples/redis-cache/cache.go)。适配器必须遵守 Context、保留 `RawValue`，并将 `ttl=0` 解释为不过期。可选实现 `SFCachePrefixDeleterWithErrors` 回收旧版本数据；版本切换是正确性条件，前缀回收失败只触发监控，不影响新版本读取。

同一 Namespace 只能用于相同数据源、权限和编解码规则；主从读偏好或读关注级别不同时，也应使用不同 Namespace。

```go
err := db.ConfigureCache(mongoplus.CacheConfig{
    Namespace: "orders-prod-primary",
    RequireErrorReporting: true,
    InvalidationRetryInterval: time.Second,
})
db.CacheObserver = func(ctx context.Context, event mongoplus.CacheEvent) {
    // 接入项目的告警/指标。事件不携带缓存键、查询值和文档。
    // 快速返回，不要在回调里同步执行缓存读写或重试。
}
```

| 行为 | 处理 |
| --- | --- |
| 后端 Get 或数据 Set 报错 | 上报 `get` / `set` 事件，正常回源，不改变数据库查询结果 |
| 版本切换失败 | 上报 `invalidate`，登记待恢复集合；当前包装器绕过可能残留的共享缓存 |
| 自动恢复 | 初始 1 秒重试，指数退避至最多 30 秒；`Database.Close` 停止重试 |
| 主动恢复 | 缓存重连/健康检查调用 `RetryCacheInvalidations(ctx)`；成功后上报 `recovered` |
| 运维观测 | `PendingCacheInvalidations()` 返回未完成集合；`InvalidateCacheContext` 显式返回失效错误 |
| 严格配置遇到旧适配器 | 绕过缓存，上报 `ErrCacheErrorReportingRequired`，不假定后端成功 |

恢复队列在当前进程内存中，不是持久化一致性协议。其他实例在故障恢复窗口或写进程崩溃后仍可能命中旧值。跨实例强一致的关键状态查询应使用 `CacheConfig{Disabled: true}`，它会忽略调用方的 `WithCache` 和 `WithSingleFlight`。数据库写入成功后发生缓存错误，不应重放业务写入；使用监控和恢复接口处理缓存。

## 独立版本迁移

不要将生产 `schema apply` 放在每个服务实例的启动流程中。复制 [迁移命令示例](../examples/migrations/main.go) 到业务项目的 `cmd/migrate`，每个版本放一个独立 Go 文件，并使用 `go:embed` 嵌入其源码计算 `migration.Checksum`。这样追加新版本不会改变旧迁移的校验和。

版本按字符串升序比较，建议使用固定宽度的时间戳和英文标识。若迁移依赖 JSON schema、脚本或回填配置，校验和必须覆盖全部不可变输入；不要在历史 Up 中读取随主分支变化的 schema 目录。

```bash
go run ./cmd/migrate -config mongo-plus.yaml -action plan
go run ./cmd/migrate -config mongo-plus.yaml -action apply
go run ./cmd/migrate -config mongo-plus.yaml -action history
```

| 能力 | 约定 |
| --- | --- |
| 执行锁 | `__mongo_plus_migration_locks` 串行迁移；锁不自动过期，避免长 DDL 或进程暂停时被抢占 |
| 历史 | `__mongo_plus_migrations` 保存版本、SHA256、状态、尝试次数和时间；元数据读主库并使用 majority |
| 校验和 | 已登记版本的源码变化被拒绝；修正业务内容应新增版本，不能修改旧记录绕过校验 |
| 部分失败 | 保留 failed/running；检查部分执行结果、确认幂等后才使用 `-resume` |
| 显式回滚 | `-action rollback -version ...` 只回滚最后一个版本，必须提供业务 Down |
| 回滚失败 | 保留 rollback_failed；通过 rollback 的 `-resume` 恢复，不能当作 Up 执行 |
| 崩溃遗留锁 | 用 `-action lock` 检查 Owner，确认原进程停止后，再 `-action unlock -owner 完整值` |

锁和历史集合属于内部保留名称，普通 Repository 和 JSON schema 不允许使用。运行迁移应使用独立部署进程、主库连接与适当的迁移权限；不要嵌入业务事务或 Session。锁不提供人工强制解锁后的并发安全保证。

MongoDB 的 DDL 和回填不会整体原子回滚。推荐采用：先增加兼容字段/索引 → 新旧服务均能读写 → 分批回填 → 确认历史数据达标 → 最后收紧校验器。已有 `schema.Plan/Apply` 可放在迁移的 Up 中；没有 Down 就拒绝自动回滚，涉及数据恢复的 Down 必须明确业务语义和备份来源。

原有 `schema.Apply`（包括生成器同步和 CLI `schema apply`）及 `migration.Backfill` 也参与同一个锁。在 Up/Down 内必须沿用回调传入的 Context，才能复用已持有的锁；传事务外的旧 Context 会因锁冲突而报错。原生管理命令不受本包控制，不要从其他发布进程并发执行 DDL。

Apply 会保留内部锁集合，即使操作完成后其中没有锁记录，它也不是业务集合。DAL 原生查询文件、批量和扩展回调的事务/缓存接入见 [DAL 文档](dal.md#原生扩展事务与缓存)。原生迁移/回填不会自动触发业务进程的 Repository 缓存失效；发布流程需要通知共享缓存失效、切换已规划的新 Namespace，或在迁移阶段关闭相关缓存，确认完成后再收紧校验规则。

```go
modified, err := migration.Backfill(ctx, db.Collection("users"),
    bson.D{{Key: "status", Value: bson.D{{Key: "$exists", Value: false}}}},
    bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: int64(1)}}}},
    500,
)
```

回填按 `_id` 分批，仅支持可重入的 `$set/$unset`；要求主键为统一的整数、string 或 ObjectID，混合类型会拒绝执行。中断后可重跑，但还要确保旧服务不会继续写入缺失字段的数据。先在快照副本上验证回填与 Down，再执行生产迁移。

## 命令日志安全

| LogConfig 属性 | 默认 | 说明 |
| --- | --- | --- |
| `HideParameters` / `hideParameters` | false | 隐藏全部参数值；go-zero 新模板默认 true |
| `SensitiveFields` / `sensitiveFields` | 空 | 追加字段或点路径，匹配不区分大小写；常见密码、令牌、手机号、邮箱和密文字段始终脱敏 |
| `MaxLength` / `maxLength` | 4096 | 命令内容的字节上限，范围 128～1048576；按 UTF-8 边界截断 |
| `IncludeErrorDetails` / `includeErrorDetails` | false | 错误详情可能包含数据库回显值，默认只输出错误标记；仅受控诊断时开启 |

脱敏在原始命令转为日志文本前执行，包含嵌套文档和数组。未知业务敏感字段应加入列表；生产建议同时开启参数隐藏。日志保留耗时、行数、命令类型与可由终端识别的 `file:line`。

## 容量验收与发布

混合读写工具使用随机专用数据库并自动清理，输出 QPS、错误率、P50/P95/P99、读写分项及堆内存峰值；不会把 URI 写入报告。详见[性能验证](performance.md)。没有目标数据量、SLO 与部署环境时，本机报告只能作为工具回归基线。

当前源码版本准备为 v0.0.2，标签发布前不要假定 `@latest` 已包含这些修复。发布者先提交完整改动，再执行：

```bash
MONGOPLUS_TEST_URI='测试专用副本集URI' make release-check
MONGOPLUS_TEST_URI='测试专用副本集URI' make tag
```

`release-check` 不推送；`tag` 要求干净工作区，拒绝覆盖已有标签，执行真实数据库、Gin/goctl、race 和漏洞检查后，将当前分支与版本标签原子推送。发布成功后业务项目固定 `github.com/kuangshp/mongo-plus@v0.0.2`，重新生成 Repository 并验证升级；不要依赖浮动 main 自动获得修复。

## 依赖扫描边界

本次将 `golang.org/x/text` 更新到 v0.41.0，修复其 [GO-2026-6629](https://pkg.go.dev/vuln/GO-2026-6629) 模块告警；其依赖带动 x/sync 更新到 v0.22.0，Go 最低版本仍为 1.25.5。

`govulncheck` 的可达调用链检查通过，不等于依赖模块中的所有包都无漏洞。为保留 Go 1.25 兼容性，本库暂保留 x/crypto v0.53.0：扫描会报告未被本库导入的 SSH/OpenPGP 模块告警，SSH 修复版本 v0.56.0 要求 Go 1.26。使用方若同时使用 SSH/OpenPGP，需要在自己的主模块独立处理并运行漏洞扫描；不要将本库扫描结果当作业务项目整体安全证明。
