package mongoplus

import (
	"context"
	"os"
	"os/exec"
	"reflect"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// 直接让真正的 mongosh 执行日志，再用官方 Go 驱动检查结果和 BSON 类型。
// 只操作 testutil 创建的随机数据库；本机可指定 mongosh 路径，CI 复用数据库容器。
func TestShellStatementsExecuteInMongosh(t *testing.T) {
	mongosh := os.Getenv("MONGOPLUS_MONGOSH_PATH")
	container := os.Getenv("MONGOPLUS_MONGOSH_CONTAINER")
	if mongosh == "" && container == "" {
		t.Skip("设置 MONGOPLUS_MONGOSH_PATH 或 MONGOPLUS_MONGOSH_CONTAINER 运行真实 mongosh 重放测试")
	}
	db := testutil.Database(t)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	// 小整数也必须保留 long/double；否则日志虽然能解析，写入却会被实际校验器拒绝。
	if err := db.RunCommand(ctx, bson.D{{Key: "create", Value: "user"}, {Key: "validator", Value: bson.D{{Key: "$jsonSchema", Value: bson.D{
		{Key: "bsonType", Value: "object"}, {Key: "properties", Value: bson.D{
			{Key: "qty", Value: bson.D{{Key: "bsonType", Value: "long"}}},
			{Key: "ratio", Value: bson.D{{Key: "bsonType", Value: "double"}}},
		}},
	}}}}}).Err(); err != nil {
		t.Fatal(err)
	}
	row := bson.D{
		{Key: "_id", Value: int64(1)}, {Key: "qty", Value: int64(28)}, {Key: "ratio", Value: float64(1)},
		{Key: "name", Value: "张三\n原始值"}, {Key: "email", Value: "real@example.com"}, {Key: "password", Value: "fixture-password"},
		{Key: "large", Value: int64(9007199254740993)}, {Key: "createdAt", Value: bson.DateTime(1792154701123)},
		{Key: "oid", Value: bson.NewObjectID()}, {Key: "tags", Value: bson.A{"dev", "mongodb"}},
		{Key: "binary", Value: bson.Binary{Data: []byte{1, 2, 3}}}, {Key: "stamp", Value: bson.Timestamp{T: 123, I: 2}},
		{Key: "pattern", Value: bson.Regex{Pattern: "^a", Options: "i"}},
	}
	id := func(n int64) bson.D { return bson.D{{Key: "_id", Value: n}} }
	inc := func(n int64) bson.D { return bson.D{{Key: "$inc", Value: bson.D{{Key: "qty", Value: n}}}} }
	commands := []bson.D{
		{{Key: "insert", Value: "user"}, {Key: "documents", Value: bson.A{row}}, {Key: "ordered", Value: true}, {Key: "maxTimeMS", Value: int64(15000)}, {Key: "lsid", Value: bson.D{{Key: "id", Value: bson.NewObjectID()}}}},
		{{Key: "insert", Value: "user"}, {Key: "documents", Value: bson.A{bson.D{{Key: "_id", Value: int64(2)}}, bson.D{{Key: "_id", Value: int64(3)}}}}, {Key: "ordered", Value: false}},
		{{Key: "find", Value: "user"}, {Key: "filter", Value: id(1)}, {Key: "projection", Value: bson.D{{Key: "_id", Value: 1}}}, {Key: "sort", Value: bson.D{{Key: "_id", Value: int64(-1)}}}, {Key: "limit", Value: int64(1)}, {Key: "singleBatch", Value: true}, {Key: "maxTimeMS", Value: int64(15000)}},
		{{Key: "update", Value: "user"}, {Key: "updates", Value: bson.A{bson.D{{Key: "q", Value: id(1)}, {Key: "u", Value: inc(3)}, {Key: "multi", Value: false}}}}},
		{{Key: "update", Value: "user"}, {Key: "updates", Value: bson.A{bson.D{{Key: "q", Value: bson.D{}}, {Key: "u", Value: bson.D{{Key: "$set", Value: bson.D{{Key: "phase", Value: "done"}}}}}, {Key: "multi", Value: true}}}}},
		{{Key: "update", Value: "user"}, {Key: "updates", Value: bson.A{bson.D{{Key: "q", Value: id(3)}, {Key: "u", Value: bson.D{{Key: "_id", Value: int64(3)}, {Key: "qty", Value: int64(5)}}}}}}},
		{{Key: "update", Value: "user"}, {Key: "updates", Value: bson.A{bson.D{{Key: "q", Value: id(1)}, {Key: "u", Value: inc(2)}}, bson.D{{Key: "q", Value: id(3)}, {Key: "u", Value: inc(2)}}}}},
		{{Key: "findAndModify", Value: "user"}, {Key: "query", Value: id(1)}, {Key: "sort", Value: bson.D{{Key: "qty", Value: int64(-1)}}}, {Key: "update", Value: inc(1)}, {Key: "new", Value: true}},
		{{Key: "delete", Value: "user"}, {Key: "deletes", Value: bson.A{bson.D{{Key: "q", Value: id(2)}, {Key: "limit", Value: int64(1)}}}}},
		{{Key: "delete", Value: "user"}, {Key: "deletes", Value: bson.A{bson.D{{Key: "q", Value: id(99)}, {Key: "limit", Value: int64(0)}}}}},
		{{Key: "aggregate", Value: "user"}, {Key: "pipeline", Value: bson.A{bson.D{{Key: "$match", Value: id(1)}}, bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: int64(1)}}}}}}, {Key: "cursor", Value: bson.D{{Key: "batchSize", Value: int64(2)}}}},
		{{Key: "count", Value: "user"}, {Key: "query", Value: bson.D{}}},
		{{Key: "count", Value: "user"}},
		{{Key: "distinct", Value: "user"}, {Key: "key", Value: "qty"}, {Key: "query", Value: bson.D{}}},
	}
	statements := []string{}
	for _, command := range commands {
		statement := shellCommand(logRaw(t, command), LogConfig{Replayable: true, MaxLength: 16384})
		if !strings.HasSuffix(statement, ";") || strings.Contains(statement, "[REDACTED]") {
			t.Fatalf("不是完整的可执行语句: %s", statement)
		}
		statements = append(statements, statement)
	}
	// 直接交给 mongosh 的解释器，使用与用户粘贴命令相同的自动 await 机制。
	// 游标追加 toArray 主动触发查询；此处执行的只有上面的固定测试数据。
	var script strings.Builder
	script.WriteString("db = db.getSiblingDB(" + shellQuote(db.Name()) + ");\n")
	for i, statement := range statements {
		value := strings.TrimSuffix(statement, ";")
		if strings.Contains(statement, ".find(") || strings.Contains(statement, ".aggregate(") {
			value += ".toArray()"
		}
		name := "result" + strconv.Itoa(i)
		script.WriteString("const " + name + " = " + value + ";\nif (" + name + " && " + name + ".ok === 0) throw new Error('command failed');\n")
	}
	var cmd *exec.Cmd
	if mongosh != "" {
		cmd = exec.CommandContext(ctx, mongosh, os.Getenv("MONGOPLUS_TEST_URI"), "--quiet", "--eval", script.String())
	} else {
		cmd = exec.CommandContext(ctx, "docker", "exec", container, "mongosh", os.Getenv("MONGOPLUS_TEST_URI"), "--quiet", "--eval", script.String())
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("mongosh 执行日志失败: %v\n%s\n%s", err, out, script.String())
	}
	var actual bson.M
	if err := db.Collection("user").FindOne(ctx, id(1)).Decode(&actual); err != nil {
		t.Fatal(err)
	}
	for _, field := range row {
		want := field.Value
		if field.Key == "qty" {
			want = int64(34)
		}
		if !reflect.DeepEqual(actual[field.Key], want) {
			t.Errorf("重放改变了字段 %s 的值或类型: got=%#v (%T) want=%#v (%T)", field.Key, actual[field.Key], actual[field.Key], want, want)
		}
	}
	if count, err := db.Collection("user").CountDocuments(ctx, bson.D{}); err != nil || count != 2 {
		t.Fatalf("重放增删结果不符: count=%d err=%v", count, err)
	}
}
