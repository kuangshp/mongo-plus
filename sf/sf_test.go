package sf

import (
	"testing"
	"time"
)

func TestPrefixInvalidationRemovesEveryArgument(t *testing.T) {
	ForceReplaceCache(NewMemoryCache())
	t.Cleanup(StopSFCache)
	calls := 0
	load := func() (string, error) {
		calls++
		return "ok", nil
	}
	if _, err := SF(load, "User.FindList", BuildArgs("status", 1), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := SF(load, "User.FindList", BuildArgs("status", 2), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := SF(load, "User.FindPage", BuildArgs("status", 1), time.Minute); err != nil {
		t.Fatal(err)
	}
	if calls != 3 {
		t.Fatalf("不同方法应各自查询，实际 %d", calls)
	}
	SFInvalidatePrefix("User.FindList")
	if _, err := SF(load, "User.FindPage", BuildArgs("status", 1), time.Minute); err != nil {
		t.Fatal(err)
	}
	if _, err := SF(load, "User.FindList", BuildArgs("status", 1), time.Minute); err != nil {
		t.Fatal(err)
	}
	if calls != 4 {
		t.Fatalf("只应失效 FindList，实际调用 %d", calls)
	}
}

func TestRawValueDecodesJSON(t *testing.T) {
	cache := NewMemoryCache()
	ForceReplaceCache(cache)
	t.Cleanup(StopSFCache)
	cache.Set(cacheKey("User.FindById", map[string]any{"id": 1}), RawValue(`{"id":1}`), time.Minute)
	got, err := SF(func() (map[string]any, error) {
		t.Fatal("命中缓存不应回源")
		return nil, nil
	}, "User.FindById", map[string]any{"id": 1}, time.Minute)
	if err != nil || got["id"] != float64(1) {
		t.Fatalf("RawValue = %#v, %v", got, err)
	}
}

func TestRegisterCacheRejectsSecondCall(t *testing.T) {
	StopSFCache()
	t.Cleanup(StopSFCache)
	RegisterCache(NewMemoryCache())
	defer func() {
		if recover() == nil {
			t.Fatal("重复 RegisterCache 应失败")
		}
	}()
	RegisterCache(NewMemoryCache())
}
