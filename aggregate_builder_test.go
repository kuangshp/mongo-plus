package mongoplus

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestAggregateBuilderValidation(t *testing.T) {
	root, foreign, _ := querySources(t)
	id, owner := field.New[int64]("_id"), field.New[int64]("owner_id")
	name := field.NewString("name")
	for title, q := range map[string]*Aggregation{
		"nil group field":         From(root).GroupBy(nil),
		"unknown group field":     From(root).GroupBy(field.New[int64]("missing")),
		"unknown sum field":       From(root).Sum(field.New[int64]("missing"), "total"),
		"nil sum field":           From(root).Sum(nil, "total"),
		"string sum":              From(root).Sum(name, "total"),
		"string avg":              From(root).Avg(name, "mean"),
		"string deviation":        From(root).StdDevPop(name, "sd"),
		"empty alias":             From(root).Count(""),
		"operator alias":          From(root).Count("$count"),
		"dotted alias":            From(root).Count("stats.count"),
		"reserved id":             From(root).Count("_id"),
		"reserved internal":       From(root).Count("__mp_unwind_1"),
		"duplicate metric":        From(root).Count("n").Sum(owner, "n"),
		"duplicate group":         From(root).GroupBy(owner, owner),
		"duplicate output":        From(root).GroupBy(owner).Count("owner_id"),
		"invalid key alias":       From(root).GroupBy(owner.As("bad.key")),
		"having without group":    From(root).Having(id.Gt(0)),
		"metric after having":     From(root).Count("n").Having(id.Gt(0)).Sum(owner, "total"),
		"join after group":        From(root).GroupBy(owner).LeftJoin(foreign, owner, id, "owner"),
		"sort required for first": From(root).GroupBy(owner).First(name, "first"),
		"order is not input sort": From(root).Order(id.Asc()).Last(name, "last"),
		"unknown second group":    From(root).GroupBy(owner).Count("n").GroupBy(name),
		"invalid sort":            From(root).Sort(bson.E{Key: "name", Value: 2}),
		"duplicate sort":          From(root).Sort(name.Asc(), name.Desc()),
		"empty sort":              From(root).Sort(),
		"unknown sort":            From(root).Sort(bson.E{Key: "missing", Value: 1}),
		"non array unwind":        From(root).Unwind(name),
		"nil unwind":              From(root).Unwind(nil),
		"too many unwind flags":   From(root).Unwind(name, true, false),
		"invalid select alias":    From(root).Select(name.As("bad.key")),
		"omit with alias":         From(root, Query().Omit(name).Build()).Select(owner.As("owner")),
		"project with alias":      From(root).Select(name.As("title")).Project(bson.D{{Key: "name", Value: 1}}),
	} {
		t.Run(title, func(t *testing.T) {
			if _, err := q.Build(context.Background()); err == nil {
				t.Fatal("无效聚合未报错")
			}
		})
	}
}

func TestQueryOptionRejectsAggregationAliases(t *testing.T) {
	if err := Query().Select(field.NewString("name").As("title")).Build().validate(); err == nil {
		t.Fatal("普通查询选项静默忽略 As 别名")
	}
}

func TestAggregateBuilderStagesScopesAndCloning(t *testing.T) {
	root, _, _ := querySources(t)
	root.config.TenantField = "company_id"
	ctx := WithTenant(context.Background(), int64(7))
	owner, id := field.New[int64]("owner_id"), field.New[int64]("_id")
	q := root.WithContext(ctx).Where(id.Gt(0)).Sort(id.Asc()).GroupBy(owner.As("owner")).Count("count").Sum(id, "total").First(id, "first").Having(field.NewNumeric[int64]("count").Gte(2))
	pipeline, err := q.Build(ctx)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, stage := range pipeline {
		keys = append(keys, stage[0].Key)
	}
	if !reflect.DeepEqual(keys, []string{"$match", "$match", "$sort", "$group", "$set", "$match"}) {
		t.Fatalf("阶段顺序错误: %v", keys)
	}
	json, err := bson.MarshalExtJSON(bson.D{{Key: "pipeline", Value: pipeline}}, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, part := range []string{"company_id", "deleted_at", "owner"} {
		if !strings.Contains(string(json), part) {
			t.Fatalf("缺少字段 %s: %s", part, json)
		}
	}
	if _, err := q.Build(context.Background()); !errors.Is(err, ErrTenantRequired) {
		t.Fatalf("分组绕过租户: %v", err)
	}
	base := root.WithContext(ctx).GroupBy(owner).Count("n")
	copy := base.WithContext(ctx).Sum(id, "total")
	if len(base.steps[0].group.metrics) != 1 || len(copy.steps[0].group.metrics) != 2 {
		t.Fatal("WithContext 污染原查询统计字段")
	}
	_, sort, err := q.buildBase(ctx, true)
	if err != nil || len(sort) != 1 || sort[0].Key != "_id" {
		t.Fatalf("分组分页应使用新分组主键: %+v %v", sort, err)
	}
}

func TestAggregateArrayTypesRequireUnwind(t *testing.T) {
	type item struct {
		Price int64 `bson:"price"`
	}
	type document struct {
		ID     int64           `bson:"_id"`
		Items  []item          `bson:"items"`
		Amount bson.Decimal128 `bson:"amount"`
	}
	root, _, _ := querySources(t)
	r := MustRepository[document, int64](root.db, CollectionConfig{Name: "docs", ID: IDConfig{Type: "int64", Generation: "manual"}})
	price := field.New[int64]("items.price")
	if _, err := From(r).Sum(price, "sum").Build(context.Background()); err == nil {
		t.Fatal("数组点路径被当作单个数值")
	}
	if _, err := From(r).Unwind(field.NewArray[item]("items")).Sum(price, "sum").Build(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := From(r).Sum(field.New[bson.Decimal128]("amount"), "sum").Avg(field.New[bson.Decimal128]("amount"), "avg").Build(context.Background()); err != nil {
		t.Fatal(err)
	}
}
