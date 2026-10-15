package mongoplus

import (
	"errors"
	"strings"
	"testing"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestQueryGroupingAndSnapshot(t *testing.T) {
	w := Query().Where(field.NewNumeric[int64]("status").Eq(0)).WhereIf(false, field.Condition{Err: errors.New("skip")})
	before := w.Build()
	w.WhereOrGroup(field.NewString("name").Eq("a"), field.NewString("name").Eq("b"))
	if len(before.conditions) != 1 || len(w.Build().conditions) != 2 {
		t.Fatal("Build 快照被修改")
	}
	c := field.And(w.Build().conditions...)
	if c.Err != nil || c.Document[0].Key != "$and" {
		t.Fatalf("条件丢失 %+v", c)
	}
	regex := field.NewString("name").Contains(".*").Document[0].Value.(bson.D)[0].Value.(bson.Regex)
	if regex.Pattern != `\.\*` {
		t.Fatalf("用户内容被解释成正则 %s", regex.Pattern)
	}
}

func TestUpdatePreservesZeroAndProtectsPaths(t *testing.T) {
	u, err := buildUpdate(Update(field.NewNumeric[int64]("status").Set(0), field.NewString("name").Set("")).Build(), "_id")
	if err != nil || len(u) != 1 || len(u[0].Value.(bson.D)) != 2 {
		t.Fatalf("零值更新丢失 %+v %v", u, err)
	}
	for _, ops := range [][]UpdateOp{
		{{Operator: "$set", Path: "_id", Value: 1}}, {{Operator: "$set", Path: "profile", Value: 1}, {Operator: "$set", Path: "profile.name", Value: 2}}, {{Operator: "$set", Path: "name", Value: 1}, {Operator: "$unset", Path: "name", Value: ""}}, {{Operator: "$set", Path: "$bad", Value: 1}},
	} {
		if _, err := buildUpdate(ops, "_id"); err == nil {
			t.Fatalf("允许无效更新 %+v", ops)
		}
	}
}

func TestPaginationAndInvalidOptions(t *testing.T) {
	for _, tt := range []struct{ page, size, offset, limit int64 }{{0, 0, 0, 10}, {2, 20, 20, 20}, {1, 1000, 0, 500}, {2, 1, 1, 1}} {
		a, b := DbPage(tt.page, tt.size)
		if a != tt.offset || b != tt.limit {
			t.Fatal(tt, a, b)
		}
	}
	for _, o := range []QueryOption{Query().Limit(-1).Build(), Query().Offset(-1).Build(), Query().Order(bson.E{Key: "x", Value: 2}).Build(), Query().Select(field.New[string]("x")).Omit(field.New[string]("y")).Build()} {
		if err := o.validate(); err == nil {
			t.Fatalf("允许无效选项 %+v", o)
		}
	}
	if err := Query().WhereOrGroup().Build().validate(); err == nil || !strings.Contains(err.Error(), "不能为空") {
		t.Fatal(err)
	}
}
