package mongoplus

import (
	"context"
	"testing"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestExplicitCollectionRulesAreAtomic(t *testing.T) {
	db := &Database{}
	cfg := CollectionConfig{Name: "one", SoftDelete: true}
	if err := db.RegisterCollections(cfg); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterCollections(cfg); err != nil {
		t.Fatal(err)
	}
	if err := db.RegisterCollections(CollectionConfig{Name: "two"}, CollectionConfig{Name: "one"}); err == nil {
		t.Fatal("配置冲突必须返回错误")
	}
	if _, ok := db.rules["two"]; ok {
		t.Fatal("失败注册不能部分发布")
	}
}

func TestScopePrefixFailsClosedForExpressions(t *testing.T) {
	doc := bson.D{{Key: "$nor", Value: []bson.D{{{Key: "owner_id", Value: 1}}}}}
	got, err := prefixScopeDocument(doc, "fullDocument.")
	if err != nil || !bsonEqual(got, bson.D{{Key: "$nor", Value: bson.A{bson.D{{Key: "fullDocument.owner_id", Value: 1}}}}}) {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := prefixScopeDocument(bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$owner_id", 1}}}}}, "fullDocument."); err == nil {
		t.Fatal("不能安全转换的权限表达式必须报错")
	}
}

func TestExtendedQueryCacheSeparatesSemantics(t *testing.T) {
	root, _, _ := querySources(t)
	a, ka, err := root.readKey(context.Background(), []QueryOption{Query().Collation(options.Collation{Locale: "en", Strength: 1}).Build()})
	if err != nil {
		t.Fatal(err)
	}
	_, kb, err := root.readKey(context.Background(), []QueryOption{Query().Collation(options.Collation{Locale: "en", Strength: 3}).Build()})
	if err != nil {
		t.Fatal(err)
	}
	if bsonEqual(ka, kb) {
		t.Fatal("Collation 不同不能复用缓存")
	}
	if a.collation.Strength != 1 {
		t.Fatal("查询快照丢失选项")
	}
	if err := Query().BatchSize(-1).Build().validate(); err == nil {
		t.Fatal("负批次必须报错")
	}
}

func TestExtendedDALOptionsAndOperators(t *testing.T) {
	for _, raw := range []bson.D{{{Key: "locale", Value: "en"}, {Key: "strength", Value: 2}, {Key: "numericOrdering", Value: true}}} {
		c, err := dalCollation(raw)
		if err != nil || !c.NumericOrdering || c.Strength != 2 {
			t.Fatalf("%+v %v", c, err)
		}
	}
	if _, err := dalCollation(bson.D{{Key: "locale", Value: "en"}, {Key: "stength", Value: 2}}); err == nil {
		t.Fatal("拼写错误必须报错")
	}
	update, err := buildUpdate([]UpdateOp{field.NewNumeric[int64]("amount").Mul(2), field.New[int64]("min").Min(1), field.NewArray[int64]("items").PopLast(), field.NewArray[int64]("old").PullAll(1, 2)})
	if err != nil || len(update) != 4 {
		t.Fatalf("扩展操作符 %v %v", update, err)
	}
	if _, err := buildUpdate([]UpdateOp{field.New[int64]("created_at").Max(1)}, "created_at"); err == nil {
		t.Fatal("新增操作符仍要保护系统字段")
	}
}
