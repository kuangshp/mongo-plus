package mongoplus_test

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"runtime"
	"strings"
	"testing"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/examples/query/model"
	"github.com/kuangshp/mongo-plus/examples/query/repository"
	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestCommandLoggerConnectAndBusinessCaller(t *testing.T) {
	base := testutil.Database(t)
	var out bytes.Buffer
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	db, err := mp.Connect(ctx, mp.Config{URI: os.Getenv("MONGOPLUS_TEST_URI"), Database: base.Name(), Log: mp.LogConfig{Level: mp.LogInfo, Writer: &out}})
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close(context.Background())
	r := repository.NewEventRepository(db)
	if err := r.Create(ctx, &model.EventEntity{Message: "logger integration"}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), ".insert") || !strings.Contains(out.String(), "[rows:1]") || !strings.Contains(out.String(), "logger integration") {
		t.Fatalf("insert log: %s", &out)
	}
	for _, want := range []string{"db.events.insertOne({\n", "ObjectId(", "ISODate("} {
		if !strings.Contains(out.String(), want) {
			t.Fatalf("实际插入命令未转成 shell 格式，缺少 %q: %s", want, &out)
		}
	}
	for _, noise := range []string{"lsid:", "txnNumber:", "$numberLong", "$date"} {
		if strings.Contains(out.String(), noise) {
			t.Fatalf("实际插入日志仍含协议噪声 %q: %s", noise, &out)
		}
	}
	t.Logf("实际插入日志示例:\n%s", &out)
	out.Reset()
	_, file, line, _ := runtime.Caller(0)
	got, err := r.FindList(ctx, mp.Query().Where(field.NewString("message").Eq("logger integration")).Build())
	if err != nil || len(got) != 1 {
		t.Fatalf("find: %v %v", got, err)
	}
	if !strings.Contains(out.String(), fmt.Sprintf("%s:%d\n", file, line+1)) || !strings.Contains(out.String(), "db.events.find({\n") || !strings.Contains(out.String(), "[rows:1]") {
		t.Fatalf("business caller/command missing: %s", &out)
	}
	out.Reset()
	_, _, line, _ = runtime.Caller(0)
	_, err = r.FindList(ctx, mp.Query().WithCache(time.Minute).Build())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), fmt.Sprintf("%s:%d\n", file, line+1)) {
		t.Fatalf("cached query lost business caller: %s", &out)
	}
	out.Reset()
	if _, err = r.FindList(ctx, mp.Query().WithCache(time.Minute).Build()); err != nil {
		t.Fatal(err)
	}
	if out.Len() != 0 {
		t.Fatalf("cache hit must not log an unexecuted command: %s", &out)
	}
	if err = db.Native().Collection("events").FindOne(ctx, bson.D{}, options.FindOne().SetSkip(0)).Err(); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), ".find") {
		t.Fatalf("native command not logged: %s", &out)
	}
	t.Logf("实际命令日志示例:\n%s", &out)
}
