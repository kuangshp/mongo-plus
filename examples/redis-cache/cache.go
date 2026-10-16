//go:build ignore

// 多实例缓存实现示例。复制到业务项目后去掉本行，并安装 github.com/redis/go-redis/v9。
// 注册方式：mongoplus.RegisterCache(&RedisSFCache{Client: client, Prefix: "myapp:"}).
// 同一实际数据源的各进程还需设置相同 CacheConfig.Namespace；不同集群/环境使用不同值。
package redissfcache

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/kuangshp/mongo-plus"
	"github.com/redis/go-redis/v9"
)

// RedisSFCache 用 SCAN 按前缀删除，避免 KEYS 阻塞 Redis。
type RedisSFCache struct {
	Client *redis.Client
	Prefix string
}

var _ mongoplus.SFCache = (*RedisSFCache)(nil)
var _ mongoplus.SFCacheWithErrors = (*RedisSFCache)(nil)

func (c *RedisSFCache) Get(key string) (any, bool) {
	value, found, _ := c.GetWithError(context.Background(), key)
	return value, found
}

func (c *RedisSFCache) GetWithError(ctx context.Context, key string) (any, bool, error) {
	value, err := c.Client.Get(ctx, c.Prefix+key).Bytes()
	if errors.Is(err, redis.Nil) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	return mongoplus.RawValue(value), true, nil
}

func (c *RedisSFCache) Set(key string, val any, ttl time.Duration) {
	_ = c.SetWithError(context.Background(), key, val, ttl)
}

func (c *RedisSFCache) SetWithError(ctx context.Context, key string, val any, ttl time.Duration) error {
	var encoded []byte
	switch typed := val.(type) {
	case mongoplus.RawValue:
		encoded = typed
	default:
		var err error
		encoded, err = json.Marshal(val)
		if err != nil {
			return err
		}
	}
	return c.Client.Set(ctx, c.Prefix+key, encoded, ttl).Err()
}

func (c *RedisSFCache) Del(key string) {
	_ = c.DelWithError(context.Background(), key)
}

func (c *RedisSFCache) DelWithError(ctx context.Context, key string) error {
	return c.Client.Del(ctx, c.Prefix+key).Err()
}

func (c *RedisSFCache) DelByPrefix(prefix string) {
	c.DelByPrefixes([]string{prefix})
}

func (c *RedisSFCache) DelByPrefixes(prefixes []string) {
	_ = c.DelByPrefixesWithError(context.Background(), prefixes)
}

func (c *RedisSFCache) DelByPrefixesWithError(ctx context.Context, prefixes []string) error {
	for _, prefix := range prefixes {
		var cursor uint64
		for {
			keys, next, err := c.Client.Scan(ctx, cursor, c.Prefix+prefix+"*", 500).Result()
			if err != nil {
				return err
			}
			if len(keys) > 0 {
				if err := c.Client.Del(ctx, keys...).Err(); err != nil {
					return err
				}
			}
			cursor = next
			if cursor == 0 {
				break
			}
		}
	}
	return nil
}

func (c *RedisSFCache) Close() error { return c.Client.Close() }
