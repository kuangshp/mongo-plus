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
| `TransactionCtx` / `TransactionAsCtx` | 同名入口；MongoDB 以 `ctx` 为首参，事务凭据为 Session Context |
| `repository.WithTx(tx).Create(ctx, entity)` | `repository.WithTx(txCtx).Create(ctx, entity)`；查询和写入均调用普通方法 |

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

`mongoConfig` 来自业务项目配置。go-zero 模板会在 `NewServiceContext` 连接成功后自动执行上述 `mongoplus.Use(db)`，无需给每个 Repository 传入连接。已有 ServiceContext 需手动合并这一行，goctl 不会覆盖已有实现。已有官方驱动数据库可以用 `mongoplus.NewDatabase(nativeDB)` 包装。Repository 在启动时创建一次并复用；只有拥有 Client 的入口负责关闭连接。

Repository 模板按 gorm-plus 的结构组织：`user_gen.go` 显式声明 `defaultUserRepository`、`iDefaultUserRepository`、各基础方法及 `newDefaultUserRepository`；`user.go` 声明 `customerUserRepository`、对外接口、编译期接口检查和 `NewUserRepository`。接口不再通过嵌入 `mongoplus.Contract` 隐式取得方法。新增基础能力后需要重新生成 `_gen.go`。

生成方法统一调用公共 MongoDB 执行器，保留租户、逻辑删除、缓存失效、写入确认和 Session Context 行为。业务自定义方法写在 `customerUserRepository` 上，并在 `UserRepository` 接口中声明；覆盖基础方法时使用相同签名。原有扩展文件可以继续使用，生成器不会覆盖业务扩展、DTO、VO。若业务项目手写了 Repository mock/实现，新增方法和可变参数签名需要同步调整。

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

分页会补充 `_id` 作为稳定排序键；列表和总数默认执行两次查询。联表/自定义 VO 查询改用 `repository.WithContext(ctx).LeftJoin(...).Select(...).ScanByPage(&rows, page, size)`，或泛型 `mongoplus.ScanByPage[VO](ctx, query, page, size)`。具体例子见 [关联查询](../README.md#11-关联查询lookupleftjoininnerjoin)。

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

当前 gorm-plus 已统一使用 `WithTx(tx).普通方法(...)`，mongo-plus 同样提供仓储副本绑定。区别是 MongoDB 使用 Session Context 作为事务凭据，回调中的 `txCtx` 对应 gorm-plus 的事务 DAO：

```go
err := mongoplus.TransactionCtx(ctx, db, func(txCtx context.Context) error {
    orders := orderRepository.WithTx(txCtx)
    details := orderDetailRepository.WithTx(txCtx)
    if err := orders.Create(ctx, order); err != nil {
        return err
    }
    return details.CreateBatch(ctx, detailEntities)
})
```

`WithTx` 不修改原仓储，返回完整业务接口并保留覆盖方法和注入依赖；必须接收返回值或链式调用。绑定仓储后仍传请求 `ctx`，查询、分页、聚合和全部写入自动使用事务 Session。原有向普通方法直接传 `txCtx` 的写法继续兼容。较旧 gorm-plus 项目中的 `CreateTx(ctx, tx, entity)` 应改成 `WithTx(txCtx).Create(ctx, entity)`。

`Transaction` 与 `TransactionCtx` 的参数、行为相同，均为 `(ctx, db, fn, opts...)`。`TransactionAs` 与 `TransactionAsCtx` 均为 `(ctx, db, use, fn, opts...)`，`use` 接收事务 Context 并返回业务类型，可以绑定已有 Repository 组成工作单元；例如 `TransactionAsCtx(ctx, db, userRepository.WithTx, func(users repository.UserRepository) error { return users.Create(ctx, user) })`。MongoDB 不使用 gorm-gen 的 `dao.Use`。

升级库后重新运行 `go run generator.go`，`WithTx` 会写入 `_gen.go`，无需覆盖业务扩展；手写 mock/接口实现需补充方法。自定义方法优先组合普通仓储方法，`Native()` 操作仍需显式使用 `txCtx` 并维护缓存。

无效/未跟踪绑定返回 `ErrUnmanagedTransaction`，不同 Client、Session 或轮次混用返回 `ErrTransactionMismatch`；回调退出后绑定仓储及该轮 `txCtx` 返回 `ErrTxDone`。每轮驱动重试必须重新 `WithTx`，不要保存绑定仓储供下一轮或回调外使用。

事务要求副本集或分片集群，所有 Repository 必须共用同一 Client。驱动可能重试回调；不要并行使用 Session、手动 Commit/Rollback 或执行不可重复的外部副作用。事务读取跳过缓存及 SingleFlight；提交后统一失效涉及的集合缓存，回滚不发布失效；提交结果不确定时保守失效。MongoDB 不支持 GORM 的嵌套保存点，嵌套事务明确报错。

## 缓存、数据权限与数据库特有功能

`WithSingleFlight()` 或 `WithSingleFlight(0)` 仅合并并发；正 TTL 启用缓存。`WithCache(ttl)` 显式设置缓存 TTL。过滤条件、投影、排序、分页和租户自动进入缓存键。写操作会失效本机缓存。调用 `RegisterCache` 或 `db.UseCache` 注入共享缓存后，默认仍按 Database 包装器隔离；同一实际数据源的各实例应在启动时配置相同的 `CacheConfig{Namespace: "orders-prod-primary"}`，不同环境和数据源使用不同 Namespace。仓储通过集合版本隔离旧在途查询，后端只需实现 `Get/Set/Del`，其中 `Set(..., 0)` 表示版本键不过期；前缀删除是可选的空间回收能力。Redis 的实现示例在 `examples/redis-cache/cache.go`。`SF`、`SFInvalidate` 和 `SFInvalidatePrefix` 用于仓储之外的手动查询，它们的前缀失效仍要求后端实现前缀删除。

`RegisterDataPermission` 配合 `WithDataPermission` 为查询、更新和删除追加集合级条件。`SkipDataPermission` 只用于受控的全量访问。`RegisterSensitive` 在写入时保存密文或规范化值和盲索引，读出时默认脱敏；等值查询使用 `plugin.Eq` 或 `plugin.PhoneEq`。敏感字段的展示在缓存返回独立副本后按当前请求处理，明文、脱敏和密文请求不会串用展示结果。`RegisterSlowQuery` 在现有 `Observer` 之外记录超过阈值的集合操作，应在设置 `Observer` 之后注册。

`DS.Register` 接收已经连接好的主库和从库。`DS.Auto(ctx)` 根据 `DSWithName`、`DSWithRead` 和 `DSWithWrite` 选择数据库；写标记优先，没有从库时读操作回到主库。从库的读偏好写在它自己的连接 URI 中。

`NewDal` 加载 JSON 查询文件，调用名与 gorm-plus 的 DAL 对应：`DALQuery`、`DALQueryOne`、`DALQueryPage`、`DALExec` 和 `DALWithTx`。文件内容是集合、操作和 BSON，占位符使用 `?` 或 `@name`，不是 SQL。分页计数文件名为数据文件名前加 `count_`。文件中的查询不会自动追加租户和逻辑删除，需要在文件里写明。`DALWithTx` 的回调参数是事务 Context，后续 DAL 调用继续传这个 Context。

SQL 的 `ForUpdate`、隔离级别、savepoint 和 GORM clause 没有对应的 MongoDB 语义，不能通过封装模拟行锁。事务的读关注和写关注继续使用官方 `options.Transaction()`。

同 Client 同库的包装器共享本机失效通知。未注入共享缓存时，跨进程写入要求实时读取，可以对包装器调用 `db.ConfigureCache(mongoplus.CacheConfig{Disabled: true})`。

本次对齐也修正了旧 mongo-plus 的两处语义：`BetweenIfNotZero` 不再应用单边非零范围；`OrWhereIf(true, a, b)` 不再将 a/b 隐式组成 AND 分组。已有 mongo-plus 调用若依赖旧行为，应分别改用显式 `Gte/Lte` 和 `OrGroup(a, b)`。

验证命令：`go test ./...`、`go vet ./...`。真实数据库回归需配置测试专用副本集 `MONGOPLUS_TEST_URI`，再执行 `make integration`；测试使用随机数据库并自行清理。
