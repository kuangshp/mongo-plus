# JSON 模型定义、代码生成与集合同步

统一流程：**`schema/*.json` 定义模型 → 根目录 `generator.go` 配置和执行 → 使用生成的 Entity / DAO / Repository。** 不再支持手写模型生成，也不再维护完整生成配置 YAML。

本文对应包含不兼容变更的源码；业务依赖与 CLI 需固定为包含这些变更的相同版本，旧版本不会因文档更新而改变行为。

## 1. 先创建 JSON 定义

```text
shop/
├── go.mod                       # module example.com/shop
├── generator.go                 # 生成参数与操作入口
├── application.dev.yml           # 可选：项目已有运行配置，只复用数据库连接
├── schema/user.json              # 模型字段与索引的唯一来源
└── internal/dal/                 # 生成结果、查询与业务扩展
```

创建 `schema/user.json`：

```json
{
  "comment": "用户",
  "fields": [
    {"name": "username", "type": "string", "comment": "用户名", "required": true, "unique": true},
    {"name": "status", "type": "int64", "comment": "状态：1正常，2禁用"}
  ]
}
```

默认从 user.json 推导 UserEntity / user；Pluralize 为 true 时集合为 users。旧项目通过 entity、collection、goName 保持既有命名。字段全部属性见 [README 属性表](../README.md#全部可配置属性)。主键、时间、软删除和租户字段按全局规则或 JSON 覆盖生成，不重复填写。

只加载 SchemaDir 目录直接包含的 `.json` 文件，不递归读取。没有 JSON 时直接报错，不回退到已有 Go 模型。

## 2. 配置并运行生成器

在业务根目录创建以下 `generator.go`，并将 Package 改成 go.mod 的 module。目录、主键、逻辑删除等生成规则只在 Go 中配置；无需创建 mongo-plus.yml 或 mongo-plus.yaml。

```go
//go:build ignore

// 在业务项目根目录运行；生成规则只在本文件维护，模型只在 schema/*.json 中定义。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"log"
	"os"
	"time"

	mongoplus "github.com/kuangshp/mongo-plus"
	"gopkg.in/yaml.v3"
)

func main() {
	action := flag.String("action", "generate", "generate、check、plan 或 apply")
	configFile := flag.String("config", "", "可选：已有业务 YAML，只读取 mongodb 连接")
	timeout := flag.Duration("timeout", 60*time.Second, "本次操作总超时")
	flag.Parse()
	if flag.NArg() != 0 || *timeout <= 0 {
		log.Fatal("不接受位置参数，timeout 必须大于零")
	}
	var source mongoplus.Config
	if *configFile != "" {
		data, err := os.ReadFile(*configFile)
		if err != nil {
			log.Fatal(err)
		}
		// 只读取业务连接，不读取 Package、输出目录等生成参数。
		var app struct {
			MongoDB *mongoplus.Config `yaml:"mongodb"`
		}
		if err := yaml.Unmarshal(data, &app); err != nil {
			log.Fatal(err)
		}
		if app.MongoDB == nil {
			log.Fatal("业务配置缺少 mongodb 连接段")
		}
		source = *app.MongoDB
		source.URI = os.ExpandEnv(source.URI)
		source.Database = os.ExpandEnv(source.Database)
		if source.URI == "" || source.Database == "" {
			log.Fatal("mongodb 的 uri、database 不能为空")
		}
	}
	cfg := &mongoplus.GeneratorConfig{
		Package:      "example.com/shop",        // 与业务 go.mod 的 module 一致。
		SchemaDir:    "schema",                  // JSON 模型定义的唯一来源。
		ModelPkgPath: "internal/dal/entity",     // Entity 输出目录。
		OutPath:      "internal/dal/dao",        // DAO 输出目录。
		RepoPath:     "internal/dal/repository", // Repository 输出目录。
		DtoPath:      "internal/dal/dto",        // 可选；首次生成后保留业务修改。
		VoPath:       "internal/dal/vo",         // 可选；首次生成后保留业务修改。
		Pluralize:    false,                     // true 为 users；false 为 user。
		ID: mongoplus.IDConfig{
			Type: mongoplus.IDTypeInt64, // 默认自增；实体可在 JSON 覆盖。
		},
		SoftDelete: mongoplus.Bool(true), // 默认生成逻辑删除字段。
		Timestamps: mongoplus.Bool(true), // 默认生成创建/更新时间。
		MongoDB:    source,               // 来自业务连接；省略 -config 时不提供数据源。
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	changes, err := mongoplus.RunGenerator(ctx, cfg, *action)
	if err != nil {
		log.Fatal(err)
	}
	if changes != nil {
		if err := json.NewEncoder(os.Stdout).Encode(changes); err != nil {
			log.Fatal(err)
		}
	}
	log.Printf("%s 完成", *action)
}
```

安装公共包后，在业务根目录执行：

```bash
# 下方完整示例使用此依赖读取已有业务 YAML。
go get gopkg.in/yaml.v3@v3.0.1
go run generator.go -action check
go run generator.go
go mod tidy
```

没有 `-config` 时只生成代码，不连接数据库。相对路径以 ProjectDir 为基准；不填写 ProjectDir 时取运行命令的当前目录，生成器不会根据源文件位置或 YAML 所在位置切换项目根目录。

| 文件 | 维护规则 |
| --- | --- |
| schema/*.json | 模型字段、类型、索引及实体覆盖的唯一来源 |
| generator.go | Package、输出目录、主键、时间、软删除等生成配置 |
| entity/*、dao/*、repository/*_gen.go | 自动生成并更新；禁止手写替代或直接修改字段 |
| repository/user.go | 首次生成，后续保留业务方法 |
| dto/*、vo/* | 按需首次生成，后续保留接口校验和返回字段调整 |
| query/**/*.json | DAL 查询命令，不用于定义模型或建表 |

模型方法写在同包独立扩展文件，字段仍回到 JSON 修改。删除或重命名整个实体后自行整理旧生成文件，工具不会自动删除源码。

## 3. 显式同步集合、索引与校验器

复用业务已有的数据库连接。上方示例的 `-config` 只读取 `application.dev.yml` 的连接段：

```yaml
mongodb:
  uri: "mongodb://127.0.0.1:27017"
  database: shop
  maxPoolSize: 100
```

这是业务运行配置，不是生成配置；不在里面配置 Package、输出目录、主键或 SchemaDir。项目已有加载器时直接复用其返回的连接配置即可。go-zero 示例读取服务现有的 MongoConfig，而不是要求新增上述段，见 [示例入口](../examples/gozero/generator.go)。

| action | 连接数据库 | 生成代码 | 用途 |
| --- | --- | --- | --- |
| generate（默认） | 仅提供数据源时 | 是 | 完整生成预检后，有数据源再同步结构并写文件；无数据源只写文件 |
| check | 否 | 否 | 校验配置与 JSON，渲染全部输出并检查命名、覆盖冲突 |
| plan | 是 | 否 | 预览结构差异，不写库 |
| apply | 是 | 否 | 同步集合、索引、校验器，返回变更记录 |

```bash
go run generator.go -action plan -config application.dev.yml
go run generator.go -action apply -config application.dev.yml -timeout 2m
# 默认命令先完整预检，再同步结构并写入同一快照中的代码。
go run generator.go -config application.dev.yml
```

公共包 `RunGenerator(ctx, cfg, action)` 返回变更和错误，不解析命令行或加载 YAML。由业务入口处理参数、读取已有运行配置、设置超时并展示结果。直接调用 `Generate(cfg)` / `SyncSchema(ctx, cfg)` 仍然支持；需要低层计划时，对 cfg 的副本 Normalize，再经 `schema.LoadEntities` 加载 JSON 后传给 `schema.Plan/Apply`。

Plan/Apply 只接受 LoadEntities 加载后未修改的元数据；禁止设置 Source 伪造来源、手动构造 Entity 或改写加载后的字段、索引和集合名。变更应修改 JSON 或生成参数并重新加载，非法来源在数据库访问和迁移锁写入前拒绝。

所有 action 以及 `Generate` / `SyncSchema` 都先渲染全部输出，检查 DAO 路径、跨文件类型和包级声明冲突，并检查保留的业务扩展与覆盖保护。预检失败不连接数据库、不创建文件。同步与输出使用同一份 JSON 快照，写入前再次检查磁盘上的业务文件。跨文件重名错误会标明冲突名称和文件。

这不代替业务编译：生成后仍运行 `go test ./...` 或 `go build ./...`。生成期间保持业务文件不变；数据库 DDL 与文件写入不是整体事务，磁盘故障或并发修改仍可能在同步后阻止写入。生产发布宜先完成生成与编译，再独立审核、执行结构迁移。

服务启动、Use、插件注册和仓储构造不执行集合同步。MongoDB 原生首次写入不会替你创建 JSON 声明的唯一索引和校验器。同步不会删除旧字段、替换冲突索引或回填历史文档；这些变更应使用独立迁移。

## 4. 已有手写模型如何迁移

| 已移除的能力 | 新方式 |
| --- | --- |
| GeneratorModel / schema.Model | 把模型、索引及覆盖规则迁移到 schema/*.json |
| GenerateModels | Generate(cfg) 或 RunGenerator 的 generate |
| SyncModels | SyncSchema(ctx, cfg) 或 RunGenerator 的 apply |
| schema.FromModels | schema.LoadEntities，输入为归一化后的 Go 配置值 |
| generator.GenerateEntities | generator.Generate(cfg)，或公共包 Generate |
| LoadGeneratorConfig / schema.LoadConfig | 在 generator.go 直接构造 GeneratorConfig |
| 专用完整生成 YAML | 将生成参数移到 Go；连接读取业务已有配置 |
| mongoplus generate/check/schema | go run generator.go -action generate/check/plan/apply |

这是不兼容变更，旧调用会编译失败，旧 CLI 命令会返回迁移提示。迁移时：

1. 先把已有实体字段和索引写入 JSON，核对集合名、Go 名称、主键、时间、软删除、租户和可空规则。
2. 将原生成 YAML 的目录与全局规则写入 generator.go；连接复用业务配置，删除专用生成 YAML 和加载调用。
3. 备份旧模型，业务方法移到同包扩展文件，移走与生成类型冲突的手写定义；不要给旧文件添加生成标记绕过覆盖保护。
4. 更新 Makefile / CI 为上述 action 命令，运行 check、生成和业务编译；在正确环境预览并执行同步。
5. 后续模型变化只修改 JSON 并重新生成；DTO、VO 与业务扩展保留既有不覆盖策略。

## 5. 常见错误与边界

| 现象 | 处理方式 |
| --- | --- |
| 没有 JSON 实体定义 | 检查 SchemaDir、ProjectDir 和执行目录；已有 Go 模型不能代替 JSON |
| 必须由 JSON 文件定义 / 元数据已被修改 | 修改源 JSON 并重新加载，不直接构造或改写元数据 |
| 拒绝覆盖非生成文件 | 备份迁移旧类型，移走冲突定义，保留业务扩展后生成 |
| 有 Entity 但没有集合 | 未提供数据源时只生成代码；复用运行连接后执行 apply |
| DTO / VO 没有随 JSON 改变 | 这两类文件保留业务内容，按协议维护或备份后重新生成 |
| 旧命令 / 加载 API 不可用 | 移除专用生成 YAML，迁移到 generator.go |

DTO、VO、聚合投影仍可按业务编写。原生驱动和独立迁移回调不由生成器控制，不作为手写模型建表的替代流程。历史迁移固定 JSON 快照并将其与全局生成规则纳入校验和，见 [生产迁移](production.md#独立版本迁移)。Swagger、go-zero 与业务自己的 YAML 配置仍受支持，移除的只有整套生成参数 YAML。
