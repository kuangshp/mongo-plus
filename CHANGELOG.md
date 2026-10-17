# 更新记录

## v0.1.0

待发布：包含生成入口的不兼容变更；业务需先迁移生成配置与旧 API，本文不表示标签已经发布。

- 生成流程先从 JSON 准备不可改写的代码/结构快照，统一完成渲染、DAO 路径、跨文件声明和覆盖预检，再访问数据库；check、generate、plan、apply、SyncSchema 共用该流程。同步与文件输出使用同一快照，避免生成失败后才发现名称冲突，或 JSON 变化导致结构与代码不一致。
- 检查同包内跨实体与嵌套类型、DTO/VO、保留业务扩展的声明冲突；冲突在建表、校验器更新和文件写入前报错，补充原有写入不受失败生成影响的真实数据库回归。
- 真实 mongosh 日志重放测试支持 MONGOPLUS_MONGOSH_PATH，可在没有 Docker 的本机环境执行同一套语句与 BSON 类型断言；CI 继续复用容器。
- make tag 改为发布完整已提交修复；干净工作区、版本/更新记录、标签、真实数据库、框架集成、竞态和漏洞检查均在创建标签之前完成，不自动提交版本号或递增版本。

- 修复 go-zero 示例生成器复用业务配置时丢失默认值和键名大小写兼容的问题，改用 `conf.Load`；增加省略连接池/超时、小写配置键及显式无效超时的回归检查。

- **不兼容变更：移除完整生成配置 YAML。** 删除 LoadGeneratorConfig / schema.LoadConfig、专用生成 YAML 与 mongoplus generate/check/schema 入口。生成规则只在 generator.go 配置，通过 RunGenerator(ctx, cfg, action) 执行 generate/check/plan/apply；业务与 Swagger/go-zero 的运行 YAML 继续保留。示例、迁移入口、框架回归和文档同步调整。

- **不兼容变更：数据模型统一由 JSON 定义生成。** 移除 `GeneratorModel`、`GenerateModels`、`SyncModels`、`schema.Model`、`schema.FromModels` 和 `generator.GenerateEntities`，不再提供手写 Entity 建表或生成数据层的入口。旧项目先将定义迁移至 `schema/*.json`，再使用 `Generate` / `SyncSchema`。
- `schema.Plan` / `schema.Apply` 仅接受 `LoadEntities` 从 JSON 文件加载后未修改的元数据；在数据库访问和迁移锁写入前拒绝伪造来源、空定义及加载后篡改。DAL 示例改为复用 JSON 生成模型，更新迁移说明与回归测试。

## v0.0.2

- 增加显式 Replayable 日志模式，输出可复制到 mongosh 执行的真实语句与 BSON 类型；超长语句整条省略，依赖原 Session/游标的命令明确标注；增加真实 mongosh 执行及字段类型回归，并纳入 CI。

- 命令日志默认改为多行 mongosh 风格，常见操作显示 insertOne/find/updateOne/deleteOne 等方法；移除协议噪声，保留日期、ObjectID、大整数精度、数据库标记、调用位置和脱敏，format=json 可保留单行 Extended JSON。

- 仓储构造统一使用 ResolveDatabase：无参绑定默认连接，单个显式句柄独立绑定；无效句柄和多参数初始化时报错，补充三个数据库的绑定回归、多数据源示例与旧业务扩展升级说明。

- 系统性修复公共 SF 嵌套参数类型碰撞，升级 v2 键；区分在途结果类型，支持 nil 接口结果，覆盖精确/前缀失效及并发租户回归。
- 仓储共享缓存升级 v4，写入前校验 BSON 往返值保真；不可无损表示的值绕过共享存储，避免 omitempty 改变空容器和 inline 指针状态。
- 分离 DALUnscoped 与 DALPhysicalDelete，包含已删除记录不再改变删除方式；单条/批量仍保留租户权限。
- 所有依赖字符串主键稳定排序的分页入口统一限制 simple Collation，覆盖集合默认规则；原始 Aggregate 的排序、分页、投影统一应用于最终输出。
- 加密模式拒绝持久化明文字段；表达式/let/脚本日志保守隐藏常量；补齐 FindOne 查询选项、查删 Let 和 Distinct Hint（7.1+）。
- 补充查询选项契约、跨入口矩阵、官方驱动差分与模糊测试；校验空回调和未 Normalize 结构计划，失败明确报错。

- 修复动态 DAL Provider 在一次操作中重复解析导致范围与执行不一致；分页、流式、批量与订阅共用固定选择。
- 自定义 BSON 编解码类型和动态 interface 结果绕过仓储共享缓存，避免命中后重复解码改变值；当前共享键统一使用 v4。
- 敏感字段拒绝跨字段存储冲突、父子路径重叠，以及显式路径与实体 BSON 标签不匹配的配置。
- 自增仓储明确拒绝原子 Upsert，避免污染预分配号段；普通更新/删除统一透传 Collation、Hint、Let、Comment，并拒绝不适用的查询选项。
- Capped 容量创建和 Plan 使用相同规范值，修复重复 Apply 的伪漂移；补充以上边界的单元与真实数据库回归。

- 修复 DAL 批量空过滤、更新管道、重命名目标的租户保护；嵌套租户字段按真实 BSON 路径赋值；敏感自动填充统一加密；订阅支持逻辑权限递归并对不可转换表达式报错；geoNear 合并 query 范围。
- 增加原子查改/查删/查替换、Distinct、受隔离限制的估计计数，查询命令选项和类型安全操作符；同步生成仓储与启动集合规则注册，冲突配置拒绝覆盖。
- 扩展索引类型、部分过滤、Collation、Hidden、文本权重，补齐普通集合选项和附加 JSON Schema 规则，Plan 比较完整索引语义；增加多版本 CI 和原生扩展能力说明。

- 修复嵌套敏感标签被跳过、空匿名嵌入指针 panic 和创建失败污染原实体的问题；新增递归结构体/切片/数组元数据、完整路径查询与数字下标更新，失败创建保持输入不变，有序批量仅回填已确认成功前缀。

- 公共 SF 对参数序列化失败返回 ErrInvalidCacheArgs，阻止缓存与请求合并串值，并增加可返回错误的失效入口。
- 敏感字段新增、更新、读取和实体等值查询共用类型隔离的元数据；补齐标签更新、自定义 BSON 路径、Unset 和生成仓储 SensitiveEq。
- 公共 MemoryCache 默认限制为 1024 条并按 LRU 淘汰，每秒主动回收过期项；支持配置条目上限、清理间隔、Len 和 Close。

- DAL 原生文件闭环：新增流式读取、集合变更订阅、六种混合批量模型、原生回调、命名写入与仓储 WithDAL；补齐聚合单条/分页计数、写入聚合和写后缓存/事务绑定，并增加独立属性文档及可编译业务仓储示例。

- 修复插件租户字段可被普通更新改写的问题，保护单字段、多字段、集合覆盖及父子路径。
- 增加数据权限 Strict 模式、ErrDataPermissionRequired 和中间件预检查。
- 增加共享缓存错误反馈、监控、失败失效队列及重试；关键查询仍应关闭缓存。
- 命令日志默认脱敏常见敏感字段并隐藏错误详情，支持自定义字段、参数隐藏和长度限制。
- 新增独立迁移管理：执行锁、版本与源码校验和、历史记录、失败恢复、分批回填和显式回滚；原 schema.Apply 与回填入口共用锁。
- 补齐 WithTx、TransactionCtx、TransactionAsCtx、生命周期保护与提交后缓存失效。
- 新增可配置的混合读写压测工具、P95/P99 门槛和内存指标；本机结果不代表业务容量。
- 发布检查要求真实数据库及框架集成；标签使用完整已提交修复，不再单独提交版本号。
- 修补 x/text 至 v0.41.0，并随依赖更新 x/sync 至 v0.22.0；保留 Go 1.25.5 最低版本。

升级时固定 `github.com/kuangshp/mongo-plus@v0.0.2`（需待标签发布后可用），重新运行业务项目的 `go run generator.go` 取得 `WithTx`。业务扩展和 DTO/VO 不覆盖；手写 Repository mock 需要补充 `WithTx`。

DAL 文件现在拒绝未知/重复属性及无效选项；计数入口要求 `op=count` 或返回 `total` 的聚合。旧文件中未生效的额外属性应在升级前清理；DAL 写入会自动通知同库 Repository 缓存失效。

日志默认行为有安全加固：常见敏感字段会脱敏，服务端错误详情默认隐藏。仅在受控诊断时设置 `IncludeErrorDetails: true`。数据权限默认要求注入函数；旧项目用 `AllowMissing: true` 恢复不加条件的行为。共享缓存的集合版本写在数据库中，供同一 Namespace 的其他实例避开旧条目。DAL 执行时追加仓储上的租户、数据权限和逻辑删除。

## v0.0.1

- 首个带版本标签的公开基线。
