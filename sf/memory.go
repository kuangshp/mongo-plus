package sf

import (
	"container/list"
	"context"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"time"
	"weak"
)

// MemoryCacheConfig 控制公共 SF 内存缓存，与 Repository 的 CacheConfig 独立。
// 限额按条目数计算，不是进程内存字节上限；大结果请使用共享后端或自行限制载荷。
type MemoryCacheConfig struct {
	MaxEntries      int           // 0 使用 1024；达到上限后按 LRU 淘汰，TTL 为 0 的条目也受限。
	CleanupInterval time.Duration // 0 使用 1 秒；后台定期删除过期项，不能为负数。
}

type memoryItem struct {
	key string
	val any
	exp time.Time
}

type memoryLifetime struct {
	stop chan struct{}
	done chan struct{}
	once sync.Once
}

func (l *memoryLifetime) close() { l.once.Do(func() { close(l.stop) }) }

// MemoryCache 提供有界 LRU、主动过期回收及前缀删除；零值可用。
// Get 返回原始值，调用者应避免修改共享结果。使用结束后调用 Close 立即释放资源。
type MemoryCache struct {
	mu     sync.Mutex
	items  map[string]*list.Element
	lru    list.List
	cfg    MemoryCacheConfig
	life   *memoryLifetime
	closed bool
}

// NewMemoryCache 使用默认限额和清理间隔。
func NewMemoryCache() *MemoryCache {
	c, _ := NewMemoryCacheWithConfig(MemoryCacheConfig{})
	return c
}

// NewMemoryCacheWithConfig 创建内存缓存。负数配置返回错误。
func NewMemoryCacheWithConfig(cfg MemoryCacheConfig) (*MemoryCache, error) {
	if cfg.MaxEntries < 0 || cfg.CleanupInterval < 0 {
		return nil, fmt.Errorf("sf: MaxEntries 和 CleanupInterval 不能为负数")
	}
	// 首次 Set 启动清理器；空缓存不占用后台协程。
	return &MemoryCache{cfg: cfg}, nil
}

func (c *MemoryCache) initLocked() {
	if c.items != nil {
		return
	}
	if c.cfg.MaxEntries == 0 {
		c.cfg.MaxEntries = 1024
	}
	if c.cfg.CleanupInterval == 0 {
		c.cfg.CleanupInterval = time.Second
	}
	c.items = make(map[string]*list.Element)
	c.life = &memoryLifetime{stop: make(chan struct{}), done: make(chan struct{})}
	// 后台任务仅持弱引用。旧调用方即使忘记 Close，也不会被清理协程永久持有。
	runtime.AddCleanup(c, func(l *memoryLifetime) { l.close() }, c.life)
	go sweepMemory(weak.Make(c), c.life, c.cfg.CleanupInterval)
}

func sweepMemory(ref weak.Pointer[MemoryCache], life *memoryLifetime, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	defer close(life.done)
	for {
		select {
		case <-life.stop:
			return
		case now := <-ticker.C:
			if !purgeMemory(ref, now) {
				return
			}
		}
	}
}

// 独立函数确保强引用不会跨越下一次 ticker 等待。
func purgeMemory(ref weak.Pointer[MemoryCache], now time.Time) bool {
	c := ref.Value()
	if c == nil {
		return false
	}
	c.mu.Lock()
	for _, elem := range c.items {
		item := elem.Value.(memoryItem)
		if !item.exp.IsZero() && !now.Before(item.exp) {
			c.removeLocked(elem)
		}
	}
	c.mu.Unlock()
	runtime.KeepAlive(c)
	return true
}

func (c *MemoryCache) removeLocked(elem *list.Element) {
	delete(c.items, elem.Value.(memoryItem).key)
	c.lru.Remove(elem)
}

func (c *MemoryCache) Get(key string) (any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	elem := c.items[key]
	if c.closed || elem == nil {
		return nil, false
	}
	item := elem.Value.(memoryItem)
	if !item.exp.IsZero() && !time.Now().Before(item.exp) {
		c.removeLocked(elem)
		return nil, false
	}
	c.lru.MoveToFront(elem)
	return item.val, true
}

func (c *MemoryCache) Set(key string, val any, ttl time.Duration) {
	if ttl < 0 {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.closed {
		return
	}
	c.initLocked()
	item := memoryItem{key: key, val: val}
	if ttl > 0 {
		item.exp = time.Now().Add(ttl)
	}
	if elem := c.items[key]; elem != nil {
		elem.Value = item
		c.lru.MoveToFront(elem)
		return
	}
	if len(c.items) >= c.cfg.MaxEntries {
		c.removeLocked(c.lru.Back())
	}
	c.items[key] = c.lru.PushFront(item)
}

func (c *MemoryCache) Del(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if elem := c.items[key]; elem != nil {
		c.removeLocked(elem)
	}
}

// Len 返回实际保留的条目数，不触发清理，便于监控主动回收效果。
func (c *MemoryCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// Close 幂等地停止清理任务并清空缓存；关闭后 Get 未命中，Set 不再保存数据。
func (c *MemoryCache) Close() error {
	c.mu.Lock()
	c.closed = true
	c.items = nil
	c.lru.Init()
	life := c.life
	c.mu.Unlock()
	if life != nil {
		life.close()
		<-life.done
	}
	return nil
}

// DelByPrefix 删除以 prefix 开头的全部键；空前缀不产生效果。
func (c *MemoryCache) DelByPrefix(prefix string) { c.DelByPrefixes([]string{prefix}) }

// DelByPrefixes 在同一临界区删除多个前缀。
func (c *MemoryCache) DelByPrefixes(prefixes []string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	for key, elem := range c.items {
		for _, prefix := range prefixes {
			if prefix != "" && strings.HasPrefix(key, prefix) {
				c.removeLocked(elem)
				break
			}
		}
	}
}

// 以下方法与共享后端错误反馈接口兼容，先检查请求上下文。
func (c *MemoryCache) GetWithError(ctx context.Context, key string) (any, bool, error) {
	if err := ctx.Err(); err != nil {
		return nil, false, err
	}
	v, ok := c.Get(key)
	return v, ok, nil
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
func (c *MemoryCache) DelByPrefixesWithError(ctx context.Context, prefixes []string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	c.DelByPrefixes(prefixes)
	return nil
}
