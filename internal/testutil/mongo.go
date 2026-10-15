// Package testutil 仅连接由测试显式指定的实例，为每个测试建立独立临时数据库。
package testutil

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Database 创建测试数据库；未显式提供 URI 时跳过真实数据库测试。
func Database(t *testing.T) *mongo.Database {
	t.Helper()
	uri := os.Getenv("MONGOPLUS_TEST_URI")
	if uri == "" {
		t.Skip("设置 MONGOPLUS_TEST_URI 运行真实 MongoDB 测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	clientOptions := options.Client().ApplyURI(uri).SetServerSelectionTimeout(2 * time.Second)
	if os.Getenv("MONGOPLUS_TEST_INIT") == "1" {
		clientOptions.SetReplicaSet("").SetDirect(true)
	}
	client, err := mongo.Connect(clientOptions)
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("MONGOPLUS_TEST_INIT") == "1" {
		err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetInitiate", Value: bson.D{}}}).Err()
		if err != nil {
			var command mongo.CommandError
			if !errors.As(err, &command) || command.Code != 23 {
				t.Fatal(err)
			}
		}
	}
	for {
		var hello struct {
			Primary bool `bson:"isWritablePrimary"`
		}
		err = client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello)
		if err == nil && hello.Primary {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("MongoDB 未就绪: %v", err)
		case <-time.After(100 * time.Millisecond):
		}
	}
	name := "mongo_plus_test_" + bson.NewObjectID().Hex()
	db := client.Database(name)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := db.Drop(ctx); err != nil {
			t.Error(fmt.Errorf("清理测试数据库: %w", err))
		}
		_ = client.Disconnect(ctx)
	})
	return db
}
