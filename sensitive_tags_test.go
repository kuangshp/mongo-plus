package mongoplus

import (
	"context"
	"reflect"
	"sync"
	"testing"

	"github.com/kuangshp/mongo-plus/field"
)

type tagSensitiveEntity struct {
	ID          int64  `bson:"_id"`
	Phone       string `bson:"-" mongoplus:"cipher:PhoneCipher;index:PhoneIndex;encrypt:true"`
	PhoneCipher string `bson:"phone_cipher"`
	PhoneIndex  string `bson:"phone_index"`
}

type customTagSensitiveEntity struct {
	ID          int64  `bson:"_id"`
	Phone       string `bson:"-" gormplus:"cipher:PhoneCipher;index:PhoneIndex;encrypt:true"`
	PhoneCipher string `bson:"encrypted_mobile"`
	PhoneIndex  string `bson:"mobile_lookup"`
}

func tagPlugin(t *testing.T) *SensitivePlugin {
	t.Helper()
	p, err := NewSensitivePlugin(SensitiveConfig{Key: []byte("0123456789abcdef")})
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestSensitiveTagMetadataIsSharedByCreateUpdateAndQuery(t *testing.T) {
	p := tagPlugin(t)
	entity := tagSensitiveEntity{Phone: "138-0000-8000"}
	if err := p.seal(&entity); err != nil {
		t.Fatal(err)
	}
	ops, err := p.rewrite([]field.UpdateOp{field.New[string]("phone").Set("139 0000 9000")}, reflect.TypeFor[tagSensitiveEntity]())
	if err != nil || len(ops) != 2 {
		t.Fatalf("更新: %+v %v", ops, err)
	}
	if ops[0].Path != "phone_cipher" || ops[1].Path != "phone_index" || ops[0].Value == entity.PhoneCipher || ops[1].Value == entity.PhoneIndex {
		t.Fatalf("密文/索引没有同时刷新: %+v", ops)
	}
	entity.PhoneCipher, entity.PhoneIndex = ops[0].Value.(string), ops[1].Value.(string)
	if err := p.reveal(WithSensitivePlaintext(context.Background()), &entity); err != nil || entity.Phone != "13900009000" {
		t.Fatalf("读取: %+v %v", entity, err)
	}
	r := &Repository[tagSensitiveEntity, int64]{db: &Database{sensitive: p}}
	c := r.SensitiveEq("Phone", "139-0000-9000")
	if c.Err != nil || c.Document[0].Key != "phone_index" {
		t.Fatalf("查询: %+v", c)
	}
	if r.SensitiveEq("unknown", "value").Err == nil {
		t.Fatal("未声明敏感字段应报错")
	}
	for _, op := range []field.UpdateOp{
		{Operator: "$inc", Path: "phone", Value: 1},
		{Operator: "$set", Path: "phone", Value: 1},
		{Operator: "$set", Path: "phone.child", Value: "plaintext"},
	} {
		if _, err := p.rewrite([]field.UpdateOp{op}, reflect.TypeFor[tagSensitiveEntity]()); err == nil {
			t.Fatalf("不安全操作应拒绝: %+v", op)
		}
	}
	ops, err = p.rewrite([]field.UpdateOp{field.New[string]("phone").Unset()}, reflect.TypeFor[tagSensitiveEntity]())
	if err != nil || len(ops) != 2 || ops[0].Operator != "$unset" || ops[1].Operator != "$unset" {
		t.Fatalf("删除没有同时移除密文/索引: %+v %v", ops, err)
	}
}

func TestSensitiveMetadataIsIsolatedAcrossEntityTypes(t *testing.T) {
	p := tagPlugin(t)
	var wg sync.WaitGroup
	for i := 0; i < 16; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				for typ, path := range map[reflect.Type]string{reflect.TypeFor[tagSensitiveEntity](): "phone_cipher", reflect.TypeFor[customTagSensitiveEntity](): "encrypted_mobile"} {
					ops, err := p.rewrite([]field.UpdateOp{field.New[string]("phone").Set("13800008000")}, typ)
					if err != nil || len(ops) != 2 || ops[0].Path != path {
						t.Errorf("类型间配置串值: %+v %v", ops, err)
						return
					}
				}
			}
		}()
	}
	wg.Wait()
	r := &Repository[customTagSensitiveEntity, int64]{db: &Database{sensitive: p}}
	if c := r.SensitiveEq("phone", "13800008000"); c.Err != nil || c.Document[0].Key != "mobile_lookup" {
		t.Fatalf("标签查询未使用 BSON 名: %+v", c)
	}
}

func TestSensitiveExplicitFieldsWinAndDoNotMutateCaller(t *testing.T) {
	cfg := SensitiveConfig{Key: []byte("0123456789abcdef"), Fields: []SensitiveFieldConfig{PhoneField("Phone")}}
	cfg.Fields[0].EncryptAtRest = true
	p, err := NewSensitivePlugin(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Fields[0].PlainPath != "" {
		t.Fatal("构造器不应修改调用者配置")
	}
	cfg.Fields[0].Normalize = func(string) string { return "changed" }
	entity := customTagSensitiveEntity{Phone: "13800008000"}
	if err := p.seal(&entity); err != nil {
		t.Fatal(err)
	}
	if err := p.reveal(WithSensitivePlaintext(context.Background()), &entity); err != nil || entity.Phone != "13800008000" {
		t.Fatalf("配置被外部修改: %+v %v", entity, err)
	}
	ops, err := p.rewrite([]field.UpdateOp{field.New[string]("phone").Set("13900009000")}, reflect.TypeFor[customTagSensitiveEntity]())
	if err != nil || ops[0].Path != "encrypted_mobile" || ops[1].Path != "mobile_lookup" {
		t.Fatalf("显式字段应推导实际 BSON 名: %+v %v", ops, err)
	}
	cfg.Fields[0].CipherPath = "override_cipher"
	cfg.Fields[0].IndexPath = "override_index"
	p, err = NewSensitivePlugin(cfg)
	if err != nil {
		t.Fatal(err)
	}
	fields, err := p.fieldsForType(reflect.TypeFor[customTagSensitiveEntity]())
	if err == nil {
		t.Fatalf("不能接受与实体 BSON 存储位置不一致的显式路径: %+v", fields)
	}
}

func TestSensitiveInvalidTagFailsBeforeWrite(t *testing.T) {
	p := tagPlugin(t)
	for _, entity := range []any{
		&struct {
			Phone string `mongoplus:"cipher:Cipher"`
		}{},
		&struct {
			Phone string `mongoplus:"cipher:Missing;index:Missing;encrypt:true"`
		}{},
		&struct {
			Phone  string `mongoplus:"cipher:Cipher;index:Index"`
			Cipher int
			Index  string
		}{},
		&struct {
			Phone  string `mongoplus:"cipher:Cipher;index:Index"`
			Cipher string `bson:"-"`
			Index  string
		}{},
	} {
		if err := p.seal(entity); err == nil {
			t.Fatalf("无效标签被忽略: %T", entity)
		}
	}
}
