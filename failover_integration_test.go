package mongoplus_test

import (
	"context"
	"os"
	"testing"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// 仅显式指定专用三节点 URI 时运行；该测试会主动触发整个副本集的主节点切换。
func TestReplicaSetFailover(t *testing.T) {
	uri := os.Getenv("MONGOPLUS_FAILOVER_URI")
	if uri == "" {
		t.Skip("设置测试专用 MONGOPLUS_FAILOVER_URI 运行切主测试")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := mongo.Connect(options.Client().ApplyURI(uri).SetTimeout(20 * time.Second))
	if err != nil {
		t.Fatal(err)
	}
	native := client.Database("mongo_plus_test_failover_" + bson.NewObjectID().Hex())
	db := mp.NewDatabase(native)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if err := native.Drop(ctx); err != nil {
			t.Error(err)
		}
		_ = client.Disconnect(ctx)
	})
	var initial struct {
		Primary string   `bson:"primary"`
		Hosts   []string `bson:"hosts"`
	}
	if err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&initial); err != nil {
		t.Fatal(err)
	}
	if len(initial.Hosts) < 3 {
		t.Fatal("故障切换测试要求至少三个成员，拒绝对其他实例切主")
	}
	r := mp.MustRepository[benchDocument, bson.ObjectID](db, mp.CollectionConfig{Name: "documents"})
	row := &benchDocument{Payload: "before"}
	if err := r.Create(ctx, row); err != nil {
		t.Fatal(err)
	}
	// 切换期间的连接中断是预期结果；随后通过实际读写验证驱动恢复。
	stepErr := client.Database("admin").RunCommand(ctx, bson.D{{Key: "replSetStepDown", Value: 15}, {Key: "force", Value: true}}).Err()
	t.Logf("原主节点=%s，切主返回=%v", initial.Primary, stepErr)
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	for {
		var hello struct {
			Primary string `bson:"primary"`
		}
		err := client.Database("admin").RunCommand(ctx, bson.D{{Key: "hello", Value: 1}}).Decode(&hello)
		if err == nil && hello.Primary != "" && hello.Primary != initial.Primary {
			t.Logf("新主节点=%s", hello.Primary)
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("未恢复主节点: %v", ctx.Err())
		case <-ticker.C:
		}
	}
	if got, err := r.FindById(ctx, row.ID); err != nil || got.Payload != "before" {
		t.Fatalf("切主后读取失败 %+v %v", got, err)
	}
	if err := mp.Transaction(ctx, db, func(tx context.Context) error {
		return r.CreateBatch(tx, []*benchDocument{{Payload: "tx1"}, {Payload: "tx2"}})
	}); err != nil {
		t.Fatalf("切主后事务失败 %v", err)
	}
	if n, err := r.Count(ctx); err != nil || n != 3 {
		t.Fatalf("切主后数据错误 %d %v", n, err)
	}
}
