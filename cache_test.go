package mongoplus

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestCacheDoesNotResurrectInvalidatedRead(t *testing.T) {
	db := &Database{}
	ctx := context.Background()
	option := Query().WithCache(time.Minute).Build()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := readCached(ctx, db, "users", "one", bson.D{}, option, func() (string, error) { close(started); <-release; return "old", nil })
		done <- err
	}()
	<-started
	db.InvalidateCache("users")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	value, err := readCached(ctx, db, "users", "one", bson.D{}, option, func() (string, error) { return "new", nil })
	if err != nil || value != "new" {
		t.Fatalf("失效后旧数据重新进入缓存 %s %v", value, err)
	}
}

func TestCachedValuesAreIndependent(t *testing.T) {
	type entity struct {
		Name string `bson:"name"`
	}
	db := &Database{}
	ctx := context.Background()
	option := Query().WithCache(time.Minute).Build()
	var calls atomic.Int32
	load := func() (*entity, error) { calls.Add(1); return &entity{"original"}, nil }
	first, err := readCached(ctx, db, "users", "one", bson.D{}, option, load)
	if err != nil {
		t.Fatal(err)
	}
	first.Name = "changed"
	second, err := readCached(ctx, db, "users", "one", bson.D{}, option, load)
	if err != nil || second.Name != "original" || calls.Load() != 1 {
		t.Fatalf("缓存实体被调用方修改 %+v %v", second, err)
	}
}

func TestSingleFlightWaiterCancellation(t *testing.T) {
	db := &Database{}
	option := Query().WithSingleFlight().Build()
	started := make(chan struct{})
	release := make(chan struct{})
	done := make(chan error, 1)
	var calls atomic.Int32
	load := func() (int64, error) { calls.Add(1); close(started); <-release; return 1, nil }
	go func() {
		_, err := readCached(context.Background(), db, "users", "one", bson.D{}, option, load)
		done <- err
	}()
	<-started
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := readCached(ctx, db, "users", "one", bson.D{}, option, load); !errors.Is(err, context.Canceled) {
		t.Fatalf("等待者不能取消: %v", err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 1 {
		t.Fatalf("相同查询未合并: %d", calls.Load())
	}
}

func TestCacheCapacityAndEntrySize(t *testing.T) {
	db := &Database{}
	if err := db.ConfigureCache(CacheConfig{MaxEntries: 2, MaxBytes: 2048, MaxEntryBytes: 1024}); err != nil {
		t.Fatal(err)
	}
	option := Query().WithCache(time.Minute).Build()
	ctx := context.Background()
	load := func() (string, error) { return strings.Repeat("x", 400), nil }
	for i := 0; i < 10; i++ {
		if _, err := readCached(ctx, db, "users", "one", bson.D{{Key: "id", Value: i}}, option, load); err != nil {
			t.Fatal(err)
		}
	}
	if len(db.cache.entries) != 2 || db.cache.bytes > 2048 || db.cache.bytes <= 0 {
		t.Fatalf("缓存超过容量 %d/%d", len(db.cache.entries), db.cache.bytes)
	}
	var calls atomic.Int32
	large := func() (string, error) { calls.Add(1); return strings.Repeat("x", 2048), nil }
	for i := 0; i < 2; i++ {
		if value, err := readCached(ctx, db, "users", "large", bson.D{}, option, large); err != nil || len(value) != 2048 {
			t.Fatalf("大结果未正常返回 %v", err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("超大结果仍进入缓存")
	}
	db.InvalidateCache("users")
	if len(db.cache.entries) != 0 || db.cache.bytes != 0 {
		t.Fatalf("失效未释放容量 %d", db.cache.bytes)
	}
	if err := db.ConfigureCache(CacheConfig{MaxBytes: -1}); err == nil {
		t.Fatal("允许负容量")
	}
	if err := db.ConfigureCache(CacheConfig{MaxBytes: 10, MaxEntryBytes: 20}); err == nil {
		t.Fatal("允许单条容量超过总容量")
	}
}

func TestCacheDeepCopiesNestedAndCustomValues(t *testing.T) {
	type value struct {
		Items  []map[string]*int
		At     time.Time
		Amount bson.Decimal128
	}
	n := 7
	amount, _ := bson.ParseDecimal128("12.34")
	original := value{Items: []map[string]*int{{"n": &n}}, At: time.Now(), Amount: amount}
	copy, _, ok := copyCacheValue(original)
	if !ok {
		t.Fatal("拒绝可安全复制的 BSON 类型")
	}
	*copy.Items[0]["n"] = 99
	if n != 7 || !copy.At.Equal(original.At) || copy.Amount != amount {
		t.Fatal("深拷贝改变了原值")
	}
	type privateReference struct{ data []byte }
	if _, _, ok := copyCacheValue(privateReference{data: []byte{1}}); ok {
		t.Fatal("共享了不可安全复制的私有引用")
	}
	m := map[string]any{}
	m["self"] = m
	if _, _, ok := copyCacheValue(m); ok {
		t.Fatal("允许循环引用进入缓存")
	}
}

func TestUnsupportedCacheValueUsesOnlyOneLoad(t *testing.T) {
	type private struct{ data []byte }
	var calls atomic.Int32
	load := func() (private, error) { calls.Add(1); return private{data: []byte{1}}, nil }
	db := &Database{}
	for i := 0; i < 2; i++ {
		value, err := readCached(context.Background(), db, "private", "one", bson.D{}, Query().WithCache(time.Minute).Build(), load)
		if err != nil || value.data[0] != 1 {
			t.Fatalf("不安全类型的回退错误 %v", err)
		}
		value.data[0] = 2
	}
	if calls.Load() != 2 || len(db.cache.entries) != 0 {
		t.Fatalf("重复读取或共享私有引用: calls=%d", calls.Load())
	}
}
