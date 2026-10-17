package mongoplus_test

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"

	mp "github.com/kuangshp/mongo-plus"
	"github.com/kuangshp/mongo-plus/internal/testutil"
	"go.mongodb.org/mongo-driver/v2/bson"
)

func TestRunGeneratorCheckAndGenerate(t *testing.T) {
	c := syncConfig(t)
	// 数据源语法错误也不能导致 check 连接数据库；只校验完整性和 JSON。
	c.MongoDB = mp.Config{URI: "invalid-uri", Database: "shop"}
	if changes, err := mp.RunGenerator(context.Background(), c, "check"); err != nil || changes != nil {
		t.Fatalf("check 不应连接数据库: %v %v", changes, err)
	}
	if _, err := os.Stat(c.RepoPath); !os.IsNotExist(err) {
		t.Fatal("check 写入了生成文件")
	}
	c.MongoDB = mp.Config{}
	if _, err := mp.RunGenerator(context.Background(), c, "generate"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(c.ModelPkgPath); err != nil {
		t.Fatal("未生成模型", err)
	}
	if _, err := mp.RunGenerator(context.Background(), c, ""); err != nil {
		t.Fatal("空操作应兼容默认生成", err)
	}
}

func TestRunGeneratorRejectsInvalidInputsBeforeEffects(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for _, action := range []string{"generate", "check", "plan", "apply"} {
		c := syncConfig(t)
		if _, err := mp.RunGenerator(ctx, c, action); !errors.Is(err, context.Canceled) {
			t.Fatalf("%s 未遵循取消: %v", action, err)
		}
		if _, err := os.Stat(c.RepoPath); !os.IsNotExist(err) {
			t.Fatal("取消后产生了文件")
		}
	}
	c := syncConfig(t)
	if _, err := mp.RunGenerator(nil, c, "check"); err == nil {
		t.Fatal("nil context 未报错")
	}
	if _, err := mp.RunGenerator(context.Background(), nil, "generate"); err == nil {
		t.Fatal("nil config 未报错")
	}
	if _, err := mp.RunGenerator(context.Background(), c, "drop"); err == nil {
		t.Fatal("未知动作未报错")
	}
	for _, action := range []string{"plan", "apply"} {
		if _, err := mp.RunGenerator(context.Background(), c, action); err == nil || !strings.Contains(err.Error(), "uri、database") {
			t.Fatalf("%s 没有要求数据源: %v", action, err)
		}
	}
	if err := os.RemoveAll(c.SchemaDir); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"generate", "check", "plan", "apply"} {
		if _, err := mp.RunGenerator(context.Background(), c, action); err == nil || !strings.Contains(err.Error(), "没有 JSON") {
			t.Fatalf("%s 未优先校验 JSON: %v", action, err)
		}
	}
}

func TestRunGeneratorPlanAndApply(t *testing.T) {
	db := testutil.Database(t)
	c := syncConfig(t)
	c.MongoDB = mp.Config{URI: os.Getenv("MONGOPLUS_TEST_URI"), Database: db.Name()}
	ctx := context.Background()
	changes, err := mp.RunGenerator(ctx, c, "plan")
	if err != nil || len(changes) != 1 || !changes[0].Create {
		t.Fatalf("plan: %v %v", changes, err)
	}
	if names, err := db.ListCollectionNames(ctx, bson.D{}); err != nil || len(names) != 0 {
		t.Fatalf("plan 改变了数据库: %v %v", names, err)
	}
	changes, err = mp.RunGenerator(ctx, c, "apply")
	if err != nil || len(changes) != 1 {
		t.Fatalf("apply: %v %v", changes, err)
	}
	if changes, err := mp.RunGenerator(ctx, c, "plan"); err != nil || len(changes) != 0 {
		t.Fatalf("同步不幂等: %v %v", changes, err)
	}
	if _, err := os.Stat(c.ModelPkgPath); !os.IsNotExist(err) {
		t.Fatal("plan/apply 不应生成文件")
	}
}
