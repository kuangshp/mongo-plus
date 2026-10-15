package mongoplus_test

import (
	"context"
	"strings"
	"sync"
	"testing"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
)

func TestAggregatePageExceedsSingleBSONDocument(t *testing.T) {
	var mu sync.Mutex
	var commands []bson.Raw
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "aggregate" {
			mu.Lock()
			commands = append(commands, append(bson.Raw(nil), e.Command...))
			mu.Unlock()
		}
	}}
	db := mp.NewDatabase(configuredDatabase(t, options.Client().SetMonitor(monitor)))
	r := mp.MustRepository[ackDocument, bson.ObjectID](db, mp.CollectionConfig{Name: "large_page", SoftDelete: true})
	ctx := context.Background()
	const count = 40
	payload := strings.Repeat("x", 600<<10) // 整页约 23 MiB，每条文档仍远小于 16 MiB。
	rows := make([]*ackDocument, count)
	for i := range rows {
		rows[i] = &ackDocument{Name: payload}
	}
	if err := r.CreateBatch(ctx, rows); err != nil {
		t.Fatal(err)
	}
	query := r.WithContext(ctx).Order(field.New[bson.ObjectID]("_id").Asc())
	var page []*ackDocument
	total, err := query.ScanByPage(&page, 1, count)
	if err != nil || total != count || len(page) != count {
		t.Fatalf("大于 16 MiB 的整页失败: len=%d total=%d err=%v", len(page), total, err)
	}
	for _, row := range page {
		if row.Name != payload {
			t.Fatal("页面解码截断")
		}
	}
	mu.Lock()
	if len(commands) != 2 {
		t.Errorf("预期计数与列表两个聚合，得到 %d", len(commands))
	}
	for _, command := range commands {
		if strings.Contains(command.String(), `"$facet"`) {
			t.Error("分页仍依赖 facet")
		}
	}
	mu.Unlock()
	// 明确的 snapshot 事务选项可继续传入；计数与列表保留同一个 Session。
	mu.Lock()
	commands = nil
	mu.Unlock()
	err = mp.Transaction(ctx, db, func(txCtx context.Context) error {
		result, n, err := mp.ScanByPage[ackDocument](txCtx, mp.From(r).Select(field.New[bson.ObjectID]("_id")), 2, 10)
		if err == nil && (n != count || len(result) != 10) {
			t.Fatalf("事务分页错误: %d/%d", len(result), n)
		}
		return err
	}, options.Transaction().SetReadConcern(readconcern.Snapshot()))
	if err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	if len(commands) != 2 {
		t.Errorf("事务应执行计数和列表两个聚合，得到 %d", len(commands))
	} else {
		level, err := commands[0].LookupErr("readConcern", "level")
		if err != nil || level.StringValue() != "snapshot" {
			t.Errorf("snapshot 事务选项没有发送到服务端: %v", err)
		}
		if commands[0].Lookup("lsid").String() != commands[1].Lookup("lsid").String() || commands[0].Lookup("txnNumber").String() != commands[1].Lookup("txnNumber").String() {
			t.Error("计数和列表没有使用同一事务")
		}
	}
	mu.Unlock()
	total, err = query.ScanByPage(&page, 10, count)
	if err != nil || total != count || len(page) != 0 || page == nil {
		t.Fatalf("越界页应保留总数和非 nil 空切片: %d %v", total, err)
	}
}
