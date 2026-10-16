package migration

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

func step(version string, up func(context.Context, *mongo.Database) error) Migration {
	return Migration{Version: version, Checksum: Checksum([]byte(version)), Up: up}
}

func TestMigrationHistoryChecksumBackfillAndRollback(t *testing.T) {
	db := testutil.Database(t)
	ctx := context.Background()
	users := db.Collection("users")
	for id := range 7 {
		if _, err := users.InsertOne(ctx, bson.M{"_id": id}); err != nil {
			t.Fatal(err)
		}
	}
	calls := 0
	first := step("2026101601", func(ctx context.Context, db *mongo.Database) error {
		calls++
		n, err := Backfill(ctx, db.Collection("users"), bson.D{{Key: "version", Value: bson.D{{Key: "$exists", Value: false}}}}, bson.D{{Key: "$set", Value: bson.D{{Key: "version", Value: 1}}}}, 2)
		if err == nil && n != 7 {
			t.Fatalf("回填数量=%d", n)
		}
		return err
	})
	second := step("2026101602", func(ctx context.Context, db *mongo.Database) error {
		calls++
		_, err := db.Collection("users").UpdateMany(ctx, bson.D{}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: 1}}}})
		return err
	})
	second.Down = func(ctx context.Context, db *mongo.Database) error {
		_, err := db.Collection("users").UpdateMany(ctx, bson.D{}, bson.D{{Key: "$unset", Value: bson.D{{Key: "status", Value: ""}}}})
		return err
	}
	steps := []Migration{second, first}
	if pending, err := Plan(ctx, db, steps); err != nil || len(pending) != 2 {
		t.Fatalf("plan=%v %v", pending, err)
	}
	if names, err := db.ListCollectionNames(ctx, bson.D{}); err != nil || len(names) != 1 {
		t.Fatal("Plan 修改了数据库", names, err)
	}
	if err := Run(ctx, db, steps, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := Run(ctx, db, steps, Options{}); err != nil || calls != 2 {
		t.Fatalf("重复执行: calls=%d err=%v", calls, err)
	}
	history, err := History(ctx, db)
	if err != nil || len(history) != 2 || history[0].Status != "applied" {
		t.Fatalf("history=%+v %v", history, err)
	}
	changed := append([]Migration(nil), steps...)
	changed[0].Checksum = Checksum([]byte("changed"))
	if err := Run(ctx, db, changed, Options{}); !errors.Is(err, ErrChecksumMismatch) {
		t.Fatal("未拒绝篡改已应用迁移", err)
	}
	if err := Rollback(ctx, db, steps, first.Version, Options{}); !errors.Is(err, ErrInvalidOrder) {
		t.Fatal("允许跳过最新版本回滚", err)
	}
	if err := Rollback(ctx, db, steps, second.Version, Options{}); err != nil {
		t.Fatal(err)
	}
	if n, err := users.CountDocuments(ctx, bson.D{{Key: "status", Value: bson.D{{Key: "$exists", Value: true}}}}); err != nil || n != 0 {
		t.Fatal("显式 Down 未执行", n, err)
	}
	if err := Rollback(ctx, db, steps, first.Version, Options{}); !errors.Is(err, ErrNoRollback) {
		t.Fatal("无 Down 时不应自动回滚", err)
	}
}

func TestMigrationExclusiveLock(t *testing.T) {
	db := testutil.Database(t)
	ctx := context.Background()
	started, release := make(chan struct{}), make(chan struct{})
	steps := []Migration{step("2026101601", func(context.Context, *mongo.Database) error { close(started); <-release; return nil })}
	done := make(chan error, 1)
	go func() { done <- Run(ctx, db, steps, Options{}) }()
	<-started
	lock, err := CurrentLock(ctx, db)
	if err != nil || lock == nil {
		t.Fatal(lock, err)
	}
	if err := Unlock(ctx, db, "another-owner"); !errors.Is(err, ErrLockLost) {
		t.Fatal("解除了其他执行者的锁", err)
	}
	if err := Run(ctx, db, steps, Options{}); !errors.Is(err, ErrLocked) {
		t.Error("并发迁移未拦截", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if lock, err := CurrentLock(ctx, db); err != nil || lock != nil {
		t.Fatal("锁未释放", lock, err)
	}
}

func TestMigrationPartialFailureAndExplicitResume(t *testing.T) {
	db := testutil.Database(t)
	ctx := context.Background()
	failure := errors.New("backfill interrupted")
	var calls atomic.Int64
	m := step("2026101601", func(ctx context.Context, db *mongo.Database) error {
		calls.Add(1)
		if _, err := db.Collection("users").ReplaceOne(ctx, bson.D{{Key: "_id", Value: 1}}, bson.M{"_id": 1, "version": 1}, options.Replace().SetUpsert(true)); err != nil {
			return err
		}
		if calls.Load() == 1 {
			return failure
		}
		return nil
	})
	if err := Run(ctx, db, []Migration{m}, Options{}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := Run(ctx, db, []Migration{m}, Options{}); !errors.Is(err, ErrIncomplete) {
		t.Fatal("失败后被自动重跑", err)
	}
	if err := Run(ctx, db, []Migration{m}, Options{ResumeFailed: true}); err != nil {
		t.Fatal(err)
	}
	if n, err := db.Collection("users").CountDocuments(ctx, bson.D{}); err != nil || n != 1 {
		t.Fatal("恢复重复写入历史数据", n, err)
	}
	history, err := History(ctx, db)
	if err != nil || history[0].Attempt != 2 || history[0].Status != "applied" {
		t.Fatal(history, err)
	}
}

func TestRollbackFailureCannotResumeAsUp(t *testing.T) {
	db := testutil.Database(t)
	ctx := context.Background()
	calls := 0
	failure := errors.New("rollback interrupted")
	m := step("2026101601", func(context.Context, *mongo.Database) error { return nil })
	m.Down = func(context.Context, *mongo.Database) error {
		calls++
		if calls == 1 {
			return failure
		}
		return nil
	}
	if err := Run(ctx, db, []Migration{m}, Options{}); err != nil {
		t.Fatal(err)
	}
	if err := Rollback(ctx, db, []Migration{m}, m.Version, Options{}); !errors.Is(err, failure) {
		t.Fatal(err)
	}
	if err := Run(ctx, db, []Migration{m}, Options{ResumeFailed: true}); !errors.Is(err, ErrIncomplete) {
		t.Fatal("回滚失败被当成 Up", err)
	}
	if err := Rollback(ctx, db, []Migration{m}, m.Version, Options{ResumeFailed: true}); err != nil {
		t.Fatal(err)
	}
	history, err := History(ctx, db)
	if err != nil || history[0].Status != "rolled_back" || history[0].Attempt != 3 {
		t.Fatal(history, err)
	}
}

func TestBackfillRejectsMixedIDsAndNonIdempotentUpdates(t *testing.T) {
	db := testutil.Database(t)
	ctx := context.Background()
	collection := db.Collection("mixed")
	if _, err := collection.InsertMany(ctx, []any{bson.M{"_id": 1}, bson.M{"_id": "two"}}); err != nil {
		t.Fatal(err)
	}
	filter := bson.D{{Key: "status", Value: bson.D{{Key: "$exists", Value: false}}}}
	if _, err := Backfill(ctx, collection, filter, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: 1}}}}, 1); err == nil {
		t.Fatal("混合主键可能漏回填")
	}
	if _, err := Backfill(ctx, collection, filter, bson.D{{Key: "$inc", Value: bson.D{{Key: "status", Value: 1}}}}, 1); err == nil {
		t.Fatal("允许非幂等回填")
	}
	if n, err := collection.CountDocuments(ctx, filter); err != nil || n != 2 {
		t.Fatal("验证前修改了数据", n, err)
	}
}

func TestMigrationPanicRecordsFailureAndReleasesLock(t *testing.T) {
	db := testutil.Database(t)
	ctx := context.Background()
	m := step("2026101601", func(context.Context, *mongo.Database) error { panic("migration panic") })
	func() {
		defer func() {
			if recover() != "migration panic" {
				t.Error("panic 未传播")
			}
		}()
		_ = Run(ctx, db, []Migration{m}, Options{})
	}()
	history, err := History(ctx, db)
	if err != nil || len(history) != 1 || history[0].Status != "failed" {
		t.Fatal(history, err)
	}
	if lock, err := CurrentLock(ctx, db); err != nil || lock != nil {
		t.Fatal("panic 后锁未释放", lock, err)
	}
}
