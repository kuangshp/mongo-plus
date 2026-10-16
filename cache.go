package mongoplus

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"golang.org/x/sync/singleflight"
)

type cacheEntry struct {
	value   any
	bytes   int64
	expires time.Time
	scope   string
}

type cacheFlight[V any] struct {
	value     *V
	owner     *byte // 无法安全复制时，仅发起者可以使用原始结果。
	cloneable bool
}
type readCache struct {
	mu              sync.Mutex
	entries         map[string]cacheEntry
	epochs          map[string]uint64
	group           singleflight.Group
	disabled        bool
	bytes           int64
	limits          CacheConfig
	types           map[reflect.Type]uint64
	generation      uint64 // ConfigureCache 后禁止旧在途查询重新填充缓存。
	namespace       string // 未显式指定 Namespace 时使用不可复用的随机标识。
	pending         map[string]uint64
	pendingSequence uint64
	retrying        bool
	retryStop       chan struct{}
	retryDone       chan struct{}
	closed          bool
}

// InvalidateCache 清除同 Client、同数据库的所有包装器中指定集合的读缓存。
// 原生 Collection 写入后应调用；不同 Client 或进程之间不广播失效。
func (d *Database) InvalidateCache(collection string) {
	d.invalidatePeers(collection, false)
}

type txCacheKey struct{}
type txCaches map[*Database]map[string]bool

func (r *Repository[T, K]) invalidate(ctx context.Context) {
	if state, ok := ctx.Value(txCacheKey{}).(txCaches); ok {
		if state[r.db] == nil {
			state[r.db] = map[string]bool{}
		}
		state[r.db][r.config.Name] = true
		return
	}
	if mongo.SessionFromContext(ctx) != nil {
		// 外部 Session 无法获知提交时机，同 Client 同库的包装器都必须禁用。
		r.db.invalidatePeers(r.config.Name, true)
		return
	}
	r.db.InvalidateCache(r.config.Name)
}

func readCached[V any](ctx context.Context, db *Database, scope, method string, key bson.D, o QueryOption, load func() (V, error)) (V, error) {
	if (o.cacheTTL <= 0 && !o.singleFlight) || mongo.SessionFromContext(ctx) != nil {
		return load()
	}
	c := &db.cache
	c.mu.Lock()
	epoch := c.epochs[scope]
	generation := c.generation
	disabled := c.disabled || c.limits.Disabled
	if c.types == nil {
		c.types = map[reflect.Type]uint64{}
	}
	typ := reflect.TypeOf((*V)(nil))
	typeID, ok := c.types[typ]
	if !ok {
		typeID = uint64(len(c.types) + 1)
		c.types[typ] = typeID
	}
	c.mu.Unlock()
	if disabled {
		return load()
	}
	encoded, err := bson.Marshal(key)
	if err != nil {
		return load() // 缓存不能改变原生驱动支持的参数类型。
	}
	hash := sha256.Sum256(encoded)
	if store := activeCacheStore(db); store != nil && o.cacheTTL > 0 {
		if !db.sharedCacheReady(ctx, store, scope) {
			return load()
		}
		return readSharedCache(ctx, c, store, db, scope, method, hex.EncodeToString(hash[:]), epoch, generation, o, load)
	}
	cacheKey := fmt.Sprintf("%s/%d/%d/%s/%d/%s", scope, generation, epoch, method, typeID, hex.EncodeToString(hash[:]))
	decode := func(value any) (V, bool) {
		typed, ok := value.(*V)
		if !ok {
			var zero V
			return zero, false
		}
		copy, _, ok := copyCacheValue(*typed)
		return copy, ok
	}
	if o.cacheTTL > 0 {
		c.mu.Lock()
		entry, ok := c.entries[cacheKey]
		if ok && !time.Now().Before(entry.expires) {
			c.remove(cacheKey)
			ok = false
		}
		if c.disabled || c.limits.Disabled {
			ok = false
		}
		c.mu.Unlock()
		if ok {
			if value, ok := decode(entry.value); ok {
				return value, nil
			}
		}
	}
	// singleflight 只共享内部快照，每个调用者都取得独立副本。
	owner := new(byte)
	result := c.group.DoChan(cacheKey, func() (any, error) {
		value, err := load()
		if err != nil {
			return nil, err
		}
		snapshot, bytes, ok := copyCacheValue(value)
		if !ok {
			return cacheFlight[V]{value: &value, owner: owner}, nil
		}
		if o.cacheTTL > 0 {
			c.mu.Lock()
			defer c.mu.Unlock()
			limits := c.limits.defaults()
			if !c.disabled && !limits.Disabled && c.generation == generation && c.epochs[scope] == epoch && bytes <= limits.MaxEntryBytes && bytes <= limits.MaxBytes {
				if c.entries == nil {
					c.entries = map[string]cacheEntry{}
				}
				for key, entry := range c.entries {
					if time.Now().After(entry.expires) {
						c.remove(key)
					}
				}
				c.remove(cacheKey)
				for len(c.entries) >= limits.MaxEntries || c.bytes+bytes > limits.MaxBytes {
					for key := range c.entries {
						c.remove(key)
						break
					}
				}
				c.entries[cacheKey] = cacheEntry{value: &snapshot, bytes: bytes, expires: time.Now().Add(o.cacheTTL), scope: scope}
				c.bytes += bytes
			}
		}
		return cacheFlight[V]{value: &snapshot, cloneable: true}, nil
	})
	select {
	case <-ctx.Done():
		var zero V
		return zero, ctx.Err()
	case result := <-result:
		if result.Err != nil {
			var zero V
			return zero, result.Err
		}
		flight := result.Val.(cacheFlight[V])
		if flight.cloneable {
			if value, ok := decode(flight.value); ok {
				return value, nil
			}
		} else if flight.owner == owner {
			return *flight.value, nil
		}
		return load()
	}
}

func (r *Repository[T, K]) readKey(ctx context.Context, opts []QueryOption) (QueryOption, bson.D, error) {
	o := mergeOptions(opts)
	filter, err := r.filter(ctx, o, false)
	return o, bson.D{{Key: "filter", Value: filter}, {Key: "sort", Value: o.sort}, {Key: "projection", Value: o.projection}, {Key: "limit", Value: o.limit}, {Key: "offset", Value: o.skip}}, err
}

// FindOne 获取单条记录，可通过 WithCache 或 WithSingleFlight 显式启用本机读优化。
func (r *Repository[T, K]) FindOne(ctx context.Context, opts ...QueryOption) (*T, error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	o, key, err := r.readKey(ctx, opts)
	if err != nil {
		return nil, err
	}
	ctx = cachedCommandContext(ctx, o)
	entity, err := readCached(ctx, r.db, r.config.Name, "FindOne", key, o, func() (*T, error) { return r.findOne(ctx, opts...) })
	if err != nil {
		return nil, err
	}
	// 缓存只保存数据库读出的原始值；每个请求在自己的副本上决定解密或脱敏。
	if err = r.reveal(ctx, entity); err != nil {
		return nil, err
	}
	return entity, nil
}

// FindList 获取列表；默认直接读取数据库。
func (r *Repository[T, K]) FindList(ctx context.Context, opts ...QueryOption) ([]*T, error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	o, key, err := r.readKey(ctx, opts)
	if err != nil {
		return nil, err
	}
	ctx = cachedCommandContext(ctx, o)
	entities, err := readCached(ctx, r.db, r.config.Name, "FindList", key, o, func() ([]*T, error) { return r.findList(ctx, opts...) })
	if err != nil {
		return nil, err
	}
	if err = r.reveal(ctx, entities); err != nil {
		return nil, err
	}
	return entities, nil
}

// Count 统计记录；读缓存默认关闭。
func (r *Repository[T, K]) Count(ctx context.Context, opts ...QueryOption) (int64, error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	o, key, err := r.readKey(ctx, opts)
	if err != nil {
		return 0, err
	}
	ctx = cachedCommandContext(ctx, o)
	return readCached(ctx, r.db, r.config.Name, "Count", key, o, func() (int64, error) { return r.count(ctx, opts...) })
}

// Exists 判断存在性；默认直接读取数据库。
func (r *Repository[T, K]) Exists(ctx context.Context, opts ...QueryOption) (bool, error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return false, err
	}
	defer cleanup()
	o, key, err := r.readKey(ctx, opts)
	if err != nil {
		return false, err
	}
	ctx = cachedCommandContext(ctx, o)
	return readCached(ctx, r.db, r.config.Name, "Exists", key, o, func() (bool, error) { return r.exists(ctx, opts...) })
}
