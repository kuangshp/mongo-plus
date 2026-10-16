package mongoplus

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// 导出类型用于验证官方驱动支持的 BSON inline 映射。
type SensitiveContact struct {
	Phone       string `bson:"-" mongoplus:"cipher:PhoneCipher;index:PhoneIndex;encrypt:true"`
	PhoneCipher string `bson:"secure_phone"`
	PhoneIndex  string `bson:"lookup_phone"`
	Display     string `bson:"display"`
}

type nestedSensitiveEntity struct {
	ID       int64                `bson:"_id"`
	Profile  SensitiveContact     `bson:"profile"`
	Optional *SensitiveContact    `bson:"optional,omitempty"`
	Contacts []SensitiveContact   `bson:"contacts"`
	Pointers []*SensitiveContact  `bson:"pointers"`
	Matrix   [][]SensitiveContact `bson:"matrix"`
	Fixed    [2]*SensitiveContact `bson:"fixed"`
}

type inlineSensitiveEntity struct {
	ID                int64 `bson:"_id"`
	*SensitiveContact `bson:",inline"`
}

func nestedSensitiveFixture() nestedSensitiveEntity {
	return nestedSensitiveEntity{
		ID:       1,
		Profile:  SensitiveContact{Phone: "138-0000-8000"},
		Optional: &SensitiveContact{Phone: "13900009000"},
		Contacts: []SensitiveContact{{Phone: "13700007000"}},
		Pointers: []*SensitiveContact{nil, {Phone: "13600006000"}},
		Matrix:   [][]SensitiveContact{{{Phone: "13500005000"}}},
		Fixed:    [2]*SensitiveContact{nil, {Phone: "13400004000"}},
	}
}

func TestSensitiveNestedRoundTripAndCopyIsolation(t *testing.T) {
	p := tagPlugin(t)
	original := nestedSensitiveFixture()
	prepared := original
	if err := p.copyWriteFields(&prepared); err != nil {
		t.Fatal(err)
	}
	if err := p.seal(&prepared); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, nestedSensitiveFixture()) {
		t.Fatalf("加密污染原实体: %+v", original)
	}
	contacts := []*SensitiveContact{&prepared.Profile, prepared.Optional, &prepared.Contacts[0], prepared.Pointers[1], &prepared.Matrix[0][0], prepared.Fixed[1]}
	for _, contact := range contacts {
		if contact.PhoneCipher == "" || contact.PhoneCipher == contact.Phone || contact.PhoneIndex == "" {
			t.Fatalf("嵌套字段未加密: %+v", contact)
		}
	}
	raw, err := bson.Marshal(prepared)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "138-0000-8000") || strings.Contains(string(raw), "13900009000") {
		t.Fatal("BSON 中不应有明文")
	}
	var decoded nestedSensitiveEntity
	if err := bson.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if err := p.reveal(context.Background(), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Profile.Phone != "138****8000" || decoded.Optional.Phone != "139****9000" || decoded.Pointers[1].Phone != "136****6000" {
		t.Fatalf("嵌套脱敏失败: %+v", decoded)
	}
	if err := p.reveal(WithSensitivePlaintext(context.Background()), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Profile.Phone != "13800008000" || decoded.Contacts[0].Phone != "13700007000" || decoded.Matrix[0][0].Phone != "13500005000" || decoded.Fixed[1].Phone != "13400004000" {
		t.Fatalf("嵌套解密失败: %+v", decoded)
	}
	if decoded.Pointers[0] != nil || decoded.Fixed[0] != nil {
		t.Fatal("空元素不应被自动初始化")
	}
}

func TestSensitiveNilInlinePointerAndExplicitFields(t *testing.T) {
	for _, explicit := range []bool{false, true} {
		cfg := SensitiveConfig{Key: []byte("0123456789abcdef")}
		if explicit {
			phone := PhoneField("Phone")
			phone.EncryptAtRest = true
			cfg.Fields = []SensitiveFieldConfig{phone}
		}
		p, err := NewSensitivePlugin(cfg)
		if err != nil {
			t.Fatal(err)
		}
		empty := inlineSensitiveEntity{ID: 1}
		if err := p.copyWriteFields(&empty); err != nil {
			t.Fatal(err)
		}
		if err := p.seal(&empty); err != nil {
			t.Fatal(err)
		}
		if err := p.reveal(context.Background(), &empty); err != nil {
			t.Fatal(err)
		}
		if empty.SensitiveContact != nil {
			t.Fatal("空嵌入指针应保持 nil")
		}
		original := inlineSensitiveEntity{ID: 2, SensitiveContact: &SensitiveContact{Phone: "13800008000"}}
		prepared := original
		if err := p.copyWriteFields(&prepared); err != nil {
			t.Fatal(err)
		}
		if err := p.seal(&prepared); err != nil {
			t.Fatal(err)
		}
		if prepared.SensitiveContact == original.SensitiveContact || original.PhoneCipher != "" || original.PhoneIndex != "" {
			t.Fatal("匿名指针未隔离")
		}
		raw, err := bson.Marshal(prepared)
		if err != nil {
			t.Fatal(err)
		}
		var decoded inlineSensitiveEntity
		if err := bson.Unmarshal(raw, &decoded); err != nil {
			t.Fatal(err)
		}
		if err := p.reveal(WithSensitivePlaintext(context.Background()), &decoded); err != nil || decoded.Phone != "13800008000" {
			t.Fatalf("inline 读取失败: %+v %v", decoded, err)
		}
	}
}

func TestSensitiveNestedUpdatePathsAndQueries(t *testing.T) {
	p := tagPlugin(t)
	typ := reflect.TypeFor[nestedSensitiveEntity]()
	for _, tc := range []struct{ logical, cipher, index string }{
		{"profile.phone", "profile.secure_phone", "profile.lookup_phone"},
		{"Profile.Phone", "profile.secure_phone", "profile.lookup_phone"},
		{"optional.phone", "optional.secure_phone", "optional.lookup_phone"},
		{"contacts.0.phone", "contacts.0.secure_phone", "contacts.0.lookup_phone"},
		{"Pointers.1.Phone", "pointers.1.secure_phone", "pointers.1.lookup_phone"},
		{"matrix.1.2.phone", "matrix.1.2.secure_phone", "matrix.1.2.lookup_phone"},
	} {
		for _, op := range []field.UpdateOp{field.New[string](tc.logical).Set("139-0000-9000"), field.New[string](tc.logical).Unset()} {
			ops, err := p.rewrite([]field.UpdateOp{op}, typ)
			if err != nil || len(ops) != 2 || ops[0].Path != tc.cipher || ops[1].Path != tc.index || ops[0].Operator != op.Operator {
				t.Fatalf("%s 更新失败: %+v %v", tc.logical, ops, err)
			}
		}
	}
	for _, path := range []string{"profile", "profile.phone.child", "contacts", "contacts.0", "contacts.phone", "matrix.0", "matrix.0.phone", "Phone"} {
		if _, err := p.rewrite([]field.UpdateOp{field.New[any](path).Set("plaintext")}, typ); err == nil {
			t.Fatalf("不安全更新应拒绝: %s", path)
		}
	}
	ops, err := p.rewrite([]field.UpdateOp{field.New[string]("contacts.0.display").Set("name")}, typ)
	if err != nil || len(ops) != 1 || ops[0].Path != "contacts.0.display" {
		t.Fatalf("普通字段不应受影响: %+v %v", ops, err)
	}
	r := &Repository[nestedSensitiveEntity, int64]{db: &Database{sensitive: p}}
	for _, tc := range []struct{ name, path string }{
		{"Profile.Phone", "profile.lookup_phone"},
		{"profile.phone", "profile.lookup_phone"},
		{"Contacts.Phone", "contacts.lookup_phone"},
	} {
		c := r.SensitiveEq(tc.name, "13800008000")
		if c.Err != nil || c.Document[0].Key != tc.path {
			t.Fatalf("%s 查询失败: %+v", tc.name, c)
		}
	}
	if r.SensitiveEq("Phone", "13800008000").Err == nil {
		t.Fatal("同名嵌套字段不能任选一个")
	}
}

func TestSensitiveNestedExplicitOverrideAndUnsupportedTypes(t *testing.T) {
	phone := PhoneField("Profile.Phone")
	phone.EncryptAtRest = true
	phone.ReturnMode = SensitiveReturnPlain
	p, err := NewSensitivePlugin(SensitiveConfig{Key: []byte("0123456789abcdef"), Fields: []SensitiveFieldConfig{phone}})
	if err != nil {
		t.Fatal(err)
	}
	entity := nestedSensitiveFixture()
	if err := p.copyWriteFields(&entity); err != nil {
		t.Fatal(err)
	}
	if err := p.seal(&entity); err != nil {
		t.Fatal(err)
	}
	if err := p.reveal(context.Background(), &entity); err != nil {
		t.Fatal(err)
	}
	if entity.Profile.Phone != "13800008000" || entity.Optional.Phone != "139****9000" {
		t.Fatal("嵌套显式配置应只覆盖指定字段")
	}
	type recursive struct {
		Info SensitiveContact
		Next *recursive
	}
	for _, entity := range []any{
		&struct{ Values map[string]SensitiveContact }{},
		&recursive{},
		&struct{ Payload any }{Payload: SensitiveContact{Phone: "13800008000"}},
		&struct{ Payload map[string]any }{Payload: map[string]any{"info": &SensitiveContact{Phone: "13800008000"}}},
	} {
		if err := p.seal(entity); err == nil {
			t.Fatalf("不支持的敏感结构必须在写入前报错: %T", entity)
		}
	}
	ordinary := struct{ Payload map[string]any }{Payload: map[string]any{"status": 1, "tags": []any{"name", 2}}}
	if err := p.copyWriteFields(&ordinary); err != nil {
		t.Fatalf("普通动态 BSON 数据应继续可用: %v", err)
	}
	if err := p.seal(&ordinary); err != nil {
		t.Fatal(err)
	}
	var cycle any
	cycle = &cycle
	if err := p.copyWriteFields(&struct{ Payload any }{Payload: cycle}); err == nil {
		t.Fatal("动态循环指针必须返回错误")
	}
	if _, err := p.rewrite([]field.UpdateOp{field.New[any]("payload").Set(SensitiveContact{Phone: "13800008000"})}, reflect.TypeFor[struct{ Payload any }]()); err == nil {
		t.Fatal("更新也不能把敏感实体放入动态容器后静默丢失")
	}
}
