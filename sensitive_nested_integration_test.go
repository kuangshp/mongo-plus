package mongoplus

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func sensitiveIntegrationDB(t *testing.T) *Database {
	t.Helper()
	db := NewDatabase(testutil.Database(t))
	if _, err := RegisterSensitive(db, SensitiveConfig{Key: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	return db
}

func sensitiveManualCollection(name string) CollectionConfig {
	return CollectionConfig{Name: name, ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}}
}

func TestSensitiveNestedPersistenceCacheAndUpdates(t *testing.T) {
	for _, shared := range []bool{false, true} {
		name := "local"
		if shared {
			name = "shared"
		}
		t.Run(name, func(t *testing.T) {
			db := sensitiveIntegrationDB(t)
			if shared {
				cache := NewMemoryCache()
				t.Cleanup(func() { _ = cache.Close() })
				db.UseCache(cache)
			}
			r := MustRepository[nestedSensitiveEntity, int64](db, sensitiveManualCollection("users"))
			ctx := context.Background()
			plainCtx := WithSensitivePlaintext(ctx)
			entity := nestedSensitiveFixture()
			if err := r.Create(ctx, &entity); err != nil {
				t.Fatal(err)
			}
			raw, err := r.Native().FindOne(ctx, bson.M{"_id": int64(1)}).Raw()
			if err != nil {
				t.Fatal(err)
			}
			if _, err := raw.LookupErr("profile", "phone"); err == nil {
				t.Fatal("不应落库嵌套明文")
			}
			if cipher := raw.Lookup("profile", "secure_phone").StringValue(); cipher == "" || cipher == "13800008000" {
				t.Fatal("嵌套密文未保存")
			}
			if index := raw.Lookup("profile", "lookup_phone").StringValue(); index == "" {
				t.Fatal("嵌套盲索引未保存")
			}
			// 失败的单条插入覆盖指针、切片、数组和多维切片，不能污染任意原输入。
			duplicate := nestedSensitiveFixture()
			if err := r.Create(ctx, &duplicate); !mongo.IsDuplicateKeyError(err) {
				t.Fatalf("预期重复主键错误: %v", err)
			}
			if !reflect.DeepEqual(duplicate, nestedSensitiveFixture()) {
				t.Fatalf("失败创建污染嵌套实体: %+v", duplicate)
			}
			cached := Query().WithCache(time.Minute).Build()
			got, err := r.FindById(plainCtx, 1, cached)
			if err != nil || got.Profile.Phone != "13800008000" || got.Optional.Phone != "13900009000" || got.Contacts[0].Phone != "13700007000" {
				t.Fatalf("嵌套敏感值丢失: %+v %v", got, err)
			}
			got, err = r.FindById(ctx, 1, cached)
			if err != nil || got.Profile.Phone != "138****8000" || got.Matrix[0][0].Phone != "135****5000" {
				t.Fatalf("缓存未按本次权限脱敏: %+v %v", got, err)
			}
			if _, err := r.UpdateById(ctx, 1, field.New[string]("profile.phone").Set("139-8888-9000")); err != nil {
				t.Fatal(err)
			}
			got, err = r.FindById(plainCtx, 1, cached)
			if err != nil || got.Profile.Phone != "13988889000" {
				t.Fatalf("嵌套更新读取旧值: %+v %v", got, err)
			}
			for _, value := range []string{"13800008000", "13988889000"} {
				rows, err := r.FindList(plainCtx, Query().Where(r.SensitiveEq("Profile.Phone", value)).Build())
				want := 0
				if value == "13988889000" {
					want = 1
				}
				if err != nil || len(rows) != want {
					t.Fatalf("嵌套索引没有刷新: value=%s rows=%d err=%v", value, len(rows), err)
				}
			}
			if _, err := r.UpdateMapById(ctx, 1, map[string]any{"contacts.0.phone": "13688886666", "contacts.0.display": "updated"}); err != nil {
				t.Fatal(err)
			}
			rows, err := r.FindList(plainCtx, Query().Where(r.SensitiveEq("Contacts.Phone", "13688886666")).Build())
			if err != nil || len(rows) != 1 || rows[0].Contacts[0].Phone != "13688886666" || rows[0].Contacts[0].Display != "updated" {
				t.Fatalf("数组更新/索引查询失败: %+v %v", rows, err)
			}
			if _, err := r.UpdateById(ctx, 1, field.New[string]("optional.phone").Unset()); err != nil {
				t.Fatal(err)
			}
			raw, err = r.Native().FindOne(ctx, bson.M{"_id": int64(1)}).Raw()
			if err != nil {
				t.Fatal(err)
			}
			for _, leaf := range []string{"phone", "secure_phone", "lookup_phone"} {
				if _, err := raw.LookupErr("optional", leaf); err == nil {
					t.Fatalf("Unset 遗留字段 %s", leaf)
				}
			}
			if _, err := r.UpdateMapById(ctx, 1, map[string]any{"profile": SensitiveContact{Phone: "plaintext"}}); err == nil {
				t.Fatal("整个父对象更新必须拒绝")
			}
		})
	}
}

func TestSensitiveNilInlineCreatesAndReadsWithoutPanic(t *testing.T) {
	db := sensitiveIntegrationDB(t)
	r := MustRepository[inlineSensitiveEntity, int64](db, sensitiveManualCollection("inline"))
	ctx := context.Background()
	entity := inlineSensitiveEntity{ID: 1}
	if err := r.Create(ctx, &entity); err != nil {
		t.Fatal(err)
	}
	got, err := r.FindById(WithSensitivePlaintext(ctx), 1)
	if err != nil || got.SensitiveContact != nil {
		t.Fatalf("空 inline 对象读取错误: %+v %v", got, err)
	}
	if entity.SensitiveContact != nil {
		t.Fatal("创建不应初始化空嵌入对象")
	}
}

func TestSensitiveFailedCreateAndPartialBatchPreserveInput(t *testing.T) {
	db := sensitiveIntegrationDB(t)
	r := MustRepository[inlineSensitiveEntity, int64](db, sensitiveManualCollection("inline"))
	ctx := context.Background()
	if err := r.Create(ctx, &inlineSensitiveEntity{ID: 1, SensitiveContact: &SensitiveContact{Phone: "13800008000"}}); err != nil {
		t.Fatal(err)
	}
	shared := &SensitiveContact{Phone: "13900009000", PhoneCipher: "caller-cipher", PhoneIndex: "caller-index"}
	want := *shared
	duplicate := inlineSensitiveEntity{ID: 1, SensitiveContact: shared}
	if err := r.Create(ctx, &duplicate); !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("预期重复主键错误: %v", err)
	}
	if duplicate.SensitiveContact != shared || *shared != want {
		t.Fatal("失败的单条插入修改原始嵌入指针")
	}
	untouched := &SensitiveContact{Phone: "13700007000", PhoneCipher: "untouched", PhoneIndex: "untouched"}
	untouchedWant := *untouched
	rows := []*inlineSensitiveEntity{
		{ID: 2, SensitiveContact: shared},
		{ID: 1, SensitiveContact: shared},
		{ID: 3, SensitiveContact: untouched},
	}
	if err := r.CreateBatch(ctx, rows); !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("预期批量重复主键错误: %v", err)
	}
	if rows[0].SensitiveContact == shared || rows[0].PhoneCipher == want.PhoneCipher {
		t.Fatal("已确认成功前缀应回填独立的加密结果")
	}
	if rows[1].SensitiveContact != shared || *shared != want || rows[2].SensitiveContact != untouched || *untouched != untouchedWant {
		t.Fatal("批量失败或未执行的输入被修改")
	}
	if n, err := r.Count(ctx); err != nil || n != 2 {
		t.Fatalf("部分成功应只有 2 条: %d %v", n, err)
	}
	if _, err := r.FindById(ctx, 3); !errors.Is(err, ErrNotFound) {
		t.Fatalf("失败点之后不应写入: %v", err)
	}
	preflight := []*inlineSensitiveEntity{{ID: 4, SensitiveContact: shared}, {ID: 0, SensitiveContact: untouched}}
	if err := r.CreateBatch(ctx, preflight); err == nil {
		t.Fatal("无效主键应在整块写入前失败")
	}
	if *shared != want || *untouched != untouchedWant {
		t.Fatal("批量预处理失败修改了原输入")
	}
	if _, err := r.FindById(ctx, 4); !errors.Is(err, ErrNotFound) {
		t.Fatalf("预处理失败不应写入块内第一条: %v", err)
	}
}
