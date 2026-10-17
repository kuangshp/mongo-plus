// 独立发布命令示例：go run ./examples/migrations -uri "$MONGOPLUS_URI" -database "$MONGOPLUS_DATABASE" -action plan。
// 将本目录复制为业务项目的 cmd/migrate，并将固定集合名、迁移内容替换为业务定义。
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/migration"
)

func main() {
	uri := flag.String("uri", os.Getenv("MONGOPLUS_URI"), "数据库连接地址；默认读取 MONGOPLUS_URI")
	database := flag.String("database", os.Getenv("MONGOPLUS_DATABASE"), "数据库名；默认读取 MONGOPLUS_DATABASE")
	action := flag.String("action", "plan", "plan、apply、history、rollback、lock 或 unlock")
	version := flag.String("version", "", "显式回滚的最新版本")
	owner := flag.String("owner", "", "已确认原进程退出后，人工释放的锁 Owner")
	resume := flag.Bool("resume", false, "检查部分执行结果并确认幂等后恢复失败迁移")
	timeout := flag.Duration("timeout", 30*time.Minute, "独立迁移命令总超时")
	flag.Parse()
	if *timeout <= 0 {
		fmt.Fprintln(os.Stderr, "timeout 必须大于零")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()
	db, err := mp.Connect(ctx, mp.Config{URI: *uri, Database: *database})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	err = func() error {
		defer db.Close(context.Background())
		steps := []mp.Migration{statusIndexMigration()}
		var result any
		switch *action {
		case "plan":
			pending, e := migration.Plan(ctx, db.Native(), steps)
			if e != nil {
				return e
			}
			versions := []string{}
			for _, m := range pending {
				versions = append(versions, m.Version)
			}
			result = versions
		case "apply":
			err = mp.RunMigrations(ctx, db, steps, mp.MigrationOptions{ResumeFailed: *resume})
		case "rollback":
			err = migration.Rollback(ctx, db.Native(), steps, *version, migration.Options{ResumeFailed: *resume})
		case "history":
			result, err = migration.History(ctx, db.Native())
		case "lock":
			result, err = migration.CurrentLock(ctx, db.Native())
		case "unlock":
			err = migration.Unlock(ctx, db.Native(), *owner)
		default:
			return fmt.Errorf("不支持的 action %q", *action)
		}
		if err != nil {
			return err
		}
		if result != nil {
			return json.NewEncoder(os.Stdout).Encode(result)
		}
		fmt.Fprintln(os.Stdout, "迁移操作完成")
		return nil
	}()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
