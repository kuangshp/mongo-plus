package schema

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCollectionNaming(t *testing.T) {
	for _, item := range []struct {
		file                           string
		suffix                         bool
		definition, entity, collection string
	}{
		{"user", false, `{"fields":[]}`, "UserEntity", "user"},
		{"user", true, `{"fields":[]}`, "UserEntity", "users"},
		{"sys_user", false, `{"fields":[]}`, "SysUserEntity", "sys_user"},
		{"sys_user", true, `{"fields":[]}`, "SysUserEntity", "sys_users"},
		{"sysUser", false, `{"fields":[]}`, "SysUserEntity", "sys_user"},
		{"sysUser", true, `{"fields":[]}`, "SysUserEntity", "sys_users"},
		{"HTTPUser", true, `{"fields":[]}`, "HTTPUserEntity", "http_users"},
		{"sysUser", true, `{"collection":"CustomUsers","fields":[]}`, "SysUserEntity", "CustomUsers"},
		{"sysUser", true, `{"entity":"AccountEntity","fields":[]}`, "AccountEntity", "sys_users"},
	} {
		t.Run(item.file+item.collection, func(t *testing.T) {
			c := testConfig(t, map[string]string{item.file: item.definition})
			c.Pluralize = item.suffix
			entities, err := LoadEntities(c)
			if err != nil {
				t.Fatal(err)
			}
			e := entities[0]
			if e.Entity != item.entity || e.Collection != item.collection || e.Runtime().Name != item.collection {
				t.Fatalf("推导错误 %+v", e)
			}
			// 重复 Normalize 不得重复追加后缀或修改显式集合。
			if err := e.Normalize(c, item.file); err != nil || e.Collection != item.collection {
				t.Fatalf("命名不幂等 %+v %v", e, err)
			}
		})
	}
}

func TestCollectionNamingCollision(t *testing.T) {
	c := testConfig(t, map[string]string{"sysUser": `{"fields":[]}`, "sys_user": `{"fields":[]}`})
	if _, err := LoadEntities(c); err == nil || !strings.Contains(err.Error(), "重复") {
		t.Fatalf("未检测到驼峰/下划线命名冲突: %v", err)
	}
}

type SysUserEntity struct {
	ID int64 `bson:"_id"`
}

func TestHandwrittenModelCollectionNaming(t *testing.T) {
	c := testConfig(t, nil)
	c.SoftDelete, c.Timestamps = Bool(false), Bool(false)
	for _, suffix := range []bool{false, true} {
		c.Pluralize = suffix
		entities, err := FromModels(c, Model{Value: SysUserEntity{}})
		want := "sys_user"
		if suffix {
			want += "s"
		}
		if err != nil || len(entities) != 1 || entities[0].Collection != want {
			t.Fatalf("手写实体命名错误 %+v %v", entities, err)
		}
	}
}

func TestPluralizeYAMLBool(t *testing.T) {
	for _, item := range []struct {
		value         string
		want, invalid bool
	}{
		{"", false, false},
		{"pluralize: false\n", false, false},
		{"pluralize: true\n", true, false},
		{"pluralize: s\n", false, true},
		{"pluralize: \"\"\n", false, true},
	} {
		path := filepath.Join(t.TempDir(), "generator.yml")
		if err := os.WriteFile(path, []byte("package: example.com/shop\n"+item.value), 0600); err != nil {
			t.Fatal(err)
		}
		c, err := LoadConfig(path)
		if (err != nil) != item.invalid {
			t.Fatalf("配置 %q 结果不符: %v", item.value, err)
		}
		if !item.invalid && c.Pluralize != item.want {
			t.Fatalf("配置 %q 开关值错误: %v", item.value, c.Pluralize)
		}
	}
}
