// Package sf 提供可替换的 SingleFlight 缓存。默认使用本机内存；
// 多实例部署实现 SFCache 后通过 RegisterCache 注入 Redis 等共享缓存。
package sf

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"sync"
	"time"

	"golang.org/x/sync/singleflight"
)

// DefaultSFTTL 是 SF 未指定 TTL 时的缓存时长。
const DefaultSFTTL = 5 * time.Minute

// SFCache 是可替换缓存。Get 在缺失或过期时返回 (nil, false)。
// Set 的 ttl 为 0 时不过期；RawValue 必须逐字节保存并以 RawValue 读出。
type SFCache interface {
	Get(key string) (any, bool)
	Set(key string, val any, ttl time.Duration)
	Del(key string)
}

// SFCachePrefixDeleter 让 SFInvalidatePrefix 一次删除同一方法或集合下的全部缓存。
// 未实现时前缀失效不产生效果。
type SFCachePrefixDeleter interface {
	DelByPrefix(prefix string)
}

// SFCachePrefixBatchDeleter 用一次调用删除多个前缀，避免共享缓存对每个前缀单独扫描。
type SFCachePrefixBatchDeleter interface {
	DelByPrefixes(prefixes []string)
}

// SFCacheCloser 在 StopSFCache 时释放连接。
type SFCacheCloser interface {
	Close() error
}

// RawValue 表示缓存中已经是序列化字节。
// 共享缓存的 Get 应返回 RawValue，SF 会按 JSON 还原；仓储读缓存则按 BSON 还原。
type RawValue []byte

type memoryItem struct {
	val any
	exp time.Time
}

// MemoryCache 是进程内缓存，已支持前缀删除。
type MemoryCache struct {
	mu    sync.Mutex
	items map[string]memoryItem
}

// NewMemoryCache 创建空的内存缓存。
func NewMemoryCache() *MemoryCache {
	return &MemoryCache{items: map[string]memoryItem{}}
}

func (c *MemoryCache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	item, ok := c.items[key]
	if !ok {
		return nil, false
	}
	if !item.exp.IsZero() && !time.Now().Before(item.exp) {
		delete(c.items, key)
		return nil, false
	}
	return item.val, true
}

func (c *MemoryCache) Set(key string, val any, ttl time.Duration) {
	if ttl < 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	item := memoryItem{val: val}
	if ttl > 0 {
		item.exp = time.Now().Add(ttl)
	}
	c.items[key] = item
}

func (c *MemoryCache) Del(key string) {
	c.mu.Lock()
	delete(c.items, key)
	c.mu.Unlock()
}

// GetWithError/SetWithError/DelWithError 与共享后端的错误反馈接口兼容。
func (c *MemoryCache) GetWithError(ctx context.Context, key string) (any, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	value, found := c.Get(key)
	return value, found, nil
}
func (c *MemoryCache) SetWithError(ctx context.Context, key string, value any, ttl time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.Set(key, value, ttl)
	return nil
}
func (c *MemoryCache) DelWithError(ctx context.Context, key string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.Del(key)
	return nil
}

// DelByPrefix 删除以 prefix 开头的全部键。
func (c *MemoryCache) DelByPrefix(prefix string) {
	c.DelByPrefixes([]string{prefix})
}

// DelByPrefixes 删除任一前缀下的键。
func (c *MemoryCache) DelByPrefixes(prefixes []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key := range c.items {
		for _, prefix := range prefixes {
			if prefix != "" && len(key) >= len(prefix) && key[:len(prefix)] == prefix {
				delete(c.items, key)
				break
			}
		}
	}
}

func (c *MemoryCache) DelByPrefixesWithError(ctx context.Context, prefixes []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.DelByPrefixes(prefixes)
	return nil
}

var (
	registryMu sync.Mutex
	registered SFCache
	custom     bool
	flights    singleflight.Group
)

// RegisterCache 在启动时注入共享缓存。重复注册会 panic，测试隔离使用 ForceReplaceCache。
func RegisterCache(cache SFCache) {
	if cache == nil {
		panic("mongoplus: RegisterCache 不能为 nil")
	}
	registryMu.Lock()
	defer registryMu.Unlock()
	if custom {
		panic("mongoplus: RegisterCache 只能调用一次")
	}
	registered = cache
	custom = true
}

// ForceReplaceCache 替换缓存且不检查是否已经注册，仅用于测试隔离。
func ForceReplaceCache(cache SFCache) {
	if cache == nil {
		panic("mongoplus: ForceReplaceCache 不能为 nil")
	}
	registryMu.Lock()
	registered = cache
	custom = true
	registryMu.Unlock()
}

// Explicit 返回 RegisterCache 或 ForceReplaceCache 注入的缓存。
// 未注入时返回 nil，仓储继续使用本机缓存。
func Explicit() SFCache {
	registryMu.Lock()
	defer registryMu.Unlock()
	if !custom {
		return nil
	}
	return registered
}

func current() SFCache {
	registryMu.Lock()
	defer registryMu.Unlock()
	if registered == nil {
		registered = NewMemoryCache()
	}
	return registered
}

// StopSFCache 关闭并清除当前缓存，之后可以重新注册。
func StopSFCache() {
	registryMu.Lock()
	cache := registered
	registered = nil
	custom = false
	registryMu.Unlock()
	if closer, ok := cache.(SFCacheCloser); ok && closer != nil {
		_ = closer.Close()
	}
}

// BuildArgs 把成对的键值组装为 SF 参数；奇数个参数时忽略最后一个键。
func BuildArgs(kv ...any) map[string]any {
	args := make(map[string]any, len(kv)/2)
	for i := 0; i+1 < len(kv); i += 2 {
		args[fmt.Sprint(kv[i])] = kv[i+1]
	}
	return args
}

// SF 合并并发请求并缓存结果。ttl 省略时使用 DefaultSFTTL，ttl 为 0 时只合并并发。
func SF[T any](fn func() (T, error), fnName string, args map[string]any, ttl ...time.Duration) (T, error) {
	duration := DefaultSFTTL
	if len(ttl) > 0 {
		duration = ttl[0]
	}
	if duration <= 0 {
		return SFNoCache(fn, fnName, args)
	}
	return SFWithTTL(fn, fnName, args, duration)
}

// SFWithTTL 使用指定的缓存时长。
func SFWithTTL[T any](fn func() (T, error), fnName string, args map[string]any, ttl time.Duration) (T, error) {
	if ttl <= 0 {
		return SFNoCache(fn, fnName, args)
	}
	key := cacheKey(fnName, args)
	if value, ok := decode[T](current().Get(key)); ok {
		return value, nil
	}
	return flight(key, fn, func(value T) { current().Set(key, value, ttl) })
}

// SFNoCache 只合并同一瞬间的并发调用，不保存结果。
func SFNoCache[T any](fn func() (T, error), fnName string, args map[string]any) (T, error) {
	return flight(cacheKey(fnName, args), fn, nil)
}

func flight[T any](key string, fn func() (T, error), store func(T)) (T, error) {
	result, err, _ := flights.Do(key, func() (any, error) {
		value, err := fn()
		if err != nil {
			return nil, err
		}
		if store != nil {
			store(value)
		}
		return value, nil
	})
	if err != nil {
		var zero T
		return zero, err
	}
	value, ok := result.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("缓存类型与 %T 不一致", zero)
	}
	return value, nil
}

func decode[T any](value any, ok bool) (T, bool) {
	if !ok {
		var zero T
		return zero, false
	}
	if raw, isRaw := value.(RawValue); isRaw {
		var decoded T
		if err := json.Unmarshal(raw, &decoded); err != nil {
			var zero T
			return zero, false
		}
		return decoded, true
	}
	typed, isTyped := value.(T)
	return typed, isTyped
}

// SFInvalidate 删除一次查询对应的缓存，参数必须与读取时一致。
func SFInvalidate(fnName string, args map[string]any) {
	current().Del(cacheKey(fnName, args))
}

// SFInvalidatePrefix 删除 fnName 下全部参数组合。fnName 末尾加点可避免误删前缀相同的其他名称。
func SFInvalidatePrefix(fnName string) {
	SFInvalidatePrefixes(fnName)
}

// SFInvalidatePrefixes 删除多个前缀。实现了批量接口时只调用一次。
func SFInvalidatePrefixes(fnNames ...string) {
	prefixes := make([]string, 0, len(fnNames))
	for _, name := range fnNames {
		if name != "" {
			prefixes = append(prefixes, "sf:"+name)
		}
	}
	if len(prefixes) == 0 {
		return
	}
	cache := current()
	if batch, ok := cache.(SFCachePrefixBatchDeleter); ok {
		batch.DelByPrefixes(prefixes)
		return
	}
	if deleter, ok := cache.(SFCachePrefixDeleter); ok {
		for _, prefix := range prefixes {
			deleter.DelByPrefix(prefix)
		}
	}
}

func cacheKey(fnName string, args map[string]any) string {
	keys := make([]string, 0, len(args))
	for key := range args {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	ordered := make([]any, 0, len(keys)*2)
	for _, key := range keys {
		ordered = append(ordered, key, args[key])
	}
	encoded, _ := json.Marshal(ordered)
	sum := sha256.Sum256(encoded)
	return "sf:" + fnName + ":" + hex.EncodeToString(sum[:])
}
