package mongoplus

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

type dalTestRow struct {
	ID     int64 `bson:"_id"`
	Value  int64 `bson:"value"`
	Tenant int64 `bson:"tenant_id"`
}

func dalIntegration(t *testing.T, files map[string]string) (*Database, *DAL, context.Context, *Repository[dalTestRow, int64]) {
	t.Helper()
	db := NewDatabase(testutil.Database(t))
	dal := testDALFiles(files)
	dal.provider = fixedDALProvider{db}
	repo := MustRepository[dalTestRow, int64](db, CollectionConfig{Name: "rows", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	ctx := WithDALDB(context.Background(), dal)
	for _, row := range []*dalTestRow{{ID: 1, Value: 1, Tenant: 1}, {ID: 2, Value: 2, Tenant: 1}, {ID: 3, Value: 3, Tenant: 2}} {
		if err := repo.Create(ctx, row); err != nil {
			t.Fatal(err)
		}
	}
	return db, dal, ctx, repo
}

func TestDALNativeQueriesAndWrites(t *testing.T) {
	files := map[string]string{
		"window.json":     `{"collection":"rows","op":"aggregate","pipeline":[{"$match":{"tenant_id":"@tenant"}},{"$setWindowFields":{"sortBy":{"_id":1},"output":{"running":{"$sum":"$value","window":{"documents":["unbounded","current"]}}}}}],"options":{"allowDiskUse":true,"batchSize":1}}`,
		"page.json":       `{"collection":"rows","op":"aggregate","pipeline":[{"$match":{"tenant_id":"@tenant"}},{"$sort":{"_id":1}},{"$skip":"@offset"},{"$limit":"@limit"}]}`,
		"count_page.json": `{"collection":"rows","op":"aggregate","pipeline":[{"$match":{"tenant_id":"@tenant"}},{"$count":"total"}]}`,
		"update.json":     `{"collection":"rows","op":"updateOne","filter":{"_id":"@id","tenant_id":"@tenant"},"update":{"$set":{"value":"@value"}}}`,
		"bulk.json":       `{"collection":"rows","op":"bulkWrite","options":{"ordered":false},"operations":[{"op":"insertOne","document":{"_id":5,"value":5,"tenant_id":1}},{"op":"insertOne","document":{"_id":1}},{"op":"insertOne","document":{"_id":6,"value":6,"tenant_id":1}}]}`,
		"upsert.json":     `{"collection":"rows","op":"bulkWrite","operations":[{"op":"updateOne","filter":{"_id":7,"tenant_id":1},"update":[{"$set":{"value":7}}],"upsert":true},{"op":"replaceOne","filter":{"_id":2,"tenant_id":1},"replacement":{"value":20,"tenant_id":1}}]}`,
		"out.json":        `{"collection":"rows","op":"aggregate","pipeline":[{"$match":{"tenant_id":1}},{"$out":"snapshot"}]}`,
		"merge.json":      `{"collection":"rows","op":"aggregate","pipeline":[{"$match":{"_id":1}},{"$merge":{"into":{"db":"@db","coll":"snapshot"},"whenMatched":"replace","whenNotMatched":"insert"}}]}`,
	}
	db, _, ctx, repo := dalIntegration(t, files)
	var window []struct {
		ID      int64 `bson:"_id"`
		Running int64 `bson:"running"`
	}
	window, err := DALQueryNamed[struct {
		ID      int64 `bson:"_id"`
		Running int64 `bson:"running"`
	}](ctx, "window.json", map[string]any{"tenant": int64(1)})
	if err != nil || len(window) != 2 || window[1].Running != 3 {
		t.Fatalf("window: %+v %v", window, err)
	}
	one, err := DALQueryOneNamed[bson.M](ctx, "window.json", map[string]any{"tenant": int64(1)})
	if err != nil || one == nil || (*one)["running"] == nil {
		t.Fatalf("aggregate one: %v %v", one, err)
	}
	page, err := DALQueryPageNamed[dalTestRow](ctx, "page.json", map[string]any{"tenant": int64(1), "offset": int64(1), "limit": int64(1)})
	if err != nil || page.Total != 2 || len(page.List) != 1 || page.List[0].ID != 2 {
		t.Fatalf("page: %+v %v", page, err)
	}
	stop := errors.New("stop export")
	seen := 0
	err = DALWalkNamed(ctx, "window.json", map[string]any{"tenant": int64(1)}, func(bson.M) error { seen++; return stop })
	if !errors.Is(err, stop) || seen != 1 {
		t.Fatalf("walk: %d %v", seen, err)
	}
	cache := Query().WithCache(time.Minute).Build()
	if _, err := repo.FindById(ctx, 1, cache); err != nil {
		t.Fatal(err)
	}
	if err := DALExecNamed(ctx, "update.json", map[string]any{"id": int64(1), "tenant": int64(1), "value": int64(10)}); err != nil {
		t.Fatal(err)
	}
	row, err := repo.FindById(ctx, 1, cache)
	if err != nil || row.Value != 10 {
		t.Fatalf("DAL cache invalidation: %+v %v", row, err)
	}
	if _, err := repo.Count(ctx, cache); err != nil {
		t.Fatal(err)
	}
	partial, err := DALBulkWrite(ctx, "bulk.json")
	if err == nil || partial == nil || partial.InsertedCount != 2 {
		t.Fatalf("partial bulk: %+v %v", partial, err)
	}
	count, err := repo.Count(ctx, cache)
	if err != nil || count != 5 {
		t.Fatalf("partial invalidation: %d %v", count, err)
	}
	bulk, err := DALBulkWrite(ctx, "upsert.json")
	if err != nil || bulk.UpsertedCount != 1 || bulk.MatchedCount != 1 {
		t.Fatalf("upsert: %+v %v", bulk, err)
	}
	snapshot := MustRepository[dalTestRow, int64](db, CollectionConfig{Name: "snapshot", ID: IDConfig{Type: IDTypeInt64, Generation: IDGenerationManual}})
	if err := snapshot.Create(ctx, &dalTestRow{ID: 1, Value: 0}); err != nil {
		t.Fatal(err)
	}
	if _, err := snapshot.FindById(ctx, 1, cache); err != nil {
		t.Fatal(err)
	}
	if _, err := DALQuery[bson.M](ctx, "out.json"); err == nil {
		t.Fatal("read accepted $out")
	}
	if err := DALExec(ctx, "out.json"); err != nil {
		t.Fatal(err)
	}
	row, err = snapshot.FindById(ctx, 1, cache)
	if err != nil || row.Value != 10 {
		t.Fatalf("out target invalidation: %+v %v", row, err)
	}
	if err := DALExecNamed(ctx, "update.json", map[string]any{"id": int64(1), "tenant": int64(1), "value": int64(11)}); err != nil {
		t.Fatal(err)
	}
	if err := DALExecNamed(ctx, "merge.json", map[string]any{"db": db.Native().Name()}); err != nil {
		t.Fatal(err)
	}
	row, err = snapshot.FindById(ctx, 1, cache)
	if err != nil || row.Value != 11 {
		t.Fatalf("merge target invalidation: %+v %v", row, err)
	}
	if err := DALExecNamed(ctx, "merge.json", map[string]any{"db": "other_database"}); err == nil {
		t.Fatal("cross database write must be explicit")
	}
	err = DALNative(ctx, []string{"rows"}, func(ctx context.Context, native *mongo.Database) error {
		_, e := native.Collection("rows").UpdateOne(ctx, bson.D{{Key: "_id", Value: int64(1)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "value", Value: int64(12)}}}})
		if e != nil {
			return e
		}
		return stop
	})
	if !errors.Is(err, stop) {
		t.Fatal(err)
	}
	row, err = repo.FindById(ctx, 1, cache)
	if err != nil || row.Value != 12 {
		t.Fatalf("native partial invalidation: %+v %v", row, err)
	}
}

type countingDALProvider struct {
	db    *Database
	calls int
}

func (p *countingDALProvider) Get(context.Context) (*Database, error) { p.calls++; return p.db, nil }

func TestDALTransactionsPinSourceAndBoundRepository(t *testing.T) {
	db, dal, ctx, repo := dalIntegration(t, map[string]string{
		"update.json": `{"collection":"rows","op":"updateOne","filter":{"_id":1},"update":{"$set":{"value":"?"}}}`,
		"one.json":    `{"collection":"rows","filter":{"_id":1}}`,
	})
	provider := &countingDALProvider{db: db}
	dal.provider = provider
	cache := Query().WithCache(time.Minute).Build()
	if _, err := repo.FindById(ctx, 1, cache); err != nil {
		t.Fatal(err)
	}
	rollback := errors.New("rollback")
	err := DALWithTx(ctx, func(tx context.Context) error {
		if err := DALExec(tx, "update.json", int64(50)); err != nil {
			return err
		}
		row, err := DALQueryOne[dalTestRow](tx, "one.json")
		if err != nil {
			return err
		}
		if row.Value != 50 {
			t.Fatal(row)
		}
		cached, err := repo.FindById(ctx, 1, cache)
		if err != nil || cached.Value != 1 {
			t.Fatalf("uncommitted cache: %+v %v", cached, err)
		}
		return rollback
	})
	if !errors.Is(err, rollback) || provider.calls != 1 {
		t.Fatalf("routing %d %v", provider.calls, err)
	}
	row, err := repo.FindById(ctx, 1, cache)
	if err != nil || row.Value != 1 {
		t.Fatalf("rollback %+v %v", row, err)
	}
	var escaped context.Context
	err = Transaction(ctx, db, func(tx context.Context) error {
		bound := repo.WithTx(tx)
		return bound.WithDAL(ctx, dal, func(queryCtx context.Context) error {
			escaped = queryCtx
			return DALExec(queryCtx, "update.json", int64(60))
		})
	})
	if err != nil {
		t.Fatal(err)
	}
	row, err = repo.FindById(ctx, 1, cache)
	if err != nil || row.Value != 60 {
		t.Fatalf("commit invalidation %+v %v", row, err)
	}
	if err := DALExec(escaped, "update.json", int64(70)); !errors.Is(err, ErrTxDone) {
		t.Fatalf("escaped transaction: %v", err)
	}
}

func TestDALWatchResumeAndCancellation(t *testing.T) {
	_, _, ctx, repo := dalIntegration(t, map[string]string{
		"watch.json": `{"collection":"rows","op":"watch","pipeline":[{"$match":{"operationType":"insert","fullDocument.tenant_id":"@tenant"}}],"options":{"resumeAfter":"@resume","fullDocument":"updateLookup","maxAwaitTimeMS":100,"batchSize":1}}`,
	})
	params := map[string]any{"tenant": int64(1), "resume": nil}
	stream, err := DALWatchNamed(ctx, "watch.json", params)
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close(context.Background())
	if err := repo.Create(ctx, &dalTestRow{ID: 4, Value: 4, Tenant: 2}); err != nil {
		t.Fatal(err)
	}
	if err := repo.Create(ctx, &dalTestRow{ID: 5, Value: 5, Tenant: 1}); err != nil {
		t.Fatal(err)
	}
	wait, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	if !stream.Next(wait) {
		t.Fatal("missing event", stream.Err())
	}
	var event struct {
		Document dalTestRow `bson:"fullDocument"`
	}
	if err := stream.Decode(&event); err != nil || event.Document.ID != 5 {
		t.Fatalf("event %+v %v", event, err)
	}
	token := append(bson.Raw(nil), stream.ResumeToken()...)
	if err := stream.Close(ctx); err != nil {
		t.Fatal(err)
	}
	params["resume"] = token
	resumed, err := DALWatchNamed(ctx, "watch.json", params)
	if err != nil {
		t.Fatal(err)
	}
	defer resumed.Close(context.Background())
	if err := repo.Create(ctx, &dalTestRow{ID: 6, Value: 6, Tenant: 1}); err != nil {
		t.Fatal(err)
	}
	if !resumed.Next(wait) {
		t.Fatal("missing resumed event", resumed.Err())
	}
	if err := resumed.Decode(&event); err != nil || event.Document.ID != 6 {
		t.Fatalf("resumed %+v %v", event, err)
	}
	cancelled, stop := context.WithCancel(ctx)
	stop()
	if resumed.Next(cancelled) || resumed.Err() == nil {
		t.Fatal("subscription ignored cancellation")
	}
	if err := DALWithTx(ctx, func(tx context.Context) error { _, err := DALWatchNamed(tx, "watch.json", params); return err }); err == nil {
		t.Fatal("watch in transaction accepted")
	}
}

func TestDALBulkArrayFiltersAndAcknowledgement(t *testing.T) {
	db, dal, ctx, _ := dalIntegration(t, map[string]string{
		"array.json":  `{"collection":"rows","op":"bulkWrite","operations":[{"op":"updateOne","filter":{"_id":1,"tenant_id":1},"update":{"$set":{"items.$[entry].value":9}},"arrayFilters":[{"entry.value":{"$gte":2}}],"hint":"_id_"}]}`,
		"insert.json": `{"collection":"rows","op":"insertOne","document":{"_id":100}}`,
		"bulk.json":   `{"collection":"rows","op":"bulkWrite","operations":[{"op":"insertOne","document":{"_id":101}}]}`,
		"out.json":    `{"collection":"rows","op":"aggregate","pipeline":[{"$out":"snapshot_ack"}]}`,
	})
	_, err := db.Native().Collection("rows").UpdateOne(ctx, bson.D{{Key: "_id", Value: int64(1)}}, bson.D{{Key: "$set", Value: bson.D{{Key: "items", Value: bson.A{bson.D{{Key: "value", Value: 1}}, bson.D{{Key: "value", Value: 2}}}}}}})
	if err != nil {
		t.Fatal(err)
	}
	result, err := DALBulkWrite(ctx, "array.json")
	if err != nil || result.ModifiedCount != 1 {
		t.Fatalf("array filters %+v %v", result, err)
	}
	var row struct {
		Items []struct {
			Value int `bson:"value"`
		} `bson:"items"`
	}
	if err := db.Native().Collection("rows").FindOne(ctx, bson.D{{Key: "_id", Value: int64(1)}}).Decode(&row); err != nil || row.Items[0].Value != 1 || row.Items[1].Value != 9 {
		t.Fatalf("array result %+v %v", row, err)
	}
	unack := NewDatabase(db.Native().Client().Database(db.Native().Name(), options.Database().SetWriteConcern(writeconcern.Unacknowledged())))
	dal.provider = fixedDALProvider{unack}
	if err := DALExec(ctx, "insert.json"); !errors.Is(err, ErrUnacknowledgedWrite) {
		t.Fatalf("unack insert %v", err)
	}
	if _, err := DALBulkWrite(ctx, "bulk.json"); !errors.Is(err, ErrUnacknowledgedWrite) {
		t.Fatalf("unack bulk %v", err)
	}
	// 写入聚合使用独立 majority 集合选项，不能继承 w=0 后误报成功。
	if err := DALExec(ctx, "out.json"); err != nil {
		t.Fatal(err)
	}
	count, err := db.Native().Collection("snapshot_ack").CountDocuments(ctx, bson.D{{Key: "_id", Value: int64(1)}})
	if err != nil || count != 1 {
		t.Fatalf("acknowledged output %d %v", count, err)
	}
}
