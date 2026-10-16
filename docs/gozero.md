# go-zero 内嵌模板

mongo-plus 的模板参照 gorm-plus 实现，保留 Handler → Logic → Repository 的调用方式。模板包含中文参数校验、数字/字符串入参兼容、统一响应、CORS、公开静态文件、文件日志与 Swagger。HTTP 功能位于独立子包，数据层不依赖 go-zero、Gin、GORM、gorm-plus 或 go-utils。

本次实测 goctl/go-zero 1.10.2。goctl 负责生成 HTTP 代码；mongoplus 为它提供内嵌模板。`go run generator.go` 通过公共包生成数据层，CLI 的 `mongoplus generate` 保留兼容，启动服务不会自动同步集合或索引。

## 1. 安装与准备

在业务开发环境安装公共包的 CLI：

```bash
go install github.com/kuangshp/mongo-plus/cmd/mongoplus@latest
```

需要 goctl 时单独安装：

```bash
go install github.com/zeromicro/go-zero/tools/goctl@v1.10.2
```

将 Go 的安装目录加入 PATH。业务项目的 go.mod 同时需要 mongo-plus 和 go-zero；CLI 安装不等于添加运行时依赖。工具版本应与业务项目的 mongo-plus 依赖版本保持一致。

业务项目按下文创建 generator.go、生成配置和 .api 文件即可使用内嵌模板；可参考 [go-zero 示例模块](../examples/gozero/README.md) 的目录结构。

## 2. 公共包 generator.go 与 .api 分工

服务目录可以按下面组织：

```text
shop/
├── shop.api                 # 请求、响应和路由协议。
├── generator.go             # 公共包生成入口，配置与 gorm-plus 使用方式一致。
├── mongo-plus.yml           # 生成配置，mongodb 数据源可选。
├── schema/user.json         # 数据模型。
├── etc/shop-api.yaml        # 生成后的 HTTP 服务运行配置。
├── internal/dal/            # Entity、DAO、Repository、DTO、VO。
└── Makefile
```

README 提供 [两种 generator.go 用法](../README.md#1-全局配置一次实体按需覆盖)：直接在 Go 中配置输出目录、ID、逻辑删除等参数，并按需读取业务 YAML 数据源；或通过 mongo-plus.yml 加载完整生成配置。任选一种，Package 都应与 go.mod 的 module 一致。

`pluralize: false` 表示 user、sys_user；`pluralize: true` 表示 users、sys_users。不填写 mongodb 时只生成代码；填写完整数据源后，执行同一条命令会先创建/同步集合、索引、校验器，再生成文件。数据源不完整或连接失败会停止。生成器与服务运行时 MongoConfig 应读取同一个项目的数据源，但生成器不会把连接串写入 Entity、Repository。

```bash
go run generator.go
mongoplus goctl api go --api shop.api --dir . --style=goZero
go mod tidy
```

仓库内的 examples/gozero 使用 mongo-plus.yaml（同样是 YAML），generator.go 默认路径与该文件一致；可以通过 -config 指定其他 .yml/.yaml。

也支持 `mongoplus goctl api new shop --style=goZero` 创建新服务。自定义模板可以通过原有 `--home` 指定；导出内嵌模板则执行：

```bash
mongoplus export-goctl-templates --output .goctl
```

goctl 的增量生成会保留已有的主入口、配置、ServiceContext、Handler 和 Logic 等文件；升级模板不会自动升级这些旧文件。迁移已有服务时，在临时目录生成一份进行对照，合并需要的初始化和校验逻辑，保留业务实现。

## 3. .api 中直接声明校验

```text
type CheckReq {
    Id int64 `json:"id" validate:"required,gt=0"`
    Amount string `json:"amount" validate:"required,decimal"`
    Mobile string `json:"mobile" validate:"required,checkMobile"`
    StartTime string `json:"startTime,optional" validate:"omitempty,dateTime"`
    DocumentID string `json:"documentId,optional" validate:"omitempty,objectId"`
}
```

生成的 Handler 依次执行：

```go
// 兼容数字与字符串输入；string 字段不需要 decimal:"true" 标签。
if err := decimaljson.PrepareRequest(r, &req); err != nil {
    httpx.ErrorCtx(r.Context(), w, err)
    return
}
// 保留 go-zero 对 JSON、path、form 等参数的解析与检查。
if err := httpx.Parse(r, &req); err != nil {
    httpx.ErrorCtx(r.Context(), w, err)
    return
}
// 根据 validate 标签校验，错误为首个字段的中文提示。
if validation.HasRules(&req) {
    if err := validation.Validate(&req); err != nil {
        httpx.ErrorCtx(r.Context(), w, err)
        return
    }
}
```

支持整数接收 `"1"`、字符串接收 JSON 数字、嵌套字段和数组元素的兼容转换；整数溢出、非整数的小数字符串会报错，不先经过 float64 而丢失 int64 精度。它不负责把 string 转成 bson.ObjectID 或 bson.Decimal128，进入 Repository 前仍需在业务层显式解析。

`decimal` 是校验规则，用于非负、至多两位小数字符串；它与自动类型兼容是两回事。更高精度的金额需使用项目自定义规则。嵌套数组验证可写 `validate:"required,dive"`，元素结构体内继续声明字段规则。

ObjectID 请求使用 string，`objectId` 校验 24 位十六进制格式；通过后用 `bson.ObjectIDFromHex(req.DocumentID)` 解析。金额通过 `bson.ParseDecimal128(req.Amount)` 解析；两者都处理解析错误。DTO/VO 不直接复用数据库 Entity。

配置 `DtoPath`、`VoPath` 后，公共包生成器还会创建 `dto/user_dto.go`（CreateUserDTO、ModifyUserDTO）与 `vo/user_vo.go`（UserVO）。它们仅首次创建，后续保留业务修改。VO 保留 BSON 标签，可以直接传入 Repository 的 Scan/ScanByPage；DTO 将 ObjectID、金额、时间转换为请求用字符串，并生成数字兼容解码方法。`.api` 仍负责 go-zero HTTP 契约和 types，DTO 不会自动替换 goctl 的类型，业务可按需映射。完整的字段规则见 [DTO 与 VO](../README.md#dto-与-vo)。

可空请求字段同时考虑 go-zero 的 `json:",optional"` 和校验器的 `omitempty`；数字允许 0 时不能用 `required` 表示“必须出现”，应根据协议使用指针区分缺失与零值。

更多内置规则、中文提示、自定义规则以及 Gin 绑定复用见 [参数校验文档](validation.md)。已有项目使用本地规则时保留自己的配置实例，避免同一请求重复校验。

## 4. MongoDB 连接与 Repository 注入

生成的 `etc/shop-api.yaml` 中有运行时连接配置：

```yaml
MongoConfig:
  Enabled: true
  URI: "mongodb://127.0.0.1:27017"
  Database: shop
  MaxPoolSize: 100
  TimeoutSeconds: 10
```

实际访问数据库时必须开启 MongoConfig.Enabled。启用后，NewServiceContext 在启动时创建一个 Client 并 Ping，连接成功后自动调用 `mongoplus.Use(db)` 注册进程默认数据库，所有生成的 `repository.NewXRepository()` 均可无参构造。连接失败则停止启动；超时必须在 1～3600 秒之间。关闭时 MongoDB 字段为 nil，不注册或清除已有默认连接，不应调用数据库业务接口。

已有服务的 ServiceContext 不会被 goctl 增量生成覆盖；升级工具后，需要在原 `mongoplus.Connect` 成功且错误检查通过后手动合并一次 `mongoplus.Use(db)`。默认连接是进程级配置，多个服务初始化时以后一次成功注册为准；多数据源需要固定连接时，继续使用 `repository.NewXRepository(db)` 显式指定。

需要同步 schema 时，运行时配置与可选 `mongo-plus.yaml` 中的数据库配置应指向相同的目标环境；URI 从业务配置读取，不写进 Entity、Repository 或 Logic。

在生成的 ServiceContext 中添加业务需要的 Repository 字段和构造赋值，例如：

```go
// 在现有 ServiceContext 中增加字段；保留 Config、MongoDB 和中间件。
UserRepository repository.UserRepository

// 在 NewServiceContext 已创建 db 后、返回前构造 Repository。
// 业务需要 MongoDB 时应要求开启 MongoConfig，再初始化所需仓储。
if db == nil {
    panic("用户模块需要开启 MongoConfig.Enabled")
}
users := repository.NewUserRepository() // 模板已注册默认连接，无需每个仓储重复传 db。
// 需要固定到其他数据源时可用 repository.NewUserRepository(otherDB)。

// 在原 return &ServiceContext{...} 内增加：
UserRepository: users,
```

其中 repository 导入路径为 `example.com/shop/internal/dal/repository`。这些是需要合并到现有结构体/函数的片段，不是独立 Go 文件。模板不会根据所有集合自动暴露 HTTP 接口，也不会猜测某个业务模块必须注入哪些 Repository。

Logic 中通过注入的仓储访问数据：

```go
user, err := l.svcCtx.UserRepository.FindById(l.ctx, req.Id)
if err != nil {
    return nil, err
}
```

复杂关联、统计与事务放在非生成的 Repository 扩展中，继续传递请求 ctx 或 Transaction 提供的 txCtx。main 在服务结束时先 Stop HTTP，再通过 ServiceContext.Close 释放 Client。集合、校验器和索引可在配置了数据源的代码生成阶段同步，或单独使用 schema plan/apply 预览和执行；不随服务启动自动迁移。

## 5. Makefile：生成、启动与构建

在业务服务目录创建以下 Makefile；命令前使用 Tab。当前未发布的源码先按第 1 节安装 mongoplus，CLI 与业务依赖保持一致。

```makefile
.PHONY: gen validate swagger run run-open build schema-plan schema-apply

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

schema-plan:
	mongoplus schema plan --config mongo-plus.yaml

schema-apply:
	mongoplus schema apply --config mongo-plus.yaml
```

可参考 [示例 Makefile](../examples/gozero/Makefile) 将 install-tools 作为 validate 的前置依赖，以保证安装、校验、生成按顺序执行。日常 run/build 不自动更新 CLI、不重新生成 Handler 或改动业务 Logic。

## 6. HTTP 配置与 Swagger

以下配置沿用 gorm-plus 的使用习惯，生成 YAML 中均有中文注释：

| 配置 | 功能 |
| --- | --- |
| ResponseConfig | `code/message/result` 统一响应，BypassPaths 精确路径跳过包装 |
| CorsConfig | 来源、额外请求头、前端需要读取的响应头 |
| StaticConfig | 本机公开文件目录映射，默认关闭；上传接口由业务实现 |
| Log / LogRotation | 日志级别与格式、按天/小时文件归档、压缩和清理 |
| SwaggerConfig | 内嵌 Swagger UI 和文档，默认关闭 |

开启文档示例：

```yaml
SwaggerConfig:
  Enabled: true
  Path: /swagger
  ApiFile: shop.api
```

执行 make run/build 会先按同一份配置更新文档，再运行/构建。关闭时准备占位文件；开启但未生成有效文档时启动失败。运行时打印完整 Swagger UI/JSON 地址，部署只需二进制与业务配置，不要求部署机安装 goctl，也不依赖 CDN。

详细配置、独立部署和 Air 热重载见 [Swagger 文档](swagger.md)。已有服务的自定义错误回调可以继续使用，模板默认成功码为 0、默认失败码为 1，业务错误可通过 httpresponse.NewError 指定自己的 code。
