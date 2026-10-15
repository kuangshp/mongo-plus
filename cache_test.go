package mongoplus

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCacheDoesNotResurrectInvalidatedRead(t *testing.T) {
	db := &Database{}
	ctx := context.Background()
	option := Query().WithCache(time.Minute).Build()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := readCached(ctx, db, "users", "one", bson.D{}, option, func() (string, error) { close(started); <-release; return "old", nil })
		done <- err
	}()
	<-started
	db.InvalidateCache("users")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	value, err := readCached(ctx, db, "users", "one", bson.D{}, option, func() (string, error) { return "new", nil })
	if err != nil || value != "new" {
		t.Fatalf("失效后旧数据重新进入缓存 %s %v", value, err)
	}
}

func TestCachedValuesAreIndependent(t *testing.T) {
	type entity struct {
		Name string `bson:"name"`
	}
	db := &Database{}
	ctx := context.Background()
	option := Query().WithCache(time.Minute).Build()
	var calls atomic.Int32
	load := func() (*entity, error) { calls.Add(1); return &entity{"original"}, nil }
	first, err := readCached(ctx, db, "users", "one", bson.D{}, option, load)
	if err != nil {
		t.Fatal(err)
	}
	first.Name = "changed"
	second, err := readCached(ctx, db, "users", "one", bson.D{}, option, load)
	if err != nil || second.Name != "original" || calls.Load() != 1 {
		t.Fatalf("缓存实体被调用方修改 %+v %v", second, err)
	}
}

func TestSingleFlightWaiterCancellation(t *testing.T) {
	db := &Database{}
	option := Query().WithSingleFlight().Build()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	var calls atomic.Int32
	load := func() (int64, error) { calls.Add(1); close(started); <-release; return 1, nil }
	go func() {
		_, err := readCached(context.Background(), db, "users", "one", bson.D{}, option, load)
		done <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readCached(ctx, db, "users", "one", bson.D{}, option, load); !errors.Is(err, context.Canceled) {
		t.Fatalf("等待者不能取消: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("相同查询未合并: %d", calls.Load())
	}
}
