package mongoplus

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

func TestCacheInvalidationAcrossWrappers(t *testing.T) {
	root, _, cross := querySources(t)
	peer := NewDatabase(root.db.Native().Client().Database(root.db.Native().Name()))
	option := Query().WithCache(time.Minute).Build()
	ctx := context.Background()
	for _, db := range []*Database{root.db, peer, cross.db} {
		if _, err := readCached(ctx, db, "main", "one", bson.D{}, option, func() (string, error) { return "old", nil }); err != nil {
			t.Fatal(err)
		}
	}
	peer.InvalidateCache("other_collection")
	if len(root.db.cache.entries) != 1 {
		t.Fatal("其他集合更新影响了缓存")
	}
	peer.InvalidateCache("main")
	if len(root.db.cache.entries) != 0 || len(peer.cache.entries) != 0 {
		t.Fatal("同 Client 同库的包装器未同步失效")
	}
	if len(cross.db.cache.entries) != 1 {
		t.Fatal("影响了其他数据库")
	}
	// peer 的正在读取的旧结果也不能在另一个包装器失效后重新填入。
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := readCached(ctx, peer, "main", "one", bson.D{}, option, func() (string, error) { close(started); <-release; return "old", nil })
		done <- err
	}()
	<-started
	root.db.InvalidateCache("main")
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	value, err := readCached(ctx, peer, "main", "one", bson.D{}, option, func() (string, error) { return "new", nil })
	if err != nil || value != "new" {
		t.Fatalf("旧结果重新进入其他包装器: %s %v", value, err)
	}
}

func TestExternalSessionDisablesPeerCaches(t *testing.T) {
	root, _, _ := querySources(t)
	peer := NewDatabase(root.db.Native())
	session, err := root.db.Native().Client().StartSession()
	if err != nil {
		t.Fatal(err)
	}
	defer session.EndSession(context.Background())
	root.invalidate(mongo.NewSessionContext(context.Background(), session))
	late := NewDatabase(root.db.Native())
	for _, db := range []*Database{root.db, peer, late} {
		if !db.cache.disabled {
			t.Fatal("外部 Session 未关闭同库包装器缓存")
		}
		if err := db.ConfigureCache(CacheConfig{}); err != nil {
			t.Fatal(err)
		}
		if !db.cache.disabled {
			t.Fatal("容量配置不应重新打开外部 Session 禁用的缓存")
		}
	}
}

func TestDisabledCacheIgnoresQueryOptionsAndOldFlights(t *testing.T) {
	db := &Database{}
	ctx := context.Background()
	option := Query().WithCache(time.Minute).Build()
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := readCached(ctx, db, "main", "one", bson.D{}, option, func() (string, error) { close(started); <-release; return "old", nil })
		done <- err
	}()
	<-started
	if err := db.ConfigureCache(CacheConfig{Disabled: true}); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := readCached(ctx, db, "main", "one", bson.D{}, option, func() (string, error) { calls.Add(1); return "new", nil })
			if err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if calls.Load() != 8 {
		t.Fatal("Disabled 仍使用缓存或 SingleFlight")
	}
	// 重新配置后，旧在途请求也不能把清理前的结果重新填入。
	if err := db.ConfigureCache(CacheConfig{}); err != nil {
		t.Fatal(err)
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if len(db.cache.entries) != 0 {
		t.Fatal("重新配置后旧查询重新填入缓存")
	}
}
