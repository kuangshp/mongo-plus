package mongoplus

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"reflect"
	"time"

	"github.com/kuangshp/mongo-plus/sf"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// 以下名称与 gorm-plus 的可插拔缓存保持一致，实现可以是 Redis。
type (
	SFCache                   = sf.SFCache
	SFCachePrefixDeleter      = sf.SFCachePrefixDeleter
	SFCachePrefixBatchDeleter = sf.SFCachePrefixBatchDeleter
	SFCacheCloser             = sf.SFCacheCloser
	RawValue                  = sf.RawValue
	MemoryCache               = sf.MemoryCache
)

// DefaultSFTTL 是 SF 未传 TTL 时的默认缓存时长。
var DefaultSFTTL = sf.DefaultSFTTL

// RegisterCache 注入进程内所有仓储和 SF 查询共用的缓存。重复注册会 panic。
func RegisterCache(cache SFCache) { sf.RegisterCache(cache) }

// ForceReplaceCache 替换已注册缓存，只用于测试隔离。
func ForceReplaceCache(cache SFCache) { sf.ForceReplaceCache(cache) }

// NewMemoryCache 创建支持前缀删除的内存缓存。
func NewMemoryCache() *MemoryCache { return sf.NewMemoryCache() }

// StopSFCache 关闭并清除 RegisterCache 注入的缓存。
func StopSFCache() { sf.StopSFCache() }

// BuildArgs 组装 SF 的查询参数。
func BuildArgs(kv ...any) map[string]any { return sf.BuildArgs(kv...) }

// SF 合并并发并缓存结果。多实例需要先 RegisterCache。
func SF[T any](fn func() (T, error), fnName string, args map[string]any, ttl ...time.Duration) (T, error) {
	return sf.SF(fn, fnName, args, ttl...)
}

// SFWithTTL 使用明确的缓存时长。
func SFWithTTL[T any](fn func() (T, error), fnName string, args map[string]any, ttl time.Duration) (T, error) {
	return sf.SFWithTTL(fn, fnName, args, ttl)
}

// SFNoCache 只合并并发，不保存结果。
func SFNoCache[T any](fn func() (T, error), fnName string, args map[string]any) (T, error) {
	return sf.SFNoCache(fn, fnName, args)
}

// SFInvalidate 删除与参数完全一致的一条缓存。
func SFInvalidate(fnName string, args map[string]any) { sf.SFInvalidate(fnName, args) }

// SFInvalidatePrefix 删除该名称下的全部参数组合。
func SFInvalidatePrefix(fnName string) { sf.SFInvalidatePrefix(fnName) }

// SFInvalidatePrefixes 删除多个查询前缀。
func SFInvalidatePrefixes(fnNames ...string) { sf.SFInvalidatePrefixes(fnNames...) }

// UseCache 只替换当前数据库的仓储读缓存。nil 恢复为 RegisterCache 的全局缓存或本机缓存。
func (d *Database) UseCache(cache SFCache) {
	if d != nil {
		d.cache.mu.Lock()
		defer d.cache.mu.Unlock()
		d.cacheStore = cache
		d.cache.generation++
		d.cache.entries = nil
		d.cache.bytes = 0
	}
}

func activeCacheStore(db *Database) sf.SFCache {
	if db == nil {
		return nil
	}
	db.cache.mu.Lock()
	store := db.cacheStore
	db.cache.mu.Unlock()
	if store != nil {
		return store
	}
	return sf.Explicit()
}

func sharedCachePrefix(db *Database, collection string) string {
	db.cache.mu.Lock()
	namespace := db.cache.limits.Namespace
	if namespace == "" {
		if db.cache.namespace == "" {
			db.cache.namespace = rand.Text()
		}
		namespace = db.cache.namespace
	}
	db.cache.mu.Unlock()
	name := "default"
	if db.native != nil && db.native.Name() != "" {
		name = db.native.Name()
	}
	// 长度和分隔符不会造成命名空间碰撞，v2 也隔离修复前的缓存条目。
	raw, _ := bson.Marshal(bson.D{{Key: "source", Value: namespace}, {Key: "database", Value: name}, {Key: "collection", Value: collection}})
	hash := sha256.Sum256(raw)
	return "mprepo:v2:" + hex.EncodeToString(hash[:]) + ":"
}

func deleteSharedCache(peers []*Database, collection string) {
	for _, peer := range peers {
		store := activeCacheStore(peer)
		if store == nil {
			continue
		}
		prefix := sharedCachePrefix(peer, collection)
		// 先换版本。旧查询即使在删除后写回，也只会写入无人再读取的旧版本。
		// 版本键不设置 TTL；若被后端逐出，读取方会生成新随机版本，不复用旧键。
		store.Set(prefix+"version", sf.RawValue(rand.Text()), 0)
		if batch, ok := store.(sf.SFCachePrefixBatchDeleter); ok {
			batch.DelByPrefixes([]string{prefix + "data:"})
			continue
		}
		if deleter, ok := store.(sf.SFCachePrefixDeleter); ok {
			deleter.DelByPrefix(prefix + "data:")
		}
	}
}

func sharedCacheVersion(store sf.SFCache, prefix string) string {
	decode := func(value any, ok bool) string {
		if !ok {
			return ""
		}
		if raw, ok := value.(sf.RawValue); ok && len(raw) > 0 {
			return string(raw)
		}
		return ""
	}
	key := prefix + "version"
	if version := decode(store.Get(key)); version != "" {
		return version
	}
	store.Set(key, sf.RawValue(rand.Text()), 0)
	// 后端不可用或未保存版本时直接回源，不能退回到可复用的空版本。
	return decode(store.Get(key))
}

func readSharedCache[V any](ctx context.Context, local *readCache, store sf.SFCache, db *Database, scope, method, hash string, epoch, generation uint64, o QueryOption, load func() (V, error)) (V, error) {
	prefix := sharedCachePrefix(db, scope)
	version := sharedCacheVersion(store, prefix)
	if version == "" {
		return load()
	}
	typ := reflect.TypeFor[V]()
	base := typ
	for base.Kind() == reflect.Pointer || base.Kind() == reflect.Slice || base.Kind() == reflect.Array || base.Kind() == reflect.Map {
		base = base.Elem()
	}
	typeHash := sha256.Sum256([]byte(base.PkgPath() + ":" + typ.String()))
	key := prefix + "data:" + version + ":" + hex.EncodeToString(typeHash[:]) + ":" + method + ":" + hash
	if value, ok := sharedValue[V](store.Get(key)); ok {
		if copied, _, copiedOK := copyCacheValue(value); copiedOK {
			return copied, nil
		}
	}
	owner := new(byte)
	flightKey := fmt.Sprintf("%s/%d/%d", key, generation, epoch)
	result := local.group.DoChan(flightKey, func() (any, error) {
		value, err := load()
		if err != nil {
			return nil, err
		}
		copied, bytes, ok := copyCacheValue(value)
		if !ok {
			return sharedFlight[V]{value: value, owner: owner}, nil
		}
		if raw, err := bson.Marshal(bson.D{{Key: "v", Value: copied}}); err == nil {
			local.mu.Lock()
			limits := local.limits.defaults()
			if !local.disabled && !limits.Disabled && local.generation == generation && local.epochs[scope] == epoch && bytes <= limits.MaxEntryBytes && bytes <= limits.MaxBytes {
				store.Set(key, sf.RawValue(raw), o.cacheTTL)
			}
			local.mu.Unlock()
		}
		return sharedFlight[V]{value: copied, shared: true}, nil
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
		flight := result.Val.(sharedFlight[V])
		if flight.shared {
			if copied, _, ok := copyCacheValue(flight.value); ok {
				return copied, nil
			}
		} else if flight.owner == owner {
			return flight.value, nil
		}
		return load()
	}
}

type sharedFlight[V any] struct {
	value  V
	owner  *byte
	shared bool
}

func sharedValue[V any](value any, ok bool) (V, bool) {
	if !ok {
		var zero V
		return zero, false
	}
	raw, isRaw := value.(sf.RawValue)
	if !isRaw {
		typed, isTyped := value.(V)
		return typed, isTyped
	}
	var box struct {
		V V `bson:"v"`
	}
	if err := bson.Unmarshal(raw, &box); err != nil {
		var zero V
		return zero, false
	}
	return box.V, true
}
