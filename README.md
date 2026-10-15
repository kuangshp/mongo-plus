# mongo-plus

基于 `go.mongodb.org/mongo-driver/v2` 的 Go 数据访问库。沿用 gorm-plus 的类型安全字段、Repository、可选条件和分页使用习惯，支持 JSON 定义生成、手写 Entity、全局主键配置、逻辑删除、关联查询与链式聚合统计。

从 gorm-plus 切换时，先看[迁移指南](docs/gorm-plus-migration.md)：保留根目录 `generator.go`、生成 Repository 注入、`FindPageByWrapper` 和 `QueryOpt` 的调用习惯，并列出更新返回值、BSON 条件与事务上下文的调整方式。

数据层不依赖 GORM、gorm-plus、go-utils、Gin 或 go-zero。可选的 go-zero 内嵌模板包含参数校验、统一响应、CORS、静态文件、日志和 Swagger；生成的 HTTP 服务使用 go-zero。当前固定官方驱动 v2.9.2，最低要求 Go 1.25.5；本仓库通过 toolchain 使用 Go 1.26.9 安全补丁版本。业务项目也应使用受支持且已修复漏洞的 Go 工具链，依赖库的 toolchain 不会强制升级业务项目的构建环境。

## 作为公共包使用

业务项目通过 `github.com/kuangshp/mongo-plus` 导入公共包，在项目根目录放置 `generator.go`，执行 `go run generator.go` 即可生成代码，使用方式与 gorm-plus 一致。可以直接在 Go 中配置生成目录、主键等参数，也可以将完整生成配置放进 YAML。数据源可选，生成代码不要求安装 CLI。

在业务项目中安装依赖：

```bash
go get github.com/kuangshp/mongo-plus@latest
```

下面两种方式任选一种，业务项目根目录均使用 generator.go 作为入口，执行 `go run generator.go`、`go mod tidy`，再通过生成的 Repository 访问数据库。

## 1. 全局配置一次，实体按需覆盖

### 方式一：直接在 generator.go 配置（常用）

生成目录、主键和逻辑删除等参数直接写在 Go 中；数据源可从项目已有的业务 YAML 读取。下面是完整的 generator.go：

<!-- business-generator-go -->
```go
//go:build ignore

// 在业务项目根目录运行：go run generator.go。
package main

import (
    "flag"
    "log"
    "os"

    mongoplus "github.com/kuangshp/mongo-plus"
    "gopkg.in/yaml.v3"
)

func main() {
    configFile := flag.String("config", "", "可选：包含 mongodb 数据源的业务 YAML")
    flag.Parse()
    var mongoConfig mongoplus.Config
    if *configFile != "" {
        data, err := os.ReadFile(*configFile)
        if err != nil { log.Fatal(err) }
        var app struct {
            MongoDB *mongoplus.Config `yaml:"mongodb"`
        }
        if err := yaml.Unmarshal(data, &app); err != nil { log.Fatal(err) }
        if app.MongoDB != nil {
            if app.MongoDB.URI == "" || app.MongoDB.Database == "" {
                log.Fatal("mongodb 的 uri、database 不能为空")
            }
            mongoConfig = *app.MongoDB
        }
    }
    cfg := &mongoplus.GeneratorConfig{
        Package:      "example.com/shop",        // 与业务 go.mod 的 module 一致。
        SchemaDir:    "schema",                  // JSON 实体定义目录。
        ModelPkgPath: "internal/dal/entity",     // Entity 输出目录。
        OutPath:      "internal/dal/dao",        // DAO 输出目录。
        RepoPath:     "internal/dal/repository", // Repository 输出目录。
        DtoPath:      "internal/dal/dto",        // DTO 首次创建后不覆盖。
        VoPath:       "internal/dal/vo",         // VO 首次创建后不覆盖。
        Pluralize:    false,                     // true：users、sys_users；false：user、sys_user。
        MongoDB: mongoplus.Config{               // 可选；完整填写则在生成时同步数据库，省略或全零则仅生成文件。
            URI:         mongoConfig.URI,         // 读取 YAML mongodb.uri，如 mongodb://127.0.0.1:27017。
            Database:    mongoConfig.Database,    // 读取 YAML mongodb.database，如 shop；这是数据库名。
            MaxPoolSize: mongoConfig.MaxPoolSize, // 读取 YAML mongodb.maxPoolSize，如 100；0 使用驱动默认值。
            Timeout:     mongoConfig.Timeout,     // 可选的 Go time.Duration；0 不设置驱动操作超时。
        },
        ID: mongoplus.IDConfig{                  // 全局主键，单个实体可以覆盖。
            Type: mongoplus.IDTypeInt64,          // 只配置类型，自动使用 int64 自增主键。
        },
        SoftDelete: mongoplus.Bool(true),        // 启用逻辑删除，false 明确关闭。
        Timestamps: mongoplus.Bool(true),        // 自动维护 CreatedAt、UpdatedAt。
    }
    // MongoDB 已配置：先同步集合、索引、校验器，再生成文件；未配置：只生成文件。
    if err := mongoplus.Generate(cfg); err != nil { log.Fatal(err) }
}
```

业务配置示例 application.dev.yml（可包含项目的其他配置段）：

```yaml
mongodb:
  uri: "mongodb://127.0.0.1:27017"
  database: shop
  maxPoolSize: 100
```

```bash
# 直接生成文件，不要求存在 YAML 数据源。
go run generator.go

# 从业务 YAML 读取数据源，先创建/同步集合，再生成文件。
go run generator.go -config application.dev.yml
go mod tidy
```

项目已有配置加载器时，可以替换示例中的 YAML 读取部分。上方 MongoDB 已展开全部连接字段；URI、Database、MaxPoolSize 来自业务 YAML，Timeout 仅供 Go 代码设置（例如 `10 * time.Second`，需导入 time），不直接从 YAML 读取。

如果直接在 Go 中填写数据源，可将上面的 MongoDB 配置替换为：

```go
MongoDB: mongoplus.Config{
    URI:         "mongodb://127.0.0.1:27017", // MongoDB 连接地址；认证、副本集、TLS 参数也通过 URI 配置。
    Database:    "shop",                      // 数据库名，集合名由 JSON 文件名或 collection 决定。
    MaxPoolSize: 100,                         // 可选，连接池最大连接数；0 使用驱动默认值。
    Timeout:     0,                           // 可选，Go time.Duration；例如 10 * time.Second。
},
```

不需要在生成阶段同步数据库时，省略整个 MongoDB 字段。Go 中的相对输出路径以运行命令的业务项目根目录为准。

### 方式二：完整生成配置放在 YAML

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
  type: int64 # 只配置类型，默认自增；无需再写 generation。
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

所有实体继承全局 ID、SoftDelete、Timestamps，不用逐个重复配置。未配置 ID 时默认 objectId + auto；SoftDelete、Timestamps 默认 true，可通过 Bool(false) 关闭。

主键通常只配置 Type，Generation 会自动关联：

| Go 主键类型 | YAML/JSON type | 默认生成方式 |
| --- | --- | --- |
| mongoplus.IDTypeObjectID | objectId | auto：自动生成 ObjectID |
| mongoplus.IDTypeInt64 | int64 | increment：自动分配自增数字 |
| mongoplus.IDTypeString | string | manual：由业务提供字符串主键 |

例如 `ID: mongoplus.IDConfig{Type: mongoplus.IDTypeInt64}` 即可开启自增主键。整个 ID 配置省略时，默认自动生成 ObjectID。单个 JSON 实体也只需写 `"id": {"type": "objectId"}`；覆盖为不同类型时，生成方式会按新类型重新推导。

需要覆盖默认生成方式时，才填写 Generation，例如 int64 主键改为由业务提供：

```go
ID: mongoplus.IDConfig{
    Type:       mongoplus.IDTypeInt64,
    Generation: mongoplus.IDGenerationManual, // 关闭自增，创建文档时由业务传入 ID。
},
```

YAML/JSON 对应填写 `type: int64`、`generation: manual`。Go 配置可用常量如下：

| 配置字段 | 可用常量 | YAML/JSON 值 |
| --- | --- | --- |
| ID.Type | mongoplus.IDTypeObjectID | objectId |
| ID.Type | mongoplus.IDTypeInt64 | int64 |
| ID.Type | mongoplus.IDTypeString | string |
| ID.Generation | mongoplus.IDGenerationAuto | auto |
| ID.Generation | mongoplus.IDGenerationIncrement | increment |
| ID.Generation | mongoplus.IDGenerationManual | manual |

YAML/JSON 仍使用字符串，不写 Go 常量名称。auto 仅用于 ObjectID，increment 仅用于 int64，manual 支持三种主键类型；非法值和不支持的组合会在生成或创建 Repository 时返回错误。


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

主键、时间和逻辑删除字段由工具补充，JSON 的 `fields` 不重复定义它们。

单个实体覆盖全局设置：

```json
{
  "comment": "事件记录",
  "id": {"type": "objectId"},
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

自增计数存储在当前数据库的 `__mongo_plus_sequences` 集合中，按业务集合分配，使用 majority 写确认和原子 `$inc`，多个进程共享序列。序列独立于业务事务，因此回滚和失败允许跳号，不承诺连续编号。批量创建按块一次申请多个号码，避免每条额外访问序列表。导入已有数字主键的集合时，应先将序列调整至现有最大 ID；不要同时用 manual 和 increment 模式写同一个集合。

单条高频写入可选开启号码预分配；默认仍按需申请，不改变常用 Type 配置：

```go
ID: mongoplus.IDConfig{
    Type:      mongoplus.IDTypeInt64,
    BlockSize: 100, // 可选；同一 Database 包装器下，同集合的仓储共享号码区间。
},
```

YAML/JSON 对应 `blockSize: 100`。范围为 0～1000000，仅用于 int64 自增；0/1 表示按需申请，单个实体可用 1 关闭继承的预分配。不同类型/生成方式的实体覆盖会清除继承的区间设置。预分配保证号码唯一，不保证多实例之间按请求或提交顺序递增；重启、回滚和切换包装器可能跳过未用号码。需要严格按分配请求逐次递增时保持默认配置。


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

Entity、DAO、Repository、DTO、VO 的模板在 `generator/template`，生成时内嵌渲染，目录组织与 gorm-plus 的 `generator/template` 相同。

两个 Repository 模板也沿用 gorm-plus 的职责划分：`repository_gen_template.txt` 显式生成 default 结构体、iDefault 接口、基础方法和底部构造函数；`repository_template.txt` 生成 customer 结构体、对外接口、接口检查及公开构造函数。基础读写统一委托给公共 MongoDB 执行器。自定义方法写在 customer 结构体上，并在对外接口中声明；覆盖基础方法时保持相同签名。

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
- DTO 同时生成相同规则的 `validate` 和 `binding` 标签，分别供普通 Go/go-zero 和 Gin 使用。初始规则包括必填、ObjectID、时间格式和数组元素校验。必填数字、bool、集合使用指针区分缺失与合法的 `0`、`false`、空集合；必填字符串要求非空，可空字段允许省略或 null。时间格式为 `2006-01-02 15:04:05`。这是常见请求校验初值，可按业务调整，与数据库 required（字段存在）并非完全相同。

#### Gin 控制器示例

参考 gof_cos_ser 的接口、实现结构体及统一响应写法。先按 [Gin 校验初始化](docs/validation.md#gin统一初始化保留现有-handler) 配置 Gin 的 binding 引擎和中文翻译，再使用下面的控制器；utils 是业务项目自己的响应和校验适配器。

<!-- gin-dto-controller -->
```go
package user

import (
    "example.com/shop/internal/dal/dto"
    "example.com/shop/internal/dal/entity"
    "example.com/shop/internal/dal/repository"
    "example.com/shop/internal/dal/vo"
    "example.com/shop/utils"

    "github.com/gin-gonic/gin"
)

type IUser interface {
    Create(ctx *gin.Context)
}

type user struct {
    userRepository repository.UserRepository
}

func NewUser(userRepository repository.UserRepository) IUser {
    return &user{userRepository: userRepository}
}

func (u *user) Create(ctx *gin.Context) {
    var req dto.CreateUserDTO
    if err := ctx.ShouldBindJSON(&req); err != nil {
        utils.Fail(ctx, utils.ShowErrorMessage(err))
        return
    }
    // ShouldBindJSON 已按 binding 标签完成校验，不再调用 validation.Validate。
    record := &entity.UserEntity{Username: req.Username, Status: req.Status}
    if err := u.userRepository.Create(ctx.Request.Context(), record); err != nil {
        utils.Fail(ctx, "创建用户失败")
        return
    }
    utils.Success(ctx, vo.UserVO{
        ID: record.ID, Username: record.Username, Status: record.Status,
        CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt,
    })
}
```

在路由装配时构造一次控制器并注册方法，例如 `userAPI := user.NewUser(userRepository)`，再 `router.POST("/users", userAPI.Create)`。Repository 使用启动阶段建立的 MongoDB 连接，并通过构造函数注入；HTTP 层不另建数据库连接。

ShouldBindJSON 会调用 DTO 自带的 UnmarshalJSON，数字与数字字符串兼容仍然生效；随后由 Gin 按 binding 标签执行一次校验。ObjectID、日期等自定义规则及中文翻译必须在启动 HTTP 服务前注册，详见 [完整初始化及错误适配](docs/validation.md)。

**已有 DTO 不会被重新生成覆盖。** 如已有文件仅包含 validate 标签，应补上内容相同的 binding 标签，或备份业务修改后重新生成；仅修改 Handler 不会让 Gin 自动识别 validate 标签。不必更改 Gin 全局校验标签，以便原有 binding DTO 继续使用。

#### 查询结果填充 VO

在 Repository 业务扩展中封装查询，并从控制器传入 ctx.Request.Context()。查询结果可直接填入 VO：

```go
var rows []vo.UserVO
total, err := userRepository.WithContext(ctx).
    Where(dao.UserEntity.Status.Eq(1)).
    Order(dao.UserEntity.ID.Desc()).
    ScanByPage(&rows, 1, 20)
```

以上查询示例中的 ctx 为传入 Repository 的 context.Context。普通 Go/go-zero 仍可使用 validation.Validate 校验 validate 标签；go-zero 的 .api 是 HTTP 契约来源，goctl 的 types 不会自动替换为 DTO，在 Logic 中按需映射。

`schema apply` 不删除集合、文档、旧字段或现有索引，不重建冲突索引。同步新增字段不会补齐历史文档。字段重命名、类型转换、数据回填、索引替换应编写显式迁移。更新 strict 校验规则后，旧文档的后续写入可能因不符合新规则而失败，先通过 plan 审阅变更并安排迁移。

集合管理和索引操作不是整体原子事务；中途失败时已完成的操作会保留，修复原因后重新执行。再次执行无变化的定义不会重复修改数据库。同步也会检查 validationLevel/validationAction；已有集合若被改为 off、moderate 或 warn，会恢复为 strict/error。

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

mongoplus.Use(db)
userRepository := repository.NewUserRepository()
// 需要显式注入时：repository.NewUserRepository(db)，传入连接优先于 Use。
```

数据库包装器和 Repository 在启动时创建并注入业务层，不要每个请求新建 Client。已有官方驱动数据库可用 `mongoplus.NewDatabase(nativeDB)` 包装；创建文档直接使用该 Client 的 BSONOptions/Registry，兼容 UseJSONStructTags 和自定义字段编码器，无需重复配置。自动管理字段（ID、租户、时间、逻辑删除、审计）应使用明确的 bson 标签。关闭连接由拥有该 Client 的启动入口负责。

Gin 和 go-zero 都只需传入请求的 `context.Context`，没有框架绑定。框架的参数校验仍由业务项目负责；MongoDB 校验规则需通过配置了数据源的 Generate、SyncSchema 或 schema apply 建立。

Gin 项目如果沿用 gorm-plus 中直接传 `*gin.Context` 的写法，可在启动时通过 `RegisterCtxResolver` 将它解析为 `c.Request.Context()`，注册示例见[迁移指南](docs/gorm-plus-migration.md#gin-context)。

```go
user := &model.UserEntity{Username: "admin", Status: 1}
err := userRepository.Create(ctx, user)
// 成功后自动回填 ID、CreatedAt、UpdatedAt。

user, err = userRepository.FindById(ctx, user.ID)
users, err := userRepository.FindByIdList(ctx, []int64{1, 2, 3})
```

找不到记录使用 `errors.Is(err, mongoplus.ErrNotFound)` 判断；重复索引保持官方驱动错误，可使用 `mongo.IsDuplicateKeyError(err)`。连接、取消、超时等错误沿用驱动语义。

`CreateBatch` 使用有序 InsertMany，每块最多 1000 条；自增主键每块只申请一次号码区间。每块先检查实体和自动字段，再交给原生驱动编码，成功后回填实体。非事务下失败时，前面的文档可能已写入，只回填已确认成功的前缀，后续文档不再插入；错误链保留驱动错误，唯一索引冲突仍可用 mongo.IsDuplicateKeyError 判断。网络中断或写确认失败时，当前块的写入结果可能不确定，不回填该块；应按业务幂等键核对结果。需要整批回滚时使用 Transaction。w=0 未确认写入返回 ErrUnacknowledgedWrite。

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

`BetweenIfNotZero` 与 gorm-plus 一致：任一边界为零值时跳过整个范围，两端均非零时包含两端。时间参数应先校验成对输入并解析为 `time.Time`；单边范围或包含 0 的范围需显式使用 Gte/Lte。每次查询创建新的 Wrapper，不跨 goroutine 复用正在构造的对象。

Wrapper 同时支持 `LLikeIf/RLikeIf`、`OrLike/OrLLike/OrRLike` 及各自的 `If` 方法、`OrGroup/OrGroupIf`、`WhereGroupFn/OrGroupFn`、`OrderIf/OrderTriState/OrderDefault`。`OrWhereIf(true, a, b)` 逐个追加 OR 条件；需要 `OR (a AND b)` 时使用 `OrGroup(a, b)`。回调分组只贡献过滤条件，空回调跳过。`OrderDefault` 仅在没有显式排序时生效，显式排序无论先后调用均优先。`QueryOpt` 与 `Query` 使用同一构造器，`WithUnscoped` 与 `WithDeleted` 等价。

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
_, err = userRepository.DeleteByIdList(ctx, []int64{1, 2, 3})
_, err = userRepository.RestoreById(ctx, id)
_, err = userRepository.HardDeleteById(ctx, id) // 明确物理删除。
// 也可沿用 gorm-plus 的删除选项写法。
_, err = userRepository.DeleteById(ctx, id,
    mongoplus.Delete().WithPhysicalDelete().Build(),
)
```

逻辑删除默认开启。普通 Find、Count、Exists、更新和删除都过滤已删除文档；Restore 专门处理删除记录。`WithDeleted` 查询全部，`OnlyDeleted` 仅查询删除记录：

```go
user, err := userRepository.FindById(ctx, id,
    mongoplus.Query().WithDeleted().Build(),
)
```

查询和删除遵循同一租户规则。无业务条件的批量更新/删除返回 ErrUnsafeWrite；确需全量操作时明确调用 `w.AllowAll()`，它仍保留租户过滤。

`DeleteByIdList` 的空列表返回 `(0, nil)`，任一无效主键会在写入前报错。`DeleteByCondition(ctx, conditions...)` 将条件按 AND 合并并遵循逻辑删除配置；物理删除选项适用于 `DeleteById`、`DeleteByIdList` 和 `DeleteByWrapper`，也可以直接传 `mongoplus.WithPhysicalDelete()`。物理删除仍受租户约束，且可命中已软删除记录。所有 Wrapper 回调均允许 nil；无条件写入仍会被拦截。`CountByWrapper` 和 `ExistsByWrapper` 与 gorm-plus 一样接受额外的 `QueryOption`。

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

默认关闭读缓存。WithCache 自动合并相同在途查询；WithSingleFlight 只合并请求，不保存结果。缓存键包含租户、过滤、投影、排序和分页，不需要手写 Args。返回已解码 Go 值的独立深拷贝，调用方修改嵌套切片、map、指针不会污染缓存，也不会二次使用默认 BSON 编解码器。无法安全复制的自定义私有引用类型和循环引用绕过共享，不改变正常查询结果。

每个 Database 包装器独立保存查询结果，默认最多保留 1024 条结果、约 64 MiB 保留对象，单条最多约 1 MiB，使用 TTL。超出单条限制仍正常返回，只是不进入缓存；总容量不足时清理过期结果并淘汰已有结果。容量按 Go 对象保守估算，不包含在途查询、驱动缓冲区和运行时额外开销，不是进程内存的硬上限。同一个 Client、同一个数据库的所有包装器共享集合失效通知，写操作返回前同步清除其他包装器中的相关缓存，并阻止旧的在途读取重新填充。结果、解码配置和容量配置不跨包装器共享。Transaction 内跳过读缓存，在事务结束后通知相关包装器失效。外部 Session 写入无法获知提交时刻，会关闭同 Client 同库包装器的本机缓存；需要缓存的事务请使用本库 Transaction。

不同 Client、不同进程或外部程序写入不会自动广播失效。要求这些场景实时读取数据库时，在启动时调用 `db.ConfigureCache(mongoplus.CacheConfig{Disabled: true})`：即使业务代码传入 WithCache 或 WithSingleFlight，也始终直读数据库，不共享在途结果。所有业务 Database 包装器均应配置该选项；省略时保留本机 TTL 缓存的原有行为。本版没有内置分布式缓存后端。通过 Native 写入后，需要调用 `db.InvalidateCache(collection)` 通知同 Client 同库的包装器；原生写入也需要自行处理租户、审计和逻辑删除。

可在启动时调整缓存容量，缓存仍由每次查询的 WithCache 显式开启：

```go
err := db.ConfigureCache(mongoplus.CacheConfig{
    MaxEntries:    1024,
    MaxBytes:      64 << 20, // 总容量约 64 MiB。
    MaxEntryBytes: 1 << 20,  // 单条约 1 MiB，避免缓存整个大集合。
})
```

SingleFlight 的数据库查询使用发起者 Context；发起者取消可能使同组调用者收到错误，其他调用者自己的取消会立即结束等待。

```go
db.Observer = func(ctx context.Context, event mongoplus.Observation) {
    if event.Duration > time.Second || event.Err != nil {
        log.Printf("%s.%s duration=%s err=%v",
            event.Collection, event.Operation, event.Duration, event.Err)
    }
}
```

在启动时设置 Observer，不在并发请求中更换。Create、CreateBatch、FindOne、FindList、Update、Delete 会报告耗时与错误，不默认记录请求文档或连接密钥。

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

ScanByPage 在关联和过滤完成后，先执行计数聚合，再通过独立列表聚合游标读取当前页。整页不再装入单个 `$facet` 结果文档，避免 facet 的 100 MB 内存限制和整页 16 MiB 文档限制。Lookup 数组形式的 total 是主记录行数；LeftJoin/InnerJoin 一对多展开后的 total 是最终行数。想按主记录分页并返回子列表时，使用 Lookup。

分页自动补充主集合 `_id`、各展开关联的 `_id` 作为稳定排序键；不要在展开关联的子投影中排除 `_id`。空结果和超出末页返回空切片，超出末页仍返回实际 total。使用 ScanByPage 时不要再设置最终查询的 Limit/Offset；普通 Scan 可以设置它们。

本版关联查询要求 MongoDB 5.0+，使用官方的等值关联加子管道语法。[官方 Lookup 说明](https://www.mongodb.com/docs/manual/reference/operator/aggregation/lookup/)

计数和列表均开启 allowDiskUse，但两次查询默认不保证来自同一快照；并发写入时 total 与列表可能有差异。需要一致快照时，给 `Transaction` 或 `TransactionAs` 的最后参数传入 `options.Transaction().SetReadConcern(readconcern.Snapshot())`，查询都使用 txCtx。参数类型来自官方驱动的 `mongo/options` 和 `mongo/readconcern`。单条输出文档仍受 MongoDB 的 16 MiB 限制，Lookup/Push 生成的大数组仍应通过 Limit/Select 或业务范围控制。

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

分组分页在 GroupBy、Having 后统计 total，并按分组 `_id` 补充稳定排序；计数和列表分别执行，不使用 facet。Push/AddToSet 生成的大数组仍需控制单条结果规模。

### 原生聚合扩展

完整用法参考 [订单统计示例](examples/statistics/main.go)，包含全部订单汇总、分类金额分页与标签计数。

当前链式封装覆盖上表常用统计，不代表所有 MongoDB 运算符均已封装。日期表达式、条件计算、Bucket、窗口函数等可在自定义仓储中使用原生聚合；原有 `mongoplus.Aggregate[VO](ctx, r.Repository, pipeline)` 支持白名单内的单集合只读管道。涉及额外集合或写入的原生阶段需通过 Native 显式处理权限、事务与数据生命周期。

## 12. go-zero 模板与中文参数校验

与 gorm-plus 一样，通过 CLI 调用内嵌模板。数据模型与接口协议分开维护：

```bash
# 安装模板工具。
go install github.com/kuangshp/mongo-plus/cmd/mongoplus@latest
go install github.com/zeromicro/go-zero/tools/goctl@v1.10.2

# 在业务项目的服务目录执行。
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

首次先安装 goctl 和与业务依赖版本一致的 mongoplus。Makefile 命令行前使用 Tab。开启 SwaggerConfig.Enabled 后，run/build 自动准备并内嵌文档；关闭时准备编译占位文件。

- [完整 go-zero 配置、MongoDB 生命周期与 Repository 注入](docs/gozero.md)
- [参数校验、自定义规则与 Gin 接入](docs/validation.md)
- [Swagger 配置、部署与热重载](docs/swagger.md)
- [独立的 go-zero 示例模块](examples/gozero/README.md)

已有服务重新生成时不会自动覆盖所有旧入口和 Handler，应先对照新版模板合并所需功能，保留业务代码。

## 性能与生产使用

大批量写入使用 CreateBatch，高频数字主键按需启用 BlockSize；大集合分页优先使用 FindCursorPage，大量导出使用 Walk。关联字段应建立合适的索引，并通过 Select、Limit 控制一对多结果大小；ScanByPage 使用两个独立聚合游标，单条输出文档仍遵循 MongoDB 服务端大小限制。

生产使用前应完成真实数据量与目标并发的压测、超时与故障恢复验证。仓库持续集成包含真实三节点副本集、竞态检查、Gin/goctl 集成、主节点切换后读写与事务恢复、漏洞扫描；这些检查不替代业务环境的容量和长期稳定性验证。详细命令与限制见 [性能验证与生产接入](docs/performance.md)。
