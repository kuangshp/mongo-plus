package mongoplus

import (
	"context"
	"fmt"

	"github.com/kuangshp/mongo-plus/generator"
)

// RunGenerator 供业务根目录 generator.go 统一执行生成、检查、计划或同步。
// action 支持 generate（或空）、check、plan、apply；不读取配置文件或全局命令行参数。
// 所有操作先完成配置、JSON、渲染、跨文件名称与覆盖预检；check 不写入。
// plan/apply 返回变更记录，生成配置必须带完整数据源。
// generate 在预检后按需同步数据库，再写入同一快照的代码；没有数据源只写文件。
// 调用方负责解析参数、加载业务连接配置、设置 Context 期限并展示变更结果。
func RunGenerator(ctx context.Context, c *GeneratorConfig, action string) ([]SchemaChange, error) {
	if ctx == nil {
		return nil, fmt.Errorf("生成器 context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if c == nil {
		return nil, fmt.Errorf("GeneratorConfig 不能为空")
	}
	if action == "" {
		action = "generate"
	}
	switch action {
	case "generate", "check", "plan", "apply":
	default:
		return nil, fmt.Errorf("不支持的生成操作 %q，可用值为 generate、check、plan、apply", action)
	}
	cfg := *c
	if err := cfg.Normalize(); err != nil {
		return nil, err
	}
	// 必须在任何数据库访问前，完成同一 JSON 快照的渲染、跨文件名称与覆盖检查。
	plan, err := generator.Prepare(cfg)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	switch action {
	case "check":
		return nil, nil
	case "plan", "apply":
		return schemaChanges(ctx, cfg, plan, action == "apply")
	default:
		if cfg.HasMongoDB() {
			if _, err := schemaChanges(ctx, cfg, plan, true); err != nil {
				return nil, err
			}
		}
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		_, err := plan.Write()
		return nil, err
	}
}
