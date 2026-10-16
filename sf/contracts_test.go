package sf

import (
	"context"
	"fmt"
	"reflect"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestTypedKeysPreserveNestedBSONIdentity(t *testing.T) {
	id := bson.NewObjectID()
	date := time.Date(2026, 10, 16, 1, 2, 3, 0, time.UTC)
	pairs := [][2]any{
		{id, id.Hex()}, {date, date.Format(time.RFC3339Nano)}, {int64(1), "1"}, {int64(1), float64(1)},
		{[]any{id}, []any{id.Hex()}}, {map[string]any{"nested": map[string]any{"id": id}}, map[string]any{"nested": map[string]any{"id": id.Hex()}}},
		{bson.D{{Key: "a", Value: 1}, {Key: "b", Value: 2}}, bson.D{{Key: "b", Value: 2}, {Key: "a", Value: 1}}},
		{string([]byte{0xff}), string([]byte{0xfe})}, {[]string(nil), []string{}},
	}
	for i, pair := range pairs {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			a, err := cacheKey("typed", BuildArgs("value", pair[0]))
			if err != nil {
				t.Fatal(err)
			}
			b, err := cacheKey("typed", BuildArgs("value", pair[1]))
			if err != nil {
				t.Fatal(err)
			}
			if a == b {
				t.Fatalf("不同类型/值碰撞: %T %T", pair[0], pair[1])
			}
		})
	}
	a, err := cacheKey("order", map[string]any{"value": map[string]any{"a": int64(1), "b": "two"}})
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 100; i++ {
		b, err := cacheKey("order", map[string]any{"value": map[string]any{"b": "two", "a": int64(1)}})
		if err != nil || a != b {
			t.Fatalf("map 顺序不应改变键: %v", err)
		}
	}
}

func TestTypedKeysSeparateCacheAndConcurrentFlights(t *testing.T) {
	ForceReplaceCache(NewMemoryCache())
	t.Cleanup(StopSFCache)
	id := bson.NewObjectID()
	for _, value := range []any{id, id.Hex()} {
		want := fmt.Sprintf("%T", value)
		got, err := SF(func() (string, error) { return want, nil }, "tenant", BuildArgs("tenant", value))
		if err != nil || got != want {
			t.Fatalf("类型化租户串缓存: %q want=%q %v", got, want, err)
		}
	}
	started := make(chan struct{}, 2)
	release := make(chan struct{})
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	for _, value := range []any{id, id.Hex()} {
		wg.Add(1)
		go func(value any) {
			defer wg.Done()
			want := fmt.Sprintf("%T", value)
			got, err := SFNoCache(func() (string, error) { started <- struct{}{}; <-release; return want, nil }, "concurrent", BuildArgs("tenant", value))
			if err != nil || got != want {
				t.Errorf("类型化租户串在途结果: %q %v", got, err)
			}
		}(value)
	}
	for i := 0; i < 2; i++ {
		select {
		case <-started:
		case <-ctx.Done():
			close(release)
			wg.Wait()
			t.Fatal("不同参数类型加入了同一请求")
		}
	}
	close(release)
	wg.Wait()
	if err := SFInvalidateWithError("tenant", BuildArgs("tenant", id)); err != nil {
		t.Fatal(err)
	}
	got, err := SF(func() (string, error) { return "new ObjectID", nil }, "tenant", BuildArgs("tenant", id))
	if err != nil || got != "new ObjectID" {
		t.Fatalf("类型化精确失效失败: %q %v", got, err)
	}
	got, err = SF(func() (string, error) { return "wrong reload", nil }, "tenant", BuildArgs("tenant", id.Hex()))
	if err != nil || got != "string" {
		t.Fatalf("删除了一同值字符串的缓存: %q %v", got, err)
	}
	SFInvalidatePrefix("tenant")
	got, err = SF(func() (string, error) { return "fresh", nil }, "tenant", BuildArgs("tenant", id.Hex()))
	if err != nil || got != "fresh" {
		t.Fatalf("类型化前缀失效失败: %q %v", got, err)
	}
}

func FuzzTypedKeysKeepStringAndBytesDistinct(f *testing.F) {
	f.Add("tenant")
	f.Add("\xff")
	f.Add("")
	f.Fuzz(func(t *testing.T, value string) {
		a, err := cacheKey("fuzz", BuildArgs("v", value))
		if err != nil {
			t.Fatal(err)
		}
		b, err := cacheKey("fuzz", BuildArgs("v", []byte(value)))
		if err != nil {
			t.Fatal(err)
		}
		if a == b {
			t.Fatal("string 与 bytes 不能共用缓存")
		}
	})
}

func TestSFNilInterfaceResultIsValid(t *testing.T) {
	ForceReplaceCache(NewMemoryCache())
	t.Cleanup(StopSFCache)
	loads := 0
	for i := 0; i < 2; i++ {
		got, err := SF(func() (any, error) { loads++; return nil, nil }, "nil-interface", nil)
		if err != nil || got != nil {
			t.Fatalf("nil 接口结果被当成类型错误: %v %v", got, err)
		}
	}
	if loads != 1 {
		t.Fatalf("nil 结果未缓存: %d", loads)
	}
}

func TestTypeIdentityPreservesCompositionBoundaries(t *testing.T) {
	a := reflect.TypeFor[struct {
		X struct{ Y int }
		Z int
	}]()
	b := reflect.TypeFor[struct {
		X struct {
			Y int
			Z int
		}
	}]()
	if a == b || typeIdentity(a) == typeIdentity(b) {
		t.Fatal("嵌套类型边界丢失导致在途结果类型碰撞")
	}
}
