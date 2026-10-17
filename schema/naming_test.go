package schema

import (
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

func TestPluralizeGoConfig(t *testing.T) {
	for _, value := range []bool{false, true} {
		c := testConfig(t, map[string]string{"user": `{"fields":[]}`})
		c.Pluralize = value
		entities, err := LoadEntities(c)
		if err != nil {
			t.Fatal(err)
		}
		want := "user"
		if value {
			want = "users"
		}
		if entities[0].Collection != want {
			t.Fatal("集合复数配置错误")
		}
	}
}
