package mongoplus

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type reviewAlternatingProvider struct {
	dbs   []*Database
	calls int
}

func (p *reviewAlternatingProvider) Get(context.Context) (*Database, error) {
	db := p.dbs[p.calls%len(p.dbs)]
	p.calls++
	return db, nil
}

func TestReviewDALPinsProviderForWholeOperation(t *testing.T) {
	native := testutil.Database(t)
	loose, strict := NewDatabase(native), NewDatabase(native)
	if err := RegisterTenant(strict, TenantConfig[int64]{TenantField: "tenant_id"}); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := native.Collection("rows").InsertMany(ctx, []any{dalTestRow{ID: 1, Tenant: 1}, dalTestRow{ID: 2, Tenant: 2}}); err != nil {
		t.Fatal(err)
	}
	dal := testDALFiles(map[string]string{
		"read.json":       `{"collection":"rows","op":"find"}`,
		"count_read.json": `{"collection":"rows","op":"count"}`,
		"update.json":     `{"collection":"rows","op":"updateMany","filter":{"_id":{"$gt":0}},"update":{"$set":{"value":7}}}`,
		"bulk.json":       `{"collection":"rows","op":"bulkWrite","operations":[{"op":"updateMany","filter":{"_id":{"$gt":0}},"update":{"$set":{"value":8}}}]}`,
		"watch.json":      `{"collection":"rows","op":"watch","pipeline":[]}`,
	})
	p := &reviewAlternatingProvider{dbs: []*Database{strict, loose}}
	dal.provider = p
	ctx = WithDALDB(ctx, dal)
	// 首次选择严格包装器时，缺失租户必须拒绝；后续阶段不能再选择宽松包装器。
	if _, err := DALQuery[dalTestRow](ctx, "read.json"); err == nil || p.calls != 1 {
		t.Fatalf("缺失租户未拒绝或 Provider 重复解析: calls=%d err=%v", p.calls, err)
	}
	scoped := WithTenantID(ctx, int64(1))
	checks := map[string]func(*testing.T) error{
		"find": func(t *testing.T) error {
			rows, err := DALQuery[dalTestRow](scoped, "read.json")
			if err == nil && (len(rows) != 1 || rows[0].Tenant != 1) {
				t.Fatalf("跨租户读取: %+v", rows)
			}
			return err
		},
		"page": func(t *testing.T) error {
			page, err := DALQueryPage[dalTestRow](scoped, "read.json", nil, nil)
			if err == nil && (len(page.List) != 1 || page.Total != 1) {
				t.Fatalf("分页数据源不一致: %+v", page)
			}
			return err
		},
		"count": func(t *testing.T) error {
			n, err := DALCount(scoped, "count_read.json")
			if err == nil && n != 1 {
				t.Fatalf("计数跨租户: %d", n)
			}
			return err
		},
		"walk": func(t *testing.T) error {
			n := 0
			err := DALWalk(scoped, "read.json", func(row dalTestRow) error {
				n++
				if row.Tenant != 1 {
					t.Fatal("流式查询跨租户")
				}
				return nil
			})
			if err == nil && n != 1 {
				t.Fatalf("walk rows=%d", n)
			}
			return err
		},
		"update": func(t *testing.T) error {
			res, err := DALExecAffected(scoped, "update.json")
			if err == nil && res.RowsAffected != 1 {
				t.Fatalf("跨租户更新: %+v", res)
			}
			return err
		},
		"bulk": func(t *testing.T) error {
			res, err := DALBulkWrite(scoped, "bulk.json")
			if err == nil && res.MatchedCount != 1 {
				t.Fatalf("跨租户批量写: %+v", res)
			}
			return err
		},
		"watch": func(t *testing.T) error {
			stream, err := DALWatch(scoped, "watch.json")
			if err == nil {
				return stream.Close(scoped)
			}
			return err
		},
		"native": func(t *testing.T) error {
			return DALNative(scoped, nil, func(context.Context, *mongo.Database) error { return nil })
		},
	}
	for name, check := range checks {
		t.Run(name, func(t *testing.T) {
			p.calls = 0
			if err := check(t); err != nil {
				t.Fatal(err)
			}
			if p.calls != 1 {
				t.Fatalf("单次操作重复解析 Provider: %d", p.calls)
			}
		})
	}
	var other dalTestRow
	if err := native.Collection("rows").FindOne(ctx, bson.M{"_id": int64(2)}).Decode(&other); err != nil || other.Value != 0 {
		t.Fatalf("修改了其他租户: %+v %v", other, err)
	}
	// 下一次调用重新路由；修复不能把一次选择永久缓存到调用方 Context。
	p.calls = 0
	p.dbs = []*Database{loose, strict}
	rows, err := DALQuery[dalTestRow](ctx, "read.json")
	if err != nil || len(rows) != 2 || p.calls != 1 {
		t.Fatalf("没有使用首次选中的包装器: rows=%d calls=%d err=%v", len(rows), p.calls, err)
	}
	if _, err = DALQuery[dalTestRow](ctx, "read.json"); err == nil || p.calls != 2 {
		t.Fatalf("下次调用未重新路由: calls=%d err=%v", p.calls, err)
	}
}

func TestReviewSharedCacheKeepsCustomDecodedValues(t *testing.T) {
	db := NewDatabase(testutil.Database(t))
	store := NewMemoryCache()
	t.Cleanup(func() { _ = store.Close() })
	db.UseCache(store)
	r := MustRepository[reviewScaledRow, int64](db, CollectionConfig{Name: "custom_values", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	ctx := context.Background()
	if _, err := r.Native().InsertOne(ctx, bson.M{"_id": int64(1), "value": int64(2)}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		got, err := r.FindById(ctx, 1, Query().WithCache(time.Minute).Build())
		if err != nil || got.Value != 20 {
			t.Fatalf("共享缓存改变已解码的值: %+v %v", got, err)
		}
	}
	direct, err := r.FindById(ctx, 1)
	if err != nil || direct.Value != 20 {
		t.Fatalf("直读: %+v %v", direct, err)
	}
}

func TestReviewSensitiveCollisionRejectedBeforeInsert(t *testing.T) {
	db := NewDatabase(testutil.Database(t))
	if _, err := RegisterSensitive(db, SensitiveConfig{Key: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	r := MustRepository[reviewSensitiveCollision, int64](db, CollectionConfig{Name: "collision", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	input := &reviewSensitiveCollision{ID: 1, Phone: "13800008000", Mobile: "13900009000"}
	before := *input
	if err := r.Create(context.Background(), input); err == nil {
		t.Fatal("冲突配置不能写入")
	}
	if !reflect.DeepEqual(before, *input) {
		t.Fatal("失败写入修改了输入")
	}
	if n, err := r.Native().CountDocuments(context.Background(), bson.D{}); err != nil || n != 0 {
		t.Fatalf("失败写入留下文档: %d %v", n, err)
	}
}

func TestReviewSensitiveExplicitPathLifecycle(t *testing.T) {
	native := testutil.Database(t)
	ctx := context.Background()
	for _, valid := range []bool{false, true} {
		name := "invalid_paths"
		cfg := PhoneField("Phone")
		cfg.EncryptAtRest = true
		cfg.CipherPath, cfg.IndexPath = "other_cipher", "other_index"
		if valid {
			name = "matching_paths"
			cfg.CipherPath, cfg.IndexPath = "encrypted_mobile", "mobile_lookup"
		}
		t.Run(name, func(t *testing.T) {
			db := NewDatabase(native)
			if _, err := RegisterSensitive(db, SensitiveConfig{Key: []byte("0123456789abcdef"), Fields: []SensitiveFieldConfig{cfg}}); err != nil {
				t.Fatal(err)
			}
			r := MustRepository[customTagSensitiveEntity, int64](db, CollectionConfig{Name: name, ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
			input := &customTagSensitiveEntity{ID: 1, Phone: "13800008000"}
			before := *input
			err := r.Create(ctx, input)
			if !valid {
				if err == nil || !reflect.DeepEqual(before, *input) {
					t.Fatalf("不匹配配置必须拒绝且保持输入: %+v %v", input, err)
				}
				if n, e := r.Native().CountDocuments(ctx, bson.D{}); e != nil || n != 0 {
					t.Fatalf("不匹配配置留下文档: %d %v", n, e)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			rows, err := r.FindList(ctx, Query().Where(r.SensitiveEq("Phone", "13800008000")).Build())
			if err != nil || len(rows) != 1 {
				t.Fatalf("新增和等值查询路径不一致: %+v %v", rows, err)
			}
			if _, err = r.UpdateById(ctx, 1, field.New[string]("phone").Set("13900009000")); err != nil {
				t.Fatal(err)
			}
			got, err := r.FindById(WithSensitivePlaintext(ctx), 1)
			if err != nil || got.Phone != "13900009000" {
				t.Fatalf("更新和读取路径不一致: %+v %v", got, err)
			}
			rows, err = r.FindList(ctx, Query().Where(r.SensitiveEq("Phone", "13900009000")).Build())
			if err != nil || len(rows) != 1 {
				t.Fatalf("更新未刷新索引: %+v %v", rows, err)
			}
		})
	}
}

func TestReviewIncrementingUpsertCannotConsumeReservedIDs(t *testing.T) {
	db := NewDatabase(testutil.Database(t))
	r := MustRepository[scopeReviewRow, int64](db, CollectionConfig{Name: "sequence", ID: IDConfig{Type: IDTypeInt64, BlockSize: 32}})
	ctx := context.Background()
	first := &scopeReviewRow{Name: "first"}
	if err := r.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	query := Query().Where(field.New[int64]("_id").Eq(2)).Build()
	if _, err := r.FindOneAndUpdate(ctx, query, ModifyOptions{Upsert: true, ReturnDocument: options.After}, field.New[string]("name").Set("upserted")); err == nil {
		t.Fatal("自增原子 Upsert 必须拒绝")
	}
	replacement := &scopeReviewRow{ID: 2, Name: "replacement"}
	before := *replacement
	if _, err := r.FindOneAndReplace(ctx, query, replacement, ModifyOptions{Upsert: true}); err == nil {
		t.Fatal("自增替换 Upsert 必须拒绝")
	}
	if !reflect.DeepEqual(before, *replacement) {
		t.Fatal("失败替换修改了输入")
	}
	second := &scopeReviewRow{Name: "second"}
	if err := r.Create(ctx, second); err != nil || first.ID != 1 || second.ID != 2 {
		t.Fatalf("预分配号段被污染: first=%d second=%d err=%v", first.ID, second.ID, err)
	}
	if got, err := r.FindOneAndUpdate(ctx, Query().Where(field.New[int64]("_id").Eq(1)).Build(), ModifyOptions{ReturnDocument: options.After}, field.New[string]("name").Set("updated")); err != nil || got.Name != "updated" {
		t.Fatalf("普通原子更新不应受限: %+v %v", got, err)
	}
}

func TestReviewWrapperWritesHonorOptions(t *testing.T) {
	db := NewDatabase(testutil.Database(t))
	ctx := context.Background()
	for _, soft := range []bool{false, true} {
		name := "physical"
		if soft {
			name = "soft"
		}
		t.Run(name, func(t *testing.T) {
			r := MustRepository[scopeReviewRow, int64](db, CollectionConfig{Name: name, ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}, SoftDelete: soft})
			if err := r.Create(ctx, &scopeReviewRow{ID: 1, Name: "Alice"}); err != nil {
				t.Fatal(err)
			}
			wrapper := func(w *Wrapper) {
				w.Where(field.New[string]("name").Eq("alice"), Condition{Document: bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$name", "$$target"}}}}}}).Collation(options.Collation{Locale: "en", Strength: 2}).Hint("_id_").Let(bson.D{{Key: "target", Value: "Alice"}}).Comment("write-regression")
			}
			res, err := r.UpdateByWrapper(ctx, wrapper, field.New[int64]("target").Set(1))
			if err != nil || res.MatchedCount != 1 {
				t.Fatalf("Collation/Let/Hint 更新失效: %+v %v", res, err)
			}
			// 非法 hint 必须由服务端拒绝，而不是悄悄使用其他执行计划。
			if _, err = r.UpdateByWrapper(ctx, func(w *Wrapper) { w.Where(field.New[int64]("_id").Eq(1)).Hint("missing_index") }, field.New[int64]("target").Set(2)); err == nil {
				t.Fatal("Update 忽略 Hint")
			}
			n, err := r.DeleteByWrapper(ctx, wrapper)
			if err != nil || n != 1 {
				t.Fatalf("删除选项失效: n=%d %v", n, err)
			}
			if soft {
				n, err = r.HardDeleteByWrapper(ctx, wrapper)
				if err != nil || n != 1 {
					t.Fatalf("软删除后的物理删除选项失效: n=%d %v", n, err)
				}
			}
		})
	}
}
