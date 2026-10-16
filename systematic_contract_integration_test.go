package mongoplus

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type systematicRow struct {
	ID        int64            `bson:"_id"`
	Tenant    any              `bson:"tenant_id"`
	Name      string           `bson:"name"`
	Tags      []string         `bson:"tags,omitempty"`
	Lookup    map[string]int64 `bson:"lookup,omitempty"`
	DeletedAt *time.Time       `bson:"deleted_at"`
}

func TestSystematicSFTenantTypesStayIsolated(t *testing.T) {
	db := NewDatabase(testutil.Database(t))
	ForceReplaceCache(NewMemoryCache())
	t.Cleanup(StopSFCache)
	if err := RegisterTenant(db, TenantConfig[any]{TenantField: "tenant_id"}); err != nil {
		t.Fatal(err)
	}
	r := MustRepository[systematicRow, int64](db, CollectionConfig{Name: "typed", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	id := bson.NewObjectID()
	ctx := context.Background()
	if _, err := r.Native().InsertMany(ctx, []any{systematicRow{ID: 1, Tenant: id, Name: "ObjectID"}, systematicRow{ID: 2, Tenant: id.Hex(), Name: "string"}}); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		tenant any
		want   string
	}{{id, "ObjectID"}, {id.Hex(), "string"}, {id, "ObjectID"}} {
		got, err := SF(func() (string, error) {
			row, e := r.FindOne(WithTenantID[any](ctx, test.tenant))
			if e != nil {
				return "", e
			}
			return row.Name, nil
		}, "systematic.tenant", BuildArgs("tenant", test.tenant))
		if err != nil || got != test.want {
			t.Fatalf("跨 BSON 类型租户串值: %q want=%q %v", got, test.want, err)
		}
	}
}

func TestSystematicSharedCacheMatchesDirectValues(t *testing.T) {
	db := NewDatabase(testutil.Database(t))
	store := NewMemoryCache()
	t.Cleanup(func() { _ = store.Close() })
	db.UseCache(store)
	r := MustRepository[systematicRow, int64](db, CollectionConfig{Name: "values", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	ctx := context.Background()
	if _, err := r.Native().InsertMany(ctx, []any{
		bson.M{"_id": int64(1), "name": "empty", "tags": bson.A{}, "lookup": bson.M{}},
		bson.M{"_id": int64(2), "name": "populated", "tags": bson.A{"a"}, "lookup": bson.M{"a": int64(1)}},
	}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []int64{1, 2} {
		direct, err := r.FindById(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		for i := 0; i < 3; i++ {
			got, err := r.FindById(ctx, id, Query().WithCache(time.Minute).Build())
			if err != nil || !reflect.DeepEqual(got, direct) {
				t.Fatalf("缓存与直读不一致 id=%d: got=%+v direct=%+v err=%v", id, got, direct, err)
			}
			if len(got.Tags) > 0 {
				got.Tags[0] = "caller mutation"
				got.Lookup["a"] = 2
			}
		}
	}
}

func TestSystematicDALDeleteScopeMatrix(t *testing.T) {
	native := testutil.Database(t)
	ctx := context.Background()
	for _, bulk := range []bool{false, true} {
		for _, includeDeleted := range []bool{false, true} {
			for _, physical := range []bool{false, true} {
				name := fmt.Sprintf("bulk_%t_include_%t_physical_%t", bulk, includeDeleted, physical)
				t.Run(name, func(t *testing.T) {
					db := NewDatabase(native)
					if err := RegisterTenant(db, TenantConfig[int64]{TenantField: "tenant_id"}); err != nil {
						t.Fatal(err)
					}
					if err := db.RegisterCollections(CollectionConfig{Name: name, SoftDelete: true, ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}}); err != nil {
						t.Fatal(err)
					}
					now := time.Now().UTC()
					if _, err := native.Collection(name).InsertMany(ctx, []any{systematicRow{ID: 1, Tenant: int64(1)}, systematicRow{ID: 2, Tenant: int64(1), DeletedAt: &now}, systematicRow{ID: 3, Tenant: int64(2)}}); err != nil {
						t.Fatal(err)
					}
					body := fmt.Sprintf(`{"collection":%q,"op":"deleteMany","filter":{"_id":{"$gt":0}}}`, name)
					if bulk {
						body = fmt.Sprintf(`{"collection":%q,"op":"bulkWrite","operations":[{"op":"deleteMany","filter":{"_id":{"$gt":0}}}]}`, name)
					}
					dal := testDALFiles(map[string]string{"delete.json": body})
					dal.provider = fixedDALProvider{db}
					queryCtx := WithDALDB(WithTenantID(ctx, int64(1)), dal)
					if includeDeleted {
						queryCtx = DALUnscoped(queryCtx)
					}
					if physical {
						queryCtx = DALPhysicalDelete(queryCtx)
					}
					if err := DALExec(queryCtx, "delete.json"); err != nil {
						t.Fatal(err)
					}
					remaining, err := native.Collection(name).CountDocuments(ctx, bson.D{})
					if err != nil {
						t.Fatal(err)
					}
					want := int64(3)
					if physical {
						want = 2
						if includeDeleted {
							want = 1
						}
					}
					if remaining != want {
						t.Fatalf("范围标记改变了删除语义: remaining=%d want=%d", remaining, want)
					}
					var other systematicRow
					if err := native.Collection(name).FindOne(ctx, bson.M{"_id": int64(3)}).Decode(&other); err != nil || other.DeletedAt != nil {
						t.Fatalf("修改其他租户: %+v %v", other, err)
					}
					if !physical {
						var deleted systematicRow
						if err := native.Collection(name).FindOne(ctx, bson.M{"_id": int64(1)}).Decode(&deleted); err != nil || deleted.DeletedAt == nil {
							t.Fatalf("没有逻辑删除: %+v %v", deleted, err)
						}
					}
				})
			}
		}
	}
}

type systematicStringRow struct {
	ID   string `bson:"_id"`
	Name string `bson:"name"`
}

func TestSystematicStringCursorUsesBinaryOrder(t *testing.T) {
	native := testutil.Database(t)
	db := NewDatabase(native)
	ctx := context.Background()
	for _, defaults := range []bool{false, true} {
		name := fmt.Sprintf("default_collation_%t", defaults)
		if defaults {
			if err := native.CreateCollection(ctx, name, options.CreateCollection().SetCollation(&options.Collation{Locale: "en", Strength: 2})); err != nil {
				t.Fatal(err)
			}
		}
		r := MustRepository[systematicStringRow, string](db, CollectionConfig{Name: name, ID: IDConfig{Type: IDTypeString}})
		ids := []string{"A", "a", "B"}
		if defaults {
			ids = []string{"ä", "z"}
		}
		for _, id := range ids {
			if err := r.Create(ctx, &systematicStringRow{ID: id, Name: id}); err != nil {
				t.Fatal(err)
			}
		}
		if _, err := r.FindCursorPage(ctx, nil, 1, Query().Collation(options.Collation{Locale: "en", Strength: 2}).Build()); err == nil {
			t.Fatal("不安全字符串 Collation 必须拒绝")
		}
		unsafe := Query().Collation(options.Collation{Locale: "en", Strength: 2}).Build()
		if _, _, err := r.FindPage(ctx, 1, 1, unsafe); err == nil {
			t.Fatal("页码分页也必须拒绝不安全 Collation")
		}
		if _, _, err := ScanByPage[systematicStringRow](ctx, From(r, unsafe), 1, 1); err == nil {
			t.Fatal("关联分页也必须拒绝不安全 Collation")
		}
		var after *string
		seen := []string{}
		for i := 0; i < 10; i++ {
			page, err := r.FindCursorPage(ctx, after, 1)
			if err != nil {
				t.Fatal(err)
			}
			for _, row := range page.List {
				seen = append(seen, row.ID)
			}
			if !page.HasMore {
				break
			}
			after = page.Next
		}
		cursor, err := r.Native().Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}).SetCollation(&options.Collation{Locale: "simple"}))
		if err != nil {
			t.Fatal(err)
		}
		var rows []systematicStringRow
		if err = cursor.All(ctx, &rows); err != nil {
			t.Fatal(err)
		}
		want := []string{}
		for _, row := range rows {
			want = append(want, row.ID)
		}
		if !reflect.DeepEqual(seen, want) {
			t.Fatalf("游标漏数据/不稳定: got=%v want=%v", seen, want)
		}
		page, total, err := r.FindPage(ctx, 1, 10)
		if err != nil || total != int64(len(want)) {
			t.Fatalf("页码分页: %d %v", total, err)
		}
		for i, row := range page {
			if row.ID != want[i] {
				t.Fatalf("页码分页没有覆盖集合默认 Collation: %+v", page)
			}
		}
		aggregate, total, err := ScanByPage[systematicStringRow](ctx, From(r), 1, 10)
		if err != nil || total != int64(len(want)) {
			t.Fatalf("关联分页: %d %v", total, err)
		}
		for i, row := range aggregate {
			if row.ID != want[i] {
				t.Fatalf("关联分页没有覆盖集合默认 Collation: %+v", aggregate)
			}
		}
	}
}

func TestSystematicAggregateMatchesDriverOptions(t *testing.T) {
	db := NewDatabase(testutil.Database(t))
	ctx := context.Background()
	r := MustRepository[scopeReviewRow, int64](db, CollectionConfig{Name: "aggregate", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	for i := int64(1); i <= 4; i++ {
		if err := r.Create(ctx, &scopeReviewRow{ID: i, Name: fmt.Sprint(i)}); err != nil {
			t.Fatal(err)
		}
	}
	o := Query().Order(field.New[int64]("_id").Desc()).Offset(1).Limit(2).Select(field.New[string]("name")).Build()
	got, err := Aggregate[scopeReviewRow](ctx, r, mongo.Pipeline{}, o)
	if err != nil {
		t.Fatal(err)
	}
	native := mongo.Pipeline{bson.D{{Key: "$sort", Value: bson.D{{Key: "_id", Value: -1}}}}, bson.D{{Key: "$skip", Value: int64(1)}}, bson.D{{Key: "$limit", Value: int64(2)}}, bson.D{{Key: "$project", Value: bson.D{{Key: "name", Value: 1}}}}}
	cursor, err := r.Native().Aggregate(ctx, native)
	if err != nil {
		t.Fatal(err)
	}
	var want []scopeReviewRow
	if err = cursor.All(ctx, &want); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) || len(got) != 2 || got[0].ID != 3 {
		t.Fatalf("聚合选项与官方不一致: got=%+v want=%+v", got, want)
	}
	if _, err = Aggregate[scopeReviewRow](ctx, r, nil, Query().WithCache(time.Minute).Build()); err == nil {
		t.Fatal("聚合不能静默忽略缓存选项")
	}
}

type systematicUnsafeEncrypted struct {
	ID          int64  `bson:"_id"`
	Phone       string `bson:"phone" mongoplus:"cipher:PhoneCipher;index:PhoneIndex;encrypt:true"`
	PhoneCipher string `bson:"phone_cipher"`
	PhoneIndex  string `bson:"phone_index"`
}

func TestSystematicEncryptionRejectsPlaintextPersistence(t *testing.T) {
	db := NewDatabase(testutil.Database(t))
	ctx := context.Background()
	if _, err := RegisterSensitive(db, SensitiveConfig{Key: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	r := MustRepository[systematicUnsafeEncrypted, int64](db, CollectionConfig{Name: "encrypted", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	row := &systematicUnsafeEncrypted{ID: 1, Phone: "13800008000"}
	before := *row
	if err := r.Create(ctx, row); err == nil {
		t.Fatal("必须拒绝加密字段同时持久化明文")
	}
	if !reflect.DeepEqual(*row, before) {
		t.Fatal("失败写入污染实体")
	}
	if n, err := r.Native().CountDocuments(ctx, bson.D{}); err != nil || n != 0 {
		t.Fatalf("失败写入留下文档: %d %v", n, err)
	}
}

func TestSystematicAtomicDeleteForwardsLet(t *testing.T) {
	db := NewDatabase(testutil.Database(t))
	ctx := context.Background()
	for _, soft := range []bool{false, true} {
		name := fmt.Sprintf("delete_let_%t", soft)
		r := MustRepository[scopeReviewRow, int64](db, CollectionConfig{Name: name, SoftDelete: soft, ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
		if err := r.Create(ctx, &scopeReviewRow{ID: 1, Name: "wanted"}); err != nil {
			t.Fatal(err)
		}
		query := Query().RawWhere(bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$name", "$$wanted"}}}}}).Let(bson.D{{Key: "wanted", Value: "wanted"}}).Build()
		got, err := r.FindOneAndDelete(ctx, query)
		if err != nil || got.ID != 1 {
			t.Fatalf("Let 查删未透传: %+v %v", got, err)
		}
	}
}

func TestSystematicDistinctHintCapability(t *testing.T) {
	db := NewDatabase(testutil.Database(t))
	ctx := context.Background()
	r := MustRepository[scopeReviewRow, int64](db, CollectionConfig{Name: "distinct_hint", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	if err := r.Create(ctx, &scopeReviewRow{ID: 1, Name: "value"}); err != nil {
		t.Fatal(err)
	}
	var version struct {
		VersionArray []int `bson:"versionArray"`
	}
	if err := db.native.Client().Database("admin").RunCommand(ctx, bson.D{{Key: "buildInfo", Value: 1}}).Decode(&version); err != nil {
		t.Fatal(err)
	}
	values, err := r.Distinct(ctx, "name", Query().Hint("_id_").Build())
	supported := len(version.VersionArray) >= 2 && (version.VersionArray[0] > 7 || version.VersionArray[0] == 7 && version.VersionArray[1] >= 1)
	if supported {
		if err != nil || !reflect.DeepEqual(values, []any{"value"}) {
			t.Fatalf("Distinct Hint 未透传: %v %v", values, err)
		}
	} else if err == nil {
		t.Fatal("不支持的服务端版本不能静默忽略 Hint")
	}
}
