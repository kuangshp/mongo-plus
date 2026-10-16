package mongoplus

import (
	"context"
	"testing"

	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// 只验证句柄选择，不执行数据库命令，也不依赖运行中的 MongoDB。
func TestResolveDatabaseSelection(t *testing.T) {
	previous := DefaultDB()
	t.Cleanup(func() { Use(previous) })
	client, err := mongo.Connect(options.Client().ApplyURI("mongodb://127.0.0.1:1"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = client.Disconnect(context.Background()) })
	mainDB := NewDatabase(client.Database("main"))
	archiveDB := NewDatabase(client.Database("archive"))
	reportDB := NewDatabase(client.Database("report"))
	Use(mainDB)

	for _, tc := range []struct {
		name string
		args []*Database
		want *Database
	}{
		{"default", nil, mainDB},
		{"explicit main", []*Database{mainDB}, mainDB},
		{"explicit archive", []*Database{archiveDB}, archiveDB},
		{"explicit report", []*Database{reportDB}, reportDB},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ResolveDatabase(tc.args...)
			if err != nil || got != tc.want {
				t.Fatalf("未返回指定的原始句柄: got=%p want=%p err=%v", got, tc.want, err)
			}
			if DefaultDB() != mainDB {
				t.Fatal("显式选择修改了默认数据库")
			}
		})
	}

	for _, args := range [][]*Database{{nil}, {{}}, {mainDB, archiveDB}} {
		if got, err := ResolveDatabase(args...); err == nil || got != nil {
			t.Fatalf("无效参数被接受或回退到默认连接: got=%p err=%v", got, err)
		}
	}
	for _, invalidDefault := range []*Database{nil, {}} {
		Use(invalidDefault)
		if got, err := ResolveDatabase(); err == nil || got != nil {
			t.Fatalf("无效默认连接被接受: got=%p err=%v", got, err)
		}
		if got, err := ResolveDatabase(archiveDB); err != nil || got != archiveDB {
			t.Fatalf("显式连接错误依赖默认连接: got=%p err=%v", got, err)
		}
	}
}
