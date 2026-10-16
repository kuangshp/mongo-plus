package mongoplus

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type failingSharedCache struct {
	*MemoryCache
	failGet atomic.Bool
	failSet atomic.Bool
}

var errCacheTest = errors.New("cache temporarily unavailable")

func (c *failingSharedCache) GetWithError(ctx context.Context, key string) (any, bool, error) {
	if c.failGet.Load() {
		return nil, false, errCacheTest
	}
	return c.MemoryCache.GetWithError(ctx, key)
}
func (c *failingSharedCache) SetWithError(ctx context.Context, key string, value any, ttl time.Duration) error {
	if c.failSet.Load() {
		return errCacheTest
	}
	return c.MemoryCache.SetWithError(ctx, key, value, ttl)
}

func cacheErrorDatabase(t *testing.T, store SFCache, interval time.Duration) *Database {
	t.Helper()
	db := &Database{}
	db.UseCache(store)
	if err := db.ConfigureCache(CacheConfig{Namespace: "error-test", RequireErrorReporting: true, InvalidationRetryInterval: interval}); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := db.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return db
}

func TestSharedCacheErrorsBypassAndRecovery(t *testing.T) {
	ctx := context.Background()
	store := &failingSharedCache{MemoryCache: NewMemoryCache()}
	writer, reader := cacheErrorDatabase(t, store, time.Hour), cacheErrorDatabase(t, store, time.Hour)
	events := make(chan CacheEvent, 100)
	writer.CacheObserver = func(_ context.Context, e CacheEvent) { events <- e }
	opt := Query().WithCache(time.Minute).Build()
	read := func(db *Database, want string) string {
		t.Helper()
		v, err := readCached(ctx, db, "users", "one", bson.D{}, opt, func() (string, error) { return want, nil })
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	if got := read(reader, "old"); got != "old" {
		t.Fatal(got)
	}
	store.failSet.Store(true)
	if err := writer.InvalidateCacheContext(ctx, "users"); !errors.Is(err, errCacheTest) {
		t.Fatalf("失效错误被吞掉: %v", err)
	}
	if len(writer.PendingCacheInvalidations()) != 1 {
		t.Fatal("失效失败未登记恢复")
	}
	if got := read(writer, "fresh"); got != "fresh" {
		t.Fatalf("故障时读到残留缓存: %s", got)
	}
	store.failSet.Store(false)
	if err := writer.RetryCacheInvalidations(ctx); err != nil {
		t.Fatal(err)
	}
	if len(writer.PendingCacheInvalidations()) != 0 {
		t.Fatal("恢复后队列未清理")
	}
	if got := read(reader, "fresh"); got != "fresh" {
		t.Fatalf("恢复后跨实例仍读取旧版本: %s", got)
	}
	store.failGet.Store(true)
	if got := read(writer, "direct"); got != "direct" {
		t.Fatal("读故障没有回源")
	}
	operations := map[string]bool{}
	for len(events) > 0 {
		e := <-events
		operations[e.Operation] = true
	}
	for _, op := range []string{"get", "set", "invalidate", "recovered"} {
		if !operations[op] {
			t.Errorf("缺少缓存事件 %s", op)
		}
	}
}

func TestSharedCacheAutomaticInvalidationRetry(t *testing.T) {
	store := &failingSharedCache{MemoryCache: NewMemoryCache()}
	db := cacheErrorDatabase(t, store, time.Millisecond)
	store.failSet.Store(true)
	db.InvalidateCache("users")
	store.failSet.Store(false)
	deadline := time.After(2 * time.Second)
	for len(db.PendingCacheInvalidations()) > 0 {
		select {
		case <-deadline:
			t.Fatal("未自动恢复失效")
		case <-time.After(time.Millisecond):
		}
	}
}

func TestSharedCacheRequiresErrorReportingAndCanBeDisabled(t *testing.T) {
	db := cacheErrorDatabase(t, basicSharedCache{NewMemoryCache()}, time.Hour)
	loads := 0
	ctx := context.Background()
	opt := Query().WithCache(time.Minute).WithSingleFlight().Build()
	for i := 0; i < 2; i++ {
		_, err := readCached(ctx, db, "users", "one", bson.D{}, opt, func() (int, error) { loads++; return loads, nil })
		if err != nil {
			t.Fatal(err)
		}
	}
	if loads != 2 {
		t.Fatal("严格模式使用了无法报告错误的后端")
	}
	db.UseCache(NewMemoryCache())
	if err := db.ConfigureCache(CacheConfig{Disabled: true}); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		_, _ = readCached(ctx, db, "users", "one", bson.D{}, opt, func() (int, error) { loads++; return loads, nil })
	}
	if loads != 4 {
		t.Fatal("Disabled 未强制直读")
	}
}
