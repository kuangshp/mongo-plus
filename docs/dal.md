# DAL 原生查询文件与仓储扩展

DAL 对应 gorm-plus 的原始 SQL 文件层。在 MongoDB 中使用 JSON / Extended JSON 描述 BSON 条件、聚合管道、批量模型和变更订阅，不执行 SQL，也不执行 JavaScript。常规查询优先使用生成的 Repository；复杂逻辑仍封装在业务 Repository，业务层只调用业务方法。

完整可编译示例见 [examples/dal](../examples/dal/repository.go)，包含跨集合聚合分页、流式导出、批量状态更新、变更订阅、原生 Collation 查询和事务绑定；示例 JSON 文件也参与真实数据库测试。实体来自 [schema/user.json](../examples/dal/schema/user.json)，在 `examples/dal` 目录执行 `go run generator.go` 可重新生成；`query/*.json` 负责查询命令，不能替代用于定义模型的 `schema/*.json`。

## 模型 JSON 与查询 JSON 的分工

| 输入 | 用途 | 执行入口 |
| --- | --- | --- |
| `schema/*.json` | 集合字段、主键覆盖、索引、校验器及模型生成 | `Generate` / `SyncSchema` / CLI `schema` |
| `internal/dal/query/**/*.json` | 使用既有集合执行查询、聚合、批量写入或订阅 | DAL 方法 |

先维护模型 JSON，生成 Entity / DAO / Repository 并按环境同步结构，再使用 DAL 查询文件。两类 JSON 目录分别管理；查询文件不能代替建表定义。业务仓储复用生成模型，不能另行手写持久化 Entity；聚合 VO 和读取部分字段的结果结构体仍可按查询定义。具体步骤见 [JSON 模型生成文档](generator.md)。

## 目录与初始化

```text
internal/dal/
├── entity/                  # Entity，只能从 schema JSON 生成
├── dao/                     # 生成的类型安全字段
├── repository/
│   ├── user_gen.go          # 生成代码
│   └── user.go              # 业务方法，在这里调用 DAL
├── vo/                      # 聚合、关联等查询结果
└── query/
    ├── files.go             # embed 与加载入口
    └── user/
        ├── page.json
        ├── count_page.json
        ├── summary.json
        ├── count_summary.json
        ├── bulk_status.json
        └── watch.json
```

在 `internal/dal/query/files.go` 声明资源：

```go
package query

import "embed"

//go:embed user
var Files embed.FS
```

启动时复用项目配置建立的数据库，创建一次 DAL 并注入业务仓储：

```go
func NewDAL(db *mongoplus.Database) (*mongoplus.DAL, error) {
    dal, err := mongoplus.NewDal(db, mongoplus.NewEmbedLoader(query.Files))
    if err != nil {
        return nil, err
    }
    if err := dal.Preload("user/page.json", "user/count_page.json"); err != nil {
        dal.Close()
        return nil, err
    }
    return dal, nil
}
```

`Preload` 只检查文件读取；绑定参数和文件语义在执行时校验。`NewDal` 同时注册进程默认 DAL；多实例项目通过 `WithDALDB(ctx, dal)` 或下面的 `Repository.WithDAL` 明确选择实例。`dal.Close()` 只停止文件缓存清理，数据库由拥有 Client 的启动入口关闭。`NewDalWithProvider` 的 provider 实现 `Get(context.Context) (*Database, error)` 即可按上下文选择数据源；每次公开 DAL 调用只解析一次 Provider，权限检查、执行和缓存失效始终使用同一包装器；分页的数据与计数也共用该选择。下一次调用重新路由。Repository.WithDAL 以仓储数据源为准，DALWithTx 在事务开始时固定所选数据库。

## 与 gorm-plus 的调用对应

| 需求 | 当前 API | 说明 |
| --- | --- | --- |
| 初始化 / 文件加载 | `NewDal` / `NewDalWithProvider` / `NewEmbedLoader` | 启动时创建，loader 通常使用 embed.FS |
| 实例选择 | `WithDALDB(ctx, dal)` | 显式覆盖默认 DAL；WithDAL 同时固定仓储数据源 |
| 位置参数列表 | `DALQuery[T](ctx, file, args...)` | `find` / `aggregate` |
| 命名参数列表 | `DALQueryNamed[T](ctx, file, params)` | `map[string]any`，参数保留 BSON 类型 |
| 单条 | `DALQueryOne[T]` / `DALQueryOneNamed[T]` | 无记录返回 `(nil, nil)`；支持聚合结果 |
| 数量 | `DALCount(ctx, file, args...)` | 命名参数可传一个 map；聚合计数结果列必须为 `total` |
| 分页 | `DALQueryPage[T]` / `DALQueryPageNamed[T]` | 同目录自动查找 `count_` 文件 |
| 单条写入 | `DALExec` / `DALExecNamed` | insertOne / update / delete 等 |
| 影响数 | `DALExecAffected` / `DALExecAffectedNamed` | 更新返回匹配数；批量返回插入＋匹配＋删除＋upsert 数量 |
| 大结果集 | `DALWalk[T]` / `DALWalkNamed[T]` | 逐条回调，错误即停止并关闭游标 |
| 特殊批量 | `DALBulkWrite` / `DALBulkWriteNamed` | 返回官方 `BulkWriteResult`；错误时可能仍有部分成功结果 |
| 变更订阅 | `DALWatch` / `DALWatchNamed` | 返回官方 `*mongo.ChangeStream`，调用方管理消费与关闭 |
| 文件能力未覆盖的官方操作 | `DALNative(ctx, writes, callback)` | 同一 DAL 数据源、Context、Hook；声明当前库的写入集合 |
| DAL 事务 | `DALWithTx(ctx, callback)` | 固定本轮数据库，回调使用 `txCtx` |
| 业务仓储事务与 DAL 衔接 | `Repository.WithDAL(ctx, dal, callback)` | 沿用仓储数据源和 `WithTx`，回调使用 `queryCtx` |
| 观测 | `WithDALDebug` / `WithDALHook` | Debug 只记录文件、参数个数、耗时和失败标记；Hook 可接项目指标 |
| 预热与文件缓存 | `DALPreload` / `dal.Preload` / `WithDALCacheCleanup` | 可选定时清空文件内容缓存，不是查询结果缓存；`dal.Close` 停止清理 |
| 启动断言 | `DALMustExec` / `DALMustQueryOne` | 失败时 panic；仅适合启动断言，业务请求处理返回 error |

## 文件全部顶层属性

| 属性 | 类型 / 默认 | 适用操作 | 含义 |
| --- | --- | --- | --- |
| `collection` | string，必填 | 全部 | 当前数据库中的集合名；内部迁移/序列表保留名称被拒绝 |
| `op` | string，默认 `find` | 全部 | 见下表 |
| `filter` | document，默认空 | find / count / update / delete | 原生 BSON 过滤；聚合条件写入 `pipeline.$match` |
| `sort` | document | find / findOne | 保留 JSON 属性顺序的 BSON 排序 |
| `projection` | document | find / findOne | BSON 投影 |
| `limit` | 非负整数 | find / findOne | 0 / 省略表示不限制；单条入口最多返回一条 |
| `skip` | 非负整数 | find / findOne | 跳过条数 |
| `pipeline` | document[] | aggregate / watch | 原生管道，各阶段必须是单操作符文档；空管道可用 |
| `document` | document | insertOne | 插入数据，主键及审计字段由业务明确提供 |
| `update` | document | update / updateOne / updateMany | 原生更新操作符；更新管道使用 bulkWrite 模型 |
| `operations` | document[]，非空 | bulkWrite | 六种批量模型，见下文 |
| `allowAll` | bool，默认 false | update / delete / bulkWrite | 显式允许空过滤；bulkWrite 模型可以单独覆盖 |
| `options` | document | find / aggregate / watch / bulkWrite | 按操作分别验证，见选项表；其他官方选项走 DALNative |
| `invalidates` | string[]，默认空 | 写入操作 | 额外失效的同库 Repository 缓存集合；正常写入目标自动失效 |

未知顶层属性、重复属性、无效选项、缺少参数和多余位置参数会返回错误，避免把拼错的配置当作已生效。`limit` / `skip` 不接受小数、NaN 或 int64 溢出值。文件名由服务端固定，不接受用户传入路径。

| `op` | 对应入口 | 行为 |
| --- | --- | --- |
| `find` / `findOne` | Query / QueryOne / Walk | findOne 文件限制一条；QueryOne 也可执行聚合 |
| `count` | Count / 分页 count 文件 | CountDocuments，忽略列表分页 |
| `aggregate` | Query / Walk / Count / Exec | 读写入口分离；`$out/$merge` 必须使用 Exec |
| `insertOne` | Exec | 原生插入 |
| `updateOne` | Exec | 原生单条更新 |
| `update` / `updateMany` | Exec | 都是原生多条更新 |
| `deleteOne` | Exec | 默认按集合配置逻辑/物理删除一条；物理删除需显式 DALPhysicalDelete |
| `delete` / `deleteMany` | Exec | 集合仓储未开启逻辑删除时是物理删除；已开启时改为设置 deleted_at，除非 Context 显式使用 DALPhysicalDelete |
| `bulkWrite` | BulkWrite / Exec | 六种同集合混合写入模型 |
| `watch` | Watch | 集合级变更流；不能放进 Session / 事务 |

## 参数绑定与普通分页

`user/page.json`：

```json
{
  "collection": "users",
  "op": "find",
  "filter": {"company_id": "@companyID", "deleted_at": null, "status": "@status"},
  "sort": {"_id": 1},
  "limit": "@limit",
  "skip": "@offset"
}
```

`user/count_page.json` 使用相同过滤，`op` 改为 `count`，省略 sort/limit/skip。仓储内调用：

```go
offset, limit := mongoplus.DbPage(pageNumber, pageSize)
page, err := mongoplus.DALQueryPageNamed[UserVO](queryCtx, "user/page.json", map[string]any{
    "companyID": companyID, // 从服务端认证上下文取，不能直接信任请求租户 ID。
    "status":    int64(1),
    "offset":    offset,
    "limit":     limit,
})
```

位置参数把值改成完整字符串 `"?"`，按文件遍历顺序消费。`DALQueryPage` 的 `filterArgs` 同时传给数据/计数文件，`pageArgs` 只追加给数据文件。命名参数允许 map 中有未引用的键，便于分页共用同一份参数。

参数是整值替换，不做字符串拼接，不在替换后再次解释占位符。传 `int64`、`bson.ObjectID`、`time.Time`、`bson.Decimal128`、`bson.Raw` 等真实类型；静态日期可使用 Extended JSON 的 `$date`。`@name` 和 `?` 是保留占位值，文件中不要用它们表示普通字面量。不要把前端提交的 document / pipeline / 操作符对象直接当作可信参数。

## 复杂聚合与流式导出

`aggregate` 使用 MongoDB 原生管道，不受 Repository 简化聚合白名单限制。`$lookup` 子管道、`$setWindowFields`、`$graphLookup`、`$facet` 等由服务端版本决定是否支持；Search 还取决于部署能力。

示例 [summary.json](../examples/dal/query/user/summary.json) 同时在主集合和 `$lookup` 子集合写入租户/软删除条件，再分组、排序和分页。[count_summary.json](../examples/dal/query/user/count_summary.json) 统计分组数量，返回 `{"total": ...}`；无分组时返回空结果，DAL 计为 0。计数文件需要和数据文件的业务过滤、关联展开及分组语义一致。

大数据导出使用 `DALWalkNamed`，不用 `DALQuery` 将全部数据载入内存：

```go
err := mongoplus.DALWalkNamed(queryCtx, "user/export.json", params, func(row UserVO) error {
    return writer.Write(row) // 返回错误立即停止；DAL 负责关闭游标。
})
```

分页数据和 total 是两次操作，默认不保证同一时刻快照；需要一致快照时使用事务及合适的 readConcern。批次大小不代表查询总数限制，单条 BSON 文档仍遵循服务端大小限制。

## 特殊批量写入

```json
{
  "collection": "users",
  "op": "bulkWrite",
  "options": {"ordered": false},
  "operations": [
    {"op": "insertOne", "document": {"_id": "@newID", "company_id": "@companyID", "username": "@name"}},
    {"op": "updateOne", "filter": {"_id": "@existingID", "company_id": "@companyID"}, "update": [{"$set": {"status": 1}}], "upsert": false}
  ]
}
```

| 模型属性 | 支持模型 | 说明 |
| --- | --- | --- |
| `op` | 全部，必填 | insertOne / updateOne / updateMany / replaceOne / deleteOne / deleteMany |
| `document` | insertOne | 必填插入文档 |
| `filter` | 其他五种 | 必须非空，除非显式 allowAll |
| `update` | updateOne / updateMany | 必填更新文档或更新管道数组 |
| `replacement` | replaceOne | 必填替换文档，业务补齐需保留的租户/审计字段 |
| `upsert` | updateOne / updateMany / replaceOne | bool，默认 false |
| `arrayFilters` | updateOne / updateMany | 数组过滤器，适用性由服务端检查 |
| `hint` | 除 insertOne 外 | 原生索引名或索引文档 |
| `allowAll` | 全部 | 默认继承文件配置，模型可显式覆盖；写过滤应包含权限条件 |

使用 `DALBulkWriteNamed` 获取 Inserted/Matched/Modified/Deleted/UpsertedCount 和 UpsertedIDs。即使返回 error，也可能有非 nil 的部分成功结果。`ordered=true` 遇错停止，`false` 尽可能继续；两者都不是整体事务。所有模型的支持属性与必填项先在本地检查；BSON 编码和数据库约束仍可能在批量执行过程中失败，产生部分成功。需要业务核对，不能盲目整批重放。

`DALExecAffected` 执行 bulkWrite 时的 RowsAffected 是插入＋匹配＋删除＋upsert 的操作累计数，可能重复涉及同一文档，不代表唯一文档数。需要整体提交/回滚时用 `DALWithTx` 或业务仓储 `WithTx`。

## 变更订阅与恢复

参考 [watch.json](../examples/dal/query/user/watch.json) 与 [WatchActive](../examples/dal/repository.go)。订阅需要副本集或分片部署；本库只打开集合级官方 ChangeStream，不提供“恰好一次”消费保证。

```go
stream, err := reportRepository.WatchActive(ctx, persistedToken)
if err != nil {
    return err
}
defer stream.Close(context.WithoutCancel(ctx))

for stream.Next(ctx) {
    var event bson.Raw
    if err := stream.Decode(&event); err != nil {
        return err
    }
    if err := handleIdempotently(ctx, event); err != nil {
        return err // 业务处理成功后才推进持久化进度。
    }
    token := append(bson.Raw(nil), stream.ResumeToken()...)
    if err := saveCheckpoint(ctx, token); err != nil {
        return err
    }
}
return stream.Err()
```

第一次 `resumeAfter` 传 nil，恢复时传持久化 BSON token；`resumeAfter` / `startAfter` / `startAtOperationTime` 只能设置一个。驱动处理可恢复的连接错误，业务仍需处理进程重启、重复事件、token 超出 oplog 历史、消费者故障和重建快照。不要在事件处理成功前保存检查点。

示例只观察仍可见文档的 insert/update/replace，不包含物理删除或已软删除文档。物理删除事件没有 fullDocument；严格租户隔离时需结合 pre-image、路由索引或独立集合设计，不能直接把全部删除事件暴露给租户消费者。订阅 Hook 只记录打开耗时，逐事件延迟由消费者监控。

## 全部文件选项

| `options` 属性 | 操作 | 默认 / 类型与限制 |
| --- | --- | --- |
| `batchSize` | find / aggregate / watch | 未设置沿用驱动；整数 1～2147483647 |
| `allowDiskUse` | find / aggregate | find 沿用驱动；aggregate 为 true；bool |
| `hint` | find / aggregate | 索引名或文档 |
| `comment` | find / aggregate / watch / bulkWrite | 原生注释值，不放业务敏感信息 |
| `ordered` | bulkWrite | true；bool |
| `fullDocument` | watch | 驱动默认；default / updateLookup / whenAvailable / required |
| `fullDocumentBeforeChange` | watch | 驱动默认；off / whenAvailable / required，pre-image 需服务端配置 |
| `resumeAfter` | watch | nil 或非空 BSON token 文档 |
| `startAfter` | watch | nil 或非空 BSON token 文档 |
| `startAtOperationTime` | watch | nil 或 BSON Timestamp / Extended JSON `$timestamp` |
| `maxAwaitTimeMS` | watch | 驱动默认；整数 1～3600000 毫秒 |

文件选项没有覆盖每个官方参数。跨命名空间 `Client.BulkWrite` 需要 MongoDB 8.0+，本次 MongoDB 6.0 本机测试验证的是集合级 BulkWrite。Context 控制超时；Collation、自定义 BSON、数据库/集群级 Watch、跨命名空间 BulkWrite、GridFS、特定官方新选项等仍放在非生成 Repository 中，通过 `DALNative` 使用官方 API，而不用将数据库代码散到 Logic / Handler。

## 原生扩展、事务与缓存

业务仓储扩展统一沿用现有绑定：

```go
func (r *customerUserRepository) Summary(ctx context.Context) ([]UserVO, error) {
    var rows []UserVO
    err := r.Repository.WithDAL(ctx, r.dal, func(queryCtx context.Context) error {
        // 租户/数据权限从 queryCtx 读取并组装命名参数。
        var err error
        rows, err = mongoplus.DALQueryNamed[UserVO](queryCtx, "user/summary.json", params)
        return err
    })
    return rows, err
}
```

上面是合并到现有自定义仓储的片段，`r.dal` 为启动时注入的依赖，`params` 为服务端根据权限组装的参数。完整独立实现见 examples/dal。生成文件不用增加每个业务报表的方法；方法和接口放在项目的非生成扩展文件。

使用 `repo.WithTx(txCtx)` 后，即使扩展方法传入请求 ctx，`WithDAL` 也会合并事务上下文，再将 queryCtx 传给 DAL。事务内不要用原 ctx 调用全局 DAL，也不要把回调 Context 保留到事务结束后。`DALWithTx` 会固定 Provider 当时选择的数据库，后续调用不重新路由；开启事务前要先明确选择写库。

```go
err := mongoplus.DALNative(queryCtx, []string{"users"}, func(nativeCtx context.Context, db *mongo.Database) error {
    _, err := db.Collection("users").UpdateOne(nativeCtx, filter, update, nativeOptions)
    return err
})
```

`writes` 是当前数据库中可能写入的集合，nil 表示只读，必须如实声明；即使回调部分失败，也保守失效这些缓存。跨数据库原生写入还需业务显式处理其他库的缓存。

DAL 单条写入、BulkWrite、原生回调及 `$out/$merge` 使用统一失效时序：普通调用在返回前失效，本库事务在最终提交后失效，回滚不发布失效；外部 Session 的提交时机未知，相关同 Client 同库包装器保守绕过缓存。共享后端失败仍遵循[生产缓存恢复约定](production.md#共享缓存错误与恢复)，不构成跨实例强一致协议。

写入聚合通过 `DALExec` 显式执行，必须以 `$out/$merge` 结束，同库目标集合自动识别并失效缓存。由于 Aggregate 不提供 Acknowledged 标记，写入聚合显式使用 majority；不在 Session/事务中执行。它不返回影响数量，RowsAffected 为 0 表示未知。跨库目标通过 DALNative 显式实现，处理目标库缓存。读入口拒绝 `$out/$merge`，避免一次报表调用意外写库。

DAL 执行时为顶层 filter、pipeline、变更订阅、插入和批量操作追加数据库上的租户、数据权限，以及该集合仓储声明的逻辑删除。`DALUnscoped` 只放开已删除记录的过滤范围，不改变删除方式。要物理删除必须使用 `DALPhysicalDelete(ctx)`；要同时包含历史软删除文档，使用 `DALPhysicalDelete(DALUnscoped(ctx))`。两个标记都保留租户和数据权限。`$lookup` / `$unionWith` 子管道、自增主键、操作人自动填充和敏感字段仍要业务自己处理。文件不经过 Repository 结果缓存。Hook 可见原始参数，仅向可信内部观测系统传递并自行脱敏。

## 验证与官方语义

```bash
MONGOPLUS_TEST_URI='测试专用副本集 URI' go test -race -count=1 -run '^TestDAL' .
MONGOPLUS_TEST_URI='测试专用副本集 URI' go test -race -count=1 ./examples/dal
```

测试包含窗口聚合、聚合单条、聚合分页计数、游标提前停止、部分成功批量与 upsert、写入聚合目标失效、原生回调部分失败、仓储 WithTx、Provider 固定、回滚和提交、订阅 token 恢复及取消。示例另验证 lookup 子集合隔离和嵌入文件可执行。

原生语义参见官方 [BulkWrite](https://www.mongodb.com/docs/drivers/go/current/crud/bulk/)、[Change Streams](https://www.mongodb.com/docs/drivers/go/current/monitoring-and-logging/change-streams/) 和[事务](https://www.mongodb.com/docs/drivers/go/current/crud/transactions/)。服务端/部署不支持的阶段不会被本库模拟或自动降级。

## 隔离与特殊语法

`allowAll` 仅放宽业务过滤检查。批量模型省略 filter 时，仍会补齐租户、权限与删除范围；没有业务过滤且未 AllowAll 时执行前报错。系统条件不能被模型上的 allowAll 覆盖。

更新管道仅自动接受 `$set` / `$addFields` / `$unset`，并保护租户与系统路径；`$project` / `$replaceRoot` / `$replaceWith` 等无法证明保留系统字段的阶段要求显式原生扩展。普通更新同时检查 `$rename` 的来源和目标路径。

`$geoNear` 保持首阶段，范围条件合入其 query。带隔离策略的 `$search` / `$searchMeta` / `$vectorSearch` / `$documents` / `$collStats` / `$indexStats` 等特殊入口明确报错，使用原生扩展设计过滤；不会静默挪动阶段或取消隔离。

订阅权限支持 `$and` / `$or` / `$nor` 的递归路径转换；`$expr` 等不能安全自动转换的权限语法直接报错。订阅仍只观察符合 fullDocument 范围的可见记录；删除事件方案见前文。

新增选项：

| 位置 | 属性 | 说明 |
| --- | --- | --- |
| find / aggregate | options.collation | Collation 文档，locale 必填，未知属性报错 |
| find / aggregate / bulkWrite | options.let | 命令变量文档 |
| count | options.collation / hint / comment | 使用 CountDocuments 对应选项 |
| bulkWrite 模型 | collation | update/replace/delete 模型的字符串比较规则 |
| bulkWrite 模型 | sort | updateOne/replaceOne 的目标选择顺序，要求支持该功能的服务端版本 |
| watch | options.collation / showExpandedEvents | 比较规则与扩展事件；具体事件受服务端能力约束 |

## 高级官方能力的接入边界

原生回调运行在 DAL 选择的数据源上；通过 Repository.WithDAL 调用可继承事务绑定。写入集合必须如实列入 writes，回调部分失败也会失效缓存。原生回调不自动改写租户、审计或敏感字段，应在业务扩展中封装授权入口。

| 官方能力 | 接入方式 | 需要业务维护 |
| --- | --- | --- |
| TimeSeries / Clustered / View | `migration.Run` 的原生 Database 回调 | 存储设计、TTL、索引与版本兼容；不通过普通 schema.Apply 改造现有集合 |
| GridFS | `DALNative` 中的 `db.GridFSBucket(...)` | 文件授权、Metadata 租户字段，声明 bucket.files / bucket.chunks 写入范围 |
| 数据库 / Client 级订阅 | `db.Watch` / `db.Client().Watch` | 命名空间白名单、租户可见性、ResumeToken 持久化和消费幂等；不在事务中打开 |
| 跨命名空间批量 | `db.Client().BulkWrite` | 服务端版本支持（MongoDB 8.0+）、每个模型权限、部分成功、全部目标库缓存 |
| Search / VectorSearch 索引 | `Collection.SearchIndexes()` | 部署能力、索引生命周期及查询范围过滤 |
| 官方其他选项 | 在原生回调中构造 options Builder | 读写关注、读偏好、原生执行计划以及超时 Context |

例如 GridFS 文件仓储可在已完成业务授权后使用：

```go
err := repo.WithDAL(ctx, dal, func(queryCtx context.Context) error {
    return mongoplus.DALNative(queryCtx, []string{"assets.files", "assets.chunks"},
        func(nativeCtx context.Context, db *mongo.Database) error {
            bucket := db.GridFSBucket(options.GridFSBucket().SetName("assets"))
            _, err := bucket.UploadFromStream(nativeCtx, filename, reader,
                options.GridFSUpload().SetMetadata(bson.D{
                    {Key: "company_id", Value: companyID},
                }))
            return err
        },
    )
})
```

GridFS 写入不支持多文档事务，以上普通调用不传事务 Context。下载或删除前须校验文件 Metadata，不能只凭客户端传来的 fileID 授权。


删除方式与查询范围是两个独立授权：不要用 DALUnscoped 隐式执行硬删除。原生 DALNative 仍由业务负责选择删除方式与范围，库不会解析回调内容。
