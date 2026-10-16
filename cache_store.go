package mongoplus

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
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
	MemoryCacheConfig         = sf.MemoryCacheConfig
)

// DefaultSFTTL 是 SF 未传 TTL 时的默认缓存时长。
var DefaultSFTTL = sf.DefaultSFTTL

// ErrInvalidCacheArgs 表示 SF 参数不能序列化，此时不访问缓存或执行加载函数。
var ErrInvalidCacheArgs = sf.ErrInvalidCacheArgs

// RegisterCache 注入进程内所有仓储和 SF 查询共用的缓存。重复注册会 panic。
func RegisterCache(cache SFCache) { sf.RegisterCache(cache) }

// ForceReplaceCache 替换已注册缓存，只用于测试隔离。
func ForceReplaceCache(cache SFCache) { sf.ForceReplaceCache(cache) }

// NewMemoryCache 创建最多 1024 条、支持 LRU 和主动过期清理的公共内存缓存。
func NewMemoryCache() *MemoryCache { return sf.NewMemoryCache() }

// NewMemoryCacheWithConfig 配置公共 SF 的条目上限与主动清理间隔。
func NewMemoryCacheWithConfig(cfg MemoryCacheConfig) (*MemoryCache, error) {
	return sf.NewMemoryCacheWithConfig(cfg)
}

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

// SFInvalidateWithError 与 SFInvalidate 相同，但返回无效参数错误。
func SFInvalidateWithError(fnName string, args map[string]any) error {
	return sf.SFInvalidateWithError(fnName, args)
}

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

func cacheNamespace(db *Database) string {
	db.cache.mu.Lock()
	defer db.cache.mu.Unlock()
	if db.cache.limits.Namespace != "" {
		return db.cache.limits.Namespace
	}
	if db.cache.namespace == "" {
		db.cache.namespace = rand.Text()
	}
	return db.cache.namespace
}

func sharedCachePrefix(db *Database, collection string) string {
	namespace := cacheNamespace(db)
	name := "default"
	if db.native != nil && db.native.Name() != "" {
		name = db.native.Name()
	}
	// v4 隔离未做值保真校验的旧条目；长度和分隔符不会造成命名空间碰撞。
	raw, _ := bson.Marshal(bson.D{{Key: "source", Value: namespace}, {Key: "database", Value: name}, {Key: "collection", Value: collection}})
	hash := sha256.Sum256(raw)
	return "mprepo:v4:" + hex.EncodeToString(hash[:]) + ":"
}

func deleteSharedCache(ctx context.Context, peers []*Database, collection string) error {
	var failures []error
	for _, peer := range peers {
		store := activeCacheStore(peer)
		if store == nil {
			continue
		}
		// 先换版本。旧查询即使在删除后写回，也只会写入无人再读取的旧版本。
		// 版本键不设置 TTL；若被后端逐出，读取方会生成新随机版本，不复用旧键。
		if err := publishCacheInvalidation(ctx, peer, store, collection); err != nil {
			peer.queueCacheInvalidation(collection)
			peer.cacheEvent(ctx, "invalidate", collection, err)
			failures = append(failures, err)
			continue
		}
		cleanupCachePrefix(ctx, peer, store, collection)
	}
	return errors.Join(failures...)
}

func sharedCacheVersion(ctx context.Context, db *Database, store sf.SFCache, collection, prefix string) string {
	decode := func(value any, ok bool) string {
		if !ok {
			return ""
		}
		if raw, ok := value.(sf.RawValue); ok && len(raw) > 0 {
			return string(raw)
		}
		return ""
	}
	if db.native != nil {
		epoch, err := db.currentCacheEpoch(ctx, collection)
		if err != nil || epoch == "" {
			if err != nil {
				db.cacheEvent(ctx, "epoch", collection, err)
			}
			return ""
		}
		return epoch
	}
	key := prefix + "version"
	value, found, err := cacheGet(ctx, db, store, collection, key)
	if err != nil {
		return ""
	}
	if version := decode(value, found); version != "" {
		return version
	}
	if err := cacheSet(ctx, db, store, collection, key, sf.RawValue(rand.Text()), 0); err != nil {
		return ""
	}
	// 后端不可用或未保存版本时直接回源，不能退回到可复用的空版本。
	value, found, err = cacheGet(ctx, db, store, collection, key)
	if err != nil {
		return ""
	}
	return decode(value, found)
}

func readSharedCache[V any](ctx context.Context, local *readCache, store sf.SFCache, db *Database, scope, method, hash string, epoch, generation uint64, o QueryOption, load func() (V, error)) (V, error) {
	// 已解码的对象不能保证再次 BSON 编解码仍保持值不变。
	// 自定义 BSON 方法和动态 interface 容器直接回源，既不读旧缓存也不合并请求。
	if !sharedBSONSafe(reflect.TypeFor[V](), map[reflect.Type]bool{}) {
		return load()
	}
	prefix := sharedCachePrefix(db, scope)
	version := sharedCacheVersion(ctx, db, store, scope, prefix)
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
	stored, found, cacheErr := cacheGet(ctx, db, store, scope, key)
	if cacheErr != nil {
		return load()
	}
	if value, ok := sharedValue[V](stored, found); ok {
		if copied, _, copiedOK := copyCacheValue(value); copiedOK {
			db.cacheEvent(ctx, "hit", scope, nil)
			return copied, nil
		}
	}
	db.cacheEvent(ctx, "miss", scope, nil)
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
		if raw, stable := encodeSharedValue(copied); stable {
			local.mu.Lock()
			limits := local.limits.defaults()
			eligible := !local.disabled && !limits.Disabled && local.generation == generation && local.epochs[scope] == epoch && bytes <= limits.MaxEntryBytes && bytes <= limits.MaxBytes
			local.mu.Unlock()
			if eligible {
				_ = cacheSet(ctx, db, store, scope, key, sf.RawValue(raw), o.cacheTTL)
			}
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

// 仅静态排除自定义解码器不足以覆盖 omitempty、inline 指针、时区及私有状态。
// 每次保存前验证 BSON 往返保持完整 Go 值；不能无损保存时只共享 Go 副本，不写共享后端。
func encodeSharedValue[V any](value V) (sf.RawValue, bool) {
	raw, err := bson.Marshal(bson.D{{Key: "v", Value: value}})
	if err != nil {
		return nil, false
	}
	decoded, ok := sharedValue[V](sf.RawValue(raw), true)
	if !ok || !reflect.DeepEqual(value, decoded) {
		return nil, false
	}
	return sf.RawValue(raw), true
}

// sharedBSONSafe 只允许可静态证明不含自定义 BSON 方法的类型使用共享 BSON 缓存。
// interface 的运行时类型无法在读取缓存前验证，保守绕过；本地 Go 值快照缓存不受影响。
func sharedBSONSafe(typ reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[typ] {
		return true
	}
	seen[typ] = true
	for _, contract := range []reflect.Type{
		reflect.TypeFor[bson.Marshaler](), reflect.TypeFor[bson.Unmarshaler](),
		reflect.TypeFor[bson.ValueMarshaler](), reflect.TypeFor[bson.ValueUnmarshaler](),
	} {
		if typ.Implements(contract) || reflect.PointerTo(typ).Implements(contract) {
			return false
		}
	}
	switch typ.Kind() {
	case reflect.Interface:
		return false
	case reflect.Pointer, reflect.Slice, reflect.Array:
		return sharedBSONSafe(typ.Elem(), seen)
	case reflect.Map:
		return sharedBSONSafe(typ.Key(), seen) && sharedBSONSafe(typ.Elem(), seen)
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if f.IsExported() && f.Tag.Get("bson") != "-" && !sharedBSONSafe(f.Type, seen) {
				return false
			}
		}
	}
	return true
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
