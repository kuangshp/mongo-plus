package sf

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"sync"
	"testing"
	"time"
)

type brokenJSON struct{}

func (brokenJSON) MarshalJSON() ([]byte, error) { return nil, errors.New("encode failed") }

func TestInvalidArgsNeverShareTenantResults(t *testing.T) {
	c := NewMemoryCache()
	ForceReplaceCache(c)
	t.Cleanup(StopSFCache)
	cycle := map[string]any{}
	cycle["self"] = cycle
	for name, invalid := range map[string]any{
		"nan": math.NaN(), "inf": math.Inf(1), "channel": make(chan int),
		"cycle": cycle, "marshaler": brokenJSON{},
	} {
		t.Run(name, func(t *testing.T) {
			for _, run := range []func(func() (string, error), string, map[string]any) (string, error){
				func(fn func() (string, error), name string, args map[string]any) (string, error) {
					return SF(fn, name, args)
				},
				func(fn func() (string, error), name string, args map[string]any) (string, error) {
					return SFWithTTL(fn, name, args, time.Minute)
				},
				SFNoCache[string],
			} {
				for _, tenant := range []string{"A", "B"} {
					got, err := run(func() (string, error) { t.Fatal("无效参数不得执行加载器"); return tenant, nil }, "tenants", BuildArgs("tenant", tenant, "value", invalid))
					if got != "" || !errors.Is(err, ErrInvalidCacheArgs) {
						t.Fatalf("tenant=%s got=%q err=%v", tenant, got, err)
					}
				}
			}
		})
	}
	if c.Len() != 0 {
		t.Fatal("无效参数不应写入缓存")
	}
}

func TestValidKeysAndInvalidation(t *testing.T) {
	ForceReplaceCache(NewMemoryCache())
	t.Cleanup(StopSFCache)
	calls := 0
	load := func() (int, error) { calls++; return calls, nil }
	for _, args := range []map[string]any{{"tenant": 1, "id": 2}, {"id": 2, "tenant": 1}} {
		if value, err := SF(load, "valid", args); err != nil || value != 1 {
			t.Fatalf("value=%d err=%v", value, err)
		}
	}
	if err := SFInvalidateWithError("valid", BuildArgs("value", math.NaN())); !errors.Is(err, ErrInvalidCacheArgs) {
		t.Fatal(err)
	}
	if value, err := SF(load, "valid", BuildArgs("tenant", 1, "id", 2)); value != 1 || err != nil {
		t.Fatalf("误删有效缓存: %v %v", value, err)
	}
	if err := SFInvalidateWithError("valid", BuildArgs("id", 2, "tenant", 1)); err != nil {
		t.Fatal(err)
	}
	if value, err := SF(load, "valid", BuildArgs("tenant", 1, "id", 2)); value != 2 || err != nil {
		t.Fatalf("有效失效: %v %v", value, err)
	}
	_, err := cacheKey("bad", BuildArgs("value", math.NaN()))
	var unsupported *json.UnsupportedValueError
	if !errors.As(err, &unsupported) {
		t.Fatalf("应保留编码错误类型: %v", err)
	}
}

func TestMemoryCacheLRUAndExpiry(t *testing.T) {
	c, err := NewMemoryCacheWithConfig(MemoryCacheConfig{MaxEntries: 2, CleanupInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	c.Set("a", 1, 0)
	c.Set("b", 2, 0)
	c.Get("a")
	c.Set("c", 3, 0)
	if _, ok := c.Get("b"); ok {
		t.Fatal("应淘汰最久未使用的 b")
	}
	c.Set("a", 4, 0)
	if c.Len() != 2 {
		t.Fatal("替换不应增加条目数")
	}
	c.Set("a", 5, time.Millisecond)
	c.Set("c", 6, time.Millisecond)
	deadline := time.Now().Add(3 * time.Second)
	for c.Len() != 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if c.Len() != 0 {
		t.Fatal("无人读取时也必须主动回收过期项")
	}
	c.Set("persistent", 7, 0)
	c.Set("ignored", 8, -1)
	if c.Len() != 1 {
		t.Fatal("TTL 0 应保留，负 TTL 不缓存")
	}
}

func TestMemoryCacheHighCardinalityIsBounded(t *testing.T) {
	c, err := NewMemoryCacheWithConfig(MemoryCacheConfig{MaxEntries: 256, CleanupInterval: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	for cycle := 0; cycle < 3; cycle++ {
		for i := 0; i < 50000; i++ {
			c.Set(fmt.Sprintf("%d/%d", cycle, i), i, time.Millisecond)
			if c.Len() > 256 {
				t.Fatal("高基数写入超过容量")
			}
		}
		deadline := time.Now().Add(3 * time.Second)
		for c.Len() != 0 && time.Now().Before(deadline) {
			time.Sleep(time.Millisecond)
		}
		if c.Len() != 0 {
			t.Fatal("过期项未回收")
		}
	}
}

func TestMemoryCacheConcurrentClose(t *testing.T) {
	var c MemoryCache // 零值兼容。
	c.Set("start", 1, 0)
	life := c.life
	var wg sync.WaitGroup
	for g := 0; g < 8; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 1000; i++ {
				c.Set("key", i, time.Minute)
				c.Get("key")
				c.DelByPrefix("key")
				c.Del("key")
			}
		}()
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	wg.Wait()
	_ = c.Close()
	c.Set("after", 1, 0)
	if c.Len() != 0 {
		t.Fatal("Close 后不应重新保存")
	}
	select {
	case <-life.done:
	default:
		t.Fatal("Close 未等待清理任务退出")
	}
	for _, cfg := range []MemoryCacheConfig{{MaxEntries: -1}, {CleanupInterval: -time.Second}} {
		if _, err := NewMemoryCacheWithConfig(cfg); err == nil {
			t.Fatal("负配置必须拒绝")
		}
	}
}
