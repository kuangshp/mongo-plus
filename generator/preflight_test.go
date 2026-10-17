package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestPreflightRejectsDeclarationCollisionsBeforeWriting(t *testing.T) {
	for name, definitions := range map[string]map[string]string{
		"dao paths": {
			"user": `{"fields":[{"name":"profile","type":"object","fields":[{"name":"city","type":"string"}]},{"name":"profile_city","type":"string"}]}`,
		},
		"nested entity": {
			"user":  `{"fields":[{"name":"profile","type":"object","fields":[{"name":"city","type":"string"}]}]}`,
			"other": `{"entity":"UserEntityProfile","fields":[{"name":"label","type":"string"}]}`,
		},
		"dto nested type": {
			"user":  `{"fields":[{"name":"profile_dto","goName":"ProfileDTO","type":"object","fields":[{"name":"city","type":"string"}]}]}`,
			"other": `{"entity":"UserDTOProfileEntity","fields":[]}`,
		},
	} {
		t.Run(name, func(t *testing.T) {
			c, _ := fixture(t)
			c.DtoPath, c.VoPath = "query/dto", "query/vo"
			for file, definition := range definitions {
				if err := os.WriteFile(filepath.Join(c.SchemaDir, file+".json"), []byte(definition), 0600); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := Prepare(c); err == nil || !strings.Contains(err.Error(), "冲突") {
				t.Fatalf("未在准备阶段拒绝冲突: %v", err)
			}
			if _, err := Generate(c); err == nil {
				t.Fatal("冲突仍生成成功")
			}
			if _, err := os.Stat(c.Output); !os.IsNotExist(err) {
				t.Fatal("失败预检不应创建输出目录")
			}
		})
	}
}

func TestPreflightChecksPreservedBusinessDeclarations(t *testing.T) {
	c, _ := fixture(t)
	if err := os.WriteFile(filepath.Join(c.SchemaDir, "user.json"), []byte(`{"fields":[]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(c); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(c.ModelPkgPath, "custom.go")
	conflict := []byte("package model\ntype UserEntity struct{}\n")
	if err := os.WriteFile(path, conflict, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Prepare(c); err == nil || !strings.Contains(err.Error(), "UserEntity") {
		t.Fatalf("未检查保留文件中的冲突: %v", err)
	}
	if got, err := os.ReadFile(path); err != nil || string(got) != string(conflict) {
		t.Fatal("预检修改了业务文件")
	}
}

func TestPreparedOutputKeepsJSONSnapshotAndRechecksBusinessFiles(t *testing.T) {
	c, _ := fixture(t)
	path := filepath.Join(c.SchemaDir, "user.json")
	if err := os.WriteFile(path, []byte(`{"fields":[{"name":"name","type":"string"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	plan, err := Prepare(c)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(`{"fields":[{"name":"name","type":"int64"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Write(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(c.ModelPkgPath, "user_entity.go"))
	if err != nil || !strings.Contains(strings.Join(strings.Fields(string(data)), " "), "Name string") {
		t.Fatalf("写入重新读取了 JSON，而非准备快照: %v\n%s", err, data)
	}
	// 准备后新增冲突业务文件，写入必须再次拒绝，不能覆盖用户修改。
	if err := os.WriteFile(filepath.Join(c.ModelPkgPath, "late.go"), []byte("package model\ntype UserEntity struct{}\n"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := plan.Write(); err == nil {
		t.Fatal("未拒绝准备后新增的业务声明冲突")
	}
}
