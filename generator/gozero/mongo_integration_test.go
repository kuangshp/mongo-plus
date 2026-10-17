package gozero

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/kuangshp/mongo-plus/generator"
	"github.com/kuangshp/mongo-plus/internal/settings"
	"github.com/kuangshp/mongo-plus/schema"
)

func TestGoctlMongoServiceContext(t *testing.T) {
	if os.Getenv("MONGOPLUS_TEST_URI") == "" {
		t.Skip("设置 MONGOPLUS_TEST_URI 运行 ServiceContext MongoDB 测试")
	}
	dir := newServiceFixture(t, "syntax = \"v1\"\nservice sample-api {\n @handler Ping\n get /ping\n}\n")
	for name, content := range map[string]string{
		"schema/user.json": `{"fields":[{"name":"name","type":"string"}]}`,
	} {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := schema.Config{ProjectDir: dir, Package: "servicefixture", ModelPkgPath: "internal/dal/entity", OutPath: "internal/dal/dao", RepoPath: "internal/dal/repository", ID: settings.IDConfig{Type: settings.IDTypeInt64, Generation: settings.IDGenerationManual}, SoftDelete: schema.Bool(false), Timestamps: schema.Bool(false)}
	if _, err := generator.Generate(cfg); err != nil {
		t.Fatal(err)
	}
	source := `package svc
import (
 "bytes"
 "context"
 "errors"
 "os"
 "strings"
 "testing"
 "time"
 "servicefixture/internal/config"
 model "servicefixture/internal/dal/entity"
 "servicefixture/internal/dal/repository"
 mp "github.com/kuangshp/mongo-plus"
 "github.com/kuangshp/mongo-plus/field"
 "go.mongodb.org/mongo-driver/v2/bson"
 "go.mongodb.org/mongo-driver/v2/mongo"
 "github.com/zeromicro/go-zero/core/conf"
)
func TestGeneratedMongoLifecycle(t *testing.T) {
 previous:=mp.DefaultDB()
 defer mp.Use(previous)
 mp.Use(nil)
 disabled:=NewServiceContext(config.Config{})
 if disabled.MongoDB!=nil {t.Fatal("disabled MongoDB created a client")}
 if mp.DefaultDB()!=nil {t.Fatal("disabled MongoDB registered a default connection")}
 if err:=disabled.Close();err!=nil {t.Fatal(err)}
 var c config.Config
 c.MongoConfig=config.MongoConfig{Enabled:true,URI:os.Getenv("MONGOPLUS_TEST_URI"),Database:"mongo_plus_test_gozero_"+bson.NewObjectID().Hex(),MaxPoolSize:10,TimeoutSeconds:10}
 var logs bytes.Buffer
 c.MongoConfig.Log=mp.LogConfig{Level:mp.LogInfo,Writer:&logs}
 s:=NewServiceContext(c)
 defer s.Close()
 if mp.DefaultDB()!=s.MongoDB {t.Fatal("ServiceContext did not register the default connection")}
 disabled=NewServiceContext(config.Config{})
 if disabled.MongoDB!=nil || mp.DefaultDB()!=s.MongoDB {t.Fatal("disabled ServiceContext replaced the active default connection")}
 invalid:=c
 invalid.MongoConfig.URI="://invalid"
 func(){defer func(){if recover()==nil {t.Fatal("invalid MongoDB connection accepted")}}();NewServiceContext(invalid)}()
 if mp.DefaultDB()!=s.MongoDB {t.Fatal("failed MongoDB connection replaced the active default connection")}
 ctx,cancel:=context.WithTimeout(context.Background(),10*time.Second);defer cancel()
 r:=repository.NewUserRepository()
 if r.Native().Database().Name()!=s.MongoDB.Native().Name() {t.Fatal("parameterless repository did not use the service database")}
 explicitDB:=mp.NewDatabase(s.MongoDB.Native().Client().Database(s.MongoDB.Native().Name()+"_explicit"))
 explicit:=repository.NewUserRepository(explicitDB)
 if explicit.Native().Database().Name()!=explicitDB.Native().Name() || mp.DefaultDB()!=s.MongoDB {t.Fatal("explicit repository connection did not override the default independently")}
 if err:=r.Create(ctx,&model.UserEntity{ID:1,Name:"service context"});err!=nil {t.Fatal(err)}
 if !strings.Contains(logs.String(),".insert") || !strings.Contains(logs.String(),"[rows:1]") {t.Fatalf("command log not configured: %s",&logs)}
 var got model.UserEntity
 if err:=r.WithContext(ctx).Where(field.New[int64]("_id").Eq(1)).ScanOne(&got);err!=nil||got.Name!="service context" {t.Fatalf("repository injection failed: %+v %v",got,err)}
 if err:=s.MongoDB.Native().Drop(ctx);err!=nil {t.Fatal(err)}
 if err:=s.Close();err!=nil {t.Fatal(err)}
 if err:=s.MongoDB.Native().Client().Ping(ctx,nil);!errors.Is(err,mongo.ErrClientDisconnected) {t.Fatalf("Close did not disconnect client: %v",err)}
}
func TestGeneratedCommandLogYAML(t *testing.T) {
 data,err:=os.ReadFile("../../etc/sample-api.yaml")
 if err!=nil {t.Fatal(err)}
 data=bytes.Replace(data,[]byte("level: silent"),[]byte("level: info"),1)
 var c config.Config
 if err:=conf.LoadFromYamlBytes(data,&c);err!=nil {t.Fatal(err)}
 if c.MongoConfig.Log.Level!=mp.LogInfo || c.MongoConfig.Log.Format!="shell" || c.MongoConfig.Log.SlowThresholdMS!=200 {t.Fatalf("command log YAML not loaded: %+v",c.MongoConfig.Log)}
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
