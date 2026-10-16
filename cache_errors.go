package mongoplus

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"sort"
	"strconv"
	"time"

	"github.com/kuangshp/mongo-plus/sf"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
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
// Operation 为 get、set、epoch、hit、miss、invalidate、cleanup 或 recovered；正常事件 Err 为 nil。
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

// PendingCacheInvalidations 返回当前进程尚未完成的共享缓存发布，便于健康检查和告警。
// 已连接数据库时，读缓存使用 MongoDB 中的版本号；其他实例在版本号写入成功后不会再命中旧数据。
// 版本号写入失败时，本进程会绕过缓存并重试。进程在写入成功前退出，其他实例仍可能看到旧缓存。
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

const cacheEpochCollection = "__mongo_plus_cache_epochs"

func cacheEpochID(db *Database, collection string) string {
	return cacheNamespace(db) + "/" + collection
}

func cacheEpochColl(db *Database) *mongo.Collection {
	return db.native.Collection(cacheEpochCollection, options.Collection().
		SetWriteConcern(writeconcern.Majority()).
		SetReadPreference(readpref.Primary()))
}

// bumpCacheEpoch 把集合版本写入所有实例共享的数据库。共享缓存发布失败时，其他实例仍按这个版本错过旧条目。
func (d *Database) bumpCacheEpoch(ctx context.Context, collection string) (string, error) {
	if d == nil || d.native == nil {
		return "", nil
	}
	var doc struct {
		Epoch int64 `bson:"epoch"`
	}
	err := cacheEpochColl(d).FindOneAndUpdate(ctx,
		bson.D{{Key: "_id", Value: cacheEpochID(d, collection)}},
		bson.D{{Key: "$inc", Value: bson.D{{Key: "epoch", Value: int64(1)}}}},
		options.FindOneAndUpdate().SetUpsert(true).SetReturnDocument(options.After),
	).Decode(&doc)
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(doc.Epoch, 10), nil
}

func (d *Database) currentCacheEpoch(ctx context.Context, collection string) (string, error) {
	if d == nil || d.native == nil {
		return "", nil
	}
	var doc struct {
		Epoch int64 `bson:"epoch"`
	}
	err := cacheEpochColl(d).FindOne(ctx, bson.D{{Key: "_id", Value: cacheEpochID(d, collection)}}).Decode(&doc)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return "0", nil
	}
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(doc.Epoch, 10), nil
}

func publishCacheInvalidation(ctx context.Context, db *Database, store SFCache, collection string) error {
	db.cache.mu.Lock()
	require := db.cache.limits.RequireErrorReporting
	db.cache.mu.Unlock()
	if _, ok := store.(SFCacheWithErrors); require && !ok {
		return ErrCacheErrorReportingRequired
	}
	version := rand.Text()
	if db.native != nil {
		epoch, err := db.bumpCacheEpoch(ctx, collection)
		if err != nil {
			return err
		}
		version = epoch
	}
	return cacheSet(ctx, db, store, collection, sharedCachePrefix(db, collection)+"version", sf.RawValue(version), 0)
}
