# mongo-plus

基于 `go.mongodb.org/mongo-driver/v2` 的 Go 数据访问工具库，提供代码生成、类型安全的 Repository、条件构造、分页和事务，减少 MongoDB 项目中的重复数据层代码。支持 Gin、go-zero 和普通 Go 服务。

## 目录导航

按用途分组，点击链接跳转到对应章节。首次接入建议从库介绍和代码生成器开始。

**🚀 入门与代码生成**

- [库介绍与安装](#1-当前这个库是做什么的)
- [代码生成器的使用](#代码生成器)
  - [全局配置：Go 与 YAML 两种方式](#全局配置)
  - [JSON 实体定义与全部可配置属性](#json-实体定义)
  - [索引、类型和嵌套对象](#索引类型和嵌套对象)
  - [生成代码与同步数据库](#生成代码与同步数据库)
  - [DTO 与 VO](#dto-与-vo)
  - [手写 Entity 路线](#手写-entity-路线)
  - [go-zero 接口代码生成](#go-zero-接口代码生成)

**✅ 参数校验**

- [参数校验介绍与内置规则](#参数校验)
- [go-zero 使用案例](#在-go-zero-中使用)
- [Gin 使用案例](#在-gin-中使用)
- [自定义校验规则](#自定义规则)

**🔌 插件接入：注册 → 中间件 → 验证**

- [接入前准备：实体与登录上下文](#接入前准备实体与登录上下文)
- [插件一：租户隔离](#租户插件)
- [插件二：创建人、更新人自动填充](#自动填充插件)
- [插件三：数据权限](#数据权限插件)
- [三个插件一起接入：go-zero / Gin 完整装配](#三个插件一起接入共用装配代码)
- [验证接口：使用普通仓储增删改查](#插件示例仓储操作)

**🔍 查询与数据操作**

- [常用增删改查](#常用增删改查)
  - [创建与复用 Repository](#创建与复用-repository)
  - [默认连接与多个数据库](#默认连接与多个数据库)
  - [批量新增、计数与存在性](#批量新增计数与存在性)
  - [条件构造与分页](#条件构造与分页)
  - [查询结果填充 VO](#查询结果填充-vo)
  - [零值更新与逻辑删除](#零值更新与逻辑删除)
  - [原子复合操作与官方扩展选项](#原子复合操作与官方扩展选项)
  - [事务](#事务)
- [复杂查询如何使用 DAL](#dal-复杂查询)
  - [DAL 文件组织与加载](#文件组织与加载)
  - [在业务仓储中执行](#在业务仓储中执行)
  - [链式关联：Lookup、LeftJoin、InnerJoin](#11-关联查询lookupleftjoininnerjoin)
  - [链式聚合：分组、金额计算与数组统计](#12-聚合统计分组金额计算与数组统计)

**⚡ 缓存与生产配置**

- [缓存的使用](#缓存)
  - [Repository 查询缓存](#repository-查询缓存)
  - [Redis 等共享缓存后端](#redis-等共享缓存后端)
  - [公共 SF 与内存缓存](#公共-sf-与内存缓存)
- [敏感字段](#敏感字段)
- [主从数据源](#主从数据源)
- [控制台命令日志与调用位置](#控制台命令日志与调用位置)
- [性能与生产使用](#性能与生产使用)

## 1. 当前这个库是做什么的

从 JSON 实体定义或手写 Entity 生成 Entity、DAO 字段、Repository 和可选 DTO/VO，再通过仓储完成业务读写。常用操作使用类型安全方法；复杂聚合、批量操作与变更订阅放进 DAL 查询文件和自定义仓储；特殊能力可以继续调用官方驱动。

| 能力 | 解决的问题 | 阅读入口 |
| --- | --- | --- |
| 代码生成 | 统一实体、字段、仓储、DTO/VO；可同步集合、索引和校验器 | [代码生成器](#代码生成器) |
| 请求参数校验 | 必填、格式、嵌套对象及中文错误；适配 go-zero / Gin | [参数校验](#参数校验) |
| 数据层插件 | 租户隔离、操作人填充、数据权限、敏感字段及慢操作记录 | [插件](#插件) |
| DAL 原生扩展 | 用 JSON / Extended JSON 保存复杂 MongoDB 命令，封装为业务仓储方法 | [DAL](#dal-复杂查询) |
| 缓存与请求合并 | 查询缓存、SingleFlight、可替换的共享缓存后端 | [缓存](#缓存) |
| 常用数据操作 | 新增、条件查询、分页、零值更新、软删除、原子操作及事务 | [增删改查](#常用增删改查) |

数据层不依赖 GORM、gorm-plus、go-utils、Gin 或 go-zero；框架只在业务服务中引入。接口命名沿用 gorm-plus 的使用习惯，从该库切换可参考[迁移指南](docs/gorm-plus-migration.md)。

当前依赖官方 MongoDB Go Driver v2.9.2，最低 Go 1.25.5，仓库 toolchain 为 Go 1.26.9。业务项目需自行选择符合依赖要求的工具链。

```bash
go get github.com/kuangshp/mongo-plus@latest
```

本文对应当前源码；`@latest` 取得已发布版本。生产项目应固定经过验证的版本，CLI 与库依赖保持一致，发布内容见[更新记录](CHANGELOG.md)。

建议按下列顺序接入：生成数据层 → 启动时建立连接、注册插件并构造仓储 → 请求校验与鉴权 → 使用带请求 Context 的仓储方法。数据库 schema 同步是单独步骤，启动 HTTP 服务不会自动执行迁移。

<a id="代码生成器"></a>

## 2. 代码生成器的使用

生成数据层只需公共 Go 包，不要求安装 mongoplus CLI。先在业务根目录准备 `generator.go` 和 `schema/*.json`，再运行生成器；如需 go-zero 模板、Swagger 或 schema 命令，再安装 CLI。

| 步骤 | 文件 / 命令 | 作用 |
| --- | --- | --- |
| 1 | `go.mod` | 确认 module，例如 `example.com/shop` |
| 2 | `generator.go` 或 `mongo-plus.yml` | 设置输出目录、主键、时间、逻辑删除等全局规则 |
| 3 | `schema/user.json` | 声明业务字段和索引；自动字段不必重复声明 |
| 4 | `go run generator.go` | 生成数据层；配置完整 MongoDB 数据源时也同步数据库 |
| 5 | `go mod tidy` | 整理业务模块依赖 |
| 6 | 运行时初始化 | 用业务运行配置连接 MongoDB，再注入生成的 Repository |

### 全局配置

下面两种方式任选一种，`Package` / `package` 必须与业务 `go.mod` 的 module 一致。相对路径以执行生成命令的工作目录为准。

<a id="1-全局配置一次实体按需覆盖"></a>

#### 方式一：直接在 generator.go 配置（常用）

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
		if err != nil {
			log.Fatal(err)
		}
		var app struct {
			MongoDB *mongoplus.Config `yaml:"mongodb"`
		}
		if err := yaml.Unmarshal(data, &app); err != nil {
			log.Fatal(err)
		}
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
		MongoDB: mongoplus.Config{ // 可选；完整填写则在生成时同步数据库，省略或全零则仅生成文件。
			URI:         mongoConfig.URI,         // 读取 YAML mongodb.uri，如 mongodb://127.0.0.1:27017。
			Database:    mongoConfig.Database,    // 读取 YAML mongodb.database，如 shop；这是数据库名。
			MaxPoolSize: mongoConfig.MaxPoolSize, // 读取 YAML mongodb.maxPoolSize，如 100；0 使用驱动默认值。
			Timeout:     mongoConfig.Timeout,     // 可选的 Go time.Duration；0 不设置驱动操作超时。
		},
		ID: mongoplus.IDConfig{ // 全局主键，单个实体可以覆盖。
			Type: mongoplus.IDTypeInt64, // 只配置类型，自动使用 int64 自增主键。
		},
		SoftDelete: mongoplus.Bool(true), // 启用逻辑删除，false 明确关闭。
		Timestamps: mongoplus.Bool(true), // 自动维护 CreatedAt、UpdatedAt。
	}
	// MongoDB 已配置：先同步集合、索引、校验器，再生成文件；未配置：只生成文件。
	if err := mongoplus.Generate(cfg); err != nil {
		log.Fatal(err)
	}
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

#### 方式二：完整生成配置放在 YAML

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
	if err != nil {
		log.Fatal(err)
	}
	// 配置了 mongodb 就先同步集合、索引、校验器；省略则只生成文件。
	if err := mongoplus.Generate(cfg); err != nil {
		log.Fatal(err)
	}
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

#### 全局主键默认规则

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

<a id="2-json-实体定义"></a>

### JSON 实体定义

`generator.go` 配置生成目录、连接和全局默认值；`schema/*.json` 单独描述每个集合的业务字段、索引及实体级覆盖规则。一个 JSON 文件对应一个实体和集合，文件放在生成配置的 `SchemaDir` 目录下；当前只读取该目录直接包含的 `.json` 文件，不递归读取子目录。

JSON 定义由 `go run generator.go` 读取。配置了 MongoDB 数据源时，先同步集合、校验器和索引，再生成 Entity、DAO、Repository 及可选 DTO/VO；未配置数据源时只生成代码。`mongoplus goctl api go ...` 只生成 HTTP 代码，不会执行这份实体定义。

#### 基本示例

在业务项目中创建 `schema/user.json`：

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

#### 全部可配置属性

下表列出 JSON 实体定义支持的全部属性，路径中的 `[]` 表示数组元素。`fields[].fields` 中的子字段沿用同一套字段属性，可以继续嵌套。生成目录和数据库连接属于前面的全局配置，不写在实体 JSON 中。

| 属性路径 | JSON 类型 | 必填 / 默认行为 | 说明与示例 |
| --- | --- | --- | --- |
| `entity` | string | 可选；按文件名推导 | 生成的 Go 实体名，必须是合法的导出标识符。例如 `"AccountEntity"`；只影响 Go 类型名，不影响集合名。 |
| `collection` | string | 可选；按文件名及全局 `pluralize` 推导 | 实际集合名，例如 `"custom_users"`。显式填写后不再转换下划线或追加 `s`。 |
| `comment` | string | 可选；默认空字符串 | 实体说明，例如 `"用户"`，用于生成代码注释。 |
| `id` | object | 可选；继承全局 `id` | 当前实体的主键配置；只填写需要覆盖的属性，例如 `{"type":"objectId"}`。 |
| `id.type` | string | 可选；继承全局类型 | 支持 `objectId`、`int64`、`string`；全局也省略时默认 `objectId`。 |
| `id.generation` | string | 可选；继承或按类型推导 | 支持 `auto`、`increment`、`manual`。覆盖为不同主键类型时重新推导生成方式：ObjectID 为 `auto`、int64 为 `increment`、string 为 `manual`。 |
| `id.blockSize` | integer | 可选；继承全局设置 | 自增号码预分配数量，范围 `0`～`1000000`，仅用于 int64 + increment。`0` 不覆盖继承值；`1` 显式关闭继承的预分配。 |
| `softDelete` | boolean | 可选；继承全局，未配置全局时为 `true` | 是否启用逻辑删除；`false` 明确关闭，`null` 与省略一样继承全局设置。 |
| `timestamps` | boolean | 可选；继承全局，未配置全局时为 `true` | 是否自动维护 `created_at`、`updated_at`；`false` 明确关闭，`null` 与省略一样继承全局设置。 |
| `tenant` | object | 可选；继承全局 `tenant` | 当前实体的租户字段配置。显式配置时需同时提供 `name`、`type`；省略或 `null` 不会关闭全局租户配置。 |
| `tenant.name` | string | 配置 `tenant` 时必填 | 租户 BSON 字段名，例如 `"company_id"`；生成器自动补充该字段，不在业务 `fields` 中重复定义。 |
| `tenant.type` | string | 配置 `tenant` 时必填 | 支持 `int64`、`string`、`objectId`；创建和查询时从请求 Context 读取租户值。 |
| `fields` | array of object | 可选；省略时不定义额外业务字段 | 业务字段列表。主键及已启用的时间、逻辑删除和租户字段由生成器补充。 |
| `fields[].name` | string | 每个字段必填 | BSON 字段名，例如 `"username"`；必须为合法且不重复的名称，不使用点路径声明嵌套对象。 |
| `fields[].type` | string | 每个字段必填 | 支持 `string`、`int32`、`int64`、`bool`、`float64`、`objectId`、`datetime`、`decimal128`、`bytes`、`json`、`object`，以及加 `[]` 前缀的数组类型，如 `[]string`、`[]object`。 |
| `fields[].comment` | string | 可选；默认空字符串 | 字段说明，例如 `"用户名"`，用于生成代码注释与数据库校验器的字段描述。 |
| `fields[].goName` | string | 可选；从 `name` 推导 | 自定义 Go 字段名，例如 `"DisplayName"`；必须是合法且不冲突的导出标识符，不改变 BSON/JSON 字段名。 |
| `fields[].required` | boolean | 可选；默认 `false` | 数据库校验器要求字段存在；不等价于字符串非空，也不排除数字 `0` 或 `false`。生成的 DTO 校验是另一层规则。 |
| `fields[].unique` | boolean | 可选；默认 `false` | 为字段创建集合级唯一索引，索引名为 `uk_` 加字段路径并将点替换成下划线，例如 `uk_username`。逻辑删除后仍占用唯一值。 |
| `fields[].nullable` | boolean | 可选；默认 `false` | 允许字段值为 BSON null，并生成指针字段；数组字段设置后允许整个数组为 null，不表示数组元素可以为 null。 |
| `fields[].fields` | array of object | `object` / `[]object` 必填且非空 | 对象内部字段，例如 `[{"name":"city","type":"string"}]`。子字段递归使用本表中的字段属性；其他类型不能填写非空的 `fields`。 |
| `indexes` | array of object | 可选；默认无额外索引 | 显式索引列表，用于复合、唯一、稀疏、逻辑删除条件和 TTL 索引。字段 `unique` 简写产生的索引会合并到该列表。 |
| `indexes[].name` | string | 每个索引必填 | 索引名称，例如 `"idx_status_created_at"`；不能重复，也不能使用 MongoDB 保留的 `_id_`。 |
| `indexes[].fields` | array of object | 每个索引必填且非空 | 有序的索引字段列表。复合索引顺序有意义，不能改写为无序对象。 |
| `indexes[].fields[].name` | string | 每个索引字段必填 | 已定义字段的 BSON 路径，例如 `"status"`、`"address.city"`；也可以引用已启用的 `_id`、时间、逻辑删除和租户字段。 |
| `indexes[].fields[].order` | integer / string | 每个索引字段必填 | `1` / `-1`，或 `text`、`hashed`、`2d`、`2dsphere`；通配符索引使用字段名 `$**` 或 `对象.$**`，order 为 `1`。 |
| `indexes[].unique` | boolean | 可选；默认 `false` | 是否为唯一索引；和多个索引字段组合时表示复合唯一约束。 |
| `indexes[].sparse` | boolean | 可选；默认 `false` | 是否创建 MongoDB 稀疏索引；不能与 `activeOnly: true` 同时使用。 |
| `indexes[].activeOnly` | boolean | 可选；默认 `false` | 仅索引未逻辑删除的文档；要求当前实体启用逻辑删除，可用于未删除记录的唯一约束。 |
| `indexes[].expireAfterSeconds` | integer | 可选；省略时不是 TTL 索引 | TTL 过期秒数，范围 `0`～`2147483647`。仅支持一个 `datetime` 索引字段；`0` 表示按字段存储的时间点过期，TTL 执行物理删除。 |
| `fields[].rules` | object | 可选 | 附加 JSON Schema 约束，支持 `enum`、`minimum`、`maximum`、`exclusiveMinimum`、`exclusiveMaximum`、`multipleOf`、`minLength`、`maxLength`、`pattern`、`minItems`、`maxItems`、`uniqueItems`、`minProperties`、`maxProperties`、`description`。不能覆盖推导的类型/对象结构；数组规则作用于数组本身。 |
| `indexes[].partialFilterExpression` | object | 可选 | 原生部分索引过滤；与 activeOnly、sparse 互斥。 |
| `indexes[].collation` | object | 可选 | 官方 Collation 属性；未设置时继承集合 Collation。文本/hashed 索引按服务端规则使用 `{"locale":"simple"}`。 |
| `indexes[].hidden` | boolean | 默认 false | 隐藏索引不参与查询计划选择；发生差异时 Plan 要求显式迁移。 |
| `indexes[].weights` | object | 可选 | 文本字段权重，如 `{"title":2}`。 |
| `indexes[].defaultLanguage` | string | 文本索引默认 english | 文本分词语言。 |
| `indexes[].languageOverride` | string | 文本索引默认 language | 文档中的语言覆盖字段名。 |
| `options` | object | 可选 | 普通集合的存储与校验选项。 |
| `options.collation` | object | 可选 | 集合默认排序规则；创建后变更需显式迁移。 |
| `options.capped` | boolean | 默认 false | 是否为固定容量集合。 |
| `options.sizeInBytes` | integer | capped 时必填正值 | 固定容量字节数；建议按服务端分配粒度对齐。 |
| `options.maxDocuments` | integer | 可选；默认不限制 | capped 集合的文档数量上限。 |
| `options.changeStreamPreAndPostImages` | boolean | 可选 | 开启变更前后镜像；受服务端和集合类型限制，变更使用迁移。 |
| `options.validationLevel` | string | 默认 strict | strict / moderate / off。 |
| `options.validationAction` | string | 默认 error | error / warn。 |

Collation 支持 `locale`、`caseLevel`、`caseFirst`、`strength`、`numericOrdering`、`alternate`、`maxVariable`、`normalization`、`backwards`；`locale` 必填，其他省略时使用官方默认值。规则和索引组合还会受到 MongoDB 服务端版本校验。

属性名按表中的 JSON 写法填写。未知属性、重复 JSON 键、重复字段以及与自动字段冲突的定义会报错。以上是本库的实体定义格式，不是直接传给 MongoDB 的原生 `$jsonSchema`；不要在字段顶层添加 `default`、`minLength` 等属性；长度等约束放入 `rules`。

字段的 Go 类型、嵌套对象及索引示例见[索引、类型和嵌套对象](#7-索引类型和嵌套对象)。

#### 实体与集合命名

集合名自动从 JSON 文件名推导，不需要每个实体重复配置：

| 文件名 | Go Entity | 默认集合名 | Pluralize: true |
| --- | --- | --- | --- |
| `user.json` | `UserEntity` | `user` | `users` |
| `sys_user.json` | `SysUserEntity` | `sys_user` | `sys_users` |
| `sysUser.json` | `SysUserEntity` | `sys_user` | `sys_users` |

`Pluralize` 是 bool 开关，true 仅在自动推导的名称末尾追加 s，不进行英语单复数变形。上述两个 sysUser 写法是二选一；同时定义会因实体/集合名称冲突而报错。单个实体可用 `"collection": "custom_users"` 完整覆盖集合名，工具不会再对它转下划线或追加后缀；`entity` 仅覆盖 Go 类型名，不改变按文件名推导的集合名。

主键、时间和逻辑删除字段由工具补充，JSON 的 `fields` 不重复定义它们。

#### 覆盖全局配置

单个实体只需填写需要覆盖的配置，例如 `schema/event.json`：

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

只覆盖 `id.type` 为不同类型时，生成方式按新类型补全；仅覆盖 `id.generation` 时保留全局类型。`false` 是明确关闭，不会被全局的 `true` 覆盖。

#### 主键生成与号码预分配

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

<a id="7-索引类型和嵌套对象"></a>

### 索引、类型和嵌套对象

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

<a id="3-生成代码与同步数据库"></a>

### 生成代码与同步数据库

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

#### DTO 与 VO

与 gorm-plus 一样，填写 `DtoPath`、`VoPath` 才生成对应目录，留空时不生成。DTO/VO 是可直接修改的业务初始模板，后续生成保留已有文件；schema 字段变化后，按业务调整 DTO/VO，或备份并删除对应文件后重新生成。不会在重新生成 Entity 时覆盖你的业务校验、字段裁剪等修改。

- `CreateUserDTO` 包含业务字段，自动主键不出现在新增 DTO；手动主键会生成 ID。`ModifyUserDTO` 包含 ID 和全部业务字段，用于完整提交，不表示 PATCH。
- DTO 不包含自动维护的租户、创建时间、更新时间、逻辑删除字段；VO 包含主键、业务字段和查询所需时间、租户字段，省略逻辑删除字段。VO 默认列出其余字段，接口返回前应裁剪密码等敏感信息。
- ObjectID、Decimal128、日期时间在 DTO 中使用 string，分别由业务层调用 `bson.ObjectIDFromHex`、`bson.ParseDecimal128`、`time.ParseInLocation` 转成 Entity 类型；嵌套对象、数组同样生成请求类型。VO 保留 Entity 的 BSON 类型和字段标签，可直接用于 `Scan`、`ScanByPage`；按接口需要自行格式化输出。
- DTO 自带 `UnmarshalJSON`，支持数字和数字字符串兼容，不经过 float64。金额字符串保留原文，生成器不强制两位小数；仍需检查 Decimal128 解析结果和业务精度。
- DTO 同时生成相同规则的 `validate` 和 `binding` 标签，分别供普通 Go/go-zero 和 Gin 使用。初始规则包括必填、ObjectID、时间格式和数组元素校验。必填数字、bool、集合使用指针区分缺失与合法的 `0`、`false`、空集合；必填字符串要求非空，可空字段允许省略或 null。时间格式为 `2006-01-02 15:04:05`。这是常见请求校验初值，可按业务调整，与数据库 required（字段存在）并非完全相同。

`schema apply` 不删除集合、文档、旧字段或现有索引，不重建冲突索引。同步新增字段不会补齐历史文档。字段重命名、类型转换、数据回填、索引替换应编写显式迁移。更新 strict 校验规则后，旧文档的后续写入可能因不符合新规则而失败，先通过 plan 审阅变更并安排迁移。

集合管理和索引操作不是整体原子事务；中途失败时已完成的操作会保留，修复原因后重新执行。再次执行无变化的定义不会重复修改数据库。同步也会检查 validationLevel/validationAction，并恢复为实体 options 指定的目标值；未指定时默认 strict/error。

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

<a id="10-手写-entity-路线"></a>

### 手写 Entity 路线

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
	if err != nil {
		log.Fatal(err)
	}
	if err := mongoplus.GenerateModels(cfg,
		mongoplus.GeneratorModel{Value: entity.UserEntity{}},
	); err != nil {
		log.Fatal(err)
	}
}
```

执行 `go run generator.go`。YAML 中配置了 mongodb 就先同步数据库；省略则只生成 DAO、Repository 和可选 DTO/VO，不复制或覆盖手写 Entity。主键 Go 类型需匹配全局规则，特殊模型通过 GeneratorModel.ID 覆盖。

手写模型显式声明 `ID` 和 `bson:"_id"`；启用时间或逻辑删除时，同时声明对应 time.Time 和 *time.Time 字段。`mongoplus:"required,unique"`、`comment:"用户名"` 可提供额外元数据。参考 [手写模型示例](examples/manual/model/document.go)。本版显式报错的特殊结构包括 bson inline、递归结构、自定义外部命名类型与指针数组元素，避免静默生成错误类型。

手写 Entity 自动推导的集合名同样遵循转下划线加 Pluralize 的规则；GeneratorModel.Collection 可完整覆盖。需要只同步不生成时，可以单独调用 SyncModels；需要预览变更时，可通过 schema.FromModels 与 schema.Plan 取得计划。

### go-zero 接口代码生成

`go run generator.go` 生成数据层，`mongoplus goctl api go` 生成 Handler / Logic / types 等 HTTP 代码；两个命令职责不同。CLI 的可执行文件名是 `mongoplus`，安装路径属于本库 `mongo-plus`：

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

<a id="参数校验"></a>

## 3. 参数校验：go-zero 与 Gin

统一使用 `github.com/kuangshp/mongo-plus/validation`。请求参数校验、JSON 类型转换、数据库字段校验是三个步骤：

| 层次 | 入口 | 作用 |
| --- | --- | --- |
| 请求参数 | `validation` / Gin binding | 按标签检查必填、范围和格式，返回中文错误 |
| JSON 类型兼容 | `decimaljson` / 生成 DTO 的 `UnmarshalJSON` | 兼容数字与数字字符串；不代替业务校验 |
| 数据库存储约束 | 生成器同步的 MongoDB validator / 索引 | 检查持久化字段类型、约束与唯一性 |

生成 DTO 同时包含 `validate`、`binding` 标签；go-zero 使用 `.api` 生成的 types，Gin 可直接绑定生成的 DTO。HTTP 请求与数据库 Entity 分开维护，避免将自动字段和敏感字段直接暴露为入参。

### 公共入口与规则

| 入口 | 用途 |
| --- | --- |
| `Validate(value)` | 使用 `validate` 标签进行校验，返回首个中文错误 |
| `HasRules(value)` | 判断结构体及未忽略的嵌套结构体是否包含 `validate` 规则 |
| `HasRulesForTag(value, "binding")` | 检查指定标签；不执行校验 |
| `New(options...)` | 创建独立校验器，使用 `validate` 标签 |
| `Configure(engine, options...)` | 配置已有引擎，保留它原有的校验标签 |
| `instance.FirstMessage(err)` / `Translate(err)` | 翻译该实例的校验错误，不再次校验数据 |
| `WithRules(rules...)` | 注册项目规则；同名规则由最后一次配置覆盖函数和提示 |
| `WithFieldTagName("label")` | 从 `label:"手机号"` 等标签取提示中的字段名，默认取 `json` |

字段按结构体顺序取第一个校验错误；`*validation.Error` 包含 `Field`、`Tag`、`Message`，通过 `Unwrap` 保留原始错误。JSON 格式、字段类型不匹配等非字段校验错误原样返回，不会伪装成中文规则提示。

### 内置校验规则

除了 validator 自带的 `required`、`min`、`max`、`gte`、`email` 等规则，还提供：

| 标签 | 说明 |
| --- | --- |
| `mobile` / `checkMobile` | 中国大陆手机号码，沿用原 go-utils 规则 |
| `checkEmail` / `myEmail` | 邮箱地址，不接受显示名或空白字符 |
| `date` | 合法日期，格式 `2006-01-02` |
| `dateTime` | 合法日期时间，格式 `2006-01-02 15:04:05` |
| `afterDate` | 晚于本地时区的今天，不包含今天 |
| `decimal` | 沿用旧包的非负小数字符串规则，至多两位小数 |
| `objectId` | 24 位十六进制 ObjectID 字符串；业务层仍通过 bson.ObjectIDFromHex 解析 |

规则与 JSON 类型转换是两步：`validation` 不把 `"1"` 转成整数；整数、小数兼容使用 `decimaljson`。必填字段仍需写 `required`，可选字段可写 `omitempty,dateTime`。需要更高金额精度时，应覆盖 `decimal` 规则，或选用项目自己的标签。

### 在 go-zero 中使用

`.api` 文件直接声明规则，Handler 自动完成兼容转换 → go-zero 解析 → 中文校验：

```text
syntax = "v1"

type CheckReq {
    Id int64 `json:"id" validate:"required,gt=0"`
    Amount string `json:"amount" validate:"required,decimal"`
    Mobile string `json:"mobile" validate:"required,checkMobile"`
    StartTime string `json:"startTime,optional" validate:"omitempty,dateTime"`
    DocumentID string `json:"documentId,optional" validate:"omitempty,objectId"`
}

type CheckResp {
    Id int64 `json:"id"`
}

service shop-api {
    @handler Check
    post /demo/check (CheckReq) returns (CheckResp)
}
```

先按[代码生成器](#代码生成器)一节执行 `mongoplus goctl api go ...`。本库内嵌模板生成的 Handler 按以下顺序处理请求；这是 Handler 函数体中的节选，`req` 为生成的请求类型：

```go
// 1. 兼容数字 / 字符串输入。
if err := decimaljson.PrepareRequest(r, &req); err != nil {
    httpx.ErrorCtx(r.Context(), w, err)
    return
}
// 2. 沿用 go-zero 的 JSON / path / form 解析。
if err := httpx.Parse(r, &req); err != nil {
    httpx.ErrorCtx(r.Context(), w, err)
    return
}
// 3. 读取 validate 标签，返回首个中文错误。
if validation.HasRules(&req) {
    if err := validation.Validate(&req); err != nil {
        httpx.ErrorCtx(r.Context(), w, err)
        return
    }
}
// 4. 校验通过后进入 Logic，继续传递请求 Context。
l := logic.NewCheckLogic(r.Context(), svcCtx, r, w)
resp, err := l.Check(&req)
if err != nil {
    httpx.ErrorCtx(r.Context(), w, err)
    return
}
httpx.OkJsonCtx(r.Context(), w, resp)
```

对应导入为 `github.com/kuangshp/mongo-plus/decimaljson`、`github.com/kuangshp/mongo-plus/validation` 和 `github.com/zeromicro/go-zero/rest/httpx`；`logic`、`svcCtx` 是业务生成代码。示例构造函数包含 `r, w`，与本库模板一致。

Handler 只解析、校验和响应，业务放在 Logic，数据库访问通过 ServiceContext 中注入的 Repository，查询传入 `l.ctx`。可选字段同时考虑 `json:",optional"` 和 `validate:"omitempty,..."`；要求“数字必须出现但允许 0”时使用指针区分缺失与零值，不能直接对数字使用 required。

旧 Handler 不会被 goctl 自动覆盖，升级后需对照模板合并校验流程。HTTP 错误码与统一响应由业务的错误处理配置决定，完整服务见 [go-zero 示例](examples/gozero/README.md)和[模板说明](docs/gozero.md)。

### 在 Gin 中使用

Gin 的 `ShouldBindJSON` 已按 `binding` 标签校验。启动时配置 Gin 自带引擎，保存返回的校验器实例；请求失败时用同一个实例翻译，不再调用一次 `validation.Validate`。

业务 `utils/validator.go` 的最小实现如下，初始化必须在启动 HTTP 服务前完成：

```go
package utils

import (
	"fmt"

	"github.com/gin-gonic/gin/binding"
	"github.com/go-playground/validator/v10"
	"github.com/kuangshp/mongo-plus/validation"
)

var requestValidator *validation.Validator

// InitValidation 只在服务启动时调用；项目规则可通过 WithRules 一并注册。
func InitValidation() error {
	if binding.Validator == nil {
		return fmt.Errorf("Gin 参数校验器已禁用")
	}
	engine, ok := binding.Validator.Engine().(*validator.Validate)
	if !ok {
		return fmt.Errorf("不支持的 Gin 参数校验引擎")
	}
	var err error
	requestValidator, err = validation.Configure(engine)
	return err
}

// ShowErrorMessage 只翻译本次绑定错误，保留业务原有响应格式。
func ShowErrorMessage(err error) string {
	return requestValidator.FirstMessage(err)
}
```

启动入口先处理初始化错误，再装配路由：

```go
if err := utils.InitValidation(); err != nil {
    log.Fatal(err)
}
router := gin.New()
userAPI := user.NewUser(userRepository)
router.POST("/users", userAPI.Create)
```

`utils.Fail` / `utils.Success` 是业务自己的响应函数；`userRepository` 在启动时用共享数据库连接构造。完整适配器、一次性初始化保护、自定义规则与旧项目迁移见[参数校验文档](docs/validation.md)。

#### Gin 控制器示例

`dto.CreateUserDTO` 是业务项目的请求类型，由本库生成器创建，不是 mongo-plus 包中内置的类型。下面补全它的来源、定义和控制器用法。

**先生成 DTO。** 根目录 `generator.go` 配置 `DtoPath: "internal/dal/dto"`；使用 YAML 时对应 `dto_path: internal/dal/dto`。按前面的 `schema/user.json`（username、status 两个业务字段）执行：

```bash
# 在业务项目根目录运行；未配置 MongoDB 数据源时只生成文件。
go run generator.go
go mod tidy
```

生成文件为 `internal/dal/dto/user_dto.go`。该控制器使用的完整 `CreateUserDTO` 定义如下；同一生成文件还包含修改请求 `ModifyUserDTO`：

<!-- gin-create-user-dto -->
```go
package dto

import "github.com/kuangshp/mongo-plus/decimaljson"

// CreateUserDTO 创建用户的请求参数，不接收自动主键、时间或删除标记。
type CreateUserDTO struct {
	Username string `json:"username" validate:"required" binding:"required"` // 用户名，不能为空。
	Status   int64  `json:"status" validate:"omitempty" binding:"omitempty"` // 可选状态；省略时为 0。
}

// UnmarshalJSON 兼容数字和数字字符串，例如 status: 1 与 status: "1"。
func (value *CreateUserDTO) UnmarshalJSON(data []byte) error {
	type plain CreateUserDTO
	return decimaljson.Unmarshal(data, (*plain)(value))
}
```

| 字段 / 标签 | 本例行为 |
| --- | --- |
| `Username` | 必填字符串；缺失或空字符串会触发校验错误 |
| `Status` | 可选 int64；省略时为 0，不自动填充 1，也未限制只能为 1 / 2 |
| `validate` | 供普通 Go / go-zero 的 validation 校验使用 |
| `binding` | 供 Gin 的 ShouldBindJSON 校验使用 |
| `UnmarshalJSON` | 在校验前处理数字类型兼容；整数溢出或非法数字会返回错误 |

如需限制状态为 1 / 2，可在业务 DTO 中将两个标签都改为 `oneof=1 2`；如还要区分“未传”与合法的 0，使用指针字段，并相应调整 Entity 映射。schema 中的字段 comment 只是说明，不会自动转换成取值范围校验。

已生成此类型时不必再复制一份，避免同包类型重复；DTO 文件首次创建后不会被生成器覆盖，新增字段或修改校验规则需同步维护。未配置 DtoPath 时不会生成 DTO；如出现 `undefined: dto.CreateUserDTO`，先检查配置、文件是否生成，以及导入路径是否匹配业务 module。

**控制器绑定 DTO。** 接口放在模块控制器中，VO 同样由前文生成器生成；utils 是业务项目自己的响应与校验适配器。以下示例文件为 `internal/api/user/user.go`，`example.com/shop` 替换为业务 `go.mod` 的 module：

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
		ID:        record.ID,
		Username:  record.Username,
		Status:    record.Status,
		CreatedAt: record.CreatedAt,
		UpdatedAt: record.UpdatedAt,
	})
}
```

请求示例：

```json
{
  "username": "张三",
  "status": 1
}
```

`status` 也可以传字符串 `"1"`。`{}` 或 `{"username":""}` 会在绑定校验阶段返回错误，不进入 Repository.Create。

在路由装配时构造一次控制器并注册方法，例如 `userAPI := user.NewUser(userRepository)`，再 `router.POST("/users", userAPI.Create)`。Repository 使用启动阶段建立的 MongoDB 连接，并通过构造函数注入；HTTP 层不另建数据库连接。

ShouldBindJSON 会调用 DTO 自带的 UnmarshalJSON，数字与数字字符串兼容仍然生效；随后由 Gin 按 binding 标签执行一次校验。ObjectID、日期等自定义规则及中文翻译必须在启动 HTTP 服务前注册，详见 [完整初始化及错误适配](docs/validation.md)。

**已有 DTO 不会被重新生成覆盖。** 如已有文件仅包含 validate 标签，应补上内容相同的 binding 标签，或备份业务修改后重新生成；仅修改 Handler 不会让 Gin 自动识别 validate 标签。不必更改 Gin 全局校验标签，以便原有 binding DTO 继续使用。

### 自定义规则

普通 Go / go-zero 使用独立实例，项目规则和中文提示一起注册；随后调用该实例的 Validate：

```go
requestValidator, err := validation.New(validation.WithRules(
    validation.Rule{
        Tag:     "username",
        Message: "{0}长度必须在3到20个字符之间",
        Validate: func(fl validator.FieldLevel) bool {
            n := utf8.RuneCountInString(fl.Field().String())
            return n >= 3 && n <= 20
        },
    },
))
if err != nil {
    return err
}
err = requestValidator.Validate(&req) // 请求字段声明 validate:"required,username"。
```

示例还需导入标准库 `unicode/utf8` 和 `github.com/go-playground/validator/v10`。Gin 将相同的 `validation.WithRules(...)` 传给 `validation.Configure(engine, ...)`，DTO 使用 `binding:"required,username"`，错误仍由 Configure 返回的实例翻译。独立实例不会自动替换生成 Handler 使用的默认实例；go-zero 项目采用独立实例时，应同步替换 Handler 的校验入口。

<a id="插件"></a>

## 4. 插件使用：注册、中间件、验证

每个插件统一按三步说明：**1. 注册插件；2. 定义中间件并挂载到路由；3. 验证接口和数据库结果。** 登录中间件先取得可信身份，具体插件中间件再写入数据层 Context。

| 插件 | 第 1 步：数据库注册 | 第 2 步：具体中间件 | 第 3 步：验证效果 |
| --- | --- | --- | --- |
| [租户隔离](#租户插件) | `RegisterTenant` | `TenantMiddleware` / Gin `TenantMiddleware()` | 不传租户也能自动填充，跨公司查询不命中 |
| [创建人、更新人](#自动填充插件) | `RegisterAutoFill` | `OperatorMiddleware` / Gin `OperatorMiddleware()` | 创建人保持不变，更新人跟随当前账号 |
| [数据权限](#数据权限插件) | `RegisterDataPermission` | `DataPermissionMiddleware` / Gin `DataPermissionMiddleware(...)` | 五种数据范围与越权写入按验证表执行 |

本节参考 gin-admin-api 的实际结构：初始化注册 → OperatorMiddleware / DataPermissionMiddleware → 模块路由。参考项目已使用自动填充和数据权限，租户按同样结构补齐。业务 module 为 `example.com/shop`；示例文件属于业务项目，需要创建或合并，不是工具库自动提供的业务文件。

**下列文件都属于业务项目，需要按步骤创建或合并。** 工具库提供 RegisterTenant / RegisterAutoFill / RegisterDataPermission 等入口；不会自动生成业务的登录解析和插件中间件逻辑。

go-zero 项目需要接入的文件如下，已有文件按步骤合并，不要再创建同名实现：

```text
database/
├── tenant.go                         # 注册租户插件。
├── operator.go                       # 注册创建人、更新人插件。
├── permission.go                     # 注册数据权限插件。
└── plugins.go                        # 启动时统一调用注册函数。
internal/
├── authctx/value.go                  # 读取鉴权后的公司、账号、部门 ID。
├── datascope/
│   ├── context.go                    # 数据范围编号与权限上下文。
│   ├── service.go                    # 查询账号、角色、部门并合并范围。
│   └── injector.go                   # 将范围转换成 BSON 条件。
├── middleware/
│   ├── authMiddleware.go             # 项目已有的登录鉴权。
│   ├── tenantMiddleware.go           # 将公司 ID 写入租户插件 Context。
│   ├── operatorMiddleware.go         # 将账号 ID 写入操作人 Context。
│   └── dataPermissionMiddleware.go   # 注入具体的集合权限条件。
└── svc/serviceContext.go             # 注册插件，构造中间件与 Repository。
shop.api                             # 声明路由组的中间件顺序。
```

Gin 使用相同的 database 注册文件，middleware 选择下文的 Gin 版本，挂载位置改为模块路由文件。两种框架的中间件签名不同，不要将两个版本同时复制进一个 middleware 包。Gin 额外复制 loginValue.go，权限插件额外复制 internal/datascope 下的三个业务文件。

### 接入前准备：实体与登录上下文

**实体字段。** 在前文生成配置中启用 int64 主键、时间和逻辑删除，设置 Entity / Repository / DTO / VO 输出路径。本节 `schema/user.json` 如下，执行 `go run generator.go` 生成数据层：

<!-- plugins-example: schema/user.json -->
```json
{
  "collection": "user",
  "comment": "插件示例用户",
  "fields": [
    {"name": "username", "type": "string", "required": true},
    {"name": "status", "type": "int64"},
    {"name": "company_id", "type": "int64"},
    {"name": "dept_id", "type": "int64"},
    {"name": "created_by", "type": "int64"},
    {"name": "updated_by", "type": "int64"},
    {"name": "created_name", "type": "string"}
  ]
}
```

company_id 在此作为普通业务字段声明，由租户插件接管；本节不再同时设置生成器 tenant 段。业务若已经设置 tenant 段，就让生成器补充 CompanyID，不在 fields 中重复声明。审计字段也需存在于实体与数据库规则中。DTO 按前文保留 username / status 请求白名单，不让客户端填写 company_id、created_by、updated_by。

**登录中间件先执行。** gin-admin-api 的 AuthMiddleWare 用 c.Set 保存 accountId、userName、isAdmin。下面 Gin 中间件兼容 c.Get，然后转换为 Request.Context；go-zero 中间件读取 r.Context。

| Context key | 来源 | 哪个插件读取 |
| --- | --- | --- |
| `companyId` | 登录账号已获授权的公司 ID | 租户、数据权限 |
| `accountId` | 当前登录账号 ID | 创建人、更新人 |
| `deptId` | 权限服务从账号记录读取的直属部门 | 新增业务数据的部门归属 |
| `dataScopeContext` | 权限服务计算账号、角色、部门范围后的结果 | 数据权限中间件写入，可供业务复用 |

参考项目没有多租户注册，本节额外要求登录信息中有服务端确认的 companyId。不要将客户端提交的公司、部门或角色范围直接当成授权结果；deptId 和数据范围由下文权限服务查数据库得到。

为兼容参考项目中的字符串 ID 和普通 Go 的 int64，业务新增 `internal/authctx/value.go`，仅负责读取和检查 Context 值：

<!-- plugins-example: authctx/value.go -->
```go
package authctx

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
)

// Int64 只读取上游鉴权中间件已写入的 Context，不读取 Header、Query 或 Body。
func Int64(ctx context.Context, key string) (int64, error) {
	if ctx == nil {
		return 0, fmt.Errorf("缺少登录上下文")
	}
	var id int64
	var err error
	switch value := ctx.Value(key).(type) {
	case int64:
		id = value
	case int:
		id = int64(value)
	case string:
		id, err = strconv.ParseInt(value, 10, 64)
	case json.Number:
		id, err = value.Int64()
	default:
		return 0, fmt.Errorf("缺少有效的 %s", key)
	}
	if err != nil || id <= 0 {
		return 0, fmt.Errorf("无效的 %s", key)
	}
	return id, nil
}
```

Gin 现有登录中间件可以继续 c.Set；下方桥接文件负责读取，插件再写回 Request.Context。go-zero 登录中间件继续使用 next(w, r.WithContext(ctx))。Controller / Logic 不丢弃请求 Context。

Gin 新增 `internal/middleware/loginValue.go`，读取登录中间件写入的值；go-zero 不需要这个文件：

<!-- plugins-example: gin/middleware/loginValue.go -->
```go
package middleware

import (
	"context"
	"example.com/shop/internal/authctx"
	"github.com/gin-gonic/gin"
)

// loginID 兼容参考项目中 AuthMiddleWare 的 c.Set，以及已有 Request.Context。
func loginID(c *gin.Context, key string) (int64, error) {
	ctx := c.Request.Context()
	if value, exists := c.Get(key); exists {
		ctx = context.WithValue(ctx, key, value)
	}
	return authctx.Int64(ctx, key)
}
```

<a id="租户插件"></a>

### 插件一：租户隔离

目标：登录公司 1001 的请求只能读写本公司的数据，创建时自动写 company_id；业务代码不用逐次加租户条件。

#### 第 1 步：注册插件

业务新增 `database/tenant.go`：

<!-- plugins-example: database/tenant.go -->
```go
package database

import mongoplus "github.com/kuangshp/mongo-plus"

// RegisterTenant 在建立 MongoDB 连接后调用一次。
func RegisterTenant(db *mongoplus.Database) error {
	return mongoplus.RegisterTenant(db, mongoplus.TenantConfig[int64]{
		TenantField: "company_id",
	})
}
```

连接成功后执行 `database.RegisterTenant(db)` 并处理错误，再构造 Repository。多数据源分别在对应数据库包装器注册，同一个插件不要重复注册。

| TenantConfig 属性 | 说明 |
| --- | --- |
| `TenantField` | 默认租户 BSON 字段，例如 company_id |
| `TenantFields` | 默认多字段配置 |
| `CollectionFields` | 按集合覆盖字段；优先级最高，空列表表示不注入 |
| `ExcludeCollections` | 不参与租户插件的集合 |
| `GetTenantID` | 自定义默认取值函数；省略时读取 WithTenantID / WithTenant |
| `AllowOverrideTenantID` | 是否允许受控切换租户，默认 false |
| `TenantFieldConfig.Field / GetTenantID` | 单字段路径及取值函数 |

#### 第 2 步：定义并挂载中间件

##### go-zero：internal/middleware/tenantMiddleware.go

<!-- plugins-example: gozero/middleware/tenantMiddleware.go -->
```go
package middleware

import (
	"net/http"

	"example.com/shop/internal/authctx"
	mongoplus "github.com/kuangshp/mongo-plus"
	"github.com/zeromicro/go-zero/rest/httpx"
)

type TenantMiddleware struct{}

func NewTenantMiddleware() *TenantMiddleware {
	return &TenantMiddleware{}
}

// Handle 必须在登录鉴权中间件之后执行。
func (m *TenantMiddleware) Handle(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		companyID, err := authctx.Int64(r.Context(), "companyId")
		if err != nil {
			httpx.WriteJsonCtx(r.Context(), w, http.StatusForbidden,
				map[string]string{"message": "缺少有效租户信息"})
			return
		}
		ctx := mongoplus.WithTenantID(r.Context(), companyID)
		next(w, r.WithContext(ctx))
	}
}
```

##### Gin：internal/middleware/tenant.go

<!-- plugins-example: gin/middleware/tenant.go -->
```go
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	mongoplus "github.com/kuangshp/mongo-plus"
)

// Tenant 在登录鉴权之后将公司 ID 写入租户插件的 Context。
func TenantMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		companyID, err := loginID(c, "companyId")
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "缺少有效租户信息"})
			return
		}
		ctx := mongoplus.WithTenantID(c.Request.Context(), companyID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
```

##### 挂载到路由

go-zero 的 `ServiceContext` 增加 `TenantMiddleware rest.Middleware`，构造时赋值：

```go
TenantMiddleware: middleware.NewTenantMiddleware().Handle,
```

在 .api 的受保护路由组写 `middleware: AuthMiddleware,TenantMiddleware`，再执行 `mongoplus goctl api go --api shop.api --dir . --style=goZero`。第一个登录中间件必须先写入 companyId；不要手改生成的 routes.go。末尾有完整的 .api / ServiceContext 示例。

Gin 在路由装配函数中挂载：

```go
// authMiddleware 是项目已实现的登录中间件，类型为 gin.HandlerFunc。
api := router.Group("/api")
api.Use(authMiddleware, middleware.TenantMiddleware())
api.POST("/users", userAPI.Create)
api.GET("/users/:id", userAPI.Get)
api.PUT("/users/:id", userAPI.Update)
```

#### 第 3 步：验证效果

先在独立测试数据库启动服务，并通过项目登录接口取得公司 1001、公司 2002 两个账号的真实 token。使用后面的完整装配时，还需要准备该账号的授权元数据。以下用 gin-admin-api 的 token 请求头；项目使用 Authorization 时按原登录协议替换。

```bash
BASE_URL=http://127.0.0.1:8888
TOKEN_A='<公司1001账号的真实token>'
TOKEN_B='<公司2002账号的真实token>'

# 请求里不传 company_id，租户中间件负责设置。
curl -X POST "$BASE_URL/api/users" \
  -H "token: $TOKEN_A" -H "Content-Type: application/json" \
  -d '{"username":"插件验证-A","status":1}'
```

记下成功响应中的实际 ID，填入下面的 USER_ID；响应包装格式沿用业务项目：

```bash
USER_ID='<上一步返回的实际ID>'
curl "$BASE_URL/api/users/$USER_ID" -H "token: $TOKEN_A"
curl "$BASE_URL/api/users/$USER_ID" -H "token: $TOKEN_B"
```

在配置的同一个测试数据库中查看文档：

```javascript
db.user.find(
  {username: "插件验证-A"},
  {_id: 1, company_id: 1, dept_id: 1}
)
```

| 检查 | 通过标准 |
| --- | --- |
| 公司 1001 创建，不提交 company_id | 文档 company_id 为 int64 的 1001 |
| 同公司、具有对应数据权限的账号读取 | 返回刚创建的记录 |
| 公司 2002 读取同一个 ID | 不命中，不返回公司 1001 的数据 |
| 登录 Context 没有公司信息 | 中间件返回 403，Handler 不执行 |
| 只注册插件，漏挂租户中间件 | Repository 报缺少租户；补挂中间件，不用 SkipTenant 绕过 |

业务方法保持普通 FindById / Create：go-zero 传 l.ctx，Gin 传 c.Request.Context()。日志出现 company_id 条件只是辅助证据，以上返回值和数据库字段都应核对。

#### 租户配置与高级控制

集合只需要一个租户字段时，写在生成配置里即可，不必再注册插件：

```yaml
tenant:
  name: company_id
  type: int64
```

```go
ctx := mongoplus.WithTenant(request.Context(), companyID)
```

生成器补充 CompanyID，Create 从上下文填充。查询与写入使用“租户 AND 软删除 AND 业务条件”，OR 条件不能覆盖租户约束；缺少租户返回 ErrTenantRequired。WithDeleted/AllowAll 不绕过租户。

需要同一集合多个租户字段、按集合换字段名、排除集合或超管切换租户时，启动时注册一次插件。字段优先级是 CollectionFields、TenantFields、TenantField。CollectionFields 里的空切片表示该集合不注入。

```go
err := mongoplus.RegisterTenant(db, mongoplus.TenantConfig[int64]{
    TenantField: "tenant_id",
    ExcludeCollections: []string{"sys_config"},
    AllowOverrideTenantID: true,
    CollectionFields: map[string][]mongoplus.TenantFieldConfig[int64]{
        "order": {
            {Field: "company_id"},
            {Field: "org_id", GetTenantID: func(ctx context.Context) (int64, bool) {
                id, ok := ctx.Value("orgID").(int64)
                return id, ok && id != 0
            }},
        },
        "secret": {},
    },
})
ctx = mongoplus.WithTenantID(ctx, int64(1001))
ctx = mongoplus.WithOverrideTenantID(ctx, int64(2002)) // 仅 AllowOverrideTenantID 开启后生效。
ctx = mongoplus.SkipTenant(ctx)                        // 受控的跨租户查询，不再追加租户条件。
ctx = mongoplus.AllowGlobalOperation(ctx)              // 允许只有租户条件的批量更新或删除。
```

`AddTenantExcludeCollection` 和 `RemoveTenantExcludeCollection` 可以在运行时调整排除集合。插件和集合配置里的 `TenantField` 可以同时存在，同名字段只注入一次。`Native()` 不自动套用这层条件。DAL 会按已注册的仓储和插件追加租户、数据权限和逻辑删除；包含已删除数据时使用 `DALUnscoped`。

需要租户内唯一时，显式建立 company_id 与业务字段的复合唯一索引；字段的 unique 简写仍是整个集合唯一。

<a id="自动填充插件"></a>

### 插件二：创建人、更新人自动填充

目标：新增自动填写 created_by、updated_by，后续更新只改 updated_by；不要求每个 Create / Update 手工赋操作人。

#### 第 1 步：注册插件

业务新增 `database/operator.go`：

<!-- plugins-example: database/operator.go -->
```go
package database

import mongoplus "github.com/kuangshp/mongo-plus"

// RegisterOperator 注册创建 / 更新时的操作人填充规则。
func RegisterOperator(db *mongoplus.Database) error {
	return mongoplus.RegisterAutoFill(db, mongoplus.AutoFillConfig{
		Fields: []mongoplus.FieldConfig{
			{
				Name:     "CreatedBy",
				Getter:   mongoplus.OperatorGetter[int64](),
				OnCreate: true,
			},
			{
				Name:     "UpdatedBy",
				Getter:   mongoplus.OperatorGetter[int64](),
				OnCreate: true,
				OnUpdate: true,
			},
		},
	})
}
```

连接成功后调用 `database.RegisterOperator(db)`，处理错误后继续启动。配置说明：

| 属性 | 说明 |
| --- | --- |
| `AutoFillConfig.Fields` | 自动填充字段列表 |
| `FieldConfig.Name` | Go 字段名或 BSON 路径，CreatedBy 转成 created_by |
| `FieldConfig.Getter` | 从 Context 取值的函数；本例读取 WithActor 的 int64 |
| `FieldConfig.OnCreate` | 创建时填充 |
| `FieldConfig.OnUpdate` | 更新时填充 |

**只注册 Getter 还不够。** 下面的中间件必须调用 WithActor，且传 int64，才能与 OperatorGetter[int64] 匹配。

#### 第 2 步：定义并挂载中间件

##### go-zero：internal/middleware/operatorMiddleware.go

<!-- plugins-example: gozero/middleware/operatorMiddleware.go -->
```go
package middleware

import (
	"net/http"

	"example.com/shop/internal/authctx"
	mongoplus "github.com/kuangshp/mongo-plus"
	"github.com/zeromicro/go-zero/rest/httpx"
)

type OperatorMiddleware struct{}

func NewOperatorMiddleware() *OperatorMiddleware {
	return &OperatorMiddleware{}
}

// Handle 将登录账号写入自动填充插件读取的位置。
func (m *OperatorMiddleware) Handle(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		accountID, err := authctx.Int64(r.Context(), "accountId")
		if err != nil {
			httpx.WriteJsonCtx(r.Context(), w, http.StatusForbidden,
				map[string]string{"message": "缺少有效操作人"})
			return
		}
		ctx := mongoplus.WithActor(r.Context(), accountID)
		next(w, r.WithContext(ctx))
	}
}
```

##### Gin：internal/middleware/operator.go

<!-- plugins-example: gin/middleware/operator.go -->
```go
package middleware

import (
	"net/http"

	"github.com/gin-gonic/gin"
	mongoplus "github.com/kuangshp/mongo-plus"
)

// Operator 在登录鉴权之后设置当前操作人。
func OperatorMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		accountID, err := loginID(c, "accountId")
		if err != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "缺少有效操作人"})
			return
		}
		ctx := mongoplus.WithActor(c.Request.Context(), accountID)
		c.Request = c.Request.WithContext(ctx)
		c.Next()
	}
}
```

##### 挂载到路由

go-zero 的 ServiceContext 增加 `OperatorMiddleware rest.Middleware` 并赋值：

```go
OperatorMiddleware: middleware.NewOperatorMiddleware().Handle,
```

.api 路由组写 `middleware: AuthMiddleware,OperatorMiddleware`；同时启用租户时写 `AuthMiddleware,TenantMiddleware,OperatorMiddleware`。修改 .api 后重新生成 Handler / 路由。

Gin 的对应挂载：

```go
api := router.Group("/api")
api.Use(authMiddleware, middleware.OperatorMiddleware())
api.POST("/users", userAPI.Create)
api.PUT("/users/:id", userAPI.Update)
```

#### 第 3 步：验证效果

使用上一步创建的记录，以及同公司、同部门且有更新权限的两个登录账号：账号 42 创建，账号 43 更新。不要在创建 / 修改请求中传 created_by、updated_by。

```bash
BASE_URL=http://127.0.0.1:8888
TOKEN_CREATOR='<账号42的真实token>'
TOKEN_UPDATER='<账号43的真实token>'
curl -X POST "$BASE_URL/api/users" \
  -H "token: $TOKEN_CREATOR" -H "Content-Type: application/json" \
  -d '{"username":"审计验证-A","status":1}'

# USER_ID 必须是审计验证-A 的新 ID，不沿用其他测试记录。
USER_ID='<上一步新记录的实际ID>'
curl -X PUT "$BASE_URL/api/users/$USER_ID" \
  -H "token: $TOKEN_UPDATER" -H "Content-Type: application/json" \
  -d '{"status":0}'
```

数据库中检查：

```javascript
db.user.find(
  {username: "审计验证-A"},
  {_id: 1, status: 1, created_by: 1, updated_by: 1}
)
```

| 时机 | 通过标准 |
| --- | --- |
| 账号 42 新增成功后 | created_by=42，updated_by=42 |
| 账号 43 更新成功后 | status=0，created_by 仍为 42，updated_by=43 |
| 登录信息缺少 accountId | 操作人中间件拦截，业务方法不执行 |
| 只注册 Getter，漏挂 OperatorMiddleware | 插件取不到操作人，说明请求链路未闭环 |

自动填充只负责赋值，本身不决定是否允许该账号更新。若测试更新 MatchedCount=0，先确认租户和数据权限允许，再核对审计字段，不要关闭隔离以使测试通过。

#### 其他自动填充方式

集合配置里的 `CreatedByField`、`UpdatedByField` 配合 `WithActor`，适合创建人和更新人是同一个值的情况。创建时两个字段都写入，更新时只改更新人；缺少操作人会返回错误。生成器目前不会自动设置这两个仓储配置项，采用该方式需在业务仓储配置中指定 BSON 路径；本节的 RegisterAutoFill 方式不需要修改生成的仓储配置。

多个操作人字段、姓名或只在创建时填充，使用自动填充插件。`Name` 写成 `CreatedBy` 时会落到 `created_by`；已经是 `created_name` 这种路径则保持不变。

```go
err := mongoplus.RegisterAutoFill(db, mongoplus.AutoFillConfig{Fields: []mongoplus.FieldConfig{
    {Name: "CreatedBy", Getter: mongoplus.OperatorGetter[int64](), OnCreate: true},
    {Name: "UpdatedBy", Getter: mongoplus.OperatorGetter[int64](), OnCreate: true, OnUpdate: true},
    {Name: "created_name", Getter: mongoplus.CtxGetter[string](mongoplus.CtxOperatorKey2), OnCreate: true},
}})
ctx = mongoplus.WithActor(ctx, userID)
ctx = context.WithValue(ctx, mongoplus.CtxOperatorKey2, "ann")
```

`WithActor` 会同时写入 `OperatorGetter` 读取的位置。取值为空时跳过该字段。

<a id="数据权限插件"></a>

### 插件三：数据权限

与 gin-admin-api 一样，权限服务先查询账号、角色、部门，得到数据范围；中间件调用 BuildContext，再将 Inject 产生的条件注入插件。MongoDB 使用 BSON 条件，不拼 SQL 子查询。

| 值 | 范围（与 gin-admin-api 一致） | 验证时应看到什么 |
| --- | --- | --- |
| 1 | 全部 | 当前公司全部数据 |
| 2 | 本部门 | 直属部门的数据 |
| 3 | 本部门及下级 | 直属部门及子部门的数据 |
| 4 | 仅本人 | created_by 等于当前登录账号 |
| 5 | 自定义部门 | 角色已授权部门的数据 |

#### 第 1 步：注册插件

业务新增 `database/permission.go`：

<!-- plugins-example: database/permission.go -->
```go
package database

import mongoplus "github.com/kuangshp/mongo-plus"

// RegisterPermission 默认要求请求具有权限注入函数。
func RegisterPermission(db *mongoplus.Database) error {
	return mongoplus.RegisterDataPermission(db, mongoplus.DataPermissionConfig{
		ExcludeCollections: []string{"sys_config"},
	})
}
```

| 配置属性 | 说明 |
| --- | --- |
| `ExcludeCollections` | 显式放行数据权限检查的公共集合 |
| `AllowMissing` | 默认 false，缺少权限函数返回 ErrDataPermissionRequired；true 会恢复旧的放行行为 |
| `Strict` | 保留兼容字段；缺省已严格，放行缺少上下文需显式设置 AllowMissing |

#### 第 2 步：定义并挂载中间件

##### 先实现权限服务与条件注入器

下面三个文件属于业务的 `internal/datascope`，不是工具库内置的 RBAC 模型。本例使用如下 MongoDB 授权元数据；已有权限系统可保留自己的模型，实现同样的 Builder 契约：

| 集合 | 必需字段 |
| --- | --- |
| sys_account | _id、company_id、dept_id、role_ids（int64 数组）、is_admin（1 为超管）、status、deleted_at |
| sys_role | _id、company_id、data_scope、custom_dept_ids（int64 数组）、status、deleted_at |
| sys_dept | _id、company_id、ancestor_ids（完整祖先部门 int64 数组）、status、deleted_at |

账号 / 角色 / 部门的 _id 都为 int64，status=1 表示有效。参考项目的账号角色关联表、角色自定义部门表在本例中用数组承载；数据需要业务维护，不会因为注册插件自动出现。修改部门层级时需同步 ancestor_ids。

`internal/datascope/context.go`：

<!-- plugins-example: datascope/context.go -->
```go
package datascope

import (
	"context"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// 编号与 gin-admin-api 一致。
const (
	All          int64 = 1
	Dept         int64 = 2
	DeptAndChild int64 = 3
	Self         int64 = 4
	CustomDept   int64 = 5
)

type Context struct {
	CompanyID int64
	AccountID int64
	DeptID    int64
	DataScope int64
	DeptIDs   []int64
}

// Builder 由本节完整的 Service 实现；也便于测试中模拟授权查询。
type Builder interface {
	BuildContext(context.Context, int64, int64) (*Context, error)
}

func denyAll() bson.D {
	return bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: []int64{}}}}}
}
```

`internal/datascope/injector.go`：

<!-- plugins-example: datascope/injector.go -->
```go
package datascope

import (
	"fmt"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// Inject 只生成过滤条件，不从请求参数读取数据范围。
func Inject(scope *Context) func(string) (bson.D, error) {
	return func(collection string) (bson.D, error) {
		if scope == nil || scope.CompanyID <= 0 || scope.AccountID <= 0 {
			return nil, fmt.Errorf("缺少有效数据权限")
		}
		deptField := "dept_id"
		switch collection {
		case "user", "sys_account", "sys_post":
		case "sys_dept":
			deptField = "_id"
		default:
			return nil, fmt.Errorf("集合 %s 尚未配置权限规则", collection)
		}
		// All 指当前公司的全部；即使单独启用权限插件，也不放开公司边界。
		filter := bson.D{{Key: "company_id", Value: scope.CompanyID}}
		switch scope.DataScope {
		case All:
			return filter, nil
		case Self:
			return append(filter, bson.E{Key: "created_by", Value: scope.AccountID}), nil
		case Dept, DeptAndChild, CustomDept:
			ids := positiveIDs(scope.DeptIDs)
			if len(ids) == 0 {
				return denyAll(), nil
			}
			return append(filter, bson.E{Key: deptField, Value: bson.D{{Key: "$in", Value: ids}}}), nil
		default:
			return nil, fmt.Errorf("不支持的数据范围 %d", scope.DataScope)
		}
	}
}
```

<details>
<summary>展开完整 service.go：查询账号、角色、部门并合并权限范围</summary>

`internal/datascope/service.go` 是业务权限仓储。为避免权限查询递归触发自身，这里直接读取固定授权元数据集合，并显式约束公司、有效状态和软删除；Controller 不使用这一原生数据库入口查询普通业务数据。

<!-- plugins-example: datascope/service.go -->
```go
package datascope

import (
	"context"
	"fmt"
	"sort"

	mongoplus "github.com/kuangshp/mongo-plus"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Service 是业务权限仓储，只读取固定的授权元数据集合。
type Service struct{ db *mongo.Database }

func NewService(db *mongoplus.Database) *Service {
	if db == nil || db.Native() == nil {
		panic("权限服务需要有效数据库")
	}
	return &Service{db: db.Native()}
}

type accountDocument struct {
	ID        int64   `bson:"_id"`
	CompanyID int64   `bson:"company_id"`
	DeptID    int64   `bson:"dept_id"`
	IsAdmin   int64   `bson:"is_admin"`
	RoleIDs   []int64 `bson:"role_ids"`
}

type roleDocument struct {
	DataScope     int64   `bson:"data_scope"`
	CustomDeptIDs []int64 `bson:"custom_dept_ids"`
}

// BuildContext 从账号、角色、部门读取授权，不能信任前端提交的角色或部门列表。
func (s *Service) BuildContext(ctx context.Context, companyID, accountID int64) (*Context, error) {
	if companyID <= 0 || accountID <= 0 {
		return nil, fmt.Errorf("缺少公司或登录账号")
	}
	var account accountDocument
	if err := s.db.Collection("sys_account").FindOne(ctx, bson.D{
		{Key: "_id", Value: accountID},
		{Key: "company_id", Value: companyID},
		{Key: "status", Value: int64(1)},
		{Key: "deleted_at", Value: nil},
	}).Decode(&account); err != nil {
		return nil, err
	}

	result := &Context{CompanyID: companyID, AccountID: accountID, DeptID: account.DeptID, DataScope: Self}
	if account.IsAdmin == 1 {
		result.DataScope = All
		return result, nil
	}
	ids := positiveIDs(account.RoleIDs)
	if len(ids) == 0 {
		return result, nil
	}
	cursor, err := s.db.Collection("sys_role").Find(ctx, bson.D{
		{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}},
		{Key: "company_id", Value: companyID},
		{Key: "status", Value: int64(1)},
		{Key: "deleted_at", Value: nil},
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.WithoutCancel(ctx))
	var roles []roleDocument
	if err := cursor.All(ctx, &roles); err != nil {
		return nil, err
	}

	hasDeptScope, children := false, false
	var deptIDs []int64
	for _, role := range roles {
		switch role.DataScope {
		case All:
			result.DataScope = All
			return result, nil
		case Dept:
			hasDeptScope = true
			deptIDs = append(deptIDs, account.DeptID)
		case DeptAndChild:
			hasDeptScope = true
			children = true
			deptIDs = append(deptIDs, account.DeptID)
		case CustomDept:
			hasDeptScope = true
			deptIDs = append(deptIDs, role.CustomDeptIDs...)
		case Self:
		default:
			return nil, fmt.Errorf("角色存在未知数据范围 %d", role.DataScope)
		}
	}
	if !hasDeptScope {
		return result, nil
	}
	if children {
		if account.DeptID <= 0 {
			return nil, fmt.Errorf("账号缺少直属部门")
		}
		childIDs, err := s.findDeptIDs(ctx, companyID, bson.D{{Key: "$or", Value: bson.A{
			bson.D{{Key: "_id", Value: account.DeptID}},
			bson.D{{Key: "ancestor_ids", Value: account.DeptID}},
		}}})
		if err != nil {
			return nil, err
		}
		deptIDs = append(deptIDs, childIDs...)
	}
	result.DataScope = CustomDept
	if ids := positiveIDs(deptIDs); len(ids) > 0 {
		// 自定义部门必须属于当前公司且有效，避免残留或越界的部门授权。
		result.DeptIDs, err = s.findDeptIDs(ctx, companyID, bson.D{{Key: "_id", Value: bson.D{{Key: "$in", Value: ids}}}})
		if err != nil {
			return nil, err
		}
	}
	return result, nil
}

func (s *Service) findDeptIDs(ctx context.Context, companyID int64, condition bson.D) ([]int64, error) {
	filter := bson.D{
		{Key: "company_id", Value: companyID},
		{Key: "status", Value: int64(1)},
		{Key: "deleted_at", Value: nil},
	}
	filter = append(filter, condition...)
	cursor, err := s.db.Collection("sys_dept").Find(ctx, filter,
		options.Find().SetProjection(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(context.WithoutCancel(ctx))
	var rows []struct {
		ID int64 `bson:"_id"`
	}
	if err := cursor.All(ctx, &rows); err != nil {
		return nil, err
	}
	ids := make([]int64, 0, len(rows))
	for _, row := range rows {
		ids = append(ids, row.ID)
	}
	return positiveIDs(ids), nil
}

func positiveIDs(values []int64) []int64 {
	seen := make(map[int64]struct{}, len(values))
	for _, v := range values {
		if v > 0 {
			seen[v] = struct{}{}
		}
	}
	ids := make([]int64, 0, len(seen))
	for id := range seen {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool { return ids[i] < ids[j] })
	return ids
}
```

</details>

多个角色按“全部优先、部门范围取并集、只有本人范围时仅本人”合并。自定义部门为空时拒绝全部。超管的 All 仍限当前公司，不跳过租户；数据范围、角色和部门集合都从数据库取得，不信任请求中的同名字段。

##### go-zero：internal/middleware/dataPermissionMiddleware.go

<!-- plugins-example: gozero/middleware/dataPermissionMiddleware.go -->
```go
package middleware

import (
	"context"
	"example.com/shop/internal/authctx"
	"example.com/shop/internal/datascope"
	mongoplus "github.com/kuangshp/mongo-plus"
	"github.com/zeromicro/go-zero/rest/httpx"
	"net/http"
)

type DataPermissionMiddleware struct{ service datascope.Builder }

func NewDataPermissionMiddleware(service datascope.Builder) *DataPermissionMiddleware {
	if service == nil {
		panic("必须注入数据权限服务")
	}
	return &DataPermissionMiddleware{service: service}
}

// Handle 先 BuildContext，再将 Inject 返回的函数交给插件。
func (m *DataPermissionMiddleware) Handle(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		companyID, companyErr := authctx.Int64(r.Context(), "companyId")
		accountID, accountErr := authctx.Int64(r.Context(), "accountId")
		if companyErr != nil || accountErr != nil {
			httpx.WriteJsonCtx(r.Context(), w, http.StatusForbidden, map[string]string{"message": "缺少登录身份或公司授权"})
			return
		}
		scope, err := m.service.BuildContext(r.Context(), companyID, accountID)
		if err != nil || scope == nil {
			httpx.WriteJsonCtx(r.Context(), w, http.StatusForbidden, map[string]string{"message": "获取数据权限失败"})
			return
		}
		ctx := mongoplus.WithDataPermission(r.Context(), datascope.Inject(scope))
		ctx = context.WithValue(ctx, "deptId", scope.DeptID)
		ctx = context.WithValue(ctx, "dataScopeContext", scope)
		next(w, r.WithContext(ctx))
	}
}
```

##### Gin：internal/middleware/dataPermission.go

<!-- plugins-example: gin/middleware/dataPermission.go -->
```go
package middleware

import (
	"context"
	"example.com/shop/internal/datascope"
	"github.com/gin-gonic/gin"
	mongoplus "github.com/kuangshp/mongo-plus"
	"net/http"
)

// DataPermissionMiddleware 采用与 gin-admin-api 相同的 BuildContext → Inject 流程。
func DataPermissionMiddleware(service datascope.Builder) gin.HandlerFunc {
	if service == nil {
		panic("必须注入数据权限服务")
	}
	return func(c *gin.Context) {
		companyID, companyErr := loginID(c, "companyId")
		accountID, accountErr := loginID(c, "accountId")
		if companyErr != nil || accountErr != nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "缺少登录身份或公司授权"})
			return
		}
		scope, err := service.BuildContext(c.Request.Context(), companyID, accountID)
		if err != nil || scope == nil {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"message": "获取数据权限失败"})
			return
		}
		ctx := mongoplus.WithDataPermission(c.Request.Context(), datascope.Inject(scope))
		ctx = context.WithValue(ctx, "deptId", scope.DeptID)
		ctx = context.WithValue(ctx, "dataScopeContext", scope)
		c.Request = c.Request.WithContext(ctx)
		c.Set("dataScopeContext", scope)
		c.Next()
	}
}
```

##### 挂载到路由

go-zero 的 ServiceContext 增加 `DataPermissionMiddleware rest.Middleware` 并赋值：

```go
DataPermissionMiddleware: middleware.NewDataPermissionMiddleware(datascope.NewService(db)).Handle,
```

.api 路由组写 `middleware: AuthMiddleware,DataPermissionMiddleware`。三个插件同时使用时，顺序为 `AuthMiddleware,TenantMiddleware,OperatorMiddleware,DataPermissionMiddleware`。

Gin 的对应挂载：

```go
api := router.Group("/api")
api.Use(authMiddleware, middleware.DataPermissionMiddleware(datascope.NewService(db)))
api.GET("/users/:id", userAPI.Get)
api.PUT("/users/:id", userAPI.Update)
```

#### 第 3 步：验证效果

先在业务后台或隔离的测试数据中准备账号、角色和部门；不要把下表的 role / data_scope 作为 HTTP 入参来切换权限。每次由权限服务从数据库重新计算。

| 测试记录 | company_id | dept_id | created_by |
| --- | --- | --- | --- |
| A | 1001 | 10 | 42 |
| B | 1001 | 11（10 的子部门） | 43 |
| C | 1001 | 20 | 44 |
| D | 2002 | 10 | 45 |

部门 11 的 ancestor_ids 中应包含 int64 的 10，所有授权元数据的 status=1、deleted_at=null。下面以公司 1001、直属部门 10 的账号 42 为验证账号；通过业务后台依次配置其角色的数据范围，或准备五个对应权限账号：

| 配置 | 查询 A | 查询 B | 查询 C | 查询 D |
| --- | --- | --- | --- | --- |
| All=1 | 能看到 | 能看到 | 能看到 | 看不到 |
| Dept=2 | 能看到 | 看不到 | 看不到 | 看不到 |
| DeptAndChild=3 | 能看到 | 能看到 | 看不到 | 看不到 |
| Self=4 | 能看到 | 看不到 | 看不到 | 看不到 |
| CustomDept=5，custom_dept_ids=[20] | 看不到 | 看不到 | 能看到 | 看不到 |
| CustomDept=5，custom_dept_ids=[] | 看不到 | 看不到 | 看不到 | 看不到 |

用每个真实 token 依次请求上述记录的实际 ID：

```bash
BASE_URL=http://127.0.0.1:8888
TOKEN_SCOPE='<对应数据范围账号的真实token>'
RECORD_A_ID='<记录A的实际ID>'
curl "$BASE_URL/api/users/$RECORD_A_ID" -H "token: $TOKEN_SCOPE"
# 换成 B / C / D 的实际 ID，按上表核对返回。
```

| 异常 / 写入场景 | 通过标准 |
| --- | --- |
| Dept=2 的账号修改记录 B | MatchedCount=0，记录不变 |
| Self=4 的账号修改其他人记录 | MatchedCount=0，记录不变 |
| 角色范围未知，或读取授权失败 | 中间件拒绝请求，不退化为全部权限 |
| 查未配置规则的集合 | Inject 返回错误 |
| 漏挂权限中间件 | 默认严格模式返回 ErrDataPermissionRequired |
| 相同接口的多个角色 | All 优先；部门范围按并集；不要误测成各范围交集 |

新增时的部门归属由业务从权限服务写入的 deptId 得到，租户和创建人字段由另外两个插件填写。跨公司读取与更新即使在 All 范围也不能命中。权限测试应默认关闭读缓存，避免把后端缓存行为误当成授权查询结果。

#### 严格检查与授权边界

数据权限不内置“本部门 / 仅本人”规则。中间件按集合返回 BSON 条件，查询、更新、删除以及走仓储过滤的关联都会带上。注册后默认启用严格检查，未排除的集合缺少权限上下文会返回 `ErrDataPermissionRequired`。`Strict` 兼容旧调用；只有显式设置 `AllowMissing: true` 才恢复缺少上下文时放行的旧行为。鉴权中间件可先用 `RequireDataPermission(ctx)` 检查注入，说明见[生产接入](docs/production.md#租户与严格权限)。

```go
err := mongoplus.RegisterDataPermission(db, mongoplus.DataPermissionConfig{
    Strict: true,
    ExcludeCollections: []string{"sys_config"},
})
ctx = mongoplus.WithDataPermission(ctx, func(collection string) (bson.D, error) {
    if collection == "order" {
        return bson.D{{Key: "created_by", Value: userID}}, nil
    }
    return nil, fmt.Errorf("集合 %s 尚未配置数据权限", collection)
})
ctx = mongoplus.SkipDataPermission(ctx) // 受控的全量查询。
```

`AddDataPermissionExcludeCollection` 和 `RemoveDataPermissionExcludeCollection` 用于运行时调整排除集合。

权限函数返回空条件表示明确放行当前集合，不等于拒绝访问。未识别的集合应返回错误，公共集合通过 ExcludeCollections 显式排除。新增数据的业务授权仍需在业务层检查；数据权限过滤不能代替登录鉴权。

### 三个插件一起接入：共用装配代码

前三个插件各自按“注册、中间件、验证”说明。本节只把启动代码放在一起，便于一次接入；RegisterPlugins 替代逐个注册，不要重复执行。中间件选择自己的框架版本。

#### 共用注册入口：database/plugins.go

<!-- plugins-example: database/plugins.go -->
```go
package database

import mongoplus "github.com/kuangshp/mongo-plus"

// RegisterPlugins 在连接成功后、开始处理请求前执行一次。
func RegisterPlugins(db *mongoplus.Database) error {
	if err := RegisterTenant(db); err != nil {
		return err
	}
	if err := RegisterOperator(db); err != nil {
		return err
	}
	return RegisterPermission(db)
}
```

#### go-zero 装配：ServiceContext 创建具体中间件

下面是 `internal/svc/serviceContext.go` 的插件接入版本。`authMiddleware` 是业务已经实现的登录中间件，例如项目已有的 JWT / Session 登录中间件 Handle；它负责验证 token 并写入前面约定的 Context key。数据库连接读取项目运行配置，不在中间件中建立 Client。

<!-- plugins-example: gozero/svc/serviceContext.go -->
```go
package svc

import (
	"context"
	"fmt"
	"time"

	"example.com/shop/database"
	"example.com/shop/internal/config"
	"example.com/shop/internal/dal/repository"
	"example.com/shop/internal/datascope"
	"example.com/shop/internal/middleware"
	mongoplus "github.com/kuangshp/mongo-plus"
	"github.com/zeromicro/go-zero/rest"
)

type ServiceContext struct {
	Config                   config.Config
	MongoDB                  *mongoplus.Database
	UserRepository           repository.UserRepository
	AuthMiddleware           rest.Middleware
	TenantMiddleware         rest.Middleware
	OperatorMiddleware       rest.Middleware
	DataPermissionMiddleware rest.Middleware
}

// NewServiceContext 保留配置注入，并显式接收项目已经实现的登录中间件。
func NewServiceContext(c config.Config, authMiddleware rest.Middleware) *ServiceContext {
	if authMiddleware == nil {
		panic("必须注入登录鉴权中间件")
	}
	if !c.MongoConfig.Enabled {
		panic("本示例接口需要开启 MongoConfig.Enabled")
	}
	seconds := c.MongoConfig.TimeoutSeconds
	if seconds <= 0 || seconds > 3600 {
		panic("MongoConfig.TimeoutSeconds 必须在 1 到 3600 之间")
	}
	timeout := time.Duration(seconds) * time.Second
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	db, err := mongoplus.Connect(ctx, mongoplus.Config{
		URI:         c.MongoConfig.URI,
		Database:    c.MongoConfig.Database,
		MaxPoolSize: c.MongoConfig.MaxPoolSize,
		Timeout:     timeout,
		Log:         c.MongoConfig.Log,
	})
	if err != nil {
		panic(fmt.Errorf("初始化 MongoDB 失败: %w", err))
	}
	if err := database.RegisterPlugins(db); err != nil {
		closeCtx, closeCancel := context.WithTimeout(context.Background(), 5*time.Second)
		_ = db.Close(closeCtx)
		closeCancel()
		panic(fmt.Errorf("注册数据库插件失败: %w", err))
	}
	mongoplus.Use(db)
	return &ServiceContext{
		Config:                   c,
		MongoDB:                  db,
		UserRepository:           repository.NewUserRepository(db),
		AuthMiddleware:           authMiddleware,
		TenantMiddleware:         middleware.NewTenantMiddleware().Handle,
		OperatorMiddleware:       middleware.NewOperatorMiddleware().Handle,
		DataPermissionMiddleware: middleware.NewDataPermissionMiddleware(datascope.NewService(db)).Handle,
	}
}

// Close 由原有启动入口在停止 HTTP 接收后调用。
func (s *ServiceContext) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return s.MongoDB.Close(ctx)
}
```

主入口对应改为 `svc.NewServiceContext(c, authMiddleware)`，继续调用生成的 `handler.RegisterHandlers(server, svcCtx)`，并保留模板原有停止 HTTP / 关闭连接的生命周期代码。已有项目不必替换整个 ServiceContext，合并这几个字段与初始化即可。

#### go-zero 路由：.api 声明执行顺序

完整 `shop.api`：

<!-- plugins-example: gozero/shop.api -->
```text
syntax = "v1"

type CreateUserReq {
    Username string `json:"username" validate:"required"`
    Status int64 `json:"status,optional"`
}

type GetUserReq {
    Id int64 `path:"id" validate:"required,gt=0"`
}

type UpdateUserReq {
    Id int64 `path:"id" validate:"required,gt=0"`
    Status int64 `json:"status" validate:"oneof=0 1 2"`
}

type UserResp {
    Id int64 `json:"id"`
    Username string `json:"username"`
    Status int64 `json:"status"`
}

@server (
    prefix: /api
    middleware: AuthMiddleware,TenantMiddleware,OperatorMiddleware,DataPermissionMiddleware
)
service shop-api {
    @handler CreateUser
    post /users (CreateUserReq) returns (UserResp)

    @handler GetUser
    get /users/:id (GetUserReq) returns (UserResp)

    @handler UpdateUser
    put /users/:id (UpdateUserReq)
}
```

```bash
mongoplus goctl api go --api shop.api --dir . --style=goZero
go mod tidy
```

生成的 routes.go 会引用 ServiceContext 的四个中间件，执行顺序为：

```text
AuthMiddleware
  → TenantMiddleware
  → OperatorMiddleware
  → DataPermissionMiddleware
  → Handler
  → Logic
  → Repository
```

新增 / 修改 .api 后重新生成；goctl 会保留已有业务文件，需手动合并本节的 ServiceContext 与中间件实现。**生成出的空 Handle 中间件桩不会自动获得插件逻辑**，应按前面第 2 步补全。登录接口放在不含上述中间件的路由组。

#### Gin 装配：初始化插件并挂载到路由

启动入口在建立 db 后执行：

```go
if err := database.RegisterPlugins(db); err != nil {
    return err
}
if err := utils.InitValidation(); err != nil {
    return err
}
users := repository.NewUserRepository(db)
userAPI := user.NewUser(users)
router := gin.New()

// authMiddleware 为业务已经实现的登录鉴权函数；不是本库提供的占位函数。
userrouter.Register(router, authMiddleware, userAPI, datascope.NewService(db))
```

模块路由文件 `internal/router/user/routes.go`：

<!-- plugins-example: gin/router/user/routes.go -->
```go
package userrouter

import (
	userapi "example.com/shop/internal/api/user"
	"example.com/shop/internal/datascope"
	"example.com/shop/internal/middleware"

	"github.com/gin-gonic/gin"
)

// Register 只装配路由，不注册数据库插件、不新建连接。
func Register(router *gin.Engine, authMiddleware gin.HandlerFunc, userAPI userapi.IUser, service datascope.Builder) {
	if authMiddleware == nil {
		panic("必须注入登录鉴权中间件")
	}
	api := router.Group("/api")
	api.Use(
		authMiddleware,
		middleware.TenantMiddleware(),
		middleware.OperatorMiddleware(),
		middleware.DataPermissionMiddleware(service),
	)
	api.POST("/users", userAPI.Create)
	api.GET("/users/:id", userAPI.Get)
	api.PUT("/users/:id", userAPI.Update)
}
```

启动代码需导入业务的 database、repository、datascope、user、utils，以及别名 `userrouter "example.com/shop/internal/router/user"`。三个插件、校验器、Repository 均在启动阶段初始化；Handler 中只传递请求 Context。

<a id="插件示例仓储操作"></a>

### 验证接口：使用普通仓储增删改查

以下完整业务片段配合上面三个插件一起使用。创建请求仍只接收 username / status；company_id、created_by、updated_by 由插件填写，部门归属由业务从鉴权后的 deptId 取得。

#### go-zero：CreateUser Logic

把生成的 CreateUser 方法体改为下面逻辑，保留生成的构造函数与 Logger。导入业务 authctx、entity、types：

```go
func (l *CreateUserLogic) CreateUser(req *types.CreateUserReq) (*types.UserResp, error) {
    deptID, err := authctx.Int64(l.ctx, "deptId")
    if err != nil {
        return nil, err
    }
    record := &entity.UserEntity{
        Username: req.Username,
        Status:   req.Status,
        DeptID:   deptID,
    }
    if err := l.svcCtx.UserRepository.Create(l.ctx, record); err != nil {
        return nil, err
    }
    return &types.UserResp{
        Id:       record.ID,
        Username: record.Username,
        Status:   record.Status,
    }, nil
}
```

GetUser 的方法体如下，保留生成的构造函数和类型：

```go
func (l *GetUserLogic) GetUser(req *types.GetUserReq) (*types.UserResp, error) {
    record, err := l.svcCtx.UserRepository.FindById(l.ctx, req.Id)
    if err != nil {
        return nil, err
    }
    return &types.UserResp{
        Id:       record.ID,
        Username: record.Username,
        Status:   record.Status,
    }, nil
}
```

UpdateUser 仅修改允许的业务字段；导入业务 dao 和 mongoplus，操作人由中间件与插件自动填充：

```go
func (l *UpdateUserLogic) UpdateUser(req *types.UpdateUserReq) error {
    result, err := l.svcCtx.UserRepository.UpdateById(
        l.ctx,
        req.Id,
        dao.UserEntity.Status.Set(req.Status),
    )
    if err != nil {
        return err
    }
    if result.MatchedCount == 0 {
        return mongoplus.ErrNotFound
    }
    return nil
}
```

Handler 的绑定、校验与响应继续由 goctl 模板生成。

#### Gin：请求 DTO 与响应 VO

新增 DTO 使用前文的 `dto.CreateUserDTO`；下列两个文件分别保存 URI / 更新请求和接口响应：

<!-- plugins-example: gin/request/request.go -->
```go
package request

import "github.com/kuangshp/mongo-plus/decimaljson"

type UserID struct {
	ID int64 `uri:"id" binding:"required,gt=0"`
}

// 使用指针区分未传与合法的 status=0，只有本接口允许更新的字段才放进 DTO。
type UpdateStatus struct {
	Status *int64 `json:"status" binding:"required,oneof=0 1 2"`
}

func (value *UpdateStatus) UnmarshalJSON(data []byte) error {
	type plain UpdateStatus
	return decimaljson.Unmarshal(data, (*plain)(value))
}
```

<!-- plugins-example: gin/response/user.go -->
```go
package response

type User struct {
	ID       int64  `json:"id"`
	Username string `json:"username"`
	Status   int64  `json:"status"`
}
```

#### Gin：模块控制器

将前面的 user.go 合并为下面的扩展版本（不要在同一个包重复定义 IUser / user）。请求绑定使用 DTO，数据库调用使用请求 Context，响应使用单独 VO：

<!-- plugins-example: gin/user.go -->
```go
package user

import (
	"example.com/shop/internal/api/user/request"
	"example.com/shop/internal/api/user/response"
	"example.com/shop/internal/authctx"
	"example.com/shop/internal/dal/dao"
	"example.com/shop/internal/dal/dto"
	"example.com/shop/internal/dal/entity"
	"example.com/shop/internal/dal/repository"
	"example.com/shop/utils"

	"github.com/gin-gonic/gin"
)

type IUser interface {
	Create(*gin.Context)
	Get(*gin.Context)
	Update(*gin.Context)
}

type user struct {
	userRepository repository.UserRepository
}

var _ IUser = (*user)(nil)

func NewUser(users repository.UserRepository) IUser {
	return &user{userRepository: users}
}

func (u *user) Create(c *gin.Context) {
	var req dto.CreateUserDTO
	if err := c.ShouldBindJSON(&req); err != nil {
		utils.Fail(c, utils.ShowErrorMessage(err))
		return
	}
	deptID, err := authctx.Int64(c.Request.Context(), "deptId")
	if err != nil {
		utils.Fail(c, "缺少有效部门授权")
		return
	}
	record := &entity.UserEntity{
		Username: req.Username,
		Status:   req.Status,
		DeptID:   deptID,
	}
	if err := u.userRepository.Create(c.Request.Context(), record); err != nil {
		utils.Fail(c, "创建用户失败")
		return
	}
	utils.Success(c, response.User{
		ID:       record.ID,
		Username: record.Username,
		Status:   record.Status,
	})
}

func (u *user) Get(c *gin.Context) {
	var req request.UserID
	if err := c.ShouldBindUri(&req); err != nil {
		utils.Fail(c, utils.ShowErrorMessage(err))
		return
	}
	record, err := u.userRepository.FindById(c.Request.Context(), req.ID)
	if err != nil {
		utils.Fail(c, "记录不存在或无权访问")
		return
	}
	// 仅返回本接口需要的字段，避免直接输出全部 Entity / 审计信息。
	utils.Success(c, response.User{
		ID:       record.ID,
		Username: record.Username,
		Status:   record.Status,
	})
}

func (u *user) Update(c *gin.Context) {
	var req request.UserID
	if err := c.ShouldBindUri(&req); err != nil {
		utils.Fail(c, utils.ShowErrorMessage(err))
		return
	}
	var body request.UpdateStatus
	if err := c.ShouldBindJSON(&body); err != nil {
		utils.Fail(c, utils.ShowErrorMessage(err))
		return
	}
	result, err := u.userRepository.UpdateById(
		c.Request.Context(),
		req.ID,
		dao.UserEntity.Status.Set(*body.Status),
	)
	if err != nil {
		utils.Fail(c, "更新失败")
		return
	}
	if result.MatchedCount == 0 {
		utils.Fail(c, "记录不存在或无权操作")
		return
	}
	utils.Success(c, gin.H{"modified": result.ModifiedCount})
}
```

#### 三步接入后的检查清单

| 检查动作 | 预期结果 |
| --- | --- |
| 公司 1001、账号 42、部门 10 新增 | 文档有 company_id=1001、created_by=42、updated_by=42、dept_id=10 |
| 同公司同部门账号 43（同公司同部门，拥有更新范围）更新状态 | created_by 保持 42，updated_by 变为 43 |
| 公司 2002 查询该 ID | 不命中，不能读到公司 1001 的记录 |
| 本部门范围的公司 1001、部门 20 账号查询该 ID | 不命中，不能读到部门 10 的记录 |
| 少挂 TenantMiddleware | 不会从 companyId 自动转换出租户插件 Context；应排查挂载 |
| 少挂 OperatorMiddleware | Getter 取不到当前账号；本例接口应始终挂载它，不能只注册字段 |
| 少挂 DataPermissionMiddleware | 严格权限插件返回 ErrDataPermissionRequired |
| 登录 Context 缺少对应授权值 | 前面具体中间件返回 403，不继续执行 Handler |

插件与中间件必须针对同一个业务数据库生效。多个数据源分别注册，并将对应 Repository 注入业务；不要在请求中切换全局默认连接。Native / DALNative 及复杂关联子管道仍由业务明确处理授权，插件注册不自动建表、补数据或创建索引。

### 敏感字段与数据源扩展

#### 敏感字段

开启加密时明文必须标记 `bson:"-"`，密文和盲索引写入文档。等值查询使用索引字段，不要直接比较明文。读出默认脱敏；`WithSensitivePlaintext` 返回解密结果，`WithSensitiveCiphertext` 返回库存值。

```go
phone := mongoplus.PhoneField("Phone")
phone.EncryptAtRest = true
sensitive, err := mongoplus.RegisterSensitive(db, mongoplus.SensitiveConfig{
    Key:    []byte("0123456789abcdef"), // 至少 16 字节，会派生加密密钥和索引密钥。
    Fields: []mongoplus.SensitiveFieldConfig{phone},
})
condition := sensitive.PhoneEq("13800008000") // 条件落在 phone_index。
```

`Phone`、`PhoneCipher`、`PhoneIndex` 分别对应业务字段、密文字段和索引字段。未开启 `EncryptAtRest` 时，密文字段保存规范化后的值，索引仍然是 HMAC。更新 `phone` 路径时会改写到密文和索引，不把明文留在业务字段上。

也可以只注册 Key，通过实体标签声明字段。`mongoplus` 和兼容的 `gormplus` 标签均支持；新增、更新、读取和仓储的 `SensitiveEq` 使用同一份实体元数据，不需要在 `Fields` 中重复声明：

```go
type UserEntity struct {
    ID          int64  `bson:"_id"`
    Phone       string `bson:"-" mongoplus:"cipher:PhoneCipher;index:PhoneIndex;encrypt:true"`
    PhoneCipher string `bson:"encrypted_mobile"`
    PhoneIndex  string `bson:"mobile_lookup"`
}

_, err := mongoplus.RegisterSensitive(db, mongoplus.SensitiveConfig{
    Key: []byte("0123456789abcdef"), // 示例密钥，生产从密钥管理服务读取。
})
// userRepository 为当前实体的仓储，SensitiveEq 自动使用 mobile_lookup。
condition := userRepository.SensitiveEq("Phone", "13800008000")
```

| 标签属性 | 含义 |
| --- | --- |
| `cipher` | 保存值的 Go 字段名，必须为可导出的 string；实际路径使用该字段的 BSON 标签 |
| `index` | 盲索引 Go 字段名，必须为可导出的 string；实际路径使用该字段的 BSON 标签 |
| `encrypt` | `true` 开启加密；省略或 `false` 时保存规范化值 |

开启 `EncryptAtRest` / `encrypt:true` 时，明文字段必须使用 `bson:"-"`，否则写入前返回错误，避免创建时重复持久化明文。密文和索引字段不能设置 `bson:"-"`；缺少字段或不完整标签会返回错误。不同实体的同名字段独立解析。显式 `SensitiveConfig.Fields` 优先于标签；未显式指定的路径从实体 BSON 标签推导，显式持久化路径必须与实体 BSON 标签一致，不一致会在首次编译该实体元数据时返回错误。`bson:"-"` 的明文允许配置虚拟查询别名。所有敏感字段的明文、密文、索引路径都不能互相重叠，包括父子路径和不同 Go 字段使用相同 BSON 标签的情况。

`UpdateById`、`UpdateMapById`、`UpdateByWrapper` 支持对逻辑字段执行 Set，并同时刷新密文和盲索引；Unset 同时删除密文和索引。其他操作符和逻辑字段的父子路径更新会报错。等值查询优先使用仓储 `SensitiveEq`；插件的 `Eq`/`PhoneEq` 仍按显式配置或默认约定生成条件，不推断某个实体的标签。升级后重新生成 `_gen.go` 以在生成的仓储接口上使用新方法；手写 mock 需要补充 `SensitiveEq`。

修复不会自动清理旧版本已经写入的明文字段或恢复旧密文。受影响数据应先核对明文、密文与业务记录，再通过独立迁移重建密文和盲索引并删除遗留明文。DAL/Native 原生操作仍由业务负责敏感字段规则。

##### 嵌套敏感字段与失败写入

标签支持命名嵌套结构体、结构体指针、匿名嵌入、`bson:",inline"`，以及切片和数组中的结构体。空指针或空数组元素会跳过，不会为缺失对象自动分配内存。完整 Go 路径与实际 BSON 路径共用同一份元数据：

```go
type ContactEntity struct {
    Phone       string `bson:"-" mongoplus:"cipher:PhoneCipher;index:PhoneIndex;encrypt:true"`
    PhoneCipher string `bson:"secure_phone"`
    PhoneIndex  string `bson:"lookup_phone"`
}

type CustomerEntity struct {
    ID       int64           `bson:"_id"`
    Profile  *ContactEntity  `bson:"profile,omitempty"`
    Contacts []ContactEntity `bson:"contacts"`
}

// 使用完整 Go 路径或逻辑 BSON 路径；条件实际落在 profile.lookup_phone。
condition := customerRepository.SensitiveEq("Profile.Phone", "13800008000")

// 对数组的查询匹配任一元素；更新指定元素时必须提供数字下标。
condition = customerRepository.SensitiveEq("Contacts.Phone", "13800008000")
_, err := customerRepository.UpdateMapById(ctx, id, map[string]any{
    "profile.phone":    "13900009000",
    "contacts.0.phone": "13700007000",
})
```

同名字段在多个嵌套对象中出现时，`SensitiveEq("Phone", ...)` 会返回歧义错误，应使用 `Profile.Phone` 等完整路径。显式配置也可以使用 `PhoneField("Profile.Phone")`，仅覆盖该嵌套字段的规范化、加密及展示规则。

更新包含敏感字段的整个父对象或数组会报错，防止绕过加密；应逐个更新敏感叶子字段，数组使用数字下标。普通非敏感叶子字段仍可正常更新。敏感实体暂不支持存放在 map/interface 动态容器中，也不支持递归敏感实体类型；这些输入会明确返回错误。普通 map/interface BSON 数据不受影响。

创建时仅复制敏感写入涉及的指针链和切片，保留原 Go 类型及其他字段的编解码行为。`Create` 失败不修改原实体；有序 `CreateBatch` 只回填已获确认的成功前缀，失败和未执行记录保持原样，即使输入之间共享嵌套指针也不会串改。事务内成功的创建语句仍会回填；后续事务回滚不自动撤销内存中的回填，与主键回填语义一致。

旧版本若已跳过嵌套敏感字段，修复后不会凭空恢复缺失值；需从可信业务来源恢复，并核对密文、盲索引及缓存失效。

#### 主从数据源

连接由调用方先建好。从库 URI 带上 secondary 读偏好。没有从库时读操作使用主库。第一个注册的名称是 `Auto` 的默认数据源。

```go
err := mongoplus.DS.Register("default", mongoplus.DataSourceGroupConfig{
    Master: primary,
    Slaves: []*mongoplus.Database{secondaryA, secondaryB},
})
db, err := mongoplus.DS.Auto(ctx)                         // 未标记时走主库。
db, err = mongoplus.DS.Auto(mongoplus.DSWithRead(ctx))    // 从库轮询。
db, err = mongoplus.DS.Auto(mongoplus.DSWithWrite(ctx))   // 写标记优先，走主库。
db, err = mongoplus.DS.Auto(mongoplus.DSWithName(ctx, "analytics"))
```

`DS.Read`、`DS.Write`、`DS.Ping` 和 `DS.Close` 用于显式选择、健康检查和关闭。同一个数据库包装器只关闭一次。

### 控制台命令日志与调用位置

通过连接配置开启类似 GORM 的日志，打印时间、业务调用处的 `文件路径:行号`、耗时、条数、实际数据库名和多行 mongosh 风格命令。终端或 IDE 支持识别路径时可点击跳转；生成的 `_gen.go` 和库内部调用会被跳过。

```go
db, err := mongoplus.Connect(ctx, mongoplus.Config{
    URI:      "mongodb://127.0.0.1:27017",
    Database: "shop",
    Log: mongoplus.LogConfig{
        Level:           mongoplus.LogInfo, // info 全部命令，warn 慢命令和错误，error 仅错误。
        Format:          "shell",          // 默认 shell，多行显示；json 保留单行 Extended JSON。
        SlowThresholdMS: 200,               // 0 使用 200ms。
        Colorful:        true,              // 可选 ANSI 颜色；默认 false。
        HideParameters:  true,              // 生产建议开启，隐藏参数值；false 时常见敏感字段仍脱敏。
        MaxLength:       4096,              // 命令内容字节上限；0 使用 4096。
    },
})
if err != nil { return err }
```

使用 YAML 加载 `mongoplus.Config` 时对应配置为：

```yaml
mongodb:
  uri: mongodb://127.0.0.1:27017
  database: shop
  log:
    level: info
    format: shell
    slowThresholdMs: 200
    colorful: true
    hideParameters: true
    sensitiveFields: ["identity_number"]
    maxLength: 4096
    includeErrorDetails: false
```

输出格式如下，`db` 对应上方 `[db:shop]` 标注的实际数据库：

```text
2026/10/16 10:20:30 /work/shop/internal/service/user.go:77
[2.135ms] [rows:1] [parameters:redacted] [db:shop]
db.users.find({
  name: "?"
}).limit("?")
```

开发环境关闭 `HideParameters` 后，普通插入可显示为：

```javascript
db.users.insertOne({
  name: "张三",
  age: 28,
  email: "[REDACTED]",
  tags: ["dev", "mongodb"],
  createdAt: ISODate("2026-10-16T12:39:30.123Z")
})
```

日期显示实际 UTC 时间，不替换为执行日志时的 `new Date()`；ObjectID 显示为 `ObjectId("...")`，安全整数直接显示数值，超过 JavaScript 安全整数范围的 int64 保留 `NumberLong("...")`。默认隐藏 Session 等协议元数据，保留过滤、更新和业务选项。批量更新/删除及其他不能直接映射的命令使用多行 `db.runCommand({...})`，不丢弃各子操作的参数。特殊集合名使用 `db.getCollection("...")`。

`Format: "json"` / `format: json` 可恢复单行协议展示；默认两种格式共用脱敏规则与长度限制。普通 shell 是诊断展示，已脱敏、隐藏或截断的日志不能直接重放，也不包含可重放事务所需的 Session 信息。

需要复制到 mongosh 直接执行时，在受控开发环境显式开启可执行模式：

```yaml
MongoConfig:
  Log:
    level: info
    format: shell
    replayable: true
    hideParameters: false
    maxLength: 16384
```

手写 Go 配置对应 `LogConfig{Level: LogInfo, Format: "shell", Replayable: true}`。此模式使用真实参数（包括原本会脱敏的邮箱、密码等字段），不应用 `SensitiveFields`，不适用于生产日志。普通 shell 模式会标注 `[parameters:redacted]`，表示它是预览。

可执行模式按实际集合生成 `db.user.insertOne({...});`、`updateOne`、`updateMany`、`deleteOne` 等语句；保留长整数 `NumberLong("28")`、双精度 `Double(1)`、日期 `ISODate("...")` 等 BSON 类型，避免复制后违反原有集合校验。游标和选项中的数值则按 mongosh API 要求显示为普通 number。超出长度限制时整条省略并提示，不输出残缺的可执行语句。执行前切换到 `[db:...]` 标注的数据库；复制命令正文即可，时间、耗时和路径为日志信息。

getMore、提交/回滚事务依赖原有游标或 Session，不能独立重放，日志会明确标注；事务内单条 CRUD 复制后作为独立操作执行，不恢复原事务。真实 mongosh 重放回归会执行增删改查、聚合及复合操作，并通过官方驱动核对写入的值和 BSON 类型。

默认 `level` 为空或 `silent` 时关闭日志；日志默认写入标准输出。可通过 Go 配置的 `Writer` 接入输出流，例如 `Writer: log.Writer()` 复用项目标准库日志的目标。go-zero 模板已包含 `MongoConfig.Log` 配置，生成服务后修改对应 YAML 即可；独立的命令日志不自动受服务顶层 `Log` 或 `LogRotation` 控制。

密码、令牌、手机号、邮箱等常见字段会默认脱敏，`SensitiveFields` 可追加业务字段/点路径。错误详情默认隐藏，避免数据库错误回显查询值；仅受控诊断时开启 `IncludeErrorDetails`。命令内容按 `MaxLength` 限制并保留 UTF-8 边界，配置属性见[日志安全](docs/production.md#命令日志安全)。

使用已有官方驱动连接时，需要在创建 Client 前安装监控器：

```go
monitor, err := mongoplus.NewCommandLogger(mongoplus.LogConfig{Level: mongoplus.LogInfo})
if err != nil { return err }
client, err := mongo.Connect(options.Client().ApplyURI(uri).SetMonitor(monitor))
if err != nil { return err }
db := mongoplus.NewDatabase(client.Database("shop"))
```

监控器覆盖 Client 的所有数据库及原生命令调用，需在连接创建前配置；已有自定义 `CommandMonitor` 时应组合回调，避免被 `SetMonitor` 替换。命中缓存时没有数据库命令，因此不输出命令日志；启用 SingleFlight 的查询仍保留发起者的业务位置。耗时是驱动单条命令的耗时，重试和游标 `getMore` 会分别打印。读命令的 `rows` 是本批返回数量，写命令取服务器返回的 `n`（更新时为匹配条数），未知时显示 `-`。使用 `-trimpath` 构建时，运行时路径可能无法映射到本机源文件。

```go
db.Observer = func(ctx context.Context, event mongoplus.Observation) {
    if event.Duration > time.Second || event.Err != nil {
        log.Printf("%s.%s duration=%s err=%v",
            event.Collection, event.Operation, event.Duration, event.Err)
    }
}
```

在启动时设置 Observer，不在并发请求中更换。Create、CreateBatch、FindOne、FindList、Update、Delete 会报告耗时与错误，不默认记录请求文档或连接密钥。`RegisterSlowQuery` 在现有 Observer 之外记录超过阈值的集合和操作，应在设置 Observer 之后调用。Threshold 为 0 时使用 200ms。这里没有可复制执行的 SQL 文本。

```go
err := mongoplus.RegisterSlowQuery(db, mongoplus.SlowQueryConfig{
    Threshold: 200 * time.Millisecond,
    Logger: func(ctx context.Context, info mongoplus.SlowQueryInfo) {
        log.Printf("%s.%s cost=%s err=%v", info.Collection, info.Operation, info.Duration, info.Error)
    },
})
```

<a id="dal-复杂查询"></a>

## 5. 复杂查询如何使用 DAL

MongoDB 使用查询文档和聚合管道，DAL 对应关系型项目的“原始 SQL 文件层”，文件内容为 JSON / Extended JSON。建议将复杂操作封装在 DAL 目录下的业务仓储中，保持 HTTP 层只关心业务参数和结果。

本节先给出文件化的复杂聚合流程，再介绍无需查询文件的链式关联与统计。两种方式可按查询复杂度选择。

### DAL 能力与入口

| 需求 | DAL 入口 |
| --- | --- |
| 普通查询 / 复杂原生聚合 | `DALQuery` / `DALQueryNamed`，支持 find / aggregate |
| 分页与聚合计数 | `DALQueryPage` / `DALQueryPageNamed`，配套 `count_` 文件 |
| 大结果集流式导出 | `DALWalk` / `DALWalkNamed` |
| 特殊混合批量、upsert、更新管道 | `DALBulkWrite` / `DALBulkWriteNamed`，返回官方结果与部分成功信息 |
| 变更订阅及 resume token | `DALWatch` / `DALWatchNamed`，返回官方 ChangeStream |
| 单条写入 / 写入聚合 | `DALExec` / `DALExecNamed`，支持显式 `$out/$merge` |
| 其余官方 API / 细粒度选项 | `DALNative`，声明写入集合并沿用 DAL 数据源、Hook 与缓存失效 |
| 业务仓储绑定事务 | `Repository.WithDAL`，沿用 `WithTx` 的事务和仓储数据源 |

### 文件组织与加载

复杂查询写在 `internal/dal/query`；调用代码写在非生成的 `internal/dal/repository/user.go` 或独立报表仓储，Logic / Controller 只调用业务方法：

```text
internal/dal/
├── query/
│   ├── files.go
│   └── user/
│       ├── summary.json
│       └── count_summary.json
├── repository/
│   ├── user_gen.go             # 生成基础实现，不写业务代码。
│   ├── user.go                 # 自定义业务接口与方法，不被重新生成覆盖。
│   └── report_repository.go    # 可选：独立复杂查询仓储。
└── vo/department_summary.go
```

`internal/dal/query/files.go`：

```go
package query

import "embed"

//go:embed user/*.json
var Files embed.FS
```

此处嵌入后的文件路径就是 `user/summary.json`。如果在上一级嵌入整个 `query` 目录，则先用 `fs.Sub(files, "query")` 去掉目录前缀，不要混用两种路径。

下面以 `users` / `departments` 集合为例，聚合每个部门的用户数。实体默认集合为 `user` 时需相应修改文件，集合名必须与实际配置一致。

`user/summary.json`：

```json
{
  "collection": "users",
  "op": "aggregate",
  "pipeline": [
    {"$match": {"company_id": "@companyID", "deleted_at": null}},
    {"$lookup": {
      "from": "departments",
      "let": {"deptID": "$dept_id"},
      "pipeline": [
        {"$match": {"company_id": "@companyID", "deleted_at": null, "$expr": {"$eq": ["$_id", "$$deptID"]}}}
      ],
      "as": "department"
    }},
    {"$unwind": {"path": "$department", "preserveNullAndEmptyArrays": true}},
    {"$group": {"_id": "$dept_id", "department_name": {"$first": "$department.name"}, "users": {"$sum": 1}}},
    {"$project": {"_id": 0, "department_id": "$_id", "department_name": {"$ifNull": ["$department_name", "未分配"]}, "users": 1}},
    {"$sort": {"department_id": 1}},
    {"$skip": "@offset"},
    {"$limit": "@limit"}
  ],
  "options": {
    "allowDiskUse": true,
    "batchSize": 100
  }
}
```

`user/count_summary.json` 统计的是部门分组数，不能直接统计原始用户文档数：

```json
{
  "collection": "users",
  "op": "aggregate",
  "pipeline": [
    {"$match": {"company_id": "@companyID", "deleted_at": null}},
    {"$group": {"_id": "$dept_id"}},
    {"$count": "total"}
  ]
}
```

### 在业务仓储中执行

服务启动时先注册生成集合的规则，再创建一次 DAL、预加载查询文件，并注入业务仓储。以下节选用于返回 error 的启动初始化函数，`query` 导入业务的 `internal/dal/query`：

```go
if err := repository.RegisterGeneratedCollections(db); err != nil {
    return err
}
dal, err := mongoplus.NewDal(db, mongoplus.NewEmbedLoader(query.Files))
if err != nil {
    return err
}
if err := dal.Preload("user/summary.json", "user/count_summary.json"); err != nil {
    dal.Close()
    return err
}
// 将 dal 注入业务仓储；服务退出时调用 dal.Close()，数据库连接由启动入口统一关闭。
```

将非生成的 `internal/dal/repository/user.go` 按下面合并。保留普通构造函数，另加接收 DAL 的构造函数；`Repository` 是基础仓储嵌入的公共执行器：

```go
package repository

import (
	"context"

	mongoplus "github.com/kuangshp/mongo-plus"
)

type customerUserRepository struct {
	*defaultUserRepository
	dal *mongoplus.DAL
}

type UserRepository interface {
	iDefaultUserRepository
	DepartmentSummary(context.Context, int64, int64) (mongoplus.DALPageResult[DepartmentSummary], error)
}

var _ UserRepository = (*customerUserRepository)(nil)

// 普通调用沿用默认连接 / 显式句柄的规则。
func NewUserRepository(db ...*mongoplus.Database) UserRepository {
	return &customerUserRepository{
		defaultUserRepository: newDefaultUserRepository(db...),
	}
}

// 需要 DepartmentSummary 时使用此构造函数，在启动时注入已预加载的 DAL。
func NewUserRepositoryWithDAL(dal *mongoplus.DAL, db ...*mongoplus.Database) UserRepository {
	return &customerUserRepository{
		defaultUserRepository: newDefaultUserRepository(db...),
		dal:                   dal,
	}
}

type DepartmentSummary struct {
	DepartmentID   int64  `bson:"department_id" json:"departmentId"`
	DepartmentName string `bson:"department_name" json:"departmentName"`
	Users          int64  `bson:"users" json:"users"`
}

func (r *customerUserRepository) DepartmentSummary(
	ctx context.Context, pageNumber, pageSize int64,
) (mongoplus.DALPageResult[DepartmentSummary], error) {
	var page mongoplus.DALPageResult[DepartmentSummary]
	err := r.Repository.WithDAL(ctx, r.dal, func(queryCtx context.Context) error {
		companyID, ok := mongoplus.TenantIDFromCtx[int64](queryCtx)
		if !ok || companyID <= 0 {
			return mongoplus.ErrTenantRequired
		}
		offset, limit := mongoplus.DbPage(pageNumber, pageSize)
		var err error
		page, err = mongoplus.DALQueryPageNamed[DepartmentSummary](
			queryCtx,
			"user/summary.json",
			map[string]any{
				"companyID": companyID,
				"offset":    offset,
				"limit":     limit,
			},
		)
		return err
	})
	return page, err
}
```

`@companyID` 等占位符保留绑定参数的 BSON 类型，不用字符串拼接；文件名由仓储代码固定，不接收客户端指定的文件路径、集合名或任意操作符。

上面的 `NewUserRepositoryWithDAL` 是业务扩展函数，由业务项目新增，不是生成器自带方法。启动时执行 `users := repository.NewUserRepositoryWithDAL(dal, db)`，再将 users 注入 ServiceContext 或 Gin 控制器；调用 DepartmentSummary 时必须使用该实例。通过普通构造函数创建的仓储未注入 DAL，调用此扩展方法会返回错误。

`WithDAL` 复用仓储绑定的数据库与事务。两个框架都调用同一个方法：go-zero 的 Logic 使用 `repo.DepartmentSummary(l.ctx, page, size)`；Gin 使用 `repo.DepartmentSummary(c.Request.Context(), page, size)`。生成的 `WithTx` 会保留注入的 DAL。完整生命周期、流式导出、特殊批量和断点订阅见[可运行仓储示例](examples/dal/repository.go)。

`$lookup` 子管道不会自动推断全部业务授权。示例显式限制关联部门的租户和删除范围；如果部门还有独立数据权限，必须同步加进子管道及计数逻辑。新增 / 调整聚合阶段后，应核对结果文件与计数文件是否仍统计同一个业务集合。

DAL 在执行时按数据库上的租户插件、数据权限和已创建仓储的逻辑删除配置追加条件。已有文件里的租户条件会与这层条件同时生效。`DALUnscoped` 只放开已删除记录的过滤范围；物理删除必须显式使用 `DALPhysicalDelete`，删除历史软删除文档时组合两个 Context 标记。`Native()` 和 `DALNative` 仍由业务自己写这些条件。变更订阅会在 `fullDocument` 上追加同样的范围；物理删除事件的检查点和重复事件由消费者处理。

完整的属性/选项表、参数绑定、变更订阅恢复、批量部分成功与事务边界见 [DAL 使用文档](docs/dal.md)。可复制的 [业务仓储示例](examples/dal/README.md) 和 [JSON 文件](examples/dal/query/user/summary.json) 均有真实数据库回归验证。

<a id="11-关联查询lookupleftjoininnerjoin"></a>

### 链式关联：Lookup、LeftJoin、InnerJoin

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

#### 关联字段过滤、投影与多集合串联

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

#### 关联集合的条件、逻辑删除和租户

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

#### 关联后分页与事务

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

#### 原生聚合扩展

原有 `mongoplus.Aggregate[Result](ctx, r.Repository, pipeline)` 继续支持只读单集合 Match、Project、Group、Sort、Unwind 等阶段。多集合等值查询使用上面的 Repository 链式构造器。Union、GraphLookup、Out/Merge、Search/GeoNear 等高级管道可放在 [DAL 查询文件](docs/dal.md) 并通过业务仓储调用；文件未覆盖的选项通过 DALNative 扩展，明确处理集合权限和数据生命周期。

接口习惯与 gorm-plus 接近，底层使用 BSON 过滤和 MongoDB 聚合。已有 GORM 条件不能直接传入。本版不提供 SQL、FOR UPDATE 或 GORM 插件兼容层。数据层生成器不自动暴露 HTTP 接口；可选 go-zero 模板根据独立的 .api 文件生成 HTTP 层和 Swagger，见下方说明。

<a id="12-聚合统计分组金额计算与数组统计"></a>

### 链式聚合：分组、金额计算与数组统计

聚合沿用同一条 Repository 调用链，无需为常见统计手写 `$group` 管道。示例的订单 Entity/DAO/Repository 来自 [order.json](examples/schema/order.json)，金额字段定义为 `decimal128`。

#### 按字段分组并统计

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

#### 不分组，统计全部匹配记录

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

#### 支持的聚合函数

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

#### 输入排序与结果排序

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

#### 数组展开后统计

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

#### 关联后统计、隔离与事务

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

#### 原生聚合扩展

完整用法参考 [订单统计示例](examples/statistics/main.go)，包含全部订单汇总、分类金额分页与标签计数。

当前链式封装覆盖上表常用统计，不代表所有 MongoDB 运算符均已封装。日期表达式、条件计算、Bucket、窗口函数等可放入 [DAL 聚合文件](docs/dal.md)；原有 `mongoplus.Aggregate[VO](ctx, r.Repository, pipeline)` 支持白名单内的单集合只读管道。涉及额外集合或写入的阶段通过 DAL 文件或 DALNative 显式处理权限、事务与数据生命周期。

<a id="缓存"></a>

## 6. 缓存的使用

### Repository 查询缓存

| 方式 | 是否保存结果 | 适合场景 |
| --- | --- | --- |
| 默认查询 | 否 | 直接读取数据库 |
| `WithSingleFlight()` | 否，只合并相同在途请求 | 降低瞬时重复查询 |
| `WithCache(ttl)` | 是，并自动合并相同在途请求 | 允许短暂陈旧的详情、字典等读取 |
| `CacheConfig{Disabled: true}` | 否，且强制禁用请求合并 | 关键状态要求实时读取 |

```go
user, err := userRepository.FindById(ctx, id,
    mongoplus.Query().WithCache(30*time.Second).Build(),
)

users, err := userRepository.FindList(ctx,
    mongoplus.Query().Where(dao.UserEntity.Status.Eq(1)).
        WithSingleFlight().Build(),
)
```

默认关闭读缓存。WithCache 自动合并相同在途查询；WithSingleFlight 只合并请求，不保存结果。缓存键包含租户、过滤、投影、排序和分页，不需要手写 Args。本机缓存返回已解码 Go 值的独立深拷贝，调用方修改嵌套切片、map、指针不会污染缓存，也不会二次使用默认 BSON 编解码器。无法安全复制的自定义私有引用类型和循环引用绕过共享，不改变正常查询结果。敏感字段在取得独立副本后，按当前请求的 Context 或 ReturnModeResolver 决定明文、脱敏或密文形式；缓存和 SingleFlight 不共享已经按某个请求权限处理的结果。

每个 Database 包装器独立保存查询结果，默认最多保留 1024 条结果、约 64 MiB 保留对象，单条最多约 1 MiB，使用 TTL。超出单条限制仍正常返回，只是不进入缓存；总容量不足时清理过期结果并淘汰已有结果。容量按 Go 对象保守估算，不包含在途查询、驱动缓冲区和运行时额外开销，不是进程内存的硬上限。同一个 Client、同一个数据库的所有包装器共享集合失效通知，写操作返回前同步清除其他包装器中的相关缓存，并阻止旧的在途读取重新填充。结果、解码配置和容量配置不跨包装器共享。Transaction 内跳过读缓存，在事务结束后通知相关包装器失效。外部 Session 写入无法获知提交时刻，会关闭同 Client 同库包装器的本机缓存；需要缓存的事务请使用本库 Transaction。

未注入共享缓存时，不同 Client、不同进程或外部程序写入不会自动广播失效。要求这些场景实时读取数据库时，在启动时调用 `db.ConfigureCache(mongoplus.CacheConfig{Disabled: true})`：即使业务代码传入 WithCache 或 WithSingleFlight，也始终直读数据库，不共享在途结果。所有业务 Database 包装器均应配置该选项。通过 Native 写入后，需要调用 `db.InvalidateCache(collection)`；原生写入也需要自行处理租户、审计和逻辑删除。

#### Redis 等共享缓存后端

通过 `RegisterCache` 或 `db.UseCache` 可以注入 Redis 等 `SFCache` 后端，实现示例见 [Redis 适配器](examples/redis-cache/cache.go)。全局 `RegisterCache` 仅在启动时注册一次，重复注册会 panic；`db.UseCache` 只影响当前数据库的仓储缓存。共享缓存默认使用每个 Database 包装器独有的随机命名空间，避免同名数据库位于不同数据源时串数据。需要跨包装器、Client 或进程共享结果时，启动时为同一实际数据源显式配置相同 Namespace；不同环境、集群、账号权限或编解码配置使用不同 Namespace：

```go
db.UseCache(sharedCache) // 也可以在启动时统一 RegisterCache(sharedCache)。
if err := db.ConfigureCache(mongoplus.CacheConfig{Namespace: "orders-prod-primary"}); err != nil {
    return err
}
```

仓储共享缓存使用 BSON 序列化，并按结果类型区分缓存键。保存前验证 BSON 往返是否保持完整 Go 值；空切片/空 map 的 omitempty、inline 指针等导致状态变化时，不保存共享条目，查询仍返回正确值。含自定义 BSON 编解码方法的实体，以及含 interface 动态字段的结果，自动绕过共享缓存和请求合并，避免重复解码改变值；本地 Go 值快照缓存不受影响。自行注册 Client BSON Registry 编解码器的项目应关闭仓储缓存，除非已验证默认 BSON 往返不会改变已解码值。本次共享缓存键升级为 v4，不再读取旧条目；旧数据按 TTL 或运维清理回收。写入通过切换集合版本使旧缓存失效；失效后的新请求不会加入旧 SingleFlight，旧在途查询即使稍后完成也不能成为当前版本的缓存。版本键使用 `Set(..., 0)` 永久保存；后端必须保留 RawValue 字节，且支持 TTL 为 0 表示不过期。旧条目可通过可选前缀删除回收，或者按查询 TTL 过期。版本键被逐出时会生成新的随机版本，避免重新命中旧条目。

生产适配器应实现 `SFCacheWithErrors`，并开启 `RequireErrorReporting`。Get/Set 故障会触发 `CacheObserver`；失效失败进入队列，当前包装器绕过共享缓存并自动重试，支持 `PendingCacheInvalidations`、`RetryCacheInvalidations` 和显式返回错误的 `InvalidateCacheContext`。这不构成跨进程强一致协议，进程崩溃也会丢失内存恢复队列；实时强一致场景仍应设置 Disabled，外部程序写入后也必须通知失效。配置、错误接口和恢复语义见[生产接入与安全配置](docs/production.md)。共享缓存总容量由后端管理，单条结果仍遵守 MaxEntryBytes 限制。

可在启动时调整缓存容量，缓存仍由每次查询的 WithCache 显式开启：

```go
err := db.ConfigureCache(mongoplus.CacheConfig{
    MaxEntries:    1024,
    MaxBytes:      64 << 20, // 总容量约 64 MiB。
    MaxEntryBytes: 1 << 20,  // 单条约 1 MiB，避免缓存整个大集合。
})
```

SingleFlight 的数据库查询使用发起者 Context；发起者取消可能使同组调用者收到错误，其他调用者自己的取消会立即结束等待。

#### 公共 SF 与内存缓存

公共 SF 适合仓储之外的重复计算或外部查询；仓储查询优先使用上面的 WithCache，不必重复套一层 SF。例如封装业务查询时：

```go
args := mongoplus.BuildArgs(
    "companyID", companyID,
    "userID", userID,
    "id", id,
)
result, err := mongoplus.SF(func() (*entity.UserEntity, error) {
    return userRepository.FindById(ctx, id)
}, "user.detail", args, 30*time.Second)
if err != nil {
    return err
}
// 按业务使用 result。
```

业务更新成功后，使用相同参数使对应的公共 SF 条目失效。Repository 不知道自定义函数名，不会自动清除这类条目：

```go
if err := mongoplus.SFInvalidateWithError("user.detail", args); err != nil {
    return err
}
```

示例中 companyID、userID 来自服务端鉴权上下文；如结果还依赖部门、角色或其他权限条件，也要纳入参数。函数名应稳定且区分业务，Context 本身不作为缓存参数。

仓储之外使用 `SF`、`SFWithTTL`、`SFNoCache` 时，`args` 必须可被 JSON 序列化，并包含租户、权限范围等所有影响结果的参数。NaN、Inf、循环引用或自定义编码失败会返回 `ErrInvalidCacheArgs`，不会访问缓存、参与并发合并或执行加载函数。`SFInvalidateWithError` 可返回相同的参数错误；兼容入口 `SFInvalidate` 遇到无效参数时不删除任何键。

公共 SF 默认使用 `MemoryCache`，最多保存 1024 条，按 LRU 淘汰，每秒自动回收过期项，无需等待读取。此配置独立于 Repository 的 `CacheConfig`，只限制条目数，不限制载荷字节数；大结果应限制大小或使用共享后端。

```go
cache, err := mongoplus.NewMemoryCacheWithConfig(mongoplus.MemoryCacheConfig{
    MaxEntries:      2048,        // 0 使用 1024，不能为负数。
    CleanupInterval: time.Second, // 0 使用 1 秒，不能为负数。
})
if err != nil {
    return err
}
defer cache.Close() // 关闭清理任务并释放条目；关闭后不再保存数据。
mongoplus.RegisterCache(cache)
```

`MemoryCache.Set` 的 TTL 为 0 表示不过期，仍受 LRU 容量限制；负数表示不保存。`Len()` 返回实际保留条目数，可用于观察后台回收。`StopSFCache()` 会关闭全局注册的缓存。直接使用公共 SF 的 Go 结果为共享值，调用方应避免修改其中的 map、切片或指针。

Repository 的 `CacheConfig` 与公共 SF 的全局缓存是两套入口；关闭 Repository 缓存不会自动关闭业务自己调用的 SF。共享缓存适合允许短暂陈旧的数据，要求实时读取的关键状态应关闭缓存和请求合并。

<a id="常用增删改查"></a>

## 7. 一些常用增删改查

以下示例基于生成的 int64 主键 UserRepository，`ctx` 是请求传入的 `context.Context`。业务包导入 `internal/dal/entity`、`internal/dal/dao`、`internal/dal/repository`；若生成目录命名为 model，相应替换 entity。各片段按需使用，错误都应交给业务层处理。

| 场景 | 常用方法 |
| --- | --- |
| 新增 / 批量新增 | `Create` / `CreateBatch` |
| 按主键查询 / 批量查询 | `FindById` / `FindByIdList` |
| 条件列表 / 分页 / 游标 | `FindList` / `FindPageByWrapper` / `FindCursorPage` |
| 计数 / 是否存在 | `CountByWrapper` / `ExistsByWrapper` |
| 修改指定字段 / 条件修改 | `UpdateById` / `UpdateByWrapper` |
| 软删除 / 恢复 / 物理删除 | `DeleteById` / `RestoreById` / `HardDeleteById` |
| 查询并修改 / 删除 / 替换 | `FindOneAndUpdate` / `FindOneAndDelete` / `FindOneAndReplace` |
| 单数据源事务 | `TransactionCtx` + `WithTx` |

### 创建与复用 Repository

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

运行时配置和生成器配置可以分开加载，但应确认指向目标数据库。上述片段在返回 error 的启动函数内使用；`ctx` 建议设置连接超时，连接由启动入口统一关闭。

go-zero 模板已在 `NewServiceContext` 中于连接成功后自动调用 `mongoplus.Use(db)`，业务仓储可直接使用无参构造。手写启动入口按上例注册一次即可；`Connect` 和 `NewDatabase` 本身不会更改默认连接。已有服务的 ServiceContext 不会被 goctl 增量生成覆盖，需在连接成功后合并这一行初始化代码。显式传入数据库的仓储仍优先使用指定连接。

数据库包装器和 Repository 在启动时创建并注入业务层，不要每个请求新建 Client。已有官方驱动数据库可用 `mongoplus.NewDatabase(nativeDB)` 包装；创建文档直接使用该 Client 的 BSONOptions/Registry，兼容 UseJSONStructTags 和自定义字段编码器，无需重复配置。自动管理字段（ID、租户、时间、逻辑删除、审计）应使用明确的 bson 标签。关闭连接由拥有该 Client 的启动入口负责。

#### 默认连接与多个数据库

每个 Repository 在构造时绑定一个数据库。不传参数时使用 `mongoplus.Use` 注册的默认连接；传一个 `*mongoplus.Database` 时固定使用该句柄，不读取或修改默认连接。三个数据库可以同时构造同一种仓储：

```go
// mainDB、archiveDB、reportDB 是启动时已初始化的数据库句柄。
// 只将主业务库设为默认，另外两个数据库不需要调用 Use。
mongoplus.Use(mainDB)

mainUsers := repository.NewUserRepository()            // 默认主业务库。
archiveUsers := repository.NewUserRepository(archiveDB) // 显式归档库。
reportUsers := repository.NewUserRepository(reportDB)   // 显式报表库。
```

| 构造方式 | 行为 |
| --- | --- |
| `NewUserRepository()` | 在构造时取得默认句柄；未注册有效连接则初始化失败 |
| `NewUserRepository(db)` | 只使用传入的句柄；不要求存在默认连接 |
| `NewUserRepository(nil)` | 初始化失败；显式 nil 不会兜底到默认库 |
| `NewUserRepository(db1, db2)` | 初始化失败；一个仓储只绑定一个句柄 |

`Use` 建议仅在启动时设置一次。之后修改或清除默认连接，只影响后续无参构造，不影响已创建的 `mainUsers`、`archiveUsers` 和 `reportUsers`。构造仓储不会创建 Client 或自动同步 schema，各数据源的集合结构应单独同步。

生成仓储使用公共 `ResolveDatabase(db...)` 统一解析参数，该函数返回原始句柄或错误；生成构造函数沿用启动时 panic 暴露无效配置的约定。手写仓储也可调用该函数并自行返回错误。

旧业务项目升级后重新运行 `go run generator.go` 更新 `_gen.go`。业务扩展文件不会被覆盖：若其构造函数仍要求必传句柄，需要手动将签名改为 `func NewUserRepository(db ...*mongoplus.Database) UserRepository`，并向基础构造函数转发 `newDefaultUserRepository(db...)`。已经使用可变参数的文件无需修改。

Gin 和 go-zero 都只需传入请求的 `context.Context`，没有框架绑定。框架的参数校验仍由业务项目负责；MongoDB 校验规则需通过配置了数据源的 Generate、SyncSchema 或 schema apply 建立。

Gin 项目如果沿用 gorm-plus 中直接传 `*gin.Context` 的写法，可在启动时通过 `RegisterCtxResolver` 将它解析为 `c.Request.Context()`，注册示例见[迁移指南](docs/gorm-plus-migration.md#gin-context)。

```go
user := &entity.UserEntity{Username: "admin", Status: 1}
err := userRepository.Create(ctx, user)
// 成功后自动回填 ID、CreatedAt、UpdatedAt。

user, err = userRepository.FindById(ctx, user.ID)
users, err := userRepository.FindByIdList(ctx, []int64{1, 2, 3})
```

找不到记录使用 `errors.Is(err, mongoplus.ErrNotFound)` 判断；重复索引保持官方驱动错误，可使用 `mongo.IsDuplicateKeyError(err)`。连接、取消、超时等错误沿用驱动语义。

`CreateBatch` 使用有序 InsertMany，每块最多 1000 条；自增主键每块只申请一次号码区间。每块先检查实体和自动字段，再交给原生驱动编码，成功后回填实体。非事务下失败时，前面的文档可能已写入，只回填已确认成功的前缀，后续文档不再插入；错误链保留驱动错误，唯一索引冲突仍可用 mongo.IsDuplicateKeyError 判断。网络中断或写确认失败时，当前块的写入结果可能不确定，不回填该块；应按业务幂等键核对结果。需要整批回滚时使用 Transaction。w=0 未确认写入返回 ErrUnacknowledgedWrite。

### 批量新增、计数与存在性

```go
records := []*entity.UserEntity{
    {Username: "alice", Status: 1},
    {Username: "bob", Status: 1},
}
if err := userRepository.CreateBatch(ctx, records); err != nil {
    return err
}

count, err := userRepository.CountByWrapper(ctx, func(w *mongoplus.Wrapper) {
    w.Where(dao.UserEntity.Status.Eq(1))
})
if err != nil {
    return err
}
exists, err := userRepository.ExistsByWrapper(ctx, func(w *mongoplus.Wrapper) {
    w.Where(dao.UserEntity.Username.Eq("alice"))
})
```

批量写入的部分成功语义见上文；需要整批原子性时使用下文事务。Count / Exists 也遵守租户、数据权限及逻辑删除范围。

<a id="5-条件构造与分页"></a>

### 条件构造与分页

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

### 查询结果填充 VO

在 Repository 业务扩展中封装查询，并从控制器传入 ctx.Request.Context()。查询结果可直接填入 VO：

```go
var rows []vo.UserVO
total, err := userRepository.WithContext(ctx).
    Where(dao.UserEntity.Status.Eq(1)).
    Order(dao.UserEntity.ID.Desc()).
    ScanByPage(&rows, 1, 20)
```

以上查询示例中的 ctx 为传入 Repository 的 context.Context。DTO 用于请求，VO 用于响应；查询填充 VO 时应显式裁剪敏感字段。

<a id="6-零值更新与逻辑删除"></a>

### 零值更新与逻辑删除

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

<a id="16-原子复合操作与官方扩展选项"></a>

### 原子复合操作与官方扩展选项

原子接口在一次服务端操作中完成条件匹配、写入和返回，适合领取任务、状态竞争和计数后读取。更新、租户、数据权限、敏感字段、审计和事务缓存失效沿用公共仓储规则。

```go
job, err := repo.FindOneAndUpdate(ctx,
    mongoplus.Query().
        Where(dao.JobEntity.Status.Eq(1)).
        Order(dao.JobEntity.ID.Asc()).
        Build(),
    mongoplus.ModifyOptions{
        ReturnDocument: options.After,
        Upsert:         false,
    },
    dao.JobEntity.Status.Set(2),
)
```

| 接口 / 选项 | 行为与边界 |
| --- | --- |
| `FindOneAndUpdate(ctx, query, modify, updates...)` | 默认返回修改前文档；`ReturnDocument: options.After` 返回修改后文档。没有业务过滤时必须显式 AllowAll。 |
| `FindOneAndDelete(ctx, query, deleteOptions...)` | 启用软删除时返回删除前文档并原子标记删除；物理删除选项仍遵循租户与权限。 |
| `FindOneAndReplace(ctx, query, replacement, modify)` | 必须提供非零主键并追加同主键过滤；原子全量替换业务字段，保留原主键、创建时间、创建人及删除标记，重新应用租户、加密和自动填充；不修改输入实体。内部使用更新管道。 |
| `ModifyOptions.Upsert` | 自增主键仓储明确拒绝原子 Upsert（含查改和查替换），避免与其他实例预分配号段冲突；请用 Create 分配 ID，或使用 `IDGenerationManual` 的手动主键仓储。手动 int64/string 主键必须在 Query 中提供 `_id` 等值；不会静默生成 ObjectID 或分配自增号码。无匹配且返回 Before 时，成功插入也会返回 ErrNotFound；需要新记录时选择 After。 |
| `Distinct(ctx, path, query...)` | 按租户、权限和删除范围去重；支持 Collation/Hint/Comment（Hint 需要 MongoDB 7.1+）；返回原始 BSON 值，不解密敏感字段。 |
| `EstimatedDocumentCount(ctx)` | 整集合元数据估计；有租户、权限或软删除策略时直接拒绝，改用 Count。 |
| `Query().Collation(...)` | 语言比较规则；纳入缓存键，防止不同比较语义共用结果。 |
| `Hint(...)` / `Let(bson.D{...})` / `Comment(...)` | 指定索引、命令变量和注释；来自可信业务代码。CountDocuments 不支持 Let，使用聚合计数。 |
| `BatchSize(n)` / `AllowDiskUse(bool)` | Find/Walk 和根聚合命令选项；原子写入口拒绝分页、缓存和游标选项。 |
| 数值与数组字段 | 新增 Mul、Min、Max、CurrentDate、PopFirst、PopLast、PullAll、All、Size、BSONType、Nor；系统字段仍受保护。 |

原始 `Aggregate` 的 Order、Offset、Limit、Select/Omit 作用于用户管道的最终输出，拒绝缓存与请求合并选项。字符串主键的 FindCursorPage、FindPage 和稳定关联分页显式使用 simple Collation，覆盖集合默认规则；传入其他 Collation 会报错，以免大小写等价的不同主键漏页。

普通 `UpdateByWrapper` / `DeleteByWrapper` / `HardDeleteByWrapper` 会透传 Collation、Hint、Let 和 Comment，逻辑删除沿用同样选项。排序、投影、分页、缓存和游标选项在普通写入中返回错误；需要排序领取一条记录时使用 `FindOneAndUpdate`。

生成的仓储接口与显式方法已同步；升级工具后重新生成 `_gen.go`。生成器还输出 `collections_gen.go`，服务启动、创建 DAL 之前调用：

```go
if err := repository.RegisterGeneratedCollections(db); err != nil {
    return err
}
```

手写仓储可调用 `db.RegisterCollections(configs...)`。同集合的规则必须完全相同，冲突会报错；一次注册失败不会发布部分规则。每个数据源包装器都要注册自己的规则。字符串租户条件额外使用不受 Collation 影响的精确匹配，业务的大小写比较规则不会扩大租户授权范围。

DAL 文件补充了 Collation、Let、批量模型 Collation/Sort、订阅 ShowExpandedEvents，详见 [DAL 属性表](docs/dal.md)。原子复合操作通过 Repository 接口使用；文件层未覆盖的命令使用 `WithDAL` + `DALNative`。

TimeSeries、Clustered、View、GridFS、数据库/Client 级订阅、跨命名空间批量和 Search 索引仍通过官方驱动扩展。它们需要业务明确设计集合范围、订阅权限、断点与跨库缓存失效；参见 [DAL 原生扩展](docs/dal.md#高级官方能力的接入边界)。

### 事务

事务内统一使用 `WithTx(txCtx).普通方法(...)`，与 gorm-plus 的仓储绑定习惯一致。MongoDB 的事务凭据是携带 Session 的 `context.Context`，因此这里的 `txCtx` 对应 gorm-plus 的事务 DAO。

```go
err := mongoplus.TransactionCtx(ctx, db, func(txCtx context.Context) error {
    users := userRepository.WithTx(txCtx)
    profiles := profileRepository.WithTx(txCtx)

    if err := users.Create(ctx, user); err != nil {
        return err
    }
    return profiles.Create(ctx, profile)
})
```

`WithTx` 只绑定已有事务，返回完整业务 Repository 接口，必须使用返回值；原仓储保持不变。绑定后，新增、批量新增、更新、删除、恢复、查询、分页、Count、Exists、Walk 和关联聚合都使用同一 Session。普通方法仍传请求 `ctx`，保留取消信号和请求值，并从事务入口补齐租户、操作人等上下文。原有 `userRepository.Create(txCtx, user)` 用法仍然支持。

| 事务入口 | 回调参数 | 用途 |
| --- | --- | --- |
| `Transaction(ctx, db, fn, opts...)` | `context.Context` | 执行 Session 事务，可绑定多个 Repository，也可执行原生操作 |
| `TransactionCtx(ctx, db, fn, opts...)` | `context.Context` | 与 `Transaction` 等价，便于沿用 gorm-plus 的命名 |
| `TransactionAs(ctx, db, use, fn, opts...)` | 业务类型 `Q` | 每轮回调先调用 `use(txCtx)` 构造事务仓储集合 |
| `TransactionAsCtx(ctx, db, use, fn, opts...)` | 业务类型 `Q` | 与 `TransactionAs` 等价，保留请求上下文 |

MongoDB 操作始终显式传 Context，因此四个入口均以 `ctx` 为首参；`use` 的签名为 `func(context.Context) Q`，无需生成或导入业务 `dao.Use`。例如将单个仓储作为回调参数：

```go
err := mongoplus.TransactionAsCtx(ctx, db, userRepository.WithTx,
    func(users repository.UserRepository) error {
        return users.Create(ctx, user)
    },
)
```

关联查询也可以从绑定仓储开始，例如 `users.WithContext(ctx).LeftJoin(profiles, ...).Scan(&rows)`。公共执行器的 `Aggregate(ctx, boundRepository, pipeline)` 同样沿用绑定。事务读取自动跳过缓存和 SingleFlight；事务写入按集合登记，提交后统一失效，回滚不发布失效；提交结果不确定时保守失效。驱动重试时，每轮重新构造绑定仓储，前一轮的仓储不可复用。

`WithTx(nil)`、普通 Context 或未由本库事务入口跟踪的原生 Session，在执行时返回 `ErrUnmanagedTransaction`；不同 Client、Session 或事务轮次混用返回 `ErrTransactionMismatch`。回调退出后，绑定仓储及该轮 `txCtx` 不可继续操作，返回 `ErrTxDone`。生成器会复制业务结构体、基础仓储和执行器，保留业务覆盖及注入依赖；锁等不可复制状态请通过指针持有，不要并发修改原仓储字段。自定义方法优先组合普通仓储方法；`Native()` 返回原生 Collection，原生调用仍需显式传入 `txCtx`，并自行处理租户、权限与缓存。

升级工具库后，重新运行 `go run generator.go` 更新 `_gen.go` 即可获得 `WithTx`，业务扩展文件不会覆盖；手写 mock/接口实现需补充该方法。无需新增重复的 `CreateTx`、`UpdateByIdTx` 等方法。

事务要求副本集或分片集群，所有参与仓储必须共用同一 Client。[MongoDB 官方事务说明](https://www.mongodb.com/docs/drivers/go/current/crud/transactions/)规定同一事务不可并行操作，回调也可能重试；不要在回调中发送短信、调用支付或手动 Commit/Rollback。嵌套 Session 事务会明确报错，没有 SQL savepoint 或跨 MongoDB/MySQL 原子事务。读关注和写关注使用官方 `options.Transaction()` 传入。

### 性能与生产使用

大批量写入使用 CreateBatch，高频数字主键按需启用 BlockSize；大集合分页优先使用 FindCursorPage，大量导出使用 Walk。关联字段应建立合适的索引，并通过 Select、Limit 控制一对多结果大小；ScanByPage 使用两个独立聚合游标，单条输出文档仍遵循 MongoDB 服务端大小限制。

生产使用前应完成真实数据量与目标并发的压测、超时与故障恢复验证。仓库持续集成包含真实三节点副本集、竞态检查、Gin/goctl 集成、主节点切换后读写与事务恢复、漏洞扫描；这些检查不替代业务环境的容量和长期稳定性验证。详细命令与限制见 [性能验证与生产接入](docs/performance.md)。

当前源码包含租户字段更新保护、权限严格检查、缓存错误恢复及独立版本迁移。迁移执行锁、SHA256 历史、失败恢复、显式 Down 和分批回填，以及版本发布方式，统一见[生产接入与安全配置](docs/production.md)。版本发布状态见[更新记录](CHANGELOG.md)。

接口选项的支持、拒绝和原生扩展范围集中见[查询与写入契约](docs/contracts.md)。参数日志中的表达式、let 变量和脚本保留结构但隐藏标量常量；需要隐藏所有业务参数时设置 HideParameters。公共 SF 使用保留嵌套类型信息的 v2 键，旧键不再读取。
