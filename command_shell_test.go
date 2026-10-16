package mongoplus

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
)

func TestShellInsertIsReadableAndRedacted(t *testing.T) {
	raw := logRaw(t, bson.D{
		{Key: "insert", Value: "users"}, {Key: "ordered", Value: true},
		{Key: "documents", Value: bson.A{bson.D{
			{Key: "name", Value: "张三"}, {Key: "age", Value: int64(28)},
			{Key: "email", Value: "zhangsan@example.com"}, {Key: "tags", Value: bson.A{"dev", "mongodb"}},
			{Key: "createdAt", Value: time.Date(2026, 10, 16, 12, 39, 30, 123000000, time.UTC)},
		}}},
		{Key: "lsid", Value: bson.D{{Key: "id", Value: "session-value"}}},
		{Key: "txnNumber", Value: int64(99)}, {Key: "$db", Value: "shop"},
	})
	before := bytes.Clone(raw)
	got := shellCommand(raw, LogConfig{MaxLength: 4096})
	want := `db.users.insertOne({
  name: "张三",
  age: 28,
  email: "[REDACTED]",
  tags: ["dev", "mongodb"],
  createdAt: ISODate("2026-10-16T12:39:30.123Z")
})`
	if got != want {
		t.Fatalf("got:\n%s\nwant:\n%s", got, want)
	}
	if !bytes.Equal(raw, before) {
		t.Fatal("格式化修改了驱动的原始 BSON")
	}
}

func TestShellCommandMethodsAndOptions(t *testing.T) {
	filter := bson.D{{Key: "status", Value: int32(1)}}
	update := bson.D{{Key: "$inc", Value: bson.D{{Key: "version", Value: int64(1)}}}}
	for _, tc := range []struct {
		name string
		doc  bson.D
		want []string
	}{
		{"insert many", bson.D{{Key: "insert", Value: "users"}, {Key: "documents", Value: bson.A{filter, filter}}, {Key: "ordered", Value: false}}, []string{"db.users.insertMany([\n", "ordered: false"}},
		{"find", bson.D{{Key: "find", Value: "users"}, {Key: "filter", Value: filter}, {Key: "projection", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "sort", Value: bson.D{{Key: "_id", Value: -1}}}, {Key: "skip", Value: 2}, {Key: "limit", Value: 5}, {Key: "let", Value: bson.D{{Key: "p", Value: "secret-value"}}}}, []string{"db.users.find({\n", "_id: 1", "p: \"?\"", ".sort({\n", ".skip(2).limit(5)"}},
		{"update one", bson.D{{Key: "update", Value: "users"}, {Key: "updates", Value: bson.A{bson.D{{Key: "q", Value: filter}, {Key: "u", Value: update}, {Key: "upsert", Value: true}, {Key: "hint", Value: "status_1"}}}}}, []string{"db.users.updateOne({\n", "$inc:", "upsert: true", "hint: \"status_1\""}},
		{"update many", bson.D{{Key: "update", Value: "users"}, {Key: "updates", Value: bson.A{bson.D{{Key: "q", Value: filter}, {Key: "u", Value: update}, {Key: "multi", Value: true}}}}}, []string{"db.users.updateMany("}},
		{"replace", bson.D{{Key: "update", Value: "users"}, {Key: "updates", Value: bson.A{bson.D{{Key: "q", Value: filter}, {Key: "u", Value: filter}}}}}, []string{"db.users.replaceOne("}},
		{"delete one", bson.D{{Key: "delete", Value: "users"}, {Key: "deletes", Value: bson.A{bson.D{{Key: "q", Value: filter}, {Key: "limit", Value: 1}, {Key: "collation", Value: bson.D{{Key: "locale", Value: "simple"}}}}}}}, []string{"db.users.deleteOne(", "collation:", "locale: \"simple\""}},
		{"delete many", bson.D{{Key: "delete", Value: "users"}, {Key: "deletes", Value: bson.A{bson.D{{Key: "q", Value: filter}, {Key: "limit", Value: 0}}}}}, []string{"db.users.deleteMany("}},
		{"aggregate", bson.D{{Key: "aggregate", Value: "users"}, {Key: "pipeline", Value: bson.A{bson.D{{Key: "$match", Value: filter}}}}, {Key: "cursor", Value: bson.D{}}, {Key: "allowDiskUse", Value: true}}, []string{"db.users.aggregate([\n", "$match:", "allowDiskUse: true"}},
		{"find modify", bson.D{{Key: "findAndModify", Value: "users"}, {Key: "query", Value: filter}, {Key: "update", Value: update}, {Key: "new", Value: true}}, []string{"db.users.findAndModify({\n", "query:", "new: true"}},
		{"count", bson.D{{Key: "count", Value: "users"}, {Key: "query", Value: filter}}, []string{"db.users.countDocuments({\n"}},
		{"estimated count", bson.D{{Key: "count", Value: "users"}}, []string{"db.users.estimatedDocumentCount()"}},
		{"distinct", bson.D{{Key: "distinct", Value: "users"}, {Key: "key", Value: "status"}, {Key: "query", Value: filter}}, []string{"db.users.distinct(\"status\", {\n"}},
		{"batch fallback", bson.D{{Key: "update", Value: "users"}, {Key: "updates", Value: bson.A{bson.D{{Key: "q", Value: filter}, {Key: "u", Value: update}}, bson.D{{Key: "q", Value: filter}, {Key: "u", Value: update}}}}}, []string{"db.runCommand({\n", "updates: [\n"}},
		{"invalid replacement", bson.D{{Key: "update", Value: "users"}, {Key: "updates", Value: bson.A{bson.D{{Key: "q", Value: filter}, {Key: "u", Value: filter}, {Key: "multi", Value: true}}}}}, []string{"db.runCommand({\n", "multi: true"}},
		{"invalid multi", bson.D{{Key: "update", Value: "users"}, {Key: "updates", Value: bson.A{bson.D{{Key: "q", Value: filter}, {Key: "u", Value: update}, {Key: "multi", Value: "invalid"}}}}}, []string{"db.runCommand({\n", "multi: \"invalid\""}},
		{"invalid delete limit", bson.D{{Key: "delete", Value: "users"}, {Key: "deletes", Value: bson.A{bson.D{{Key: "q", Value: filter}, {Key: "limit", Value: 2}}}}}, []string{"db.runCommand({\n", "limit: 2"}},
		{"get more", bson.D{{Key: "getMore", Value: int64(123)}, {Key: "collection", Value: "users"}, {Key: "batchSize", Value: 20}}, []string{"db.runCommand({\n", "getMore: 123", "batchSize: 20"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := shellCommand(logRaw(t, tc.doc), LogConfig{MaxLength: 4096})
			for _, want := range tc.want {
				if !strings.Contains(got, want) {
					t.Fatalf("缺少 %q:\n%s", want, got)
				}
			}
			if strings.Contains(got, "secret-value") {
				t.Fatal("表达式参数被恢复为明文")
			}
		})
	}
}

func TestShellTypesEscapingAndLimits(t *testing.T) {
	id := bson.NewObjectID()
	decimal, err := bson.ParseDecimal128("12.34")
	if err != nil {
		t.Fatal(err)
	}
	got := shellCommand(logRaw(t, bson.D{{Key: "insert", Value: "order-items"}, {Key: "documents", Value: bson.A{bson.D{
		{Key: "_id", Value: id}, {Key: "large", Value: int64(9007199254740993)}, {Key: "price", Value: decimal},
		{Key: "pattern", Value: bson.Regex{Pattern: "^a", Options: "i"}}, {Key: "stamp", Value: bson.Timestamp{T: 123, I: 2}},
		{Key: "name", Value: "张三\n伪造日志\x1b[31m"}, {Key: "field.with.dot", Value: "quoted"},
	}}}}), LogConfig{MaxLength: 4096})
	for _, want := range []string{`db.getCollection("order-items").insertOne(`, `ObjectId("` + id.Hex() + `")`, `NumberLong("9007199254740993")`, `NumberDecimal("12.34")`, "EJSON.deserialize(", `\n伪造日志\u001b`, `"field.with.dot": "quoted"`} {
		if !strings.Contains(got, want) {
			t.Fatalf("类型或转义丢失 %q:\n%s", want, got)
		}
	}
	if strings.Contains(got, "\x1b") || strings.Contains(got, "\n伪造日志") {
		t.Fatal("数据中的控制字符进入日志")
	}
	raw := logRaw(t, bson.D{{Key: "insert", Value: "users"}, {Key: "documents", Value: bson.A{bson.D{{Key: "name", Value: strings.Repeat("汉字", 2000)}}}}})
	for _, limit := range []int{128, 129, 130, 256} {
		got := shellCommand(raw, LogConfig{MaxLength: limit})
		if len(got) > limit || !utf8.ValidString(got) || !strings.Contains(got, "truncated") {
			t.Fatalf("截断失效: limit=%d len=%d %q", limit, len(got), got)
		}
	}
	if got := shellCommand(bson.Raw{0}, LogConfig{MaxLength: 4096}); got != "[command unavailable]" {
		t.Fatal(got)
	}
}

func TestCommandLoggerShellDefaultAndJSONOptIn(t *testing.T) {
	for _, format := range []string{"", "shell", "json"} {
		t.Run(format, func(t *testing.T) {
			var out bytes.Buffer
			monitor, err := NewCommandLogger(LogConfig{Level: LogInfo, Format: format, HideParameters: true, Writer: &out})
			if err != nil {
				t.Fatal(err)
			}
			raw := logRaw(t, bson.D{{Key: "insert", Value: "users"}, {Key: "documents", Value: bson.A{bson.D{{Key: "name", Value: "business-name"}, {Key: "email", Value: "private@example.com"}}}}})
			ctx := context.WithValue(context.Background(), commandCallerKey{}, "/work/create.go:42")
			monitor.Started(ctx, &event.CommandStartedEvent{CommandName: "insert", DatabaseName: "shop", Command: raw, RequestID: 1})
			monitor.Succeeded(ctx, &event.CommandSucceededEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "insert", RequestID: 1, Duration: time.Millisecond}, Reply: logRaw(t, bson.M{"n": 1})})
			got := out.String()
			if strings.Contains(got, "business-name") || strings.Contains(got, "private@example.com") || !strings.Contains(got, "/work/create.go:42\n") || !strings.Contains(got, "[1.000ms] [rows:1]") {
				t.Fatalf("脱敏或调用位置丢失:\n%s", got)
			}
			if format == "json" {
				if !strings.Contains(got, "shop.insert {\"insert\":\"users\"") {
					t.Fatal(got)
				}
			} else if !strings.Contains(got, "[db:shop]\ndb.users.insertOne({\n") || !strings.Contains(got, `name: "?"`) {
				t.Fatal(got)
			}
		})
	}
}

func TestReplayableShellUsesCompleteOriginalTypedValues(t *testing.T) {
	raw := logRaw(t, bson.D{{Key: "insert", Value: "user"}, {Key: "documents", Value: bson.A{bson.D{
		{Key: "qty", Value: int64(28)}, {Key: "ratio", Value: float64(1)},
		{Key: "email", Value: "real@example.com"}, {Key: "password", Value: "fixture-password"},
	}}}, {Key: "maxTimeMS", Value: int64(5000)}})
	got := shellCommand(raw, LogConfig{Replayable: true, MaxLength: 4096})
	for _, want := range []string{"db.user.insertOne({", `qty: NumberLong("28")`, `ratio: Double(1)`, `email: "real@example.com"`, `password: "fixture-password"`, "maxTimeMS: 5000", ");"} {
		if !strings.Contains(got, want) {
			t.Fatalf("无法按原值重放，缺少 %q: %s", want, got)
		}
	}
	raw = logRaw(t, bson.D{{Key: "insert", Value: "user"}, {Key: "documents", Value: bson.A{bson.D{{Key: "name", Value: strings.Repeat("汉字", 1000)}}}}})
	if got := shellCommand(raw, LogConfig{Replayable: true, MaxLength: 128}); !strings.HasPrefix(got, "// [statement omitted:") || strings.Contains(got, "db.user") {
		t.Fatalf("输出了可误执行的残缺语句: %s", got)
	}
	for _, command := range []string{"getMore", "commitTransaction", "abortTransaction"} {
		if got := shellCommand(logRaw(t, bson.D{{Key: command, Value: 1}}), LogConfig{Replayable: true}); !strings.HasPrefix(got, "// [not replayable:") {
			t.Fatal(got)
		}
	}
	for _, cfg := range []LogConfig{{Replayable: true, HideParameters: true}, {Replayable: true, Format: "json"}} {
		if _, err := NewCommandLogger(cfg); err == nil {
			t.Fatalf("允许了不能保留真实语句的配置: %+v", cfg)
		}
	}
}
