package main

import (
	"context"
	_ "embed"
	"errors"
	"fmt"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/migration"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// 每个版本单独保存源码并嵌入自身；新版本追加文件，不修改已应用的旧文件。
//
//go:embed 2026101601_status_index.go
var statusIndexSource []byte

func statusIndexMigration() mp.Migration {
	return mp.Migration{
		Version:     "2026101601_status_index",
		Description: "示例：为已有 users 集合增加状态索引",
		Checksum:    migration.Checksum(statusIndexSource),
		Up: func(ctx context.Context, db *mongo.Database) error {
			names, err := db.ListCollectionNames(ctx, bson.D{{Key: "name", Value: "users"}})
			if err != nil {
				return err
			}
			if len(names) == 0 {
				return fmt.Errorf("示例需要已有 users 集合，请先按业务调整迁移源码，不会自动创建空集合")
			}
			_, err = db.Collection("users").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "status", Value: 1}}, Options: options.Index().SetName("idx_status_v2026101601")})
			return err
		},
		Down: func(ctx context.Context, db *mongo.Database) error {
			err := db.Collection("users").Indexes().DropOne(ctx, "idx_status_v2026101601")
			var commandErr mongo.CommandError
			if errors.As(err, &commandErr) && (commandErr.Code == 26 || commandErr.Code == 27) {
				return nil
			}
			return err
		},
	}
}
