package mongoplus

import (
	"context"
	"fmt"

	"github.com/kuangshp/mongo-plus/migration"
)

type Migration = migration.Migration
type MigrationOptions = migration.Options

// RunMigrations 在业务的独立发布命令中执行；不要放进每个服务实例的启动入口。
// 迁移历史/互斥锁由 migration 包管理，DDL 和历史数据不会自动整体回滚。
func RunMigrations(ctx context.Context, db *Database, steps []Migration, opts MigrationOptions) error {
	if db == nil || db.native == nil {
		return fmt.Errorf("迁移数据库不能为空")
	}
	return migration.Run(ctx, db.native, steps, opts)
}
