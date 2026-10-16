package mongoplus

import (
	"fmt"
	"time"
)

// CacheConfig 限制本机 TTL 缓存；零值使用默认值，不会自动开启查询缓存。
// 字节数按保留的 Go 对象保守估算，不是进程内存的硬上限。
type CacheConfig struct {
	Namespace                 string        // 共享缓存的数据源标识；留空时按 Database 包装器隔离。跨进程共享必须显式设置相同标识。
	Disabled                  bool          // 强一致、多实例或外部写入场景：忽略 WithCache/WithSingleFlight，始终直读数据库。
	MaxEntries                int           // 默认 1024 条结果。
	MaxBytes                  int64         // 默认 64 MiB。
	MaxEntryBytes             int64         // 默认单条结果 1 MiB；超出时仍正常返回查询结果。
	RequireErrorReporting     bool          // 共享后端必须实现 SFCacheWithErrors，否则绕过缓存并上报错误。
	InvalidationRetryInterval time.Duration // 失效失败的初始重试间隔；0 默认 1 秒，指数退避至最多 30 秒。
}

func (c CacheConfig) defaults() CacheConfig {
	if c.MaxEntries == 0 {
		c.MaxEntries = 1024
	}
	if c.MaxBytes == 0 {
		c.MaxBytes = 64 << 20
	}
	if c.MaxEntryBytes == 0 {
		c.MaxEntryBytes = min(1<<20, c.MaxBytes)
	}
	return c
}

// ConfigureCache 配置容量并清空已有缓存；建议在启动时调用。
func (d *Database) ConfigureCache(limits CacheConfig) error {
	if limits.MaxEntries < 0 || limits.MaxBytes < 0 || limits.MaxEntryBytes < 0 {
		return fmt.Errorf("缓存容量不能为负数")
	}
	if limits.InvalidationRetryInterval < 0 {
		return fmt.Errorf("缓存失效重试间隔不能为负数")
	}
	limits = limits.defaults()
	if limits.MaxEntryBytes > limits.MaxBytes {
		return fmt.Errorf("单条缓存容量不能超过总容量")
	}
	d.cache.mu.Lock()
	defer d.cache.mu.Unlock()
	d.cache.limits = limits
	d.cache.generation++
	d.cache.entries = nil
	d.cache.bytes = 0
	return nil
}

func (c *readCache) remove(key string) {
	if entry, ok := c.entries[key]; ok {
		c.bytes -= entry.bytes
		delete(c.entries, key)
	}
}
