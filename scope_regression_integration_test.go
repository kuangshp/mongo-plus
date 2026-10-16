package mongoplus

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type scopeReviewRow struct {
	ID        int64      `bson:"_id"`
	CompanyID int64      `bson:"company_id"`
	Name      string     `bson:"name"`
	Target    int64      `bson:"target"`
	DeletedAt *time.Time `bson:"deleted_at"`
	CreatedAt time.Time  `bson:"created_at"`
	UpdatedAt time.Time  `bson:"updated_at"`
}

func TestReviewDALScopeRegression(t *testing.T) {
	db := NewDatabase(testutil.Database(t))
	ctx := context.Background()
	if err := RegisterTenant(db, TenantConfig[int64]{TenantField: "company_id"}); err != nil {
		t.Fatal(err)
	}
	cfg := CollectionConfig{Name: "rows", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}, SoftDelete: true, Timestamps: true}
	if err := db.RegisterCollections(cfg); err != nil {
		t.Fatal(err)
	} // DAL 在仓储构造之前即可隔离。
	files := map[string]string{
		"all.json":      `{"collection":"rows","op":"bulkWrite","operations":[{"op":"updateMany","allowAll":true,"update":{"$set":{"name":"changed"}}}]}`,
		"unsafe.json":   `{"collection":"rows","op":"bulkWrite","operations":[{"op":"deleteMany"}]}`,
		"pipeline.json": `{"collection":"rows","op":"bulkWrite","operations":[{"op":"updateOne","filter":{"_id":1},"update":[{"$set":{"company_id":2}}]}]}`,
		"replace.json":  `{"collection":"rows","op":"bulkWrite","operations":[{"op":"updateOne","filter":{"_id":1},"update":[{"$replaceWith":{"company_id":2}}]}]}`,
		"rename.json":   `{"collection":"rows","op":"updateOne","filter":{"_id":1},"update":{"$rename":{"target":"company_id"}}}`,
		"safe.json":     `{"collection":"rows","op":"bulkWrite","operations":[{"op":"updateOne","filter":{"_id":1},"update":[{"$set":{"name":"safe"}}]}]}`,
		"watch.json":    `{"collection":"rows","op":"watch","pipeline":[],"options":{"fullDocument":"updateLookup","maxAwaitTimeMS":100}}`,
		"near.json":     `{"collection":"rows","op":"aggregate","pipeline":[{"$geoNear":{"near":{"type":"Point","coordinates":[0,0]},"distanceField":"distance","spherical":true}}]}`,
	}
	fs := fstest.MapFS{}
	for name, body := range files {
		fs[name] = &fstest.MapFile{Data: []byte(body)}
	}
	dal, err := NewDal(db, NewEmbedLoader(fs))
	if err != nil {
		t.Fatal(err)
	}
	defer dal.Close()
	scoped := WithDALDB(WithTenantID(ctx, int64(1)), dal)
	_, err = db.native.Collection("rows").InsertMany(ctx, []any{bson.M{"_id": int64(1), "company_id": int64(1), "target": int64(2), "name": "one"}, bson.M{"_id": int64(2), "company_id": int64(2), "name": "two"}})
	if err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"pipeline.json", "replace.json", "rename.json", "unsafe.json"} {
		t.Run(name, func(t *testing.T) {
			if err := DALExec(scoped, name); err == nil {
				t.Fatal("危险写入应在执行前拒绝")
			}
		})
	}
	res, err := DALBulkWrite(scoped, "all.json")
	if err != nil || res.MatchedCount != 1 {
		t.Fatalf("租户内全量更新: %+v %v", res, err)
	}
	var other scopeReviewRow
	if err = db.native.Collection("rows").FindOne(ctx, bson.M{"_id": int64(2)}).Decode(&other); err != nil || other.Name != "two" {
		t.Fatalf("跨租户修改: %+v %v", other, err)
	}
	if _, err = DALBulkWrite(scoped, "safe.json"); err != nil {
		t.Fatal(err)
	}
	// $geoNear 必须仍然位于首阶段，并同时受到自动租户条件约束。
	_, err = db.native.Collection("rows").Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "location", Value: "2dsphere"}}})
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.native.Collection("rows").UpdateMany(ctx, bson.D{}, bson.M{"$set": bson.M{"location": bson.M{"type": "Point", "coordinates": bson.A{0, 0}}}})
	if err != nil {
		t.Fatal(err)
	}
	rows, err := DALQuery[bson.M](scoped, "near.json")
	if err != nil || len(rows) != 1 {
		t.Fatalf("geoNear: %d %v", len(rows), err)
	}
	if err = RegisterDataPermission(db, DataPermissionConfig{}); err != nil {
		t.Fatal(err)
	}
	perm := WithDataPermission(scoped, func(string) (bson.D, error) {
		return bson.D{{Key: "$or", Value: bson.A{bson.D{{Key: "name", Value: "event"}}, bson.D{{Key: "name", Value: "other"}}}}}, nil
	})
	stream, err := DALWatch(perm, "watch.json")
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close(ctx)
	_, err = db.native.Collection("rows").InsertOne(ctx, bson.M{"_id": int64(3), "company_id": int64(1), "name": "event"})
	if err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	saw := false
	for time.Now().Before(deadline) {
		if stream.TryNext(ctx) {
			saw = true
			break
		}
		if stream.Err() != nil {
			t.Fatal(stream.Err())
		}
	}
	if !saw {
		t.Fatal("$or 权限下漏掉符合条件的事件")
	}
}

type nestedTenantReview struct {
	ID    int64              `bson:"_id"`
	Scope *nestedTenantScope `bson:"scope"`
}
type nestedTenantScope struct {
	CompanyID int64 `bson:"company_id"`
}

func TestReviewNestedTenantAndSensitiveAutoFill(t *testing.T) {
	ctx := context.Background()
	native := testutil.Database(t)
	db := NewDatabase(native)
	if err := RegisterTenant(db, TenantConfig[int64]{TenantField: "scope.company_id"}); err != nil {
		t.Fatal(err)
	}
	repo := MustRepository[nestedTenantReview, int64](db, CollectionConfig{Name: "nested", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	inputScope := &nestedTenantScope{CompanyID: 2}
	row := nestedTenantReview{ID: 1, Scope: inputScope}
	if err := repo.Create(WithTenantID(ctx, int64(1)), &row); err != nil {
		t.Fatal(err)
	}
	if inputScope.CompanyID != 2 {
		t.Fatal("创建修改了调用者共享指针")
	}
	for tenant, want := range map[int64]int64{1: 1, 2: 0} {
		got, err := repo.Count(WithTenantID(ctx, tenant))
		if err != nil || got != want {
			t.Fatalf("tenant %d count=%d err=%v", tenant, got, err)
		}
	}
	var raw bson.Raw
	if err := repo.Native().FindOne(ctx, bson.M{"_id": int64(1)}).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.LookupErr("scope.company_id"); err == nil {
		t.Fatal("不能写入点号字面字段")
	}
	// 创建、更新的逻辑敏感字段自动填充都必须先加密，再持久化。
	secure := NewDatabase(native)
	if _, err := RegisterSensitive(secure, SensitiveConfig{Key: []byte("0123456789abcdef")}); err != nil {
		t.Fatal(err)
	}
	if err := RegisterAutoFill(secure, AutoFillConfig{Fields: []FieldConfig{{Name: "phone", OnCreate: true, OnUpdate: true, Getter: func(c context.Context) any { return c.Value("test-phone") }}}}); err != nil {
		t.Fatal(err)
	}
	people := MustRepository[tagSensitiveEntity, int64](secure, CollectionConfig{Name: "people", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	person := tagSensitiveEntity{ID: 1}
	createCtx := context.WithValue(ctx, "test-phone", "13800008000")
	if err := people.Create(createCtx, &person); err != nil {
		t.Fatal(err)
	}
	updateCtx := context.WithValue(ctx, "test-phone", "13900009000")
	if _, err := people.UpdateById(updateCtx, 1, field.New[string]("name").Set("updated")); err != nil {
		t.Fatal(err)
	}
	got, err := people.FindById(WithSensitivePlaintext(ctx), 1)
	if err != nil || got.Phone != "13900009000" {
		t.Fatalf("自动填充敏感字段: %+v %v", got, err)
	}
	if err := people.Native().FindOne(ctx, bson.M{"_id": int64(1)}).Decode(&raw); err != nil {
		t.Fatal(err)
	}
	if _, err := raw.LookupErr("phone"); err == nil {
		t.Fatal("自动填充泄漏明文")
	}
	list, err := people.FindList(ctx, Query().Where(people.SensitiveEq("Phone", "13900009000")).Build())
	if err != nil || len(list) != 1 {
		t.Fatalf("盲索引未同步 %d %v", len(list), err)
	}
}

func TestReviewCompoundOperations(t *testing.T) {
	ctx := context.Background()
	db := NewDatabase(testutil.Database(t))
	if err := RegisterTenant(db, TenantConfig[int64]{TenantField: "company_id"}); err != nil {
		t.Fatal(err)
	}
	r := MustRepository[scopeReviewRow, int64](db, CollectionConfig{Name: "jobs", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}, SoftDelete: true, Timestamps: true})
	one := WithTenantID(ctx, int64(1))
	two := WithTenantID(ctx, int64(2))
	for i := int64(1); i <= 2; i++ {
		if err := r.Create(one, &scopeReviewRow{ID: i, Name: "ready"}); err != nil {
			t.Fatal(err)
		}
	}
	// 原子抢占不应将同一任务交给两个消费者。
	ids := make(chan int64, 2)
	errs := make(chan error, 2)
	for i := 0; i < 2; i++ {
		go func() {
			row, err := r.FindOneAndUpdate(one, Query().Where(field.New[string]("name").Eq("ready")).Order(field.New[int64]("_id").Asc()).Build(), ModifyOptions{ReturnDocument: options.After}, field.New[string]("name").Set("taken"))
			if err != nil {
				errs <- err
				return
			}
			ids <- row.ID
		}()
	}
	gotIDs := map[int64]bool{}
	for i := 0; i < 2; i++ {
		select {
		case id := <-ids:
			gotIDs[id] = true
		case err := <-errs:
			t.Fatal(err)
		}
	}
	if len(gotIDs) != 2 {
		t.Fatal("原子领取重复")
	}
	if _, err := r.FindOneAndUpdate(two, Query().Where(field.New[int64]("_id").Eq(1)).Build(), ModifyOptions{}, field.New[string]("name").Set("bad")); !errors.Is(err, ErrNotFound) {
		t.Fatalf("隔离失效: %v", err)
	}
	before, err := r.FindById(one, 1)
	if err != nil {
		t.Fatal(err)
	}
	replacement := scopeReviewRow{ID: 1, Name: "replacement", CompanyID: 99}
	after, err := r.FindOneAndReplace(one, Query().Build(), &replacement, ModifyOptions{ReturnDocument: options.After})
	if err != nil || after.CompanyID != 1 || !after.CreatedAt.Equal(before.CreatedAt) {
		t.Fatalf("替换系统字段: %+v %v", after, err)
	}
	if replacement.CompanyID != 99 {
		t.Fatal("修改原替换实体")
	}
	removed, err := r.FindOneAndDelete(one, Query().Where(field.New[int64]("_id").Eq(1)).Build())
	if err != nil || removed.Name != "replacement" {
		t.Fatalf("查删 %+v %v", removed, err)
	}
	if _, err = r.FindById(one, 1); !errors.Is(err, ErrNotFound) {
		t.Fatal("逻辑删除未生效")
	}
	upsert, err := r.FindOneAndUpdate(one, Query().Where(field.New[int64]("_id").Eq(3)).Build(), ModifyOptions{Upsert: true, ReturnDocument: options.After}, field.New[string]("name").Set("upserted"))
	if err != nil || upsert.ID != 3 || upsert.CompanyID != 1 || upsert.CreatedAt.IsZero() {
		t.Fatalf("upsert %+v %v", upsert, err)
	}
	if _, err = r.EstimatedDocumentCount(one); err == nil {
		t.Fatal("估计计数不得绕过租户")
	}
	values, err := r.Distinct(one, "name")
	if err != nil || len(values) != 2 {
		t.Fatalf("distinct %v %v", values, err)
	}
	if _, err = r.FindOneAndUpdate(one, Query().Where(field.New[int64]("_id").Eq(3)).Build(), ModifyOptions{}, field.New[int64]("company_id").Set(2)); err == nil {
		t.Fatal("查改不得修改租户")
	}
	if _, err = r.FindOneAndUpdate(one, Query().Build(), ModifyOptions{}, field.New[string]("name").Set("bad")); !errors.Is(err, ErrUnsafeWrite) {
		t.Fatal(fmt.Sprintf("空业务条件应拒绝: %v", err))
	}
}

func TestReviewQueryOptionsAndCompoundRollback(t *testing.T) {
	ctx := context.Background()
	db := NewDatabase(testutil.Database(t))
	r := MustRepository[scopeReviewRow, int64](db, CollectionConfig{Name: "options", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	if err := r.Create(ctx, &scopeReviewRow{ID: 1, Name: "Alice"}); err != nil {
		t.Fatal(err)
	}
	cached := Query().Where(field.New[string]("name").Eq("alice")).Collation(options.Collation{Locale: "en", Strength: 2}).WithCache(time.Minute).Build()
	if rows, err := r.FindList(ctx, cached); err != nil || len(rows) != 1 {
		t.Fatalf("Collation %d %v", len(rows), err)
	}
	strict := Query().Where(field.New[string]("name").Eq("alice")).Collation(options.Collation{Locale: "en", Strength: 3}).WithCache(time.Minute).Build()
	if rows, err := r.FindList(ctx, strict); err != nil || len(rows) != 0 {
		t.Fatalf("不同 Collation 错误复用缓存 %d %v", len(rows), err)
	}
	if found, err := r.Exists(ctx, cached); err != nil || !found {
		t.Fatalf("Exists 没有沿用 Collation %v %v", found, err)
	}
	let := Query().RawWhere(bson.D{{Key: "$expr", Value: bson.D{{Key: "$eq", Value: bson.A{"$name", "$$wanted"}}}}}).Let(bson.D{{Key: "wanted", Value: "Alice"}}).Build()
	if row, err := r.FindOne(ctx, let); err != nil || row.ID != 1 {
		t.Fatalf("Let FindOne %+v %v", row, err)
	}
	sentinel := errors.New("rollback")
	if err := Transaction(ctx, db, func(tx context.Context) error {
		_, err := r.FindOneAndUpdate(tx, Query().Where(field.New[int64]("_id").Eq(1)).Build(), ModifyOptions{ReturnDocument: options.After}, field.New[string]("name").Set("changed"))
		if err != nil {
			return err
		}
		return sentinel
	}); !errors.Is(err, sentinel) {
		t.Fatal(err)
	}
	row, err := r.FindById(ctx, 1)
	if err != nil || row.Name != "Alice" {
		t.Fatalf("原子操作没有沿用事务回滚 %+v %v", row, err)
	}
	if count, err := r.EstimatedDocumentCount(ctx); err != nil || count != 1 {
		t.Fatalf("估计计数 %d %v", count, err)
	}
}

func TestReviewCollationCannotWidenStringTenants(t *testing.T) {
	type stringTenantRow struct {
		ID      int64  `bson:"_id"`
		Company string `bson:"company"`
		Name    string `bson:"name"`
	}
	ctx := context.Background()
	db := NewDatabase(testutil.Database(t))
	if err := RegisterTenant(db, TenantConfig[string]{TenantField: "company"}); err != nil {
		t.Fatal(err)
	}
	r := MustRepository[stringTenantRow, int64](db, CollectionConfig{Name: "string_tenants", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	for i, tenant := range []string{"TenantA", "tenanta"} {
		if err := r.Create(WithTenantID(ctx, tenant), &stringTenantRow{ID: int64(i + 1), Name: "Alice"}); err != nil {
			t.Fatal(err)
		}
	}
	one := WithTenantID(ctx, "TenantA")
	query := Query().Collation(options.Collation{Locale: "en", Strength: 1}).Build()
	rows, err := r.FindList(one, query)
	if err != nil || len(rows) != 1 || rows[0].ID != 1 {
		t.Fatalf("Collation 扩大租户授权: %+v %v", rows, err)
	}
}
