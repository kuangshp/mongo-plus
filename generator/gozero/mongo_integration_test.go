package gozero

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestGoctlMongoServiceContext(t *testing.T) {
	if os.Getenv("MONGOPLUS_TEST_URI") == "" {
		t.Skip("设置 MONGOPLUS_TEST_URI 运行 ServiceContext MongoDB 测试")
	}
	dir := newServiceFixture(t, "syntax = \"v1\"\nservice sample-api {\n @handler Ping\n get /ping\n}\n")
	source := `package svc
import (
 "context"
 "errors"
 "os"
 "testing"
 "time"
 "servicefixture/internal/config"
 mp "github.com/kuangshp/mongo-plus"
 "github.com/kuangshp/mongo-plus/field"
 "go.mongodb.org/mongo-driver/v2/bson"
 "go.mongodb.org/mongo-driver/v2/mongo"
)
type testUser struct { ID int64 ` + "`bson:\"_id\"`" + `; Name string ` + "`bson:\"name\"`" + ` }
func TestGeneratedMongoLifecycle(t *testing.T) {
 disabled:=NewServiceContext(config.Config{})
 if disabled.MongoDB!=nil {t.Fatal("disabled MongoDB created a client")}
 if err:=disabled.Close();err!=nil {t.Fatal(err)}
 var c config.Config
 c.MongoConfig=config.MongoConfig{Enabled:true,URI:os.Getenv("MONGOPLUS_TEST_URI"),Database:"mongo_plus_test_gozero_"+bson.NewObjectID().Hex(),MaxPoolSize:10,TimeoutSeconds:10}
 s:=NewServiceContext(c)
 defer s.Close()
 ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel()
 r:=mp.MustRepository[testUser,int64](s.MongoDB,mp.CollectionConfig{Name:"users",ID:mp.IDConfig{Type:"int64",Generation:"manual"}})
 if err:=r.Create(ctx,&testUser{ID:1,Name:"service context"});err!=nil {t.Fatal(err)}
 var got testUser
 if err:=r.WithContext(ctx).Where(field.New[int64]("_id").Eq(1)).ScanOne(&got);err!=nil||got.Name!="service context" {t.Fatalf("repository injection failed: %+v %v",got,err)}
 if err:=s.MongoDB.Native().Drop(ctx);err!=nil {t.Fatal(err)}
 if err:=s.Close();err!=nil {t.Fatal(err)}
 if err:=s.MongoDB.Native().Client().Ping(ctx,nil);!errors.Is(err,mongo.ErrClientDisconnected) {t.Fatalf("Close did not disconnect client: %v",err)}
}
func TestInvalidMongoTimeoutFailsBeforeConnecting(t *testing.T) {
 for _,timeout:=range []int64{-1,0,3601} {
  func(){defer func(){if recover()==nil {t.Fatal("invalid timeout accepted")}}();var c config.Config;c.MongoConfig.Enabled=true;c.MongoConfig.TimeoutSeconds=timeout;NewServiceContext(c)}()
 }
}
`
	if err := os.WriteFile(filepath.Join(dir, "internal/svc/mongo_test.go"), []byte(source), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.Command("go", "test", "-mod=mod", "-race", "./internal/svc")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("生成的 MongoDB ServiceContext 失败: %v\n%s", err, output)
	}
}
