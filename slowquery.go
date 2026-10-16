package mongoplus

import (
	"context"
	"fmt"
	"log"
	"time"
)

// SlowQueryInfo 是超过阈值的操作。MongoDB 没有 SQL 文本，这里记录集合和操作名。
type SlowQueryInfo struct {
	Collection string
	Operation  string
	Duration   time.Duration
	Error      error
}

// SlowQueryConfig 配置慢操作阈值。Threshold 为 0 时使用 200ms。
type SlowQueryConfig struct {
	Threshold time.Duration
	Logger    func(context.Context, SlowQueryInfo)
}

// RegisterSlowQuery 在现有 Observer 之外记录超过阈值的操作。应在设置 Observer 之后调用。
func RegisterSlowQuery(db *Database, cfg SlowQueryConfig) error {
	if db == nil {
		return fmt.Errorf("慢查询数据库不能为空")
	}
	if cfg.Threshold <= 0 {
		cfg.Threshold = 200 * time.Millisecond
	}
	if cfg.Logger == nil {
		cfg.Logger = func(_ context.Context, info SlowQueryInfo) {
			log.Printf("慢操作 collection=%s operation=%s cost=%s err=%v", info.Collection, info.Operation, info.Duration, info.Error)
		}
	}
	previous := db.Observer
	db.Observer = func(ctx context.Context, observation Observation) {
		if previous != nil {
			previous(ctx, observation)
		}
		if observation.Duration >= cfg.Threshold {
			cfg.Logger(ctx, SlowQueryInfo{
				Collection: observation.Collection,
				Operation:  observation.Operation,
				Duration:   observation.Duration,
				Error:      observation.Err,
			})
		}
	}
	return nil
}
