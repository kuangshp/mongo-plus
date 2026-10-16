package mongoplus

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/kuangshp/mongo-plus/sf"
)

// SFCacheWithErrors 是 SFCache 的可选增强能力，不破坏现有缓存适配器。
// 用于生产共享缓存的适配器应同时实现两个接口，保留 RawValue 及 ttl=0 语义。
type SFCacheWithErrors interface {
	GetWithError(context.Context, string) (any, bool, error)
	SetWithError(context.Context, string, any, time.Duration) error
	DelWithError(context.Context, string) error
}

// SFCachePrefixDeleterWithErrors 是可选的旧版本空间回收能力。
// 正确性依赖版本切换；前缀回收失败只上报 cleanup 错误，旧值按 TTL 过期。
type SFCachePrefixDeleterWithErrors interface {
	DelByPrefixesWithError(context.Context, []string) error
}

var ErrCacheErrorReportingRequired = errors.New("共享缓存后端需要实现 SFCacheWithErrors")

// CacheEvent 不携带缓存键、文档或参数，避免监控再次泄露敏感值。
// Operation 为 get、set、invalidate、cleanup 或 recovered；recovered 的 Err 为 nil。
type CacheEvent struct {
	Operation  string
	Collection string
	Err        error
}

func (d *Database) cacheEvent(ctx context.Context, op, collection string, err error) {
	if d.CacheObserver != nil {
		d.CacheObserver(ctx, CacheEvent{Operation: op, Collection: collection, Err: err})
	}
}

func cacheGet(ctx context.Context, db *Database, store SFCache, collection, key string) (any, bool, error) {
	if backend, ok := store.(SFCacheWithErrors); ok {
		value, found, err := backend.GetWithError(ctx, key)
		if err != nil {
			db.cacheEvent(ctx, "get", collection, err)
		}
		return value, found, err
	}
	value, found := store.Get(key)
	return value, found, nil
}

func cacheSet(ctx context.Context, db *Database, store SFCache, collection, key string, value any, ttl time.Duration) error {
	if backend, ok := store.(SFCacheWithErrors); ok {
		err := backend.SetWithError(ctx, key, value, ttl)
		if err != nil {
			db.cacheEvent(ctx, "set", collection, err)
		}
		return err
	}
	store.Set(key, value, ttl)
	return nil
}

func (d *Database) sharedCacheReady(ctx context.Context, store SFCache, collection string) bool {
	d.cache.mu.Lock()
	require := d.cache.limits.RequireErrorReporting
	pending := len(d.cache.pending) > 0
	d.cache.mu.Unlock()
	if _, ok := store.(SFCacheWithErrors); require && !ok {
		d.cacheEvent(ctx, "get", collection, ErrCacheErrorReportingRequired)
		return false
	}
	// 故障后先刷新所有待失效版本；未恢复前不读取可能残留的共享数据。
	return !pending || d.RetryCacheInvalidations(ctx) == nil
}

// PendingCacheInvalidations 返回当前进程待恢复的集合，便于健康检查和告警。
// 队列只在内存中；进程退出后不能替代持久化消息或跨实例一致性协议。
func (d *Database) PendingCacheInvalidations() []string {
	d.cache.mu.Lock()
	defer d.cache.mu.Unlock()
	names := make([]string, 0, len(d.cache.pending))
	for name := range d.cache.pending {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func (d *Database) queueCacheInvalidation(collection string) {
	d.cache.mu.Lock()
	defer d.cache.mu.Unlock()
	if d.cache.pending == nil {
		d.cache.pending = map[string]uint64{}
	}
	d.cache.pendingSequence++
	d.cache.pending[collection] = d.cache.pendingSequence
	if d.cache.retrying || d.cache.closed {
		return
	}
	if d.cache.retryStop == nil {
		d.cache.retryStop = make(chan struct{})
	}
	d.cache.retrying = true
	d.cache.retryDone = make(chan struct{})
	go d.retryCacheInvalidations(d.cache.retryStop, d.cache.retryDone)
}

func (d *Database) retryCacheInvalidations(stop <-chan struct{}, done chan<- struct{}) {
	d.cache.mu.Lock()
	delay := d.cache.limits.InvalidationRetryInterval
	d.cache.mu.Unlock()
	if delay == 0 {
		delay = time.Second
	}
	delay = min(delay, 30*time.Second)
	for {
		timer := time.NewTimer(delay)
		select {
		case <-stop:
			timer.Stop()
			d.cache.mu.Lock()
			d.cache.retrying = false
			close(done)
			d.cache.mu.Unlock()
			return
		case <-timer.C:
		}
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		err := d.RetryCacheInvalidations(ctx)
		cancel()
		d.cache.mu.Lock()
		if len(d.cache.pending) == 0 {
			d.cache.retrying = false
			close(done)
			d.cache.mu.Unlock()
			return
		}
		d.cache.mu.Unlock()
		if err != nil {
			delay = min(delay*2, 30*time.Second)
		}
	}
}

// RetryCacheInvalidations 手动恢复失败的版本发布，可在缓存重连或健康检查时调用。
// 成功后新查询使用新版本；不能保证其他实例在故障恢复窗口内立刻同步。
func (d *Database) RetryCacheInvalidations(ctx context.Context) error {
	d.cache.mu.Lock()
	pending := make(map[string]uint64, len(d.cache.pending))
	for key, generation := range d.cache.pending {
		pending[key] = generation
	}
	d.cache.mu.Unlock()
	store := activeCacheStore(d)
	if store == nil && len(pending) > 0 {
		return fmt.Errorf("共享缓存后端不可用")
	}
	var failures []error
	for collection, generation := range pending {
		if err := publishCacheInvalidation(ctx, d, store, collection); err != nil {
			failures = append(failures, err)
			continue
		}
		cleanupCachePrefix(ctx, d, store, collection)
		d.cache.mu.Lock()
		recovered := d.cache.pending[collection] == generation
		if recovered {
			delete(d.cache.pending, collection)
		}
		d.cache.mu.Unlock()
		if recovered {
			d.cacheEvent(ctx, "recovered", collection, nil)
		}
	}
	return errors.Join(failures...)
}

func cleanupCachePrefix(ctx context.Context, db *Database, store SFCache, collection string) {
	prefix := sharedCachePrefix(db, collection) + "data:"
	if cleaner, ok := store.(SFCachePrefixDeleterWithErrors); ok {
		if err := cleaner.DelByPrefixesWithError(ctx, []string{prefix}); err != nil {
			db.cacheEvent(ctx, "cleanup", collection, err)
		}
		return
	}
	// 错误感知的后端不回退到无 Context 的长时间 SCAN；回收能力可单独实现。
	if _, modern := store.(SFCacheWithErrors); modern {
		return
	}
	if cleaner, ok := store.(sf.SFCachePrefixBatchDeleter); ok {
		cleaner.DelByPrefixes([]string{prefix})
		return
	}
	if cleaner, ok := store.(sf.SFCachePrefixDeleter); ok {
		cleaner.DelByPrefix(prefix)
	}
}

func publishCacheInvalidation(ctx context.Context, db *Database, store SFCache, collection string) error {
	db.cache.mu.Lock()
	require := db.cache.limits.RequireErrorReporting
	db.cache.mu.Unlock()
	if _, ok := store.(SFCacheWithErrors); require && !ok {
		return ErrCacheErrorReportingRequired
	}
	return cacheSet(ctx, db, store, collection, sharedCachePrefix(db, collection)+"version", sf.RawValue(rand.Text()), 0)
}
