package mongoplus

import (
	"context"

	"github.com/kuangshp/mongo-plus/generator/gozero"
	"github.com/kuangshp/mongo-plus/schema"
)

// GeneratorConfig 是代码生成配置；支持直接在项目根目录 generator.go 中构造。
// Package、ModelPkgPath、OutPath、RepoPath、DtoPath、VoPath 的含义与 gorm-plus 对应字段一致。
// SchemaDir 是唯一模型来源，必须先编写 JSON 实体定义；生成文件本身不需要 MongoDB 连接。
type GeneratorConfig = schema.Config

// GeneratorTenant 为所有实体设置默认租户字段。
type GeneratorTenant = schema.Tenant

// Bool 用于显式覆盖逻辑删除、时间字段等可选布尔配置。
func Bool(value bool) *bool { return schema.Bool(value) }

// Generate 根据 JSON 定义生成 Entity、DAO、Repository，以及按需配置的 DTO/VO。
// 相对路径以 ProjectDir 为基准；未设置时使用当前工作目录。
// 先完成渲染与名称/覆盖预检；有 MongoDB 时再同步集合、校验器和索引并写入同一快照。
// 同步失败会停止；已有 Repository 扩展、DTO、VO 会被保留。
func Generate(c *GeneratorConfig) error {
	_, err := RunGenerator(context.Background(), c, "generate")
	return err
}

// GoctlRunOptions 配置 goctl 可执行文件和可选的外部模板目录。
type GoctlRunOptions = gozero.RunOptions

// RunGoctl 使用内嵌模板执行 goctl；公共包本身不依赖 go-zero。
func RunGoctl(ctx context.Context, options GoctlRunOptions, arguments ...string) ([]byte, error) {
	return gozero.Run(ctx, options, arguments...)
}

// WriteGoctlTemplateHome 导出内嵌模板，可用于 goctl 的 --home 参数。
func WriteGoctlTemplateHome(destination string) error { return gozero.WriteTemplateHome(destination) }
