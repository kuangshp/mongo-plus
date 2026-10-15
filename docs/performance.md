# 性能验证与生产接入

## 默认行为

- Create 直接将准备好的实体交给官方驱动，沿用现有 Client 的 BSONOptions/Registry。自动字段使用明确的 bson 标签；业务字段可使用已配置的 JSON 标签回退或字段编码器。
- CreateBatch 每块最多 1000 条，使用有序 InsertMany；数字主键每块只访问一次序列表。块内预处理错误不写入该块；数据库写入错误可能产生已确认的成功前缀。已完成的前置块不会自动回滚。
- 单条自增默认每次按需申请。可选 ID.BlockSize 预分配区间；同一 Database 包装器的同集合仓储共享区间。跨包装器/进程由 MongoDB 原子分配互不重复区间；未消费区间可能产生跳号，提交顺序不等于 ID 顺序。预分配不减弱 majority 写确认。
- 查询缓存默认关闭。显式开启时，同时限制结果条数、总保留容量和单条容量；容量是 Go 对象估算，不是 RSS 硬限制。多进程和外部写入不会通知本机缓存失效。
- schema plan/apply 比较校验规则及 strict/error 状态；不会补齐历史数据，也不会自动替换冲突索引。结构同步不是整体事务。

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

## 接入前的业务验证

按预计数据量建立复合索引，并检查主要查询的执行计划。租户、deleted_at、业务过滤与排序应结合考虑；不要只为单个字段机械建索引。深分页使用游标，大规模导出使用 Walk，关联查询对外键建立索引并控制展开数量。

设置请求 Context 超时，复用 Client。事务回调可能重试，回调中的外部副作用需要业务幂等方案。业务应以唯一业务键核对网络错误后的写入结果，不能把重试自动理解为业务“恰好执行一次”。

构建服务时使用受支持且已修复漏洞的 Go 版本。本仓库推荐的 toolchain 不会强制升级使用方主模块的工具链；发布前在业务模块运行 govulncheck。
