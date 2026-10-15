# mongo-plus

基于 `go.mongodb.org/mongo-driver/v2` 的 Go 数据访问库。沿用 gorm-plus 的类型安全字段、Repository、可选条件和分页使用习惯，支持 JSON 定义生成、手写 Entity、全局主键配置、逻辑删除、关联查询与链式聚合统计。

数据层不依赖 GORM、gorm-plus、go-utils、Gin 或 go-zero。可选的 go-zero 内嵌模板包含参数校验、统一响应、CORS、静态文件、日志和 Swagger；生成的 HTTP 服务使用 go-zero。当前固定官方驱动 v2.9.2，要求 Go 1.25.5 或更高版本。

## 作为公共包使用

业务项目通过 `github.com/kuangshp/mongo-plus` 导入公共包，在项目根目录放置 `generator.go`，执行 `go run generator.go` 即可生成代码，使用方式与 gorm-plus 一致。推荐使用 YAML 管理生成配置和可选数据源；生成代码不要求安装 CLI，也支持直接在 Go 中构造配置。

发布版本后，在业务项目中安装依赖：

```bash
go get github.com/kuangshp/mongo-plus@latest
```

当前源码尚未发布版本，本地验证可以添加 replace（假设两个项目同级）：

```bash
go mod edit -replace=github.com/kuangshp/mongo-plus=../mongo-plus
go get github.com/kuangshp/mongo-plus@v0.0.0
```

创建下面的 generator.go 后运行 `go run generator.go`，生成后再执行 `go mod tidy`。发布后删除本地 replace 并使用实际版本；业务项目不需要复制本库的实现源码。

## 快速体验

在本项目根目录执行：

```bash
# 默认示例将 mongodb 注释，可先离线生成代码。
make check
make gen

# 需要真实数据库时，在 examples/mongo-plus.yaml 中填写 mongodb 后再执行：
make gen # 这次会先同步集合、索引和校验器。
make example
make statistics-example # 订单分组、金额统计、数组展开。
```

示例继承全局 int64 自增主键；`event.json` 覆盖为 ObjectID，`token.json` 覆盖为手动字符串主键和物理删除。

CLI 是可选工具，主要用于 goctl 模板、Swagger 和显式 schema plan/apply。在本库根目录可执行 `go install ./cmd/mongoplus` 安装当前源码。

## 1. 全局配置一次，实体按需覆盖

### 根目录 generator.go（推荐）

生成配置统一放在 `mongo-plus.yml`（也支持 .yaml），generator.go 只加载配置并调用公共包。

<!-- business-generator -->
```go
//go:build ignore

// 在项目根目录执行 go run generator.go；也可通过 -config 指定 YAML 文件。
package main

import (
    "flag"
    "log"

    mongoplus "github.com/kuangshp/mongo-plus"
)

func main() {
    configFile := flag.String("config", "mongo-plus.yml", "生成器 YAML 配置文件")
    flag.Parse()
    cfg, err := mongoplus.LoadGeneratorConfig(*configFile)
    if err != nil { log.Fatal(err) }
    // 配置了 mongodb 就先同步集合、索引、校验器；省略则只生成文件。
    if err := mongoplus.Generate(cfg); err != nil { log.Fatal(err) }
    log.Print("Entity、DAO、Repository、DTO、VO 生成完成")
}
```

在业务项目根目录创建 `mongo-plus.yml`：

<!-- business-generator-config -->
```yaml
package: example.com/shop # 与业务 go.mod 的 module 一致。
schemaDir: schema
model_pkg_path: internal/dal/entity
out_path: internal/dal/dao
repo_path: internal/dal/repository
dto_path: internal/dal/dto # 可选，留空不生成。
vo_path: internal/dal/vo # 可选，留空不生成。
pluralize: false # false（默认）为 user、sys_user；true 为 users、sys_users。
id:
  type: int64
  generation: increment
softDelete: true
timestamps: true

# 可选数据源：取消注释后，go run generator.go 会先同步数据库再生成文件。
# 不配置 mongodb 时仅生成代码，集合由首次业务插入创建。
# mongodb:
#   uri: "mongodb://127.0.0.1:27017"
#   database: shop
#   maxPoolSize: 100
```

```bash
go run generator.go
go mod tidy

# 使用其他 YAML 文件。
go run generator.go -config application-generator.yml
```

**同一条命令，是否连接数据库由 YAML 的 mongodb 段决定。** 完全不填写时只生成文件；配置完整的 uri、database 时，先创建/同步集合、校验器和索引，再生成文件。只填了一部分、空配置或连接失败会报错停止，不会静默忽略。无需 -sync 参数，也不要求通过环境变量配置；已有部署需要时，uri/database 仍支持 ${变量名} 展开，展开后为空会报错。

pluralize 就是“是否在集合名后面带 s”的全局配置：`false` 或省略表示不追加，`true` 表示追加。单个 JSON 的 collection 可完整覆盖最终集合名。

`//go:build ignore` 保证 generator.go 不参与服务正常构建。YAML 中路径相对该配置文件所在目录。Package、ModelPkgPath、OutPath、RepoPath、DtoPath、VoPath 与 gorm-plus 对应字段含义一致。Go 代码仍可直接构造 GeneratorConfig，MongoDB 同样是可选字段。

本库的 [generator.go](generator.go) 默认读取 [examples/mongo-plus.yaml](examples/mongo-plus.yaml)，用于重新生成仓库内示例；业务项目复制上面的入口即可。

所有实体继承全局 ID、SoftDelete、Timestamps，不用逐个重复配置。未配置 ID 时默认 objectId + auto；SoftDelete、Timestamps 默认 true，可通过 Bool(false) 关闭。

`schema/user.json`：

```json
{
  "comment": "用户",
  "fields": [
    {
      "name": "username",
      "type": "string",
      "comment": "用户名",
      "required": true,
      "unique": true
    },
    {
      "name": "status",
      "type": "int64",
      "comment": "状态：1正常，2禁用"
    }
  ]
}
```

集合名自动从 JSON 文件名推导，不需要每个实体重复配置：

| 文件名 | Go Entity | 默认集合名 | Pluralize: true |
| --- | --- | --- | --- |
| `user.json` | `UserEntity` | `user` | `users` |
| `sys_user.json` | `SysUserEntity` | `sys_user` | `sys_users` |
| `sysUser.json` | `SysUserEntity` | `sys_user` | `sys_users` |

`Pluralize` 是 bool 开关，true 仅在自动推导的名称末尾追加 s，不进行英语单复数变形。上述两个 sysUser 写法是二选一；同时定义会因实体/集合名称冲突而报错。单个实体可用 `"collection": "custom_users"` 完整覆盖集合名，工具不会再对它转下划线或追加后缀；`entity` 仅覆盖 Go 类型名，不改变按文件名推导的集合名。

**命名行为变更：旧版本默认追加 s；现在默认不追加。** 已使用 `users` 等集合的项目应设置 `Pluralize: true`（YAML 为 `pluralize: true`），或逐个明确 collection，避免生成的 Repository 指向新集合。本库示例已显式配置 true，继续使用原有集合。

主键、时间和逻辑删除字段由工具补充，JSON 的 `fields` 不重复定义它们。

单个实体覆盖全局设置：

```json
{
  "comment": "事件记录",
  "id": {"type": "objectId", "generation": "auto"},
  "softDelete": false,
  "fields": [
    {"name": "message", "type": "string", "comment": "内容"}
  ]
}
```

只覆盖 `id.type` 时，生成方式按新类型补全；仅覆盖 `id.generation` 时保留全局类型。`false` 是明确关闭，不会被全局的 `true` 覆盖。

| 主键类型 | 生成方式 | Go 类型与行为 |
| --- | --- | --- |
| `objectId` | `auto` | `bson.ObjectID`，未提供时生成，已有值保留 |
| `int64` | `increment` | `int64`，每次 Create 分配一个新号码，包括重试；不采用实体预填的 ID |
| `int64` | `manual` | 业务提供正整数 ID |
| `objectId` | `manual` | 业务提供非零 ObjectID |
| `string` | `manual` | 业务提供非空字符串 ID |

主键 Go 字段为 `ID`，BSON 字段为 `_id`，JSON 字段为 `id`。生成的 Repository 方法参数与主键类型一致。更换已有集合的主键类型不属于结构自动同步，需要单独迁移。

自增计数存储在当前数据库的 `__mongo_plus_sequences` 集合中，按业务集合分配，使用 majority 写确认和原子 `$inc`，多个进程共享序列。序列独立于业务事务，因此回滚和失败允许跳号，不承诺连续编号。导入已有数字主键的集合时，应先将序列调整至现有最大 ID；不要同时用 manual 和 increment 模式写同一个集合。

## 2. 生成代码与同步数据库

MongoDB 中的“表”称为集合。[MongoDB 支持首次写入时自动创建集合](https://www.mongodb.com/docs/manual/core/databases-and-collections/)。不配置生成器数据源时，生成的 Repository 会在业务服务中使用运行时连接写入，并由 MongoDB 创建集合；首次插入不会自动根据本库 JSON 建立唯一索引或字段校验器。

需要提前创建集合时，在同一份 mongo-plus.yml 中配置：

```yaml
mongodb:
  uri: "mongodb://127.0.0.1:27017"
  database: shop
  maxPoolSize: 100
```

然后仍然执行 `go run generator.go`。集合、索引、校验规则在生成时同步，不需要插入任何业务数据。运行服务时还需通过项目自己的运行配置建立连接并注入 Repository；生成器的连接会在同步结束后关闭。

公共包入口：

| API | 用途 |
| --- | --- |
| `LoadGeneratorConfig(path)` | 从 .yml/.yaml 加载生成配置，数据源可选 |
| `Generate(cfg)` | 从 JSON 生成 Entity/DAO/Repository/DTO/VO；配置 MongoDB 时先同步数据库 |
| `GenerateModels(cfg, models...)` | 从手写 Entity 生成；同样按可选 MongoDB 决定是否先同步 |
| `SyncSchema(ctx, cfg)` / `SyncModels(ctx, cfg, models...)` | 显式单独同步数据库，需要完整数据源 |
| `RunGoctl(ctx, options, args...)` | 调用内嵌 go-zero 模板 |
| `WriteGoctlTemplateHome(dir)` | 导出内嵌模板 |

CLI 与公共包采用相同的可选数据源规则：

```bash
mongoplus check --config mongo-plus.yml
mongoplus generate --config mongo-plus.yml
# 以下两项需要配置 mongodb。
mongoplus schema plan --config mongo-plus.yml
mongoplus schema apply --config mongo-plus.yml
```

旧的 module、output: query 以及 Go 代码直接构造 GeneratorConfig 的方式继续支持。未分别设置输出路径时生成在 query/model、query/dao、query/repository；DTO/VO 仍需指定路径。

数据库命令默认超时 60 秒，可以加 `--timeout 2m`。未知配置项、重复 JSON 键、重复字段、错误类型、索引冲突和保留字段冲突会明确报错。

生成目录：

```text
internal/dal/
├── entity/user_entity.go
├── dao/user_fields_gen.go
├── repository/
│   ├── user_gen.go  # 每次生成更新基础实现。
│   └── user.go      # 首次创建，后续保留业务扩展。
├── dto/user_dto.go  # CreateUserDTO、ModifyUserDTO；首次创建后不覆盖。
└── vo/user_vo.go    # UserVO；首次创建后不覆盖。
```

生成器先渲染所有文件，再写入；同名非生成文件不会被覆盖。Entity 的字段变化通过 JSON 修改后重新生成。删除或重命名整个实体时，旧生成文件需要手动整理，工具不自动删除源文件。

### DTO 与 VO

与 gorm-plus 一样，填写 `DtoPath`、`VoPath` 才生成对应目录，留空时不生成。DTO/VO 是可直接修改的业务初始模板，后续生成保留已有文件；schema 字段变化后，按业务调整 DTO/VO，或备份并删除对应文件后重新生成。不会在重新生成 Entity 时覆盖你的业务校验、字段裁剪等修改。

- `CreateUserDTO` 包含业务字段，自动主键不出现在新增 DTO；手动主键会生成 ID。`ModifyUserDTO` 包含 ID 和全部业务字段，用于完整提交，不表示 PATCH。
- DTO 不包含自动维护的租户、创建时间、更新时间、逻辑删除字段；VO 包含主键、业务字段和查询所需时间、租户字段，省略逻辑删除字段。VO 默认列出其余字段，接口返回前应裁剪密码等敏感信息。
- ObjectID、Decimal128、日期时间在 DTO 中使用 string，分别由业务层调用 `bson.ObjectIDFromHex`、`bson.ParseDecimal128`、`time.ParseInLocation` 转成 Entity 类型；嵌套对象、数组同样生成请求类型。VO 保留 Entity 的 BSON 类型和字段标签，可直接用于 `Scan`、`ScanByPage`；按接口需要自行格式化输出。
- DTO 自带 `UnmarshalJSON`，支持数字和数字字符串兼容，不经过 float64。金额字符串保留原文，生成器不强制两位小数；仍需检查 Decimal128 解析结果和业务精度。
- 初始 `validate` 标签包括必填、ObjectID、时间格式和数组元素校验。必填数字、bool、集合使用指针区分缺失与合法的 `0`、`false`、空集合；必填字符串要求非空，可空字段允许省略或 null。时间格式为 `2006-01-02 15:04:05`。这是常见请求校验初值，可按业务调整，与数据库 required（字段存在）并非完全相同。

```go
var req dto.CreateUserDTO
if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
    // 返回 JSON 解析错误。
    return
}
if err := validation.Validate(&req); err != nil {
    // 返回中文参数校验错误。
    return
}
// 将 req 的业务字段显式映射到 Entity 后调用 Repository.Create；不会自动写入数据库。

var rows []vo.UserVO
total, err := userRepository.WithContext(ctx).
    Where(dao.UserEntity.Status.Eq(1)).
    Order(dao.UserEntity.ID.Desc()).
    ScanByPage(&rows, 1, 20)
```

以上示例按业务项目导入 `internal/dal/dto`、`internal/dal/vo`、`internal/dal/dao`，校验包为 `github.com/kuangshp/mongo-plus/validation`。Gin 直接绑定这些 DTO 后调用同一个 `validation.Validate`，无需再定义一套相同请求结构，详见 [Gin 校验用法](docs/validation.md)。go-zero 的 `.api` 仍是 HTTP 契约来源，goctl 生成的 `types` 不会自动替换为 DTO；在 Logic 中按需映射，避免手动维护两份协议。

`schema apply` 不删除集合、文档、旧字段或现有索引，不重建冲突索引。同步新增字段不会补齐历史文档。字段重命名、类型转换、数据回填、索引替换应编写显式迁移。更新 strict 校验规则后，旧文档的后续写入可能因不符合新规则而失败，先通过 plan 审阅变更并安排迁移。

集合管理和索引操作不是整体原子事务；中途失败时已完成的操作会保留，修复原因后重新执行。再次执行无变化的定义不会重复修改数据库。

业务项目也可以使用简单 Makefile：

```makefile
.PHONY: gen schema-plan schema-apply

gen:
	go run generator.go

schema-plan:
	mongoplus schema plan --config mongo-plus.yaml

schema-apply:
	mongoplus schema apply --config mongo-plus.yaml
```

## 3. 创建与复用 Repository

```go
import (
    "context"
    mongoplus "github.com/kuangshp/mongo-plus"
    "example.com/shop/internal/dal/repository"
)

// 示例复用 YAML 中的数据源；服务也可传入自身运行配置加载的 MongoDB 配置。
cfg, err := mongoplus.LoadGeneratorConfig("mongo-plus.yml")
if err != nil { return err }
db, err := mongoplus.Connect(ctx, cfg.MongoDB)
if err != nil {
    return err
}

userRepository := repository.NewUserRepository(db)
```

数据库包装器和 Repository 在启动时创建并注入业务层，不要每个请求新建 Client。已有官方驱动数据库可用 `mongoplus.NewDatabase(nativeDB)` 包装；关闭连接由拥有该 Client 的启动入口负责。

Gin 和 go-zero 都只需传入请求的 `context.Context`，没有框架绑定。框架的参数校验仍由业务项目负责；MongoDB 校验规则需通过配置了数据源的 Generate、SyncSchema 或 schema apply 建立。

```go
user := &model.UserEntity{Username: "admin", Status: 1}
err := userRepository.Create(ctx, user)
// 成功后自动回填 ID、CreatedAt、UpdatedAt。

user, err = userRepository.FindById(ctx, user.ID)
users, err := userRepository.FindByIdList(ctx, []int64{1, 2, 3})
```

找不到记录使用 `errors.Is(err, mongoplus.ErrNotFound)` 判断；重复索引保持官方驱动错误，可使用 `mongo.IsDuplicateKeyError(err)`。连接、取消、超时等错误沿用驱动语义。

`CreateBatch` 顺序创建；非事务下中途失败，前面的文档可能已写入。需要整批回滚时放进 `Transaction`。它不把分批调用包装成无限大小的内存批处理。

## 4. 条件构造与分页

```go
list, total, err := userRepository.FindPageByWrapper(
    ctx, req.PageNumber, req.PageSize,
    func(w *mongoplus.Wrapper) {
        w.Like(dao.UserEntity.Username, req.Username).
            WhereIf(req.Status != nil, dao.UserEntity.Status.Eq(statusValue)).
            Order(dao.UserEntity.CreatedAt.Desc())
    },
)
```

示例的 `statusValue` 是业务从可选参数取出的值；不要直接对 nil 指针解引用。查询 0 时使用明确的 Where/WhereIf，不用 0 表示“未传”。

也可以直接构造查询选项：

```go
users, err := userRepository.FindList(ctx,
    mongoplus.Query().
        Where(dao.UserEntity.Status.Eq(0)).
        WhereOrGroup(
            dao.UserEntity.Username.Eq("admin"),
            dao.UserEntity.Username.StartsWith("test_"),
        ).
        Order(dao.UserEntity.ID.Desc()).
        Limit(20).
        Build(),
)
```

支持 Eq、Neq、Gt、Gte、Lt、Lte、In、NotIn、Exists、IsNull、AND/OR 分组、Select/Omit、排序、可选条件、BetweenIfNotZero。字符串 Contains/StartsWith 和 Wrapper.Like 按字面量匹配，自动转义正则特殊字符。原生 BSON 使用 RawWhere，只应由可信仓储代码构造，不能直接接收前端操作符对象。

`BetweenIfNotZero` 跳过零边界；业务范围包含 0 时，直接使用 Gte/Lte。每次查询创建新的 Wrapper，不跨 goroutine 复用正在构造的对象。

页码默认 1，每页默认 10、最多 500，自动追加 `_id` 保证同值排序有稳定次序。FindPage 的列表与总数是两次查询，默认不保证同一快照。大偏移分页仍有 MongoDB skip 的成本。

连续读取大量数据时：

```go
page, err := userRepository.FindCursorPage(ctx, nil, 50)
if page.HasMore {
    next, err := userRepository.FindCursorPage(ctx, page.Next, 50)
    _ = next
    _ = err
}
```

该游标固定按 `_id` 升序，支持常规过滤，但不接受自定义排序、投影或 Offset；不额外计算总数。ObjectID 顺序不是严格的业务创建时间顺序。大批量导出可以使用 `Walk(ctx, callback, options...)` 流式处理。

## 5. 零值更新与逻辑删除

```go
result, err := userRepository.UpdateById(ctx, id,
    dao.UserEntity.Status.Set(0),
    dao.UserEntity.Username.Set(""),
)
// result.MatchedCount / ModifiedCount 用于判断实际命中和修改情况。
```

字段 Set 具有编译期类型检查，0、false、空字符串不会被忽略。数值字段支持 Inc；数组字段支持 Push、Pull、AddToSet 和 ElemMatch。数组中多项条件需匹配同一元素时使用 ElemMatch。

不允许普通更新修改 `_id`、启用的租户字段、时间字段和 deleted_at；重复或互相覆盖的父子更新路径会报错。

```go
_, err = userRepository.UpdateByWrapper(ctx,
    func(w *mongoplus.Wrapper) {
        w.Where(dao.UserEntity.Status.Eq(1))
    },
    dao.UserEntity.Status.Set(0),
)

_, err = userRepository.DeleteById(ctx, id) // 默认设置 deleted_at。
_, err = userRepository.RestoreById(ctx, id)
_, err = userRepository.HardDeleteById(ctx, id) // 明确物理删除。
```

逻辑删除默认开启。普通 Find、Count、Exists、更新和删除都过滤已删除文档；Restore 专门处理删除记录。`WithDeleted` 查询全部，`OnlyDeleted` 仅查询删除记录：

```go
user, err := userRepository.FindById(ctx, id,
    mongoplus.Query().WithDeleted().Build(),
)
```

查询和删除遵循同一租户规则。无业务条件的批量更新/删除返回 ErrUnsafeWrite；确需全量操作时明确调用 `w.AllowAll()`，它仍保留租户过滤。

## 6. 索引、类型和嵌套对象

| schema 类型 | Go 类型 |
| --- | --- |
| `string` / `bool` | `string` / `bool` |
| `int32` / `int64` / `float64` | 同名 Go 类型 |
| `objectId` | `bson.ObjectID` |
| `datetime` | `time.Time` |
| `decimal128` | `bson.Decimal128` |
| `bytes` | `[]byte` |
| `json` | `bson.M` |
| `object` | 自动生成命名子结构体 |
| `[]string`、`[]object` 等 | 对应元素类型的切片 |

`nullable: true` 生成指针并允许 BSON null。可选的切片/map/bytes 使用 omitempty，避免 nil 被编码为不符合类型规则的 null。`required: true` 表示 BSON 字段必须存在，不等价于字符串非空；Entity 的零值也不能代表请求是否提供字段，HTTP 请求验证应单独执行。

嵌套对象：

```json
{
  "fields": [
    {
      "name": "address",
      "type": "object",
      "fields": [
        {"name": "city", "type": "string", "comment": "城市"}
      ]
    },
    {"name": "tags", "type": "[]string"}
  ]
}
```

生成 `Address` 对象字段和 `AddressCity` 路径字段；后者对应 BSON 的 `address.city`。金额可用 Decimal128 存储，但精度上限为 34 位有效数字，不能代替任意精度计算库。

`unique: true` 对应整个集合的唯一索引，逻辑删除后仍占用唯一值。可选字段的缺失/null 也遵循 MongoDB 唯一索引规则，不会自动忽略。需要“只约束未删除记录”时，省略字段的 unique，并显式定义：

```json
{
  "fields": [{"name": "username", "type": "string"}],
  "indexes": [
    {
      "name": "uk_active_username",
      "fields": [{"name": "username", "order": 1}],
      "unique": true,
      "activeOnly": true
    }
  ]
}
```

复合索引的 fields 是有序数组，顺序会影响查询能力。还支持 sparse 和单个 datetime 字段的 expireAfterSeconds。TTL 是物理清理机制，与逻辑删除不同。

## 7. 租户与事务

全局配置可选租户字段：

```yaml
tenant:
  name: company_id
  type: int64
```

```go
ctx := mongoplus.WithTenant(request.Context(), companyID)
```

生成器补充 CompanyID，Create 从上下文填充。查询与写入使用“租户 AND 软删除 AND 业务条件”，OR 条件不能覆盖租户约束；缺少租户返回 ErrTenantRequired。WithDeleted/AllowAll 不绕过租户。

需要租户内唯一时，显式建立 company_id 与业务字段的复合唯一索引；字段的 unique 简写仍是整个集合唯一。

```go
err := mongoplus.Transaction(ctx, db, func(txCtx context.Context) error {
    if err := userRepository.Create(txCtx, user); err != nil {
        return err
    }
    return profileRepository.Create(txCtx, profile)
})
```

所有操作必须传 txCtx 并使用同一 Client。事务需要副本集或分片集群，不支持 standalone。驱动可能重试回调，回调中不要发送短信、调用支付等不可重复外部操作；不要并行执行同一 Session 中的操作。支持 TransactionAs 构建项目自己的仓储集合，不提供 SQL savepoint 或跨 MongoDB/MySQL 的原子事务。

## 8. 可选本机缓存、SingleFlight 与监控

```go
user, err := userRepository.FindById(ctx, id,
    mongoplus.Query().WithCache(30*time.Second).Build(),
)

users, err := userRepository.FindList(ctx,
    mongoplus.Query().Where(dao.UserEntity.Status.Eq(1)).
        WithSingleFlight().Build(),
)
```

默认关闭读缓存。WithCache 自动合并相同在途查询；WithSingleFlight 只合并请求，不保存结果。缓存键包含租户、过滤、投影、排序和分页，不需要手写 Args。读出对象分别反序列化，调用方修改对象不会污染缓存。

缓存属于一个复用的 Database 包装器，最多保留 1024 条结果，使用 TTL。仓储写入自动按集合失效，并阻止旧的在途读取把失效数据重新写回缓存。Transaction 内跳过读缓存，在事务结束后失效涉及的集合。驱动外部 Session 写入无法获知提交时刻，会关闭当前包装器的本机缓存；需要缓存的事务请使用本库 Transaction。

本机缓存不提供多进程或外部写入的一致性保证。多个服务实例或外部程序写同一个集合时，保持缓存关闭，或接受所设 TTL 的陈旧窗口。本版没有内置 Redis 缓存后端。通过 Native 写入后，需要调用 `db.InvalidateCache(collection)`；原生写入也需要自行处理租户、审计和逻辑删除。

SingleFlight 的数据库查询使用发起者 Context；发起者取消可能使同组调用者收到错误，其他调用者自己的取消会立即结束等待。

```go
db.Observer = func(ctx context.Context, event mongoplus.Observation) {
    if event.Duration > time.Second || event.Err != nil {
        log.Printf("%s.%s duration=%s err=%v",
            event.Collection, event.Operation, event.Duration, event.Err)
    }
}
```

在启动时设置 Observer，不在并发请求中更换。Create、FindOne、FindList、Update、Delete 会报告耗时与错误，不默认记录请求文档或连接密钥。

## 9. 手写 Entity 路线

已有 Model 时无需再维护 JSON，复用同一份 mongo-plus.yml，通过 GeneratorModel 注册模型：

```go
//go:build ignore

package main

import (
    "log"

    "example.com/shop/internal/dal/entity"
    mongoplus "github.com/kuangshp/mongo-plus"
)

func main() {
    cfg, err := mongoplus.LoadGeneratorConfig("mongo-plus.yml")
    if err != nil { log.Fatal(err) }
    if err := mongoplus.GenerateModels(cfg,
        mongoplus.GeneratorModel{Value: entity.UserEntity{}},
    ); err != nil { log.Fatal(err) }
}
```

执行 `go run generator.go`。YAML 中配置了 mongodb 就先同步数据库；省略则只生成 DAO、Repository 和可选 DTO/VO，不复制或覆盖手写 Entity。主键 Go 类型需匹配全局规则，特殊模型通过 GeneratorModel.ID 覆盖。

手写模型显式声明 `ID` 和 `bson:"_id"`；启用时间或逻辑删除时，同时声明对应 time.Time 和 *time.Time 字段。`mongoplus:"required,unique"`、`comment:"用户名"` 可提供额外元数据。参考 [手写模型示例](examples/manual/model/document.go)。本版显式报错的特殊结构包括 bson inline、递归结构、自定义外部命名类型与指针数组元素，避免静默生成错误类型。

手写 Entity 自动推导的集合名同样遵循转下划线加 Pluralize 的规则；GeneratorModel.Collection 可完整覆盖。需要只同步不生成时，可以单独调用 SyncModels；需要预览变更时，可通过 schema.FromModels 与 schema.Plan 取得计划。

## 10. 关联查询：Lookup、LeftJoin、InnerJoin

常见关联查询无需手写 `$lookup` 管道。从生成的 Repository 开始，使用 `WithContext(ctx)`、Join、Where 到 Scan 的完整链式调用：

```go
// UserDepartmentVO 是业务自己的 VO；嵌入的 Entity 必须为导出类型。
type UserDepartmentVO struct {
    model.UserEntity `bson:",inline"`
    Department *model.DepartmentEntity `bson:"department" json:"department"`
}

query := userRepository.WithContext(ctx).
    LeftJoin(departmentRepository,
        dao.UserEntity.DeptID,
        dao.DepartmentEntity.ID,
        "department",
    ).
    Where(dao.UserEntity.Status.Eq(1)).
    Order(dao.UserEntity.ID.Desc())

var rows []UserDepartmentVO
// page 是页码（从 1 开始），size 是每页条数；total 是匹配总数。
total, err := query.ScanByPage(&rows, 1, 20)
// 不分页：err := query.Scan(&rows)
// 单条：var row UserDepartmentVO; err := query.ScanOne(&row)
// ScanOne 无匹配时返回 mongoplus.ErrNotFound。
```

也可以直接将执行方法接在末尾，无需定义 query 变量：

```go
var rows []UserDepartmentVO
total, err := userRepository.WithContext(ctx).
    LeftJoin(departmentRepository, dao.UserEntity.DeptID, dao.DepartmentEntity.ID, "department").
    Where(dao.UserEntity.Status.Eq(1)).
    Order(dao.UserEntity.ID.Desc()).
    ScanByPage(&rows, 1, 20)
```

每次 `Repository.WithContext(ctx)` 创建独立查询，不改变仓储配置；不调用 Join 时，也能直接 `Where(...).Scan(&rows)` 查询主集合。`Scan`、`ScanByPage` 接收非 nil 切片指针（支持 `[]VO` 和 `[]*VO`），`ScanOne` 接收结构体或 map 指针。已有 `mongoplus.From(repository)` 和 `mongoplus.Scan[VO](ctx, query)` 等泛型函数保留；链式执行必须显式绑定上下文，避免丢失租户、取消信号或事务。

例子中的部门字段来自 `schema/department.json`，用户在 JSON 中增加普通的 `dept_id: int64` 字段即可，不要求额外声明复杂的关系元数据。仓库已提供可运行的 [关联示例](examples/main.go)。

| 方法 | 返回形状与行数 |
| --- | --- |
| `Lookup` | 关联结果为数组，一条主记录仍是一行，适合一对多 |
| `LeftJoin` | 关联结果展开为对象，没有匹配也保留主记录；匹配多条则产生多行 |
| `InnerJoin` | 关联结果展开为对象，仅保留有匹配的记录 |

主键/外键支持 int64、ObjectID、string，以及引用数组；字段的实际 BSON 类型必须兼容，不会自动把字符串变成 ObjectID。缺失、null 和空引用数组不关联空键记录。关系为一对一时，建议为外键建立适当的唯一索引。

### 关联字段过滤、投影与多集合串联

使用 `At("别名")` 为关联字段添加路径：

```go
query := userRepository.WithContext(ctx).
    LeftJoin(departmentRepository, dao.UserEntity.DeptID, dao.DepartmentEntity.ID, "department").
    Where(dao.DepartmentEntity.Name.At("department").Contains("研发")).
    Order(dao.DepartmentEntity.Name.At("department").Asc())
```

Where 按调用位置执行：过滤主记录可放在 Join 前；过滤关联结果应放在 Join 后。对左关联结果添加部门名称过滤后，没有匹配部门的记录也会被该过滤排除。

一次查询可连续调用多个 Lookup/LeftJoin/InnerJoin。后续关联的本地字段可以来自前一个结果，例如 `dao.DepartmentEntity.ManagerID.At("department")`。每个关联使用不同别名，不能覆盖主集合已有字段；只允许同一 Client、同一数据库的仓储，不会静默查询另一个数据库里的同名集合。

想返回扁平结构时使用 Project：

```go
type UserDepartmentRow struct {
    Username string `bson:"username" json:"username"`
    DepartmentName string `bson:"departmentName" json:"departmentName"`
}

query := userRepository.WithContext(ctx).
    LeftJoin(departmentRepository, dao.UserEntity.DeptID, dao.DepartmentEntity.ID, "department").
    Project(bson.D{
        {Key: "_id", Value: 0},
        {Key: "username", Value: "$username"},
        {Key: "departmentName", Value: "$department.name"},
    })
var rows []UserDepartmentRow
err := query.Scan(&rows)
```

排序使用实际 BSON 字段路径（关联字段通过 At 添加别名），不是 Project 新生成的输出名称。也支持 Select 指定普通/关联字段，与 Project 互斥。Project 是可信业务代码中的聚合表达式，不接收前端直接传来的操作符对象。结果字段使用 BSON 标签映射到 VO；没有左关联结果时，嵌套对象推荐用指针表示。

### 关联集合的条件、逻辑删除和租户

Join 最后的查询选项只作用于关联集合：

```go
query := userRepository.WithContext(ctx).
    Lookup(roleRepository, dao.UserEntity.ID, dao.RoleEntity.UserID, "roles",
        mongoplus.Query().
            Where(dao.RoleEntity.Status.Eq(1)).
            Order(dao.RoleEntity.ID.Desc()).
            Limit(5).
            Build(),
    )
```

这里的 Limit 是“每个用户最多 5 个角色”，而非整个查询共 5 个角色。关联过滤、排序、Offset、Limit、Select/Omit 都在关联子管道中执行。Lookup 和 Join 的配置来自各自 Repository：

- 主集合和每个关联集合分别注入租户和逻辑删除条件。
- 业务 OR 条件不能覆盖这些过滤；缺少任意受保护集合需要的租户信息即返回 ErrTenantRequired。
- `repository.WithContext(ctx).WithDeleted()` 只影响主集合。
- 明确需要关联删除记录时，在 Join 的最后参数中传入 `Query().WithDeleted().Build()`；租户过滤仍有效。
- 关联集合本来没有启用逻辑删除时，不会强行添加 deleted_at 过滤。
- 关联查询不读取单集合缓存。From/Join 传入 WithCache 或 WithSingleFlight 会明确报错，防止关联集合更新后返回旧结果。

### 关联后分页与事务

ScanByPage 在关联和过滤完成后，用同一聚合流 `$facet` 生成当前页和总数。Lookup 数组形式的 total 是主记录行数；LeftJoin/InnerJoin 一对多展开后的 total 是最终行数。想按主记录分页并返回子列表时，使用 Lookup。

分页自动补充主集合 `_id`、各展开关联的 `_id` 作为稳定排序键；不要在展开关联的子投影中排除 `_id`。空结果和超出末页返回空切片，超出末页仍返回实际 total。使用 ScanByPage 时不要再设置最终查询的 Limit/Offset；普通 Scan 可以设置它们。

本版关联查询要求 MongoDB 5.0+，使用官方的等值关联加子管道语法。[官方 Lookup 说明](https://www.mongodb.com/docs/manual/reference/operator/aggregation/lookup/)

关联分页受 MongoDB `$facet` 的 100 MB 阶段内存和最终 16 MiB 文档限制，allowDiskUse 不解除 facet 的内存限制；较大子列表应通过关联 Limit/Select 控制，或改用普通 Scan。一次聚合共用数据流，不承诺并发写入场景下跨集合的一致性快照；需要事务语义时传入本库 Transaction 提供的 txCtx。[官方 Facet 限制](https://www.mongodb.com/docs/manual/reference/operator/aggregation/facet/)

```go
err := mongoplus.Transaction(ctx, db, func(txCtx context.Context) error {
    // 所有关联集合使用同一个 Client，查询直接沿用 txCtx。
    var rows []UserDepartmentVO
    // 已构造的 query 也可 query.WithContext(txCtx).Scan(&rows)。
    return userRepository.WithContext(txCtx).
        LeftJoin(departmentRepository, dao.UserEntity.DeptID, dao.DepartmentEntity.ID, "department").
        Scan(&rows)
})
```

已在 MongoDB 8.0 副本集上验证事务中的关联读取与回滚；分片集合事务的额外限制遵循实际 MongoDB 服务端版本。关联列应建立适合查询的索引；封装本身不能代替索引和文档设计。

### 原生聚合扩展

原有 `mongoplus.Aggregate[Result](ctx, r.Repository, pipeline)` 继续支持只读单集合 Match、Project、Group、Sort、Unwind 等阶段。多集合等值查询使用上面的 Repository 链式构造器。Union、GraphLookup、Out/Merge、Search/GeoNear 等高级管道通过自定义仓储的 Native 显式实现，并处理对应集合的权限和数据生命周期。

接口习惯与 gorm-plus 接近，底层使用 BSON 过滤和 MongoDB 聚合。已有 GORM 条件不能直接传入。本版不提供 SQL、FOR UPDATE 或 GORM 插件兼容层。数据层生成器不自动暴露 HTTP 接口；可选 go-zero 模板根据独立的 .api 文件生成 HTTP 层和 Swagger，见下方说明。

## 11. 聚合统计：分组、金额计算与数组统计

聚合沿用同一条 Repository 调用链，无需为常见统计手写 `$group` 管道。示例的订单 Entity/DAO/Repository 来自 [order.json](examples/schema/order.json)，金额字段定义为 `decimal128`。

### 按字段分组并统计

```go
type CategoryStats struct {
    Category string          `bson:"category" json:"category"`
    Count    int64           `bson:"count" json:"count"`
    Total    bson.Decimal128 `bson:"total" json:"total"`
    Average  bson.Decimal128 `bson:"average" json:"average"`
}

var rows []CategoryStats
total, err := orderRepository.WithContext(ctx).
    Where(dao.OrderEntity.Status.Eq(2)). // 先筛选已支付订单。
    GroupBy(dao.OrderEntity.Category).
    Count("count").                     // 每组订单笔数。
    Sum(dao.OrderEntity.Amount, "total"). // 每组金额合计。
    Avg(dao.OrderEntity.Amount, "average").
    Having(field.NewNumeric[int64]("count").Gte(2)). // 只保留至少两笔的分组。
    Order(dao.OrderEntity.Category.Asc()).
    ScanByPage(&rows, 1, 20)
// total 是筛选后的分组数，既不是订单笔数，也不是当前页条数。
// 不分页时将末尾换成 Scan(&rows)。
```

上述代码使用 `go.mongodb.org/mongo-driver/v2/bson` 和 `github.com/kuangshp/mongo-plus/field`。业务项目中，建议将查询封装在非生成的 Repository 扩展文件中，由业务层调用自定义统计方法。

分组键的缺失值与 null 归为同一组。分组键自动映射为顶层字段，VO 可以直接使用 `bson:"category"`，不必定义嵌套 `_id`；原生 `_id` 分组文档仍保留，用于稳定分页和调试。默认输出名取字段路径最后一段，`_id` 对应 `id`。使用 `As` 可以自定义输出名，也可以同时按多个字段分组：

```go
query := orderRepository.WithContext(ctx).
    GroupBy(dao.OrderEntity.Category, dao.OrderEntity.Status.As("orderStatus")).
    Count("count").
    Sum(dao.OrderEntity.Amount, "total")
// 结果包含 category、orderStatus、count、total。
// 单个字段 GroupBy(dao.OrderEntity.Category).Scan(&rows) 也可用于获取去重分类。
```

分组键与统计字段的输出名不能重复；未知字段、错误类型和无效别名会在发送数据库请求前报错。点路径或关联字段可用 `As("departmentName")` 指定顶层输出名。

### 不分组，统计全部匹配记录

省略 GroupBy 时，首次统计函数会自动创建全局分组；显式 `GroupBy()` 与此等价。

```go
var summary struct {
    Count int64           `bson:"count"`
    Total bson.Decimal128 `bson:"total"`
    Min   bson.Decimal128 `bson:"min"`
    Max   bson.Decimal128 `bson:"max"`
}
err := orderRepository.WithContext(ctx).
    Where(dao.OrderEntity.Status.Eq(2)).
    Count("count").
    Sum(dao.OrderEntity.Amount, "total").
    Min(dao.OrderEntity.Amount, "min").
    Max(dao.OrderEntity.Amount, "max").
    ScanOne(&summary)
```

这里的 `Count("count")` 只是声明统计结果字段，直到 Scan/ScanOne/ScanByPage 才执行。原有 `orderRepository.Count(ctx, opts...)` 仍是直接返回数量的普通计数方法。

无匹配记录时沿用 MongoDB `$group` 的空结果语义：Scan 返回空切片，ScanByPage 返回空切片和 total=0，ScanOne 返回 `mongoplus.ErrNotFound`。不会自动伪造一行零值统计。没有数值参与平均值或样本标准差时可能得到 null，VO 需要区分 null 与 0 时使用指针字段。

### 支持的聚合函数

| 方法 | 含义与结果类型 |
| --- | --- |
| `Count("count")` | 当前组记录数；VO 使用 int64 |
| `Sum(field, "total")` | 求和，数值类型遵循 MongoDB 提升规则；整数通常使用 int64，Decimal128 使用 bson.Decimal128 |
| `Avg(field, "average")` | 平均值；普通数值使用 float64，Decimal128 使用 bson.Decimal128 |
| `Min(field, "min")` / `Max(field, "max")` | 最小值/最大值，可用于数值、日期和其他可比较字段 |
| `First(field, "first")` / `Last(field, "last")` | 输入顺序中首条/末条的字段值，必须先 Sort |
| `Push(field, "values")` | 收集数组，保留重复项；有顺序要求时先 Sort |
| `AddToSet(field, "values")` | 收集去重数组，不保证元素顺序 |
| `StdDevPop(field, "population")` | 总体标准差；VO 使用 float64 或 *float64 |
| `StdDevSamp(field, "sample")` | 样本标准差；有效样本不足两个时为 null |

Sum/Avg/标准差要求模型字段为数值类型，支持可空数值和 Decimal128；数组需要先 Unwind。数字字符串不会自动转成金额。金额请在 JSON 中使用 `decimal128`，创建数据时使用 `bson.ParseDecimal128("12.30")`，不要先经过 float64。

MongoDB 会忽略 Sum/Avg 的非数值输入，空值与数组的具体行为遵循其原生规则；模型与数据库历史数据类型不一致时，应先清理数据。参考官方 [Sum](https://www.mongodb.com/docs/manual/reference/operator/aggregation/sum/)、[Avg](https://www.mongodb.com/docs/manual/reference/operator/aggregation/avg/)、[Group](https://www.mongodb.com/docs/manual/reference/operator/aggregation/group/)。

### 输入排序与结果排序

`Sort` 在调用位置插入排序阶段；`Order` 始终排序最终结果。First/Last 必须先 Sort，为并列值指定主键等稳定排序键：

```go
var rows []struct {
    Category string `bson:"category"`
    FirstID  int64  `bson:"firstID"`
    LastID   int64  `bson:"lastID"`
    IDs      []int64 `bson:"ids"`
}
err := orderRepository.WithContext(ctx).
    Sort(dao.OrderEntity.CreatedAt.Asc(), dao.OrderEntity.ID.Asc()).
    GroupBy(dao.OrderEntity.Category).
    First(dao.OrderEntity.ID, "firstID").
    Last(dao.OrderEntity.ID, "lastID").
    Push(dao.OrderEntity.ID, "ids").
    Order(dao.OrderEntity.Category.Asc()).
    Scan(&rows)
```

Where 按调用位置执行。Having 是分组后筛选的明确写法，另有 HavingIf。统计函数需连续放在 GroupBy 之后、Having 之前。二次分组请再次显式调用 GroupBy，例如先得到分类小计，再 `GroupBy().Sum(field.New[int64]("subtotal"), "grandTotal")`。

### 数组展开后统计

```go
var rows []struct {
    Tag   string `bson:"tag"`
    Count int64  `bson:"count"`
}
err := orderRepository.WithContext(ctx).
    Unwind(dao.OrderEntity.Tags).
    GroupBy(dao.OrderEntity.Tags.As("tag")).
    Count("count").
    Order(field.NewString("tag").Asc()).
    Scan(&rows)
```

该例统计标签出现次数，同一订单的重复标签也计数。若需要“包含该标签的订单数”，先按标签和订单 ID 两个字段分组去重，再按标签二次分组计数。

Unwind 默认丢弃缺失、null 与空数组；`Unwind(dao.OrderEntity.Tags, true)` 保留这些记录。对对象数组先 Unwind，再使用 `field.NewNumeric[int64]("items.quantity")` 等路径统计。AddToSet 如果直接作用于数组字段，会收集整个数组，不会自动展开数组元素。

Unwind 也可单独 Scan 或分页；分页 total 为展开后的记录数。内部数组下标用于稳定分页，输出时自动移除；`__mp_` 前缀保留给内部字段，分组和关联别名不能使用该前缀。

### 关联后统计、隔离与事务

```go
var rows []struct {
    DepartmentName string `bson:"departmentName"`
    Count          int64  `bson:"count"`
}
err := userRepository.WithContext(ctx).
    InnerJoin(departmentRepository, dao.UserEntity.DeptID, dao.DepartmentEntity.ID, "department").
    GroupBy(dao.DepartmentEntity.Name.At("department").As("departmentName")).
    Count("count").
    Scan(&rows)
```

所有关联必须在分组前完成。Lookup 保留数组，一对多统计前可先 Unwind；LeftJoin/InnerJoin 已展开结果，Count/Sum 会按展开后的行统计，注意避免将主记录金额重复累计。

源集合在入口应用租户与逻辑删除条件，关联集合应用各自过滤；分组结果不会再次被错误套用实体的租户字段。WithDeleted/OnlyDeleted、事务 txCtx 与上下文取消继续有效。统计查询绕过单集合缓存，不自动缓存跨集合结果。

分组分页在 GroupBy、Having 后统计 total，并按分组 `_id` 补充稳定排序；仍受上节提到的 facet 内存与文档大小限制。Push/AddToSet 生成大数组时同样需要控制结果规模。

### 运行完整示例与高级管道

```bash
# 先在 examples/mongo-plus.yaml 中填写 mongodb 的 uri、database。
make gen
make statistics-example
```

[完整订单统计示例](examples/statistics/main.go)会创建三笔演示订单，只统计本次生成的 ID，展示全部订单汇总、分类金额分页与标签计数。

当前链式封装覆盖上表常用统计，不代表所有 MongoDB 运算符均已封装。日期表达式、条件计算、Bucket、窗口函数等可在自定义仓储中使用原生聚合；原有 `mongoplus.Aggregate[VO](ctx, r.Repository, pipeline)` 支持白名单内的单集合只读管道。涉及额外集合或写入的原生阶段需通过 Native 显式处理权限、事务与数据生命周期。

## 12. go-zero 模板与中文参数校验

与 gorm-plus 一样，通过 CLI 调用内嵌模板。数据模型与接口协议分开维护：

```bash
# 先在本库安装当前源码的 CLI。
go install ./cmd/mongoplus

# 然后进入业务项目的服务目录。
go run generator.go
mongoplus goctl api go --api shop.api --dir . --style=goZero
go mod tidy

# 显式同步集合/索引，服务启动不会自动迁移数据库。
mongoplus schema plan --config mongo-plus.yaml
mongoplus schema apply --config mongo-plus.yaml
```

`.api` 文件直接声明规则，Handler 自动完成兼容转换 → go-zero 解析 → 中文校验：

```text
type CheckReq {
    Id int64 `json:"id" validate:"required,gt=0"`
    Amount string `json:"amount" validate:"required,decimal"`
    Mobile string `json:"mobile" validate:"required,checkMobile"`
    StartTime string `json:"startTime,optional" validate:"omitempty,dateTime"`
    DocumentID string `json:"documentId,optional" validate:"omitempty,objectId"`
}
```

- `validation` 支持 gorm-plus 现有的 required、min/max、手机、邮箱、日期、金额等规则及中文错误，并增加 objectId 格式校验。
- `decimaljson` 自动兼容数字与字符串，不要求 string 字段添加额外标签；校验规则仍需按业务声明。
- 模板包含统一响应、CORS、静态文件、日志、Swagger；ServiceContext 管理 MongoDB 连接，业务按需注入生成的 Repository。
- MongoConfig 默认关闭，开启后读取 URI/数据库并在启动时 Ping；MongoDB 初始化失败则停止启动。
- 校验和 HTTP 包位于 mongo-plus 自身，不要求业务引入 gorm-plus 或 go-utils。go-zero/Gin 只在对应业务服务中引入。

业务服务目录的 Makefile 可直接复制：

```makefile
.PHONY: gen validate swagger run run-open build

validate:
	goctl api validate --api shop.api

gen: validate
	go run generator.go
	mongoplus goctl api go --api shop.api --dir . --style=goZero

swagger:
	mongoplus swagger --config etc/shop-api.yaml

run: swagger
	go run . -f etc/shop-api.yaml

run-open: swagger
	@mongoplus swagger-open --config etc/shop-api.yaml &
	go run . -f etc/shop-api.yaml

build: swagger
	mkdir -p bin
	go build -o bin/shop .
```

首次先安装 goctl 和与业务依赖一致的 mongoplus；[示例 Makefile](examples/gozero/Makefile) 带有 install-tools，使用本地源码而非尚未发布的 latest。命令行前使用 Tab。开启 SwaggerConfig.Enabled 后，run/build 自动准备并内嵌文档；关闭时准备编译占位文件。

- [完整 go-zero 配置、MongoDB 生命周期与 Repository 注入](docs/gozero.md)
- [参数校验、自定义规则与 Gin 接入](docs/validation.md)
- [Swagger 配置、部署与热重载](docs/swagger.md)
- [独立的 go-zero 示例模块](examples/gozero/README.md)

已有服务重新生成时不会自动覆盖所有旧入口和 Handler，应先对照新版模板合并所需功能，保留业务代码。

## 验证

```bash
make test
make vet
make build

# 真实 goctl 生成、编译并启动 HTTP 服务，以及 Gin 绑定示例。
MONGOPLUS_GOCTL_INTEGRATION=1 MONGOPLUS_GIN_INTEGRATION=1 go test -race ./generator/gozero ./validation

# 真实 MongoDB 副本集；测试创建并清理 mongo_plus_test_<随机ID> 数据库。
export MONGOPLUS_TEST_URI='mongodb://127.0.0.1:27017/?replicaSet=rs0'
make integration
```

未设置测试 URI 时真实数据库测试明确跳过；普通测试仍验证 JSON、配置、查询构造、生成结果编译和扩展保留。真实测试覆盖并发自增、三种主键、租户隔离、零值更新、逻辑删除与恢复、分页、事务回滚/重试、缓存失效、集合校验和索引幂等性；关联测试覆盖左/内关联、数组关联、多个集合串联、关联结果过滤与 VO 投影、主/关联租户隔离、关联逻辑删除、三种 ID 类型、展开后分页总数及事务中的关联读取；链式入口还覆盖生成接口、切片/指针切片结果、单条未找到、分页空结果、上下文取消与事务回滚。聚合测试覆盖计数、求和/均值/极值、Decimal128 精度、标准差、首末值与数组收集、多字段分组、Having、二次分组、数组展开、关联后统计和分组分页。
