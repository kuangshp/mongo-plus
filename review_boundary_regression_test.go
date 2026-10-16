package mongoplus

import (
	"context"
	"encoding/binary"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// 自定义解码不必是编码的逆运算，缓存不能对解码结果再执行一次 BSON 解码。
type reviewScaled int64

func (v *reviewScaled) UnmarshalBSONValue(typ byte, data []byte) error {
	var n int64
	switch bson.Type(typ) {
	case bson.TypeInt64:
		n = int64(binary.LittleEndian.Uint64(data))
	case bson.TypeInt32:
		n = int64(int32(binary.LittleEndian.Uint32(data)))
	default:
		return fmt.Errorf("unexpected BSON type %v", typ)
	}
	*v = reviewScaled(n * 10)
	return nil
}

type reviewScaledRow struct {
	ID    int64        `bson:"_id"`
	Value reviewScaled `bson:"value"`
}

func TestReviewSharedCacheBypassesCustomBSON(t *testing.T) {
	db := &Database{}
	store := NewMemoryCache()
	t.Cleanup(func() { _ = store.Close() })
	db.UseCache(store)
	loads := 0
	for i := 0; i < 3; i++ {
		got, err := readCached(context.Background(), db, "custom", "one", bson.D{}, Query().WithCache(time.Minute).Build(), func() (*reviewScaledRow, error) {
			loads++
			return &reviewScaledRow{ID: 1, Value: 20}, nil
		})
		if err != nil || got.Value != 20 {
			t.Fatalf("解码结果被共享缓存改变: %+v %v", got, err)
		}
	}
	if loads != 3 || store.Len() != 0 {
		t.Fatalf("不安全类型不能访问共享缓存: loads=%d entries=%d", loads, store.Len())
	}
	for _, typ := range []reflect.Type{
		reflect.TypeFor[reviewScaled](), reflect.TypeFor[[]*reviewScaledRow](),
		reflect.TypeFor[map[string]reviewScaled](), reflect.TypeFor[struct{ Value any }](),
	} {
		if sharedBSONSafe(typ, map[reflect.Type]bool{}) {
			t.Fatalf("未识别自定义/动态类型: %v", typ)
		}
	}
	if !sharedBSONSafe(reflect.TypeFor[struct {
		ID    bson.ObjectID
		At    time.Time
		Names []string
	}](), map[reflect.Type]bool{}) {
		t.Fatal("普通 BSON 实体仍应支持共享缓存")
	}
}

type reviewSensitiveCollision struct {
	ID           int64  `bson:"_id"`
	Phone        string `bson:"-" mongoplus:"cipher:SharedCipher;index:PhoneIndex;encrypt:true"`
	Mobile       string `bson:"-" mongoplus:"cipher:SharedCipher;index:MobileIndex;encrypt:true"`
	SharedCipher string `bson:"shared_cipher"`
	PhoneIndex   string `bson:"phone_index"`
	MobileIndex  string `bson:"mobile_index"`
}

type reviewSensitiveAlias struct {
	Phone        string `bson:"-" mongoplus:"cipher:PhoneCipher;index:PhoneIndex;encrypt:true"`
	Mobile       string `bson:"-" mongoplus:"cipher:MobileCipher;index:MobileIndex;encrypt:true"`
	PhoneCipher  string `bson:"secret"`
	MobileCipher string `bson:"secret"`
	PhoneIndex   string `bson:"phone_index"`
	MobileIndex  string `bson:"mobile_index"`
}

func TestReviewSensitiveStorageCollisionsFailClosed(t *testing.T) {
	p := tagPlugin(t)
	for _, typ := range []reflect.Type{
		reflect.TypeFor[reviewSensitiveCollision](), reflect.TypeFor[reviewSensitiveAlias](),
		reflect.TypeFor[struct {
			Contacts []reviewSensitiveCollision `bson:"contacts"`
		}](),
	} {
		if _, err := p.fieldsForType(typ); err == nil {
			t.Fatalf("未拒绝敏感存储冲突: %v", typ)
		}
	}
	// 父子路径和虚拟明文别名也不能与另一字段的存储路径重叠。
	for _, path := range []string{"mobile_lookup.child", "encrypted_mobile"} {
		cfg := PhoneField("Phone")
		cfg.PlainPath = path
		p, err := NewSensitivePlugin(SensitiveConfig{Key: []byte("0123456789abcdef"), Fields: []SensitiveFieldConfig{cfg}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.fieldsForType(reflect.TypeFor[customTagSensitiveEntity]()); err == nil {
			t.Fatalf("未拒绝重叠路径: %s", path)
		}
	}
}

func TestReviewSensitiveExplicitPathsMatchBSON(t *testing.T) {
	for _, kind := range []string{"cipher", "index"} {
		cfg := PhoneField("Phone")
		if kind == "cipher" {
			cfg.CipherPath = "other_cipher"
		} else {
			cfg.IndexPath = "other_index"
		}
		p, err := NewSensitivePlugin(SensitiveConfig{Key: []byte("0123456789abcdef"), Fields: []SensitiveFieldConfig{cfg}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err = p.fieldsForType(reflect.TypeFor[customTagSensitiveEntity]()); err == nil {
			t.Fatalf("未拒绝不匹配的 %s 路径", kind)
		}
	}
	cfg := PhoneField("Phone")
	cfg.CipherPath, cfg.IndexPath = "encrypted_mobile", "mobile_lookup"
	p, err := NewSensitivePlugin(SensitiveConfig{Key: []byte("0123456789abcdef"), Fields: []SensitiveFieldConfig{cfg}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = p.fieldsForType(reflect.TypeFor[customTagSensitiveEntity]()); err != nil {
		t.Fatalf("与 BSON 一致的显式配置不能拒绝: %v", err)
	}
}

func TestReviewWriteQueryOptions(t *testing.T) {
	o := Query().Collation(options.Collation{Locale: "en", Strength: 2}).Hint("name_1").Let(bson.D{{Key: "target", Value: "Alice"}}).Comment("review").Build()
	if err := validateWriteQuery(o); err != nil {
		t.Fatal(err)
	}
	u1 := &options.UpdateOneOptions{}
	for _, set := range repositoryWriteOptions(options.UpdateOne(), o).List() {
		if err := set(u1); err != nil {
			t.Fatal(err)
		}
	}
	um := &options.UpdateManyOptions{}
	for _, set := range repositoryWriteOptions(options.UpdateMany(), o).List() {
		if err := set(um); err != nil {
			t.Fatal(err)
		}
	}
	d1 := &options.DeleteOneOptions{}
	for _, set := range repositoryWriteOptions(options.DeleteOne(), o).List() {
		if err := set(d1); err != nil {
			t.Fatal(err)
		}
	}
	dm := &options.DeleteManyOptions{}
	for _, set := range repositoryWriteOptions(options.DeleteMany(), o).List() {
		if err := set(dm); err != nil {
			t.Fatal(err)
		}
	}
	for _, got := range []any{u1, um, d1, dm} {
		v := reflect.ValueOf(got).Elem()
		for _, name := range []string{"Collation", "Hint", "Let", "Comment"} {
			if !reflect.DeepEqual(v.FieldByName(name).Interface(), reflect.ValueOf(u1).Elem().FieldByName(name).Interface()) {
				t.Fatalf("%T 遗漏 %s", got, name)
			}
		}
		if v.FieldByName("Hint").Interface() != "name_1" || v.FieldByName("Comment").Interface() != "review" || !reflect.DeepEqual(v.FieldByName("Let").Interface(), o.let) || !reflect.DeepEqual(v.FieldByName("Collation").Interface(), o.collation) {
			t.Fatalf("写入选项未透传: %+v", got)
		}
	}
	for _, invalid := range []QueryOption{
		Query().Limit(1).Build(), Query().Offset(1).Build(), Query().Select(field.New[string]("name")).Build(),
		Query().Order(field.New[string]("name").Asc()).Build(), Query().WithCache(time.Minute).Build(),
		Query().WithSingleFlight().Build(), Query().BatchSize(2).Build(), Query().AllowDiskUse(true).Build(),
	} {
		if err := validateWriteQuery(invalid); err == nil {
			t.Fatal("不能静默忽略不支持的写入选项")
		}
	}
}
