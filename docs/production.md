# 生产接入与安全配置

本页对应待发布的 v0.1.0 源码。先在独立模块和预发布环境验收，再按业务容量灰度。库中的回归测试和本机基线不能替代项目的授权策略、数据迁移审核、备份恢复和真实流量验证。

## JSON 模型与发布顺序

集合模型统一由 `schema/*.json` 生成。建表、调整字段或索引前先更新 JSON，再生成 Entity / DAO / Repository；不再支持手写模型或程序拼装元数据来同步集合。完整流程与旧版 API 迁移见 [JSON 模型生成文档](generator.md)。

| 阶段 | 必须完成的工作 |
| --- | --- |
| 开发 | 修改 JSON 并重新生成数据层，维护保留的 DTO、VO 和业务扩展，编译并验证接口 |
| 预发布 | 使用对应版本的 JSON 与 generator.go 配置执行 `-action plan`，验证现有数据与新校验器兼容性 |
| 生产迁移 | 使用审核后的固定 JSON 快照同步结构；必要时执行独立版本迁移与回填 |
| 启动服务 | 使用已经同步好的集合；运行时连接、Use 和插件注册不执行建表 |

`schema.Plan/Apply` 仅接受 `LoadEntities` 加载后未修改的元数据；不要在 Up 中直接构造 `schema.Entity` 或向加载结果追加字段。历史版本需要改变结构时，新增 JSON 快照并重新加载，同时将 JSON 内容和全局生成规则纳入该迁移的校验和。

## 租户与严格权限

普通更新始终保护实际生效的租户字段，包括 `RegisterTenant` 的单字段、多字段、集合覆盖配置及父子路径。`SkipTenant` 只影响过滤，不允许通过普通更新改写租户归属。集合显式排除租户插件时不会额外增加插件字段保护；`CollectionConfig.TenantField` 仍受保护。跨租户数据迁移应作为受控的独立迁移处理。

```go
err := mongoplus.RegisterDataPermission(db, mongoplus.DataPermissionConfig{
    Strict: true,
    ExcludeCollections: []string{"public_dictionary"},
})
```

注册数据权限后，未排除的集合缺少权限注入函数就返回 `ErrDataPermissionRequired`，不会命中已有缓存。旧项目要恢复“没有注入函数就不加条件”，在配置里设置 `AllowMissing: true`。鉴权中间件先将服务端生成的权限函数放入标准请求 Context，再调用 `mongoplus.RequireDataPermission(ctx)` 检查，并将该 Context 传入后续 Handler/Logic。缺失时中间件应中止请求并返回 403，Repository 是第二道检查。

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

已连接数据库时，失效会先把集合版本写入 `__mongo_plus_cache_epochs`。其他实例按这个版本读取共享缓存，版本写入成功后不会再命中旧条目。版本写入失败时，本进程绕过缓存并重试；进程在写入成功前退出，其他实例仍可能看到旧缓存。跨实例强一致的关键状态查询应使用 `CacheConfig{Disabled: true}`，它会忽略调用方的 `WithCache` 和 `WithSingleFlight`。数据库写入成功后发生缓存错误，不应重放业务写入；使用监控和恢复接口处理缓存。同一 Namespace 的实例才能共享这个版本号。

## 公共 SF 参数与内存上限

`SF`、`SFWithTTL` 和 `SFNoCache` 会先检查 JSON 参数序列化错误，再递归编码参数类型和值。ObjectID/string、time.Time/string、数字类型和嵌套 interface 中的实际类型不会共用键，map 顺序规范化、数组和 BSON D 顺序保留；私有非零状态需显式转换为键参数。自定义 JSON 类型使用其公开编码契约，同名查询应固定返回类型。NaN、Inf、循环引用和自定义编码失败返回 `ErrInvalidCacheArgs`，不会执行加载函数，也不参与缓存或请求合并。调用方应修正参数，不能丢弃错误后把返回零值当作有效数据。租户和权限范围仍必须显式包含在 Args 中。

默认 `MemoryCache` 上限为 1024 条，使用 LRU，每秒主动清理过期项。通过 `NewMemoryCacheWithConfig` 调整 `MaxEntries` 和 `CleanupInterval`；两者的零值分别使用上述默认值，负值返回错误。TTL 为 0 的条目也可能被容量淘汰。这是条目数上限，不是字节或 RSS 上限；不要用公共 SF 无限制地缓存大集合。`Len()` 可观察实际保留数量；结束使用时 `Close()`，或用 `StopSFCache()` 关闭全局缓存。

## 敏感字段标签升级

新增、更新、读取及仓储 `SensitiveEq` 共用按实体类型隔离的字段元数据。仅使用实体标签时，无需重复填写 `SensitiveConfig.Fields`。密文与盲索引使用实体 BSON 路径；不同实体的同名业务字段不会互相覆盖。字段配置、操作符及生成接口的升级说明见 [敏感字段](../README.md#敏感字段)。

命名嵌套对象、匿名嵌入、指针、切片及数组中的敏感字段均沿用该元数据；空对象安全跳过。更新敏感叶子使用完整路径，数组必须有数字下标；整个敏感父对象替换、动态 map/interface 敏感实体及递归敏感类型会明确报错。创建会隔离敏感写入涉及的指针和切片；单条失败不回填，有序批量只回填已确认成功前缀。事务回滚不会恢复已成功语句的内存回填。示例见 [嵌套敏感字段与失败写入](../README.md#嵌套敏感字段与失败写入)。

此修复阻止后续标签字段更新直接写入明文，但不会自动清洗既有数据。上线前检查受影响集合是否存在遗留业务明文字段，并核对它与旧密文的实际新旧关系；先备份，再以独立迁移重建密文与盲索引、删除遗留明文、通知缓存失效，最后验证脱敏与授权读取。不要简单假设旧密文或遗留明文中的任意一方总是正确。

## 独立版本迁移

不要将生产 `-action apply` 放在每个服务实例的启动流程中。复制 [迁移命令示例](../examples/migrations/main.go) 到业务项目的 `cmd/migrate`，每个版本放一个独立 Go 文件，并使用 `go:embed` 嵌入其源码计算 `migration.Checksum`。这样追加新版本不会改变旧迁移的校验和。

版本按字符串升序比较，建议使用固定宽度的时间戳和英文标识。若迁移依赖 JSON schema、脚本或回填配置，校验和必须覆盖全部不可变输入；不要在历史 Up 中读取随主分支变化的 schema 目录。

```bash
go run ./cmd/migrate -uri "$MONGOPLUS_URI" -database "$MONGOPLUS_DATABASE" -action plan
go run ./cmd/migrate -uri "$MONGOPLUS_URI" -database "$MONGOPLUS_DATABASE" -action apply
go run ./cmd/migrate -uri "$MONGOPLUS_URI" -database "$MONGOPLUS_DATABASE" -action history
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

MongoDB 的 DDL 和回填不会整体原子回滚。推荐采用：先增加兼容字段/索引 → 新旧服务均能读写 → 分批回填 → 确认历史数据达标 → 最后收紧校验器。已有 `schema.Plan/Apply` 可放在迁移的 Up 中，输入必须从该版本固定的 JSON 文件经 `LoadEntities` 加载，不能使用手写或改写后的元数据；没有 Down 就拒绝自动回滚，涉及数据恢复的 Down 必须明确业务语义和备份来源。

原有 `schema.Apply`（包括生成器同步和 generator.go 的 apply）及 `migration.Backfill` 也参与同一个锁。在 Up/Down 内必须沿用回调传入的 Context，才能复用已持有的锁；传事务外的旧 Context 会因锁冲突而报错。原生管理命令不受本包控制，不要从其他发布进程并发执行 DDL。

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
| `Format` / `format` | shell | 多行 mongosh 风格；json 保留单行 Extended JSON；默认两种格式均先脱敏 |
| `Replayable` / `replayable` | false | 仅用于受控开发：shell 输出真实参数和 BSON 类型供复制执行；须同时关闭 HideParameters，会输出敏感值 |
| `HideParameters` / `hideParameters` | false | 隐藏全部参数值；go-zero 新模板默认 true |
| `SensitiveFields` / `sensitiveFields` | 空 | 追加字段或点路径，匹配不区分大小写；常见密码、令牌、手机号、邮箱和密文字段始终脱敏 |
| `MaxLength` / `maxLength` | 4096 | 命令内容的字节上限，范围 128～1048576；按 UTF-8 边界截断 |
| `IncludeErrorDetails` / `includeErrorDetails` | false | 错误详情可能包含数据库回显值，默认只输出错误标记；仅受控诊断时开启 |

默认脱敏在原始命令转为日志文本前执行，包含嵌套文档和数组。未知业务敏感字段应加入列表；生产建议同时开启参数隐藏。日志保留耗时、行数、命令类型、实际数据库名与可由终端识别的 `file:line`。默认 shell 展示会移除 Session 等协议元数据；日期保留实际值，大 int64 保留精度。隐藏或截断的日志仅用于诊断，不能直接重放。Replayable 明确绕过参数脱敏；超长语句整条省略，游标后续批次和事务边界命令明确标记为无法独立重放。

## 容量验收与发布

混合读写工具使用随机专用数据库并自动清理，输出 QPS、错误率、P50/P95/P99、读写分项及堆内存峰值；不会把 URI 写入报告。详见[性能验证](performance.md)。没有目标数据量、SLO 与部署环境时，本机报告只能作为工具回归基线。

本次生成入口存在不兼容变更，版本号与更新记录已准备为 v0.1.0，但尚未打标签。发布版本以 version.go 和实际标签为准，标签发布前不要假定 `@latest` 已包含这些修复。发布者先提交完整改动，再执行：

```bash
MONGOPLUS_TEST_URI='测试专用副本集URI' make release-check
MONGOPLUS_TEST_URI='测试专用副本集URI' make tag
```

`release-check` 不提交、不创建标签、不推送；`tag` 不自动递增版本或只提交版本文件，要求提前更新 version.go 和对应 CHANGELOG 并提交全部修复；入口首先检查干净工作区，拒绝覆盖已有标签，执行真实数据库、Gin/goctl、race 和漏洞检查后，将当前分支与版本标签原子推送。发布成功后业务项目固定 `github.com/kuangshp/mongo-plus@v0.1.0`，重新生成 Repository 并验证升级；不要依赖浮动 main 自动获得修复。

## 依赖扫描边界

本次将 `golang.org/x/text` 更新到 v0.41.0，修复其 [GO-2026-6629](https://pkg.go.dev/vuln/GO-2026-6629) 模块告警；其依赖带动 x/sync 更新到 v0.22.0，Go 最低版本仍为 1.25.5。

`govulncheck` 的可达调用链检查通过，不等于依赖模块中的所有包都无漏洞。为保留 Go 1.25 兼容性，本库暂保留 x/crypto v0.53.0：扫描会报告未被本库导入的 SSH/OpenPGP 模块告警，SSH 修复版本 v0.56.0 要求 Go 1.26。使用方若同时使用 SSH/OpenPGP，需要在自己的主模块独立处理并运行漏洞扫描；不要将本库扫描结果当作业务项目整体安全证明。

## 本轮隔离与兼容性回归

新增回归覆盖：批量省略 filter 的跨租户写、更新管道和重命名目标保护、嵌套租户赋值、敏感字段自动填充、订阅 OR 权限、geoNear 首阶段、并发任务领取、原子替换系统字段、Upsert 与事务回滚，以及索引语义漂移。

启动时使用生成的 `repository.RegisterGeneratedCollections(db)` 或 `db.RegisterCollections(...)` 注册规则；不同数据源包装器分别注册。遇到同集合冲突不再按初始化顺序覆盖。

共享缓存每次读取先向 MongoDB 主节点取得版本号，再查缓存；每次发布失效需要 majority 版本写入。缓存收益应以真实数据、网络延迟和查询复杂度测量，不能以命中率直接推断响应更快。CacheObserver 新增 hit / miss / epoch 事件，可统计收益与版本读取故障。业务写入与版本发布并非一个原子协议，关键一致性查询保持 CacheConfig.Disabled=true。

CI 增加 MongoDB 6.0.18 / 7.0 / 8.0 副本集矩阵，并构建 Go 1.25.5 最低版本。配置矩阵不代表远端已通过，本机报告需标注实际服务端版本；Search、分片、跨命名空间 BulkWrite 和目标业务长稳压测仍需各自部署验证。


## 边界修复与升级行为

- DAL 在一次操作中固定 Provider 的选择，分页、游标、订阅和写后失效不再重复路由；事务和仓储绑定继续优先。
- 含自定义 BSON 编解码方法或 interface 动态字段的结果绕过仓储共享缓存与合并。共享缓存键版本变为 v4；自行注册 Client BSON Registry 编解码器的项目应关闭仓储缓存，或独立证明往返编码保持值不变。
- 敏感字段的密文/索引显式路径必须匹配实体 BSON 标签，所有绑定的存储路径不能相等或形成父子关系。无效配置在写入前报错；修复不会恢复历史上已经被覆盖的数据。
- 自增仓储拒绝原子 Upsert，包括 FindOneAndReplace；需要显式主键 Upsert 时使用手动 ID 仓储。不能只推进序列表，因为其他实例可能仍持有已分配号段。
- 普通 Wrapper 写入透传 Collation、Hint、Let、Comment；不适用的排序、投影、分页、缓存和游标选项改为明确报错。
- Capped 创建和 Plan 比较使用同一容量规范值，避免服务端取整后反复提示伪漂移；真实容量变化仍要求显式迁移。[服务端容量取整规则](https://www.mongodb.com/docs/v8.0/core/capped-collections/create-capped-collection/)。


## 系统性契约验收

查询、缓存、删除、分页、敏感字段和日志的统一行为见[接口契约表](contracts.md)。公共 SF 的 v2 键、仓储共享缓存的 v4 键都隔离旧数据，旧条目按原 TTL 或运维规则回收。

原生功能并不自动继承 Repository 保护；缺少封装的高级 MongoDB 能力应通过有权限边界的业务仓储使用。通过测试表示列明契约在测试场景中成立，不表示所有官方 API 都已封装，也不能替代目标业务的容量与长期稳定性验收。
