# 从 gorm-plus 平滑迁移

mongo-plus 以 gorm-plus 的业务使用习惯为参照：根目录 `generator.go` 配置生成器，生成 Entity/DAO/Repository，在启动入口创建 Repository 并注入业务层，复杂查询保留在同名非生成 Repository 扩展文件中。现有业务接口、DTO/VO 分层和分页请求结构可以继续沿用。

## 常用调用对应关系

| gorm-plus | mongo-plus |
| --- | --- |
| `GeneratorConfig` / `Generate` / `go run generator.go` | 同名入口；输入改为集合 JSON 定义或手写 Entity |
| `repository.NewUserRepository()` | 启动时 `mongoplus.Use(db)` 后同样 `NewUserRepository()`；`NewUserRepository(db)` 仍优先使用传入连接 |
| `dao.UserEntity.ID.Eq(id)` | 同样的类型安全字段调用；类型来自当前生成实体 |
| `FindById` / `FindByIdList` / `FindOne` / `FindOneWrapper` | 保留方法名、上下文和查询选项的位置 |
| `FindList` / `FindListByWrapper` | 保留方法名与返回的实体指针切片 |
| `FindPage` / `FindPageByWrapper` | 保留页码、每页数量和 `(list, total, err)`；内部统一使用 `DbPage` |
| `func(w gormplus.IGenWrapper[dao.IUserEntityDo])` | `func(w *mongoplus.Wrapper)` |
| `QueryOpt().Where(...).Order(...).Build()` | 同名调用，条件使用 MongoDB DAO 字段 |
| `Like/LLike/RLike`、可选条件、分组、范围、排序 | 对应的 Wrapper 方法，见下例 |
| `CountByWrapper` / `ExistsByWrapper` | 同样接受回调和额外的查询选项；回调可为 nil |
| `Update().WithColumns(...).Build()` / `WithUpdateColumns(...)` | 同名调用，直接作为更新参数；字段用 `Value` 或 `Set` |
| `DeleteByIdList` / `DeleteByCondition` | 同名方法，返回 `(affected, err)` |
| `Delete().WithPhysicalDelete().Build()` | 同名选项，支持按 ID、ID 列表和 Wrapper 删除 |
| `RegisterCtxResolver` | 同名注册入口，不引入 Gin 依赖 |
| `WithSingleFlight(0)` / `WithSingleFlight(ttl)` | 同样分别表示仅并发合并、并发合并加缓存；也支持无参写法 |
| `TransactionAs` / `CreateTx` 等 | 使用事务回调提供的 `txCtx` 调用普通 Repository 方法 |

`DbPage` 默认第一页、每页 10 条、最多 500 条，并对负页码和计算溢出作保护；返回的 offset/limit 为驱动所需的 `int64`。标准分页直接使用 Repository 的分页方法，不需要业务层重新计算。

## 生成与注入

继续使用项目根目录的 `generator.go`。`Package`、`ModelPkgPath`、`OutPath`、`RepoPath`、`DtoPath`、`VoPath` 等常用配置沿用 gorm-plus 的目录含义，配置示例见 [README](../README.md#1-全局配置一次实体按需覆盖)。

关系表 DDL 改为集合 JSON 定义，或通过 `GenerateModels` 注册已有带 BSON 标签的 Entity。配置了业务 MongoDB 数据源时，`Generate` 先同步集合、索引和校验器再生成文件；没有数据源时只生成代码。不要把 MySQL SQL 文件传入 MongoDB 生成器。

```go
db, err := mongoplus.Connect(ctx, mongoConfig)
if err != nil {
    return err
}
mongoplus.Use(db)
userRepository := repository.NewUserRepository()
// 显式注入时：repository.NewUserRepository(db)，传入连接优先于 Use。
```

`mongoConfig` 来自业务项目配置。已有官方驱动数据库可以用 `mongoplus.NewDatabase(nativeDB)` 包装。Repository 在启动时创建一次并复用；只有拥有 Client 的入口负责关闭连接。

生成的 `iDefaultUserRepository` 嵌入 `mongoplus.Contract`，因此新增的基础方法会自动出现在业务 Repository 接口上。业务扩展、DTO、VO 首次创建后不覆盖；实体或生成配置变化时仍从源定义重新生成。若业务项目手写了 Repository mock/实现，新增方法和可变参数签名需要同步调整。

## 条件、分组与分页

下面代码沿用示例生成的 `dao.UserEntity`，放在业务 Repository 扩展中。`startTime` 和 `endTime` 为已校验、已解析的 `time.Time`；请求中的时间参数继续使用带 `dateTime` 校验的字符串。

```go
list, total, err := userRepository.FindPageByWrapper(
    ctx, req.PageNumber, req.PageSize,
    func(w *mongoplus.Wrapper) {
        w.WhereIf(req.Status != nil, dao.UserEntity.Status.Eq(statusValue)).
            WhereGroupFn(func(group *mongoplus.Wrapper) {
                group.Like(dao.UserEntity.Username, keyword).
                    OrRLike(dao.UserEntity.Username, prefix)
            }).
            BetweenIfNotZero(dao.UserEntity.CreatedAt, startTime, endTime).
            OrderTriState(sortState, dao.UserEntity.CreatedAt.Asc(), dao.UserEntity.CreatedAt.Desc()).
            OrderDefault(dao.UserEntity.ID.Desc())
    },
    mongoplus.QueryOpt().WithSingleFlight(0).Build(),
)
```

`statusValue` 在请求参数非 nil 时提取；不要为了构造 `WhereIf` 的参数直接解引用 nil 指针。合法的 0/false 与“未提供”应继续由指针等请求字段表达。

- `Like` 包含匹配，`LLike` 后缀匹配，`RLike` 前缀匹配；空文本跳过。MongoDB 使用转义后的正则做字面匹配，SQL 中的 `%`、`_` 在这里是普通字符。
- `OrWhereIf(true, a, b)` 为逐个 OR；`OrGroup(a, b)` 为 `OR (a AND b)`；`WhereOrGroup(a, b)` 为 `AND (a OR b)`。
- `WhereGroupFn` 和 `OrGroupFn` 支持嵌套的可选条件；空回调跳过。子构造器只贡献过滤条件，不传播排序、分页或 `AllowAll`。
- `BetweenIfNotZero` 在任一端点为零值时跳过整个范围，两端非零时包含两端。单边输入应由请求契约明确处理；单边范围和包含 0 的范围使用字段的 `Gte/Lte`。
- `OrderTriState` 的 1/asc 为升序、2/desc 为降序，其他值跳过；`OrderIf` 支持真假分支，`OrderDefault` 在没有显式排序时提供默认值，行为与 gorm-plus GenWrapper 一致。
- Wrapper、Select、Order 都使用完整 DAO 字段名。`WhereRaw` 接收可信业务代码构造的 `bson.D`，原生 SQL 需改写为 BSON。

分页会补充 `_id` 作为稳定排序键；列表和总数默认执行两次查询。联表/自定义 VO 查询改用 `repository.WithContext(ctx).LeftJoin(...).Select(...).ScanByPage(&rows, page, size)`，或泛型 `mongoplus.ScanByPage[VO](ctx, query, page, size)`。具体例子见 [关联查询](../README.md#10-关联查询lookupleftjoininnerjoin)。

## 更新与删除

mongo-plus 的写操作保留影响行数，便于业务区别“未命中”和“实际修改”：

```go
result, err := userRepository.UpdateById(ctx, id,
    mongoplus.Update().WithColumns(
        dao.UserEntity.Status.Value(0),
        dao.UserEntity.Username.Set(""),
    ).Build(),
)
// result.MatchedCount / result.ModifiedCount

affected, err := userRepository.DeleteByIdList(ctx, ids)
affected, err = userRepository.DeleteByCondition(ctx, dao.UserEntity.Status.Eq(0))
affected, err = userRepository.DeleteById(ctx, id,
    mongoplus.Delete().WithPhysicalDelete().Build(),
)
```

更新赋值使用 `Value` 或 `Set`，两者相同；也可直接传多个字段操作，或使用 `WithUpdateColumns`。0、false、空字符串均有效。删除和更新返回值与 gorm-plus 的仅 error 返回值不同，原先 `return repo.DeleteById(...)` 的位置应显式接收影响数再返回 error。

默认遵循集合逻辑删除配置；`WithUnscoped`/`WithDeleted` 查询全部，`OnlyDeleted` 只查询已删除记录。物理删除选项也适用于 `DeleteByWrapper` 和 `DeleteByIdList`，能命中已逻辑删除文档；租户过滤始终生效。

空 ID 列表删除不访问数据库，返回 `(0, nil)`；列表内任一非法 ID 会在写入前拒绝整次调用。无条件 `DeleteByCondition`、nil 或无有效条件的写入 Wrapper 返回 `ErrUnsafeWrite`。批量全量写入需要在外层 Wrapper 显式调用 `AllowAll()`。

## Gin Context

如果当前项目传标准 `c.Request.Context()`，可继续沿用。若要保留直接传 `*gin.Context` 的写法，在启动时注册与 gorm-plus 相同的解析器：

```go
mongoplus.RegisterCtxResolver(func(ctx context.Context) context.Context {
    if c, ok := ctx.(*gin.Context); ok {
        return c.Request.Context()
    }
    return ctx
})
```

解析后的 Context 同时用于查询、写入、缓存、监控、聚合和事务。解析器必须原样保留标准 Context，尤其是带 Session 的 `txCtx`。传 nil 可重置解析器；解析结果为 nil 时保留原始 Context。取消信号、租户和操作人都应放在请求 Context 中。

## 事务

MongoDB 使用 Session Context 传递事务。迁移时将 `CreateTx(ctx, tx, entity)` 改成 `Create(txCtx, entity)`，同一回调内的查询和写入都传 `txCtx`：

```go
err := mongoplus.Transaction(ctx, db, func(txCtx context.Context) error {
    if err := orderRepository.Create(txCtx, order); err != nil {
        return err
    }
    return orderDetailRepository.CreateBatch(txCtx, details)
})
```

`TransactionAs` 的形状为 `TransactionAs(ctx, db, use, fn)`，其中 `use` 接收事务 Context，可以将它与已注入的 Repository 一起包装为项目自己的工作单元。通常直接使用 `Transaction` 更少改动，不需要为每个 Repository 重新创建连接或增加重复的 `*Tx` 方法。

事务要求副本集或分片集群，所有 Repository 必须共用同一 Client。驱动可能重试回调；事务内不要并行使用 Session，也不要执行不可重复的外部副作用。事务中的读取跳过本机缓存，事务结束后失效涉及的集合缓存。

## 缓存与数据库特有功能

`WithSingleFlight()` 或 `WithSingleFlight(0)` 仅合并并发；正 TTL 启用缓存。`WithCache(ttl)` 显式设置缓存 TTL；同一构造器同时使用两者时，显式缓存 TTL 优先。过滤条件、投影、排序、分页和租户自动进入缓存键，写操作自动失效相关集合缓存，不需要迁移 gorm-plus 的手工条件缓存参数。

同 Client 同库的包装器共享失效通知。跨 Client、多个服务实例或外部写入要求实时读取时，对所有包装器调用 `db.ConfigureCache(mongoplus.CacheConfig{Disabled: true})`，业务层即使继续传缓存选项也会直读数据库。

所有创建、更新、删除和恢复方法统一检查写入确认；`w=0` 返回 `ErrUnacknowledgedWrite`，不能把删除返回的 0 当作已确认未命中。聚合分页不再使用 facet，而是执行计数与列表两个聚合；需要一致快照时，通过 Transaction/TransactionAs 的可选参数传入官方 `options.Transaction().SetReadConcern(readconcern.Snapshot())`。

SQL 的 `ForUpdate`、GORM clause、savepoint、DAL SQL、GORM 插件不能直接作用于 MongoDB。相应业务逻辑需要按 MongoDB 的原子更新、Session 事务、聚合管道或 Repository 扩展实现。关联查询中的集合使用各自的 Repository 规则来保留租户和逻辑删除隔离。

本次对齐也修正了旧 mongo-plus 的两处语义：`BetweenIfNotZero` 不再应用单边非零范围；`OrWhereIf(true, a, b)` 不再将 a/b 隐式组成 AND 分组。已有 mongo-plus 调用若依赖旧行为，应分别改用显式 `Gte/Lte` 和 `OrGroup(a, b)`。

验证命令：`go test ./...`、`go vet ./...`。真实数据库回归需配置测试专用副本集 `MONGOPLUS_TEST_URI`，再执行 `make integration`；测试使用随机数据库并自行清理。
