//go:build ignore

// 多实例缓存实现示例。复制到业务项目后去掉本行，并安装 github.com/redis/go-redis/v9。
// 注册方式：mongoplus.RegisterCache(&RedisSFCache{Client: client, Prefix: "myapp:"}).
// 同一实际数据源的各进程还需设置相同 CacheConfig.Namespace；不同集群/环境使用不同值。
package redissfcache

import (
	"context"
	"encoding/json"
	"time"

	"github.com/kuangshp/mongo-plus"
	"github.com/redis/go-redis/v9"
)

// RedisSFCache 用 SCAN 按前缀删除，避免 KEYS 阻塞 Redis。
type RedisSFCache struct {
	Client *redis.Client
	Prefix string
}

func (c *RedisSFCache) Get(key string) (any, bool) {
	value, err := c.Client.Get(context.Background(), c.Prefix+key).Bytes()
	if err != nil {
		return nil, false
	}
	return mongoplus.RawValue(value), true
}

func (c *RedisSFCache) Set(key string, val any, ttl time.Duration) {
	var encoded []byte
	switch typed := val.(type) {
	case mongoplus.RawValue:
		encoded = typed
	default:
		var err error
		encoded, err = json.Marshal(val)
		if err != nil {
			return
		}
	}
	_ = c.Client.Set(context.Background(), c.Prefix+key, encoded, ttl).Err()
}

func (c *RedisSFCache) Del(key string) {
	_ = c.Client.Del(context.Background(), c.Prefix+key).Err()
}

func (c *RedisSFCache) DelByPrefix(prefix string) {
	c.DelByPrefixes([]string{prefix})
}

func (c *RedisSFCache) DelByPrefixes(prefixes []string) {
	ctx := context.Background()
	for _, prefix := range prefixes {
		var cursor uint64
		for {
			keys, next, err := c.Client.Scan(ctx, cursor, c.Prefix+prefix+"*", 500).Result()
			if err != nil {
				return
			}
			if len(keys) > 0 {
				_ = c.Client.Del(ctx, keys...).Err()
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
}

func (c *RedisSFCache) Close() error { return c.Client.Close() }
