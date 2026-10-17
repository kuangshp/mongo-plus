# 性能验证与生产接入

## 默认行为

- Create 直接将准备好的实体交给官方驱动，沿用现有 Client 的 BSONOptions/Registry。自动字段使用明确的 bson 标签；业务字段可使用已配置的 JSON 标签回退或字段编码器。
- CreateBatch 每块最多 1000 条，使用有序 InsertMany；数字主键每块只访问一次序列表。块内预处理错误不写入该块；数据库写入错误可能产生已确认的成功前缀。已完成的前置块不会自动回滚。
- 单条自增默认每次按需申请。可选 ID.BlockSize 预分配区间；同一 Database 包装器的同集合仓储共享区间。跨包装器/进程由 MongoDB 原子分配互不重复区间；未消费区间可能产生跳号，提交顺序不等于 ID 顺序。预分配不减弱 majority 写确认。
- 所有写方法都检查驱动的 Acknowledged；`w=0` 返回 ErrUnacknowledgedWrite，影响数为 0 时不能忽略该错误。已获确认但未命中仍返回 0、nil。
- 查询缓存默认关闭。显式开启时，同时限制结果条数、总保留容量和单条容量；容量是 Go 对象估算，不是 RSS 硬限制。同 Client 同库的包装器共享失效通知，保留独立的查询结果和解码配置。跨 Client、多进程或外部写入要求实时读取时，对所有包装器设置 `ConfigureCache(CacheConfig{Disabled: true})`，强制绕过缓存和 SingleFlight。
- ScanByPage 分别执行计数和列表聚合，列表通过游标逐文档返回，整页可以超过 16 MiB，不使用 facet。单条结果仍受 MongoDB 文档大小限制；两次查询需要一致快照时，通过 Transaction 的可选参数配置 `options.Transaction().SetReadConcern(readconcern.Snapshot())`。
- generator.go 的 plan/apply 比较校验规则及 strict/error 状态；不会补齐历史数据，也不会自动替换冲突索引。结构同步不是整体事务。

## 本地回归与基准

使用测试专用 MongoDB 副本集；测试创建随机数据库并在结束后清理。基准没有启用 race，避免测到竞态检测器开销：

```bash
export MONGOPLUS_TEST_URI='mongodb://127.0.0.1:27017/?replicaSet=mongo-plus-test&directConnection=true'
go test -race -count=1 ./...
make bench
make security
```

安装 goctl 后，可以开启 HTTP 框架和模板完整集成验证：

```bash
go install github.com/zeromicro/go-zero/tools/goctl@v1.10.2
MONGOPLUS_GIN_INTEGRATION=1 MONGOPLUS_GOCTL_INTEGRATION=1 go test -race -count=1 ./...
```

BenchmarkRepository 覆盖主键查询、原生/仓储批量插入、单条自增及号码预分配。应比较同一硬件、数据量、索引、写确认和并发设置下的多次结果。本地单节点副本集的平均耗时不能直接推算生产 QPS 或 P95/P99。

## 专用三节点副本集切主测试

以下测试会主动让整个副本集的主节点退位，仅对专用测试集群执行。URI 不使用 directConnection，确保客户端能够发现新主节点：

```bash
MONGOPLUS_FAILOVER_URI='mongodb://127.0.0.1:27017,127.0.0.1:27018,127.0.0.1:27019/?replicaSet=mongo-plus-test&retryWrites=true' \
  go test -race -count=1 -run '^TestReplicaSetFailover$' -timeout=2m .
```

该测试要求至少三个成员，确认主节点变化，并验证切主后原数据可读、批量事务可提交。持续集成在其他集成测试完成后单独运行，避免切主干扰其他测试。它不覆盖任意网络分区、磁盘故障、分片迁移或长时间高负载；这些应在业务预发布环境按实际部署演练。

## 本机 mongosh 日志重放验证

使用独立测试副本集，并将路径指向已安装的真实 mongosh：

```bash
MONGOPLUS_TEST_URI='测试专用副本集URI' \
MONGOPLUS_MONGOSH_PATH='/绝对路径/mongosh' \
  go test -race -count=1 -run '^TestShellStatementsExecuteInMongosh$' .
```

测试实际执行日志生成的增删改查及聚合语句，再由官方 Go 驱动核对数据和 BSON 类型。CI 保持使用 `MONGOPLUS_MONGOSH_CONTAINER`；两者同时配置时优先使用本机路径。只配置数据库 URI 不会启动重放测试。

## 2026-10-17 本机兼容性验收记录

基于包含未提交改动的 v0.1.0 工作区，在 macOS arm64 上使用独立的原生三节点副本集验证；不是线上部署或业务容量验收。

| MongoDB | 全量 race、Gin/go-zero 集成 | mongosh 2.13.0 日志重放 | 单独切主与事务恢复 |
| --- | --- | --- | --- |
| 6.0.18 | 597 项测试及子测试通过 | 通过 | 通过 |
| 7.0.0 | 597 项测试及子测试通过 | 通过 | 通过 |
| 8.0.0 | 597 项测试及子测试通过 | 通过 | 通过 |

全量测试中的切主项有意跳过，再在正常集成完成后单独执行并通过。Linux amd64 交叉构建、Go 1.25.5 构建及 go vet 通过；govulncheck 报告可达漏洞为 0，另有 4 条未被本库调用的依赖模块告警。生成失败不改变已有校验器或阻止旧写入、跨实体重名拒绝、JSON 快照一致性及发布保护均有回归覆盖。

该结果支持版本发布和业务预发布接入；正式生产仍应使用项目实际 JSON、历史数据与索引、权限上下文及目标并发进行验收，再灰度上线。代码生成及业务编译在发布前完成，结构变更独立审核，不在请求处理时自动迁移。

## 2026-10-17 并发、性能与响应时间实测

测量源码：`0c151fd`（库内版本 v0.1.0）。[汇总 JSON](benchmarks/2026-10-17-summary.json)、[原始 Go benchmark 输出](benchmarks/2026-10-17-bench.txt)和[官方驱动对比测试源码](benchmarks/compare_test.go.txt)可用于复核。没有修改生产代码。

环境为 Apple M4 Pro、12 核、48 GiB、macOS arm64、Go 1.26.9、MongoDB 8.0.0 原生三节点副本集；应用和三个数据库进程在同一台机器。每节点 WiredTiger 缓存上限 0.5 GiB，主节点读取，写入显式采用 `w=majority&journal=true`。

负载为均匀随机主键访问：80% FindById、20% UpdateById 的 `$inc`，10 万条文档、每条 256 字节固定重复字符载荷。关闭查询缓存和命令日志，主键由业务提供，集合未启用时间字段、软删除或业务插件。每个并发档重复三次，每次预热 2 秒、测量 8 秒；表中吞吐和分位数为各轮结果的中位数，堆为三轮中的采样最大值。连接池上限随并发设置为 `并发 + 10`，并发表示持续发请求的 goroutine 数。

| 并发 | 操作/秒中位数 | 三轮吞吐范围 | P95（ms） | P99（ms） | 客户端 Go 堆峰值（MiB） | 错误数 |
| --- | --- | --- | --- | --- | --- | --- |
| 1 | 477 | 462–487 | 10.19 | 13.22 | 3.2 | 0 |
| 8 | 2,495 | 2,454–2,533 | 16.78 | 19.52 | 2.8 | 0 |
| 32 | 9,746 | 9,737–10,072 | 16.07 | 18.70 | 4.8 | 0 |
| 64 | 18,102 | 16,886–18,860 | 17.52 | 21.76 | 7.1 | 0 |
| 128 | 35,232 | 31,077–35,342 | 17.52 | 20.83 | 13.4 | 0 |
| 256 | 48,155 | 46,445–49,805 | 21.29 | 27.02 | 27.4 | 0 |

64 并发下，读取 P95 约 0.96 ms、更新 P95 约 21.76 ms；256 并发下，读取 P95 上升到约 5.68 ms。吞吐继续增长，但增加并发的收益逐渐下降，尾延迟与分配量也增加。本次没有测出整个系统的吞吐上限。

额外场景：

| 场景 | 操作/秒 | P95（ms） | P99（ms） | 错误数 |
| --- | --- | --- | --- | --- |
| 10 万条，32 并发，100% 读取 | 71,790 | 1.00 | 1.30 | 0 |
| 10 万条，32 并发，100% 更新 | 1,965 | 19.95 | 23.22 | 0 |
| 100 万条，64 并发，混合读写，第 1 轮 | 17,128 | 17.90 | 24.78 | 0 |
| 100 万条，64 并发，混合读写，第 2 轮 | 17,282 | 19.11 | 23.22 | 0 |
| 10 万条，64 并发，持续 120 秒 | 18,273 | 17.90 | 21.76 | 0 |

全部负载场景共记录 6,159,885 次测量操作，错误为 0，不含预置、预热与 Go benchmark。持续测试完成 2,193,007 次操作，客户端采样堆峰值 7.44 MiB、结束堆 5.31 MiB；首尾各 20 秒的 RSS 中位数约 32.16 / 32.78 MiB。120 秒内未见明显 RSS 持续增长，不能据此推断数小时或数天的内存行为。客户端堆与 RSS 均包含压测器、驱动和直方图，不包含 MongoDB 进程。

100 万条数据的吞吐与 10 万条的重复测试范围重叠，当前样本没有显示明显量级退化。文档使用易压缩的固定载荷，数据量远小于机器总内存；没有验证超过内存的数据集、真实文档压缩率或磁盘受限场景。

官方驱动与封装对比使用同一 Client、集合、字段、BSON 类型和写确认，热主键、串行访问，无插件。每项基准时长 1 秒、重复三次，以下为平均单次耗时的三轮中位数，不能与上表 P95/P99 直接混用：

| 操作 | 官方驱动（ms/次） | Repository（ms/次） | 分配字节：驱动 / 仓储 | 分配次数：驱动 / 仓储 |
| --- | --- | --- | --- | --- |
| 按主键读取 | 0.045356 | 0.048136 | 14,315 / 15,552 | 103 / 121 |
| 按主键更新 | 10.379568 | 11.415573 | 8,666 / 9,706 | 107 / 134 |

读取相对耗时约增加 6.1%，绝对约 2.78 微秒，多约 1.2 KiB 和 18 次分配。更新包含多数确认及日志刷盘的调度波动，两组范围重叠，不能将全部差值认定为封装 CPU 开销。

100 条批量插入的三轮中位数：官方 InsertMany 约 12.71 ms，CreateBatch 约 12.28 ms；这些短样本不证明封装比驱动更快。int64 单条自增插入从按次申请的约 22.87 ms，降到 BlockSize=100 的约 11.78 ms；预分配允许跳号，也不保证 ID 顺序等于提交顺序。批量数字主键分配本身已按块申请，BlockSize=100 没有在本轮进一步降低整批耗时。

并发与扩展专项回归采用 race：并发自增/分页、跨仓储和包装器的号码段、默认与显式三个数据源句柄、事务 Context/Client 隔离、跨包装器缓存失效、DAL 原生读写、GridFS 的租户边界，以及生成仓储的自定义方法和事务绑定均通过。生成消费者测试通过 GOFLAGS=-race 使子进程也使用竞态检查。

扩展能力由运行时已有的 `NewDatabase`、显式数据源构造、`Observer` / `CacheObserver`、可替换缓存和 DALNative / WithDAL / DALWalk / DALWatch / DALBulkWrite 提供。高级连接池与驱动监控可以在业务创建官方 Client 后注入。原生扩展遵守权限、事务 Context、读写集合声明和缓存失效契约。上述是功能及单机并发验证；多服务多机器横向扩容、分片部署、热点写入和复杂聚合容量尚未实测。

结论：当前基本 CRUD 场景的并发、吞吐和响应时间具备中小型项目的数据层接入条件。生产验收应使用真实索引、查询分布、文档大小、网络和插件，加入 HTTP/鉴权/业务耗时；确认 SLO 后灰度上线。本轮是闭环负载，没有独立测量入口排队与过载时的开放到达率。

复现并发测试（每档重复三次；各次串行执行，避免其他基准干扰）：

```bash
go build -o /tmp/mongoplus-loadtest ./cmd/mongoplus-loadtest
for round in 1 2 3; do
  for concurrency in 1 8 32 64 128 256; do
    /tmp/mongoplus-loadtest -uri "$MONGOPLUS_TEST_URI" \
      -seed 100000 -concurrency "$concurrency" -payload-bytes 256 \
      -read-percent 80 -warmup 2s -duration 8s \
      -output "/tmp/mongoplus-c${concurrency}-r${round}.json"
  done
done
```

本次实际运行使用固定随机种子打乱每轮并发档顺序，以减小预热和时间顺序的影响。URI 需显式使用测试专用三节点及 `w=majority&journal=true`。百万条档使用 `-seed 1000000 -concurrency 64 -warmup 3s -duration 15s`，执行两轮；持续档使用 `-seed 100000 -concurrency 64 -warmup 3s -duration 120s`。

官方对比源码复制到独立临时 Go 模块中，将 mongo-plus 依赖 replace 到本次被测源码，再执行 `MONGOPLUS_TEST_URI=... go test -mod=mod -run '^$' -bench '^BenchmarkCompare$' -benchtime=1s -count=3 -benchmem .`。批量与自增基准在仓库根目录使用 `-bench '^BenchmarkRepository$'`。计时阶段关闭 race，竞态回归单独执行。

## 接入前的业务验证

按预计数据量建立复合索引，并检查主要查询的执行计划。租户、deleted_at、业务过滤与排序应结合考虑；不要只为单个字段机械建索引。深分页使用游标，大规模导出使用 Walk，关联查询对外键建立索引并控制展开数量。

设置请求 Context 超时，复用 Client。事务回调可能重试，回调中的外部副作用需要业务幂等方案。业务应以唯一业务键核对网络错误后的写入结果，不能把重试自动理解为业务“恰好执行一次”。

构建服务时使用受支持且已修复漏洞的 Go 版本。本仓库推荐的 toolchain 不会强制升级使用方主模块的工具链；发布前在业务模块运行 govulncheck。

## 可配置混合读写压测

压测工具要求显式指定测试专用 URI，在该实例创建随机 `mongo_plus_loadtest_*` 数据库，结束后删除该随机数据库。不要指向生产实例：即使集合隔离，压测仍会占用该实例的资源。

```bash
go run ./cmd/mongoplus-loadtest \
  -uri 'mongodb://127.0.0.1:27017/?replicaSet=test&directConnection=true' \
  -seed 100000 -concurrency 32 -read-percent 80 -payload-bytes 256 \
  -warmup 10s -duration 5m -timeout 5s \
  -max-p95-ms 30 -max-p99-ms 100 -max-error-rate 0.001 \
  -output /tmp/mongo-plus-loadtest.json
```

以上 SLO 仅展示参数写法，需替换为业务要求。整体分位或错误率超过已设置阈值时，工具返回非零退出码，可接入流水线。URI 不写入 JSON 报告；数据库清理失败也会返回错误。

| 参数 | 默认 | 说明 |
| --- | --- | --- |
| `-uri` | 必填 | 测试专用实例；数据库名始终随机生成，不能指定业务数据库 |
| `-seed` | 10000 | 预置文档数，1～100000000 |
| `-concurrency` | 16 | 并发工作者数，1～4096 |
| `-read-percent` | 80 | 按主键读取百分比，其余为原子递增更新 |
| `-payload-bytes` | 256 | 文档载荷，1～1048576 字节；预置分批控制在约 8 MiB 载荷以内 |
| `-warmup` | 5s | 预热，不计入测量，0～1h |
| `-duration` | 30s | 测量时长，100ms～24h；进行稳定性验证时显式加长 |
| `-timeout` | 5s | 单次数据库操作超时 |
| `-max-p95-ms` / `-max-p99-ms` | 0 | 整体分位门槛，0 表示不设置 |
| `-max-error-rate` | 0 | 允许错误率，默认不允许任何错误 |
| `-output` | 空 | 可选 JSON 文件；标准输出始终包含报告 |

报告包含总量、QPS、错误率、读写各自的 P50/P95/P99/最大/平均耗时、采样堆内存峰值、结束堆与 Go Runtime Sys、goroutine 峰值和 GC 次数。延迟按固定大小对数直方图统计，分位为桶上界，桶误差小于约 2.2%，统计内存不随请求数增长。堆指标包含压测生成器自身；Runtime Sys 不是进程 RSS。

该工作负载只覆盖主键随机查询和单文档更新，不含业务索引、复杂过滤、关联聚合或业务事务。按项目数据分布、索引、读写关注级别、目标并发和 SLO 另做预发布验收；建议采集服务端资源、RSS、连接池等待和长时间趋势。持续压测前确认专用环境的磁盘容量和清理方案。
