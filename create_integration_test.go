package mongoplus_test

import (
	"context"
	"errors"
	"fmt"
	"math"
	"os"
	"reflect"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

func configuredDatabase(t *testing.T, opts *options.ClientOptions) *mongo.Database {
	t.Helper()
	base := testutil.Database(t)
	client, err := mongo.Connect(options.MergeClientOptions(options.Client().ApplyURI(os.Getenv("MONGOPLUS_TEST_URI")), opts))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = client.Disconnect(ctx)
	})
	return client.Database(base.Name())
}

type customMoney struct{ cents int64 }
type codecEntity struct {
	ID          bson.ObjectID `bson:"_id"`
	DisplayName string        `json:"display_name"`
	Money       customMoney   `bson:"money"`
	Labels      []string      `bson:"labels"`
}

func TestCreatePreservesClientBSONConfiguration(t *testing.T) {
	registry := bson.NewRegistry()
	registry.RegisterTypeEncoder(reflect.TypeOf(customMoney{}), bson.ValueEncoderFunc(func(_ bson.EncodeContext, w bson.ValueWriter, v reflect.Value) error {
		return w.WriteString(strconv.FormatInt(v.Interface().(customMoney).cents, 10))
	}))
	registry.RegisterTypeDecoder(reflect.TypeOf(customMoney{}), bson.ValueDecoderFunc(func(_ bson.DecodeContext, r bson.ValueReader, v reflect.Value) error {
		text, err := r.ReadString()
		if err != nil {
			return err
		}
		n, err := strconv.ParseInt(text, 10, 64)
		if err != nil {
			return err
		}
		v.Set(reflect.ValueOf(customMoney{n}))
		return nil
	}))
	db := mp.NewDatabase(configuredDatabase(t, options.Client().SetRegistry(registry).SetBSONOptions(&options.BSONOptions{UseJSONStructTags: true, NilSliceAsEmpty: true})))
	repo := mp.MustRepository[codecEntity, bson.ObjectID](db, mp.CollectionConfig{Name: "codec"})
	ctx := context.Background()
	first := &codecEntity{DisplayName: "single", Money: customMoney{123}}
	if err := repo.Create(ctx, first); err != nil {
		t.Fatal(err)
	}
	rows := []*codecEntity{{DisplayName: "batch", Money: customMoney{456}}}
	if err := repo.CreateBatch(ctx, rows); err != nil {
		t.Fatal(err)
	}
	for _, want := range []*codecEntity{first, rows[0]} {
		for _, option := range []mp.QueryOption{mp.Query().Build(), mp.Query().WithCache(time.Minute).Build()} {
			got, err := repo.FindById(ctx, want.ID, option)
			if err != nil || got.DisplayName != want.DisplayName || got.Money != want.Money {
				t.Fatalf("配置丢失: %+v %v", got, err)
			}
			got.Labels = append(got.Labels, "changed")
		}
		var raw bson.Raw
		if err := repo.Native().FindOne(ctx, bson.M{"_id": want.ID}).Decode(&raw); err != nil {
			t.Fatal(err)
		}
		if raw.Lookup("display_name").StringValue() != want.DisplayName || raw.Lookup("money").StringValue() != strconv.FormatInt(want.Money.cents, 10) || raw.Lookup("labels").Type != bson.TypeArray {
			t.Fatalf("存储类型错误 %s", raw)
		}
	}
}

func TestCreateBatchChunksSequenceAndCache(t *testing.T) {
	var inserts, sequences atomic.Int32
	monitor := &event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "insert" {
			inserts.Add(1)
		}
		if e.CommandName == "findAndModify" {
			sequences.Add(1)
		}
	}}
	db := mp.NewDatabase(configuredDatabase(t, options.Client().SetMonitor(monitor)))
	r := mp.MustRepository[testUser, int64](db, mp.CollectionConfig{Name: "batch", ID: mp.IDConfig{Type: mp.IDTypeInt64}, TenantField: "company_id", SoftDelete: true, Timestamps: true})
	ctx := mp.WithTenant(context.Background(), int64(7))
	cached := mp.Query().WithCache(time.Minute).Build()
	if n, err := r.Count(ctx, cached); err != nil || n != 0 {
		t.Fatalf("预热 %d %v", n, err)
	}
	rows := make([]*testUser, 1001)
	for i := range rows {
		rows[i] = &testUser{Name: fmt.Sprint(i), CompanyID: 99}
	}
	if err := r.CreateBatch(ctx, rows); err != nil {
		t.Fatal(err)
	}
	if inserts.Load() != 2 || sequences.Load() != 2 {
		t.Fatalf("未批量执行 insert=%d sequence=%d", inserts.Load(), sequences.Load())
	}
	for i, row := range rows {
		if row.ID != int64(i+1) || row.CompanyID != 7 || row.CreatedAt.IsZero() || row.DeletedAt != nil {
			t.Fatalf("回填错误 %d %+v", i, row)
		}
	}
	last, err := r.FindOne(ctx, mp.Query().Order(field.New[int64]("_id").Asc()).Offset(1000).Build())
	if err != nil || last.ID != 1001 {
		t.Fatalf("FindOne 丢失 Offset %+v %v", last, err)
	}
	if n, err := r.Count(ctx, cached); err != nil || n != 1001 {
		t.Fatalf("缓存未失效 %d %v", n, err)
	}
	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if err := r.CreateBatch(cancelled, []*testUser{{Name: "cancel"}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("取消未生效 %v", err)
	}
}

func TestCreateBatchFailurePrefixAndRollback(t *testing.T) {
	db, r := userRepo(t)
	ctx := mp.WithTenant(context.Background(), int64(1))
	if _, err := r.Native().Indexes().CreateOne(ctx, mongo.IndexModel{Keys: bson.D{{Key: "name", Value: 1}}, Options: options.Index().SetUnique(true)}); err != nil {
		t.Fatal(err)
	}
	rows := []*testUser{{Name: "one"}, {Name: "one"}, {Name: "three"}}
	err := r.CreateBatch(ctx, rows)
	if !mongo.IsDuplicateKeyError(err) {
		t.Fatalf("错误链丢失 %v", err)
	}
	if rows[0].ID == 0 || rows[1].ID != 0 || rows[2].ID != 0 {
		t.Fatalf("失败文档被回填 %+v", rows)
	}
	if n, _ := r.Count(ctx); n != 1 {
		t.Fatalf("部分写入数量 %d", n)
	}
	rollback := errors.New("rollback")
	err = mp.Transaction(ctx, db, func(tx context.Context) error {
		if err := r.CreateBatch(tx, []*testUser{{Name: "tx1"}, {Name: "tx2"}}); err != nil {
			return err
		}
		return rollback
	})
	if !errors.Is(err, rollback) {
		t.Fatal(err)
	}
	if n, _ := r.Count(ctx); n != 1 {
		t.Fatalf("事务未回滚 %d", n)
	}
	before, _ := r.Count(ctx)
	if err := r.CreateBatch(ctx, []*testUser{{Name: "preflight"}, nil}); err == nil {
		t.Fatal("允许 nil 文档")
	}
	if n, _ := r.Count(ctx); n != before {
		t.Fatal("预处理失败仍写入文档")
	}
}

func TestSequenceBlocksAcrossRepositoriesAndWrappers(t *testing.T) {
	var calls atomic.Int32
	native := configuredDatabase(t, options.Client().SetMonitor(&event.CommandMonitor{Started: func(_ context.Context, e *event.CommandStartedEvent) {
		if e.CommandName == "findAndModify" {
			calls.Add(1)
		}
	}}))
	db := mp.NewDatabase(native)
	cfg := mp.CollectionConfig{Name: "blocks", ID: mp.IDConfig{Type: mp.IDTypeInt64, BlockSize: 16}, TenantField: "company_id", SoftDelete: true, Timestamps: true}
	repos := []*mp.Repository[testUser, int64]{mp.MustRepository[testUser, int64](db, cfg), mp.MustRepository[testUser, int64](db, cfg), mp.MustRepository[testUser, int64](mp.NewDatabase(native), cfg)}
	ctx := mp.WithTenant(context.Background(), int64(1))
	ids := make(chan int64, 48)
	errs := make(chan error, 48)
	var wg sync.WaitGroup
	for i := 0; i < 48; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			row := &testUser{Name: fmt.Sprint(i)}
			if err := repos[i%len(repos)].Create(ctx, row); err != nil {
				errs <- err
				return
			}
			ids <- row.ID
		}(i)
	}
	wg.Wait()
	close(ids)
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	seen := map[int64]bool{}
	for id := range ids {
		if id <= 0 || seen[id] {
			t.Fatalf("重复主键 %d", id)
		}
		seen[id] = true
	}
	if len(seen) != 48 || calls.Load() != 3 {
		t.Fatalf("号码申请错误 ids=%d calls=%d", len(seen), calls.Load())
	}
	if err := native.Collection("__mongo_plus_sequences").FindOneAndUpdate(ctx, bson.M{"_id": "blocks"}, bson.M{"$set": bson.M{"value": int64(math.MaxInt64 - 1)}}).Err(); err != nil {
		t.Fatal(err)
	}
	overflow := mp.MustRepository[testUser, int64](mp.NewDatabase(native), mp.CollectionConfig{Name: "blocks", ID: mp.IDConfig{Type: mp.IDTypeInt64}})
	if err := overflow.CreateBatch(ctx, []*testUser{{}, {}}); err == nil {
		t.Fatal("允许序列溢出")
	}
	var seq struct {
		Value int64 `bson:"value"`
	}
	if err := native.Collection("__mongo_plus_sequences").FindOne(ctx, bson.M{"_id": "blocks"}).Decode(&seq); err != nil || seq.Value != math.MaxInt64-1 {
		t.Fatalf("溢出修改了序列 %+v %v", seq, err)
	}
}

func TestUnacknowledgedCreateDoesNotConfirmEntities(t *testing.T) {
	db := mp.NewDatabase(configuredDatabase(t, options.Client().SetWriteConcern(&writeconcern.WriteConcern{W: 0})))
	repo := mp.MustRepository[benchDocument, bson.ObjectID](db, mp.CollectionConfig{Name: "unacknowledged"})
	row := &benchDocument{Payload: "one"}
	if err := repo.Create(context.Background(), row); !errors.Is(err, mp.ErrUnacknowledgedWrite) || !row.ID.IsZero() {
		t.Fatalf("未确认写入被当作成功: %+v %v", row, err)
	}
	rows := []*benchDocument{{Payload: "two"}, {Payload: "three"}}
	if err := repo.CreateBatch(context.Background(), rows); !errors.Is(err, mp.ErrUnacknowledgedWrite) || !rows[0].ID.IsZero() || !rows[1].ID.IsZero() {
		t.Fatalf("未确认批量写入被回填: %v", err)
	}
}

type inlineTimes struct {
	CreatedAt time.Time `bson:"created_at"`
	UpdatedAt time.Time `bson:"updated_at"`
}
type inlineEntity struct {
	ID    int64        `bson:"_id"`
	Times *inlineTimes `bson:",inline"`
}

func TestCreatePreservesInlinePointersAndDatabaseOnlyFields(t *testing.T) {
	ctx := context.Background()
	db := mp.NewDatabase(testutil.Database(t))
	repo := mp.MustRepository[inlineEntity, int64](db, mp.CollectionConfig{Name: "inline", ID: mp.IDConfig{Type: mp.IDTypeInt64, Generation: mp.IDGenerationManual}, Timestamps: true})
	original := &inlineTimes{}
	row := &inlineEntity{ID: 1, Times: original}
	if err := repo.Create(ctx, row); err != nil {
		t.Fatal(err)
	}
	if row.Times == original || row.Times.CreatedAt.IsZero() || !original.CreatedAt.IsZero() {
		t.Fatal("inline 指针共享或未填充")
	}
	got, err := repo.FindById(ctx, row.ID)
	if err != nil || !got.Times.CreatedAt.Equal(row.Times.CreatedAt) {
		t.Fatalf("回填时间与 BSON 存储精度不一致: %+v %v", got, err)
	}
	duplicate := &inlineEntity{ID: 1, Times: original}
	if err := repo.Create(ctx, duplicate); !mongo.IsDuplicateKeyError(err) {
		t.Fatal(err)
	}
	if duplicate.Times != original || !original.CreatedAt.IsZero() {
		t.Fatal("失败插入修改了调用者自动字段")
	}
	type minimal struct {
		ID int64 `bson:"_id"`
	}
	minimalRepo := mp.MustRepository[minimal, int64](db, mp.CollectionConfig{Name: "minimal", ID: mp.IDConfig{Type: mp.IDTypeInt64}, SoftDelete: true, Timestamps: true, TenantField: "company_id"})
	minimalCtx := mp.WithTenant(ctx, int64(3))
	document := &minimal{}
	if err := minimalRepo.Create(minimalCtx, document); err != nil {
		t.Fatal(err)
	}
	if _, err := minimalRepo.FindById(minimalCtx, document.ID); err != nil {
		t.Fatal(err)
	}
	var stored testUser
	if err := minimalRepo.Native().FindOne(ctx, bson.M{"_id": document.ID}).Decode(&stored); err != nil {
		t.Fatal(err)
	}
	if stored.CreatedAt.IsZero() || stored.CompanyID != 3 || stored.DeletedAt != nil {
		t.Fatalf("数据库自动字段丢失 %+v", stored)
	}
}
