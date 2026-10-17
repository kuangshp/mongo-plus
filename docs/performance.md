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
