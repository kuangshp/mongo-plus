package mongoplus

import (
	"context"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// 故意只实现最小缓存接口；仓储失效不能依赖可选的前缀删除能力。
type basicSharedCache struct{ SFCache }

func TestSharedCacheDoesNotResurrectInvalidatedRead(t *testing.T) {
	ctx := context.Background()
	store := basicSharedCache{NewMemoryCache()}
	reader, writer := &Database{}, &Database{}
	for _, db := range []*Database{reader, writer} {
		db.UseCache(store)
		if err := db.ConfigureCache(CacheConfig{Namespace: "same-deployment"}); err != nil {
			t.Fatal(err)
		}
	}
	option := Query().WithCache(time.Minute).Build()
	started, release := make(chan struct{}), make(chan struct{})
	done := make(chan error, 1)
	go func() {
		_, err := readCached(ctx, reader, "users", "one", bson.D{}, option, func() (string, error) {
			close(started)
			<-release
			return "old", nil
		})
		done <- err
	}()
	<-started
	writer.InvalidateCache("users")
	// 失效后的读取也不能加入先前的 singleflight。
	newRead := make(chan string, 1)
	go func() {
		got, err := readCached(ctx, reader, "users", "one", bson.D{}, option, func() (string, error) { return "new", nil })
		if err != nil {
			t.Error(err)
		}
		newRead <- got
	}()
	select {
	case got := <-newRead:
		if got != "new" {
			t.Errorf("失效后的新请求返回 %q", got)
		}
	case <-time.After(2 * time.Second):
		t.Error("失效后的请求仍等待旧查询")
	}
	close(release)
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	got, err := readCached(ctx, writer, "users", "one", bson.D{}, option, func() (string, error) { return "new", nil })
	if err != nil || got != "new" {
		t.Fatalf("旧在途结果重新进入共享缓存: %q, %v", got, err)
	}
}

func TestSharedCacheDeploymentIsolation(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryCache()
	var databases []*Database
	for _, uri := range []string{"mongodb://127.0.0.1:1", "mongodb://127.0.0.1:2"} {
		client, err := mongo.Connect(options.Client().ApplyURI(uri))
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = client.Disconnect(ctx) })
		db := NewDatabase(client.Database("shop"))
		db.UseCache(store)
		databases = append(databases, db)
	}
	option := Query().WithCache(time.Minute).Build()
	for i, want := range []string{"deployment A", "deployment B"} {
		got, err := readCached(ctx, databases[i], "users", "one", bson.D{}, option, func() (string, error) { return want, nil })
		if err != nil || got != want {
			t.Fatalf("不同数据源串缓存: got=%q want=%q err=%v", got, want, err)
		}
	}
}

func TestSharedCacheExplicitNamespace(t *testing.T) {
	store := basicSharedCache{NewMemoryCache()}
	ctx := context.Background()
	option := Query().WithCache(time.Minute).Build()
	var databases []*Database
	for _, namespace := range []string{"primary", "primary", "analytics"} {
		db := &Database{}
		db.UseCache(store)
		if err := db.ConfigureCache(CacheConfig{Namespace: namespace}); err != nil {
			t.Fatal(err)
		}
		databases = append(databases, db)
	}
	read := func(db *Database, value string) string {
		t.Helper()
		got, err := readCached(ctx, db, "users", "one", bson.D{}, option, func() (string, error) { return value, nil })
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if read(databases[0], "shared") != "shared" || read(databases[1], "unexpected reload") != "shared" {
		t.Fatal("显式相同命名空间没有共享结果")
	}
	if read(databases[2], "isolated") != "isolated" {
		t.Fatal("不同命名空间串数据")
	}
	databases[1].InvalidateCache("users")
	if read(databases[0], "fresh") != "fresh" || read(databases[2], "unexpected reload") != "isolated" {
		t.Fatal("跨实例失效未正确隔离数据源")
	}
	// 版本键被逐出后必须生成新版本，不能再次命中之前的结果。
	store.Del(sharedCachePrefix(databases[0], "users") + "version")
	if read(databases[1], "after eviction") != "after eviction" {
		t.Fatal("版本键逐出后复用了旧缓存")
	}
}

func TestSharedCacheLocalReconfigurationRejectsOldFlight(t *testing.T) {
	for _, disable := range []bool{false, true} {
		db := &Database{}
		db.UseCache(NewMemoryCache())
		ctx := context.Background()
		option := Query().WithCache(time.Minute).Build()
		started, release := make(chan struct{}), make(chan struct{})
		done := make(chan error, 1)
		go func() {
			_, err := readCached(ctx, db, "users", "one", bson.D{}, option, func() (string, error) {
				close(started)
				<-release
				return "old", nil
			})
			done <- err
		}()
		<-started
		if err := db.ConfigureCache(CacheConfig{Disabled: disable}); err != nil {
			t.Fatal(err)
		}
		close(release)
		if err := <-done; err != nil {
			t.Fatal(err)
		}
		got, err := readCached(ctx, db, "users", "one", bson.D{}, option, func() (string, error) { return "new", nil })
		if err != nil || got != "new" {
			t.Fatalf("重新配置后旧结果重新写入: %q %v", got, err)
		}
	}
}
