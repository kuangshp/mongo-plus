package schema

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func TestExtendedIndexAndCollectionOptions(t *testing.T) {
	db := testutil.Database(t)
	ctx := context.Background()
	cfg := Config{Package: "example.com/review"}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	var e Entity
	input := `{"collection":"extended","options":{"collation":{"locale":"en","strength":2},"changeStreamPreAndPostImages":true,"validationLevel":"moderate","validationAction":"error"},"fields":[{"name":"title","type":"string","rules":{"minLength":2}},{"name":"status","type":"int64"},{"name":"location","type":"json"}],"indexes":[{"name":"title_partial","fields":[{"name":"title","order":1}],"partialFilterExpression":{"status":1},"hidden":true},{"name":"status_hash","fields":[{"name":"status","order":"hashed"}],"collation":{"locale":"simple"}},{"name":"location_geo","fields":[{"name":"location","order":"2dsphere"}]},{"name":"title_text","fields":[{"name":"title","order":"text"}],"collation":{"locale":"simple"},"weights":{"title":2}},{"name":"wildcard","fields":[{"name":"$**","order":1}]}]}`
	if err := json.Unmarshal([]byte(input), &e); err != nil {
		t.Fatal(err)
	}
	if err := e.Normalize(cfg, "extended"); err != nil {
		t.Fatal(err)
	}
	if _, err := Apply(ctx, db, []Entity{e}); err != nil {
		t.Fatal(err)
	}
	changes, err := Plan(ctx, db, []Entity{e})
	if err != nil || len(changes) != 0 {
		t.Fatalf("重复 Plan 应无漂移: %+v %v", changes, err)
	}
	// Hidden 和 Collation 都是索引语义，不能只按键名判断已同步。
	hidden := e
	hidden.Indexes = append([]Index(nil), e.Indexes...)
	hidden.Indexes[0].Hidden = false
	if _, err = Plan(ctx, db, []Entity{hidden}); err == nil {
		t.Fatal("未识别 hidden 冲突")
	}
	collated := e
	collated.Indexes = append([]Index(nil), e.Indexes...)
	collated.Indexes[0].Collation = &options.Collation{Locale: "en", Strength: 3}
	if _, err = Plan(ctx, db, []Entity{collated}); err == nil {
		t.Fatal("未识别 collation 冲突")
	}
	doc := bson.M{"_id": bson.NewObjectID(), "title": "x", "created_at": bson.DateTime(0), "updated_at": bson.DateTime(0)}
	if _, err = db.Collection(e.Collection).InsertOne(ctx, doc); err == nil {
		t.Fatal("minLength 校验未生效")
	}
}

func TestExtendedOptionsRejectInvalidConfig(t *testing.T) {
	cfg := Config{Package: "example.com/review"}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{
		`{"options":{"capped":true},"fields":[]}`,
		`{"options":{"validationLevel":"typo"},"fields":[]}`,
		`{"fields":[{"name":"name","type":"string","rules":{"bsonType":"int"}}]}`,
		`{"fields":[{"name":"name","type":"string"}],"indexes":[{"name":"x","fields":[{"name":"name","order":"typo"}]}]}`,
	} {
		var e Entity
		if err := json.Unmarshal([]byte(raw), &e); err != nil {
			t.Fatal(err)
		}
		if err := e.Normalize(cfg, "invalid"); err == nil {
			t.Fatalf("应拒绝 %s", raw)
		}
	}
}

func TestExtendedSchemaComparisonRetainsIntegerPrecision(t *testing.T) {
	a := bson.D{{Key: "minimum", Value: int64(9007199254740992)}}
	b := bson.D{{Key: "minimum", Value: int64(9007199254740993)}}
	if equalDocument(a, b) {
		t.Fatal("不能因 JSON float64 精度丢失而漏掉校验规则变更")
	}
	if !equalDocument(bson.D{{Key: "weight", Value: float64(2)}}, bson.D{{Key: "weight", Value: int32(2)}}) {
		t.Fatal("权重的等价整数表达不应误报漂移")
	}
}

func TestReviewCappedPlanRemainsIdempotent(t *testing.T) {
	db := testutil.Database(t)
	ctx := context.Background()
	cfg := Config{Package: "example.com/capped"}
	if err := cfg.Normalize(); err != nil {
		t.Fatal(err)
	}
	for i, size := range []int64{1, 256, 257, 4096, 4097} {
		entity := Entity{Collection: fmt.Sprintf("capped_%d", i), Options: &CollectionOptions{Capped: true, SizeInBytes: size, MaxDocuments: 100}}
		if err := entity.Normalize(cfg, entity.Collection); err != nil {
			t.Fatal(err)
		}
		if _, err := Apply(ctx, db, []Entity{entity}); err != nil {
			t.Fatal(err)
		}
		changes, err := Plan(ctx, db, []Entity{entity})
		if err != nil || len(changes) != 0 {
			t.Fatalf("相同 capped 配置产生伪漂移 size=%d: %+v %v", size, changes, err)
		}
		if _, err = Apply(ctx, db, []Entity{entity}); err != nil {
			t.Fatalf("重复 Apply: %v", err)
		}
		entity.Options.SizeInBytes += 256
		if _, err = Plan(ctx, db, []Entity{entity}); err == nil {
			t.Fatal("真正容量漂移不能被忽略")
		}
	}
}

func TestReviewCappedSizeBoundaries(t *testing.T) {
	for input, want := range map[int64]int64{1: 256, 256: 256, 257: 512, 4097: 4352, 1 << 50: 1 << 50} {
		if got := cappedStorageSize(input); got != want {
			t.Fatalf("size=%d got=%d want=%d", input, got, want)
		}
	}
	e := Entity{Options: &CollectionOptions{Capped: true, SizeInBytes: 1 << 62}}
	if err := e.normalizeCollectionOptions(); err == nil {
		t.Fatal("超大容量应拒绝，避免取整溢出")
	}
}

func TestPlanRejectsUnnormalizedEntities(t *testing.T) {
	if _, err := Plan(context.Background(), nil, nil); err == nil {
		t.Fatal("nil 数据库必须报错")
	}
	if _, err := Plan(context.Background(), &mongo.Database{}, []Entity{{Collection: "raw"}}); err == nil {
		t.Fatal("未 Normalize 的实体必须报错，不能 panic")
	}
}
