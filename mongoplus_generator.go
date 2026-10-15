package mongoplus

import (
	"context"
	"fmt"

	"github.com/kuangshp/mongo-plus/generator"
	"github.com/kuangshp/mongo-plus/generator/gozero"
	"github.com/kuangshp/mongo-plus/schema"
)

// GeneratorConfig 是代码生成配置；支持直接在项目根目录 generator.go 中构造。
// Package、ModelPkgPath、OutPath、RepoPath、DtoPath、VoPath 的含义与 gorm-plus 对应字段一致。
// SchemaDir 指向 JSON 实体定义；生成文件本身不需要 MongoDB 连接。
type GeneratorConfig = schema.Config

// GeneratorModel 注册已有的手写 Entity，生成 DAO/Repository，不覆盖原始 Entity。
type GeneratorModel = schema.Model

// GeneratorTenant 为所有实体设置默认租户字段。
type GeneratorTenant = schema.Tenant

// Bool 用于显式覆盖逻辑删除、时间字段等可选布尔配置。
func Bool(value bool) *bool { return schema.Bool(value) }

// LoadGeneratorConfig 加载可选的生成器 YAML；路径相对于该配置文件。
func LoadGeneratorConfig(path string) (*GeneratorConfig, error) {
	c, err := schema.LoadConfig(path)
	if err != nil {
		return nil, err
	}
	return &c, nil
}

// Generate 根据 JSON 定义生成 Entity、DAO、Repository，以及按需配置的 DTO/VO。
// Go 配置中的相对路径以当前工作目录为基准；YAML 配置以文件所在目录为基准。
// 配置 MongoDB 时先同步集合、校验器和索引；不配置则只生成文件。
// 同步失败会停止；已有 Repository 扩展、DTO、VO 会被保留。
func Generate(c *GeneratorConfig) error {
	if c == nil {
		return fmt.Errorf("GeneratorConfig 不能为空")
	}
	if c.HasMongoDB() {
		if _, err := SyncSchema(context.Background(), c); err != nil {
			return err
		}
	}
	_, err := generator.Generate(*c)
	return err
}

// GenerateModels 根据已有 Entity 生成 DAO/Repository 和可选 DTO/VO，避免再次维护 JSON 和重复定义实体。
func GenerateModels(c *GeneratorConfig, models ...GeneratorModel) error {
	if c == nil {
		return fmt.Errorf("GeneratorConfig 不能为空")
	}
	if c.HasMongoDB() {
		if _, err := SyncModels(context.Background(), c, models...); err != nil {
			return err
		}
	}
	_, err := generator.GenerateModels(*c, models...)
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
