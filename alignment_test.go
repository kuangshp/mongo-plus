package mongoplus

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/fstest"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestDataPermissionInjectsAndCanBeSkipped(t *testing.T) {
	root, _, _ := querySources(t)
	if err := RegisterDataPermission(root.db, DataPermissionConfig{ExcludeCollections: []string{"secret"}}); err != nil {
		t.Fatal(err)
	}
	ctx := WithDataPermission(context.Background(), func(collection string) (bson.D, error) {
		return bson.D{{Key: "created_by", Value: int64(3)}}, nil
	})
	filter, err := root.filter(ctx, Query().Where(field.NewString("name").Eq("a")).Build(), false)
	want := field.And(
		field.NewString("name").Eq("a"),
		field.New[any]("deleted_at").IsNull(),
		Condition{Document: bson.D{{Key: "created_by", Value: int64(3)}}},
	)
	if err != nil || !reflect.DeepEqual(filter, want.Document) {
		t.Fatalf("数据权限 = %v, %v", filter, err)
	}
	skipped, err := root.filter(SkipDataPermission(ctx), Query().Build(), false)
	if err != nil || !reflect.DeepEqual(skipped, field.New[any]("deleted_at").IsNull().Document) {
		t.Fatalf("跳过权限 = %v, %v", skipped, err)
	}
	root.config.Name = "secret"
	excluded, err := root.filter(ctx, Query().Build(), false)
	if err != nil || !reflect.DeepEqual(excluded, skipped) {
		t.Fatalf("排除集合仍注入权限: %v", excluded)
	}
}

func TestSensitiveFieldRoundTripAndUpdateRewrite(t *testing.T) {
	phone := PhoneField("Phone")
	phone.EncryptAtRest = true
	plugin, err := NewSensitivePlugin(SensitiveConfig{Key: []byte("0123456789abcdef"), Fields: []SensitiveFieldConfig{phone}})
	if err != nil {
		t.Fatal(err)
	}
	entity := struct {
		Phone       string `bson:"-"`
		PhoneCipher string `bson:"phone_cipher"`
		PhoneIndex  string `bson:"phone_index"`
	}{Phone: "138-0000-8000"}
	if err = plugin.seal(&entity); err != nil {
		t.Fatal(err)
	}
	if entity.PhoneCipher == "" || entity.PhoneCipher == "13800008000" || entity.PhoneIndex == "" {
		t.Fatalf("未生成密文和盲索引: %+v", entity)
	}
	if err = plugin.reveal(context.Background(), &entity); err != nil {
		t.Fatal(err)
	}
	if entity.Phone != "138****8000" {
		t.Fatalf("脱敏结果 = %s", entity.Phone)
	}
	if err = plugin.reveal(WithSensitivePlaintext(context.Background()), &entity); err != nil {
		t.Fatal(err)
	}
	if entity.Phone != "13800008000" {
		t.Fatalf("明文 = %s", entity.Phone)
	}
	condition := plugin.PhoneEq("138 0000 8000")
	eq, _ := condition.Document[0].Value.(bson.D)
	if condition.Err != nil || condition.Document[0].Key != "phone_index" || len(eq) == 0 || eq[0].Value != entity.PhoneIndex {
		t.Fatalf("等值条件没有使用盲索引: %+v", condition)
	}
	ops, err := plugin.rewrite([]field.UpdateOp{{Operator: "$set", Path: "phone", Value: "13900008000"}})
	if err != nil || len(ops) != 2 || ops[0].Path != "phone_cipher" || ops[1].Path != "phone_index" {
		t.Fatalf("更新没有改写到密文字段: %+v, %v", ops, err)
	}
}

func TestSlowQueryUsesThreshold(t *testing.T) {
	db := &Database{}
	var got []SlowQueryInfo
	if err := RegisterSlowQuery(db, SlowQueryConfig{
		Threshold: time.Hour,
		Logger: func(_ context.Context, info SlowQueryInfo) {
			got = append(got, info)
		},
	}); err != nil {
		t.Fatal(err)
	}
	db.Observer(context.Background(), Observation{Collection: "users", Operation: "Find", Duration: time.Millisecond})
	if len(got) != 0 {
		t.Fatalf("未超过阈值不应记录: %+v", got)
	}
	db.Observer(context.Background(), Observation{Collection: "users", Operation: "Find", Duration: 2 * time.Hour, Err: errors.New("timeout")})
	if len(got) != 1 || got[0].Collection != "users" || got[0].Operation != "Find" || got[0].Error == nil {
		t.Fatalf("慢操作 = %+v", got)
	}
}

func TestDataSourceRoutesReadAndWrite(t *testing.T) {
	manager := NewDataSourceManager()
	master, first, second := &Database{}, &Database{}, &Database{}
	analytics := &Database{}
	if err := manager.Register("default", DataSourceGroupConfig{Master: master, Slaves: []*Database{first, second}}); err != nil {
		t.Fatal(err)
	}
	if err := manager.Register("analytics", DataSourceGroupConfig{Master: analytics}); err != nil {
		t.Fatal(err)
	}
	if got, err := manager.Auto(context.Background()); err != nil || got != master {
		t.Fatalf("默认应走主库: %p %v", got, err)
	}
	if got, err := manager.Auto(DSWithRead(context.Background())); err != nil || got != first {
		t.Fatalf("第一次读应走第一个从库: %p %v", got, err)
	}
	if got, err := manager.Read("default"); err != nil || got != second {
		t.Fatalf("第二次读应轮询: %p %v", got, err)
	}
	ctx := DSWithWrite(DSWithRead(context.Background()))
	if got, err := manager.Auto(ctx); err != nil || got != master {
		t.Fatalf("写标记应优先于读标记: %p %v", got, err)
	}
	if got, err := manager.Auto(DSWithName(context.Background(), "analytics")); err != nil || got != analytics {
		t.Fatalf("命名数据源 = %p %v", got, err)
	}
	if _, err := manager.Auto(DSWithName(context.Background(), "missing")); err == nil {
		t.Fatal("未注册的数据源应返回错误")
	}
}

func TestDALFileBindsArgumentsAndProtectsWrites(t *testing.T) {
	previous := defaultDAL.Load()
	files := fstest.MapFS{
		"user/page.json":       {Data: []byte(`{"collection":"users","op":"find","filter":{"status":"?"},"limit":"?","skip":"?"}`)},
		"user/count_page.json": {Data: []byte(`{"collection":"users","op":"count","filter":{"status":"?"}}`)},
		"user/search.json":     {Data: []byte(`{"collection":"users","op":"find","filter":{"name":"@name"}}`)},
		"user/disable.json":    {Data: []byte(`{"collection":"users","op":"updateMany","update":{"$set":{"status":0}}}`)},
	}
	dal, err := NewDal(&Database{}, NewEmbedLoader(files))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		dal.Close()
		defaultDAL.Store(previous)
	})
	command, err := dal.command("user/page.json", []any{int32(1), int64(10), int64(20)}, nil)
	if err != nil || command.collection != "users" || command.filter[0].Value != int32(1) || *command.limit != 10 || *command.skip != 20 {
		t.Fatalf("位置参数 = %+v, %v", command, err)
	}
	named, err := dal.command("user/search.json", nil, map[string]any{"name": "ann"})
	if err != nil || named.filter[0].Value != "ann" {
		t.Fatalf("命名参数 = %+v, %v", named, err)
	}
	if countFileName("user/page.json") != "user/count_page.json" {
		t.Fatal(countFileName("user/page.json"))
	}
	if _, err = dal.command("user/disable.json", nil, nil); err == nil {
		t.Fatal("无条件更新应被拒绝")
	}
	if err = DALPreload("user/page.json", "user/count_page.json"); err != nil {
		t.Fatal(err)
	}
}

func TestSharedCachePrefixInvalidation(t *testing.T) {
	cache := NewMemoryCache()
	db := &Database{}
	db.UseCache(cache)
	calls := 0
	load := func() (int64, error) {
		calls++
		return 9, nil
	}
	option := QueryOption{cacheTTL: time.Minute}
	if got, err := readCached(context.Background(), db, "users", "FindOne", bson.D{{Key: "id", Value: int64(1)}}, option, load); err != nil || got != 9 || calls != 1 {
		t.Fatalf("首次读取 = %d, %v, calls=%d", got, err, calls)
	}
	if _, err := readCached(context.Background(), db, "users", "FindOne", bson.D{{Key: "id", Value: int64(1)}}, option, load); err != nil || calls != 1 {
		t.Fatalf("共享缓存未命中，calls=%d, %v", calls, err)
	}
	db.InvalidateCache("users")
	if _, err := readCached(context.Background(), db, "users", "FindOne", bson.D{{Key: "id", Value: int64(1)}}, option, load); err != nil || calls != 2 {
		t.Fatalf("前缀失效后应回源，calls=%d, %v", calls, err)
	}
}
