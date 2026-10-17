package mongoplus

import (
	"context"
	"fmt"
	"time"

	"github.com/kuangshp/mongo-plus/generator"
	"github.com/kuangshp/mongo-plus/schema"
)

// SchemaChange 是本次结构同步的变更记录，不包含连接信息。
type SchemaChange = schema.Change

// SyncSchema 根据 JSON 定义显式创建集合、同步校验器和缺失索引。
// 先完成代码渲染与冲突预检，再使用 GeneratorConfig.MongoDB 连接；不写生成文件、不插入业务文档。
// 不删除已有集合、字段或索引；索引冲突会返回错误。整个同步不是原子事务。
func SyncSchema(ctx context.Context, c *GeneratorConfig) ([]SchemaChange, error) {
	return RunGenerator(ctx, c, "apply")
}

func schemaChanges(ctx context.Context, c GeneratorConfig, plan *generator.Plan, apply bool) ([]SchemaChange, error) {
	if ctx == nil {
		return nil, fmt.Errorf("同步数据库的 context 不能为空")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// 调用方可以传入自己的期限；未设置时限制整个同步最多 60 秒。
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 60*time.Second)
		defer cancel()
	}
	db, err := Connect(ctx, c.MongoDB)
	if err != nil {
		return nil, fmt.Errorf("连接生成器 MongoDB 数据源: %w", err)
	}
	defer func() {
		closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = db.Close(closeCtx)
	}()
	if !apply {
		return plan.PlanSchema(ctx, db.Native())
	}
	return plan.ApplySchema(ctx, db.Native())
}
