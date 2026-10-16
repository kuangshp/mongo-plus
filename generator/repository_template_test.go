package generator

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRepositoryExplicitMethodsAndBusinessOverrides(t *testing.T) {
	c, dir := fixture(t)
	if err := os.WriteFile(filepath.Join(c.SchemaDir, "user.json"), []byte(`{"fields":[{"name":"username","type":"string"}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(c); err != nil {
		t.Fatal(err)
	}
	// 模拟旧项目已有业务扩展和注入依赖；升级只重写 _gen.go。
	extensionPath := filepath.Join(c.RepoPath, "user.go")
	extension, err := os.ReadFile(extensionPath)
	if err != nil {
		t.Fatal(err)
	}
	custom := strings.Replace(string(extension), "*defaultUserRepository", "*defaultUserRepository\n\tdependency *string", 1)
	if err := os.WriteFile(extensionPath, []byte(custom), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(c); err != nil {
		t.Fatal(err)
	}
	usage := fmt.Sprintf(`package repository

import (
    "context"
    "errors"
    "fmt"
    "go/ast"
    "go/parser"
    "go/token"
    "reflect"
    "os"
    "testing"
    "time"
    mongoplus "github.com/kuangshp/mongo-plus"
    model %q
    "go.mongodb.org/mongo-driver/v2/mongo"
    "go.mongodb.org/mongo-driver/v2/bson"
    "go.mongodb.org/mongo-driver/v2/mongo/options"
)

var _ mongoplus.Contract[model.UserEntity, int64] = (*defaultUserRepository)(nil)
var _ mongoplus.Contract[model.UserEntity, int64] = (UserRepository)(nil)

func TestExplicitPublicContract(t *testing.T) {
    file, err := parser.ParseFile(token.NewFileSet(), "user_gen.go", nil, 0)
    if err != nil { t.Fatal(err) }
    signatures, implementations := map[string]bool{}, map[string]bool{}
    for _, decl := range file.Decls {
        switch decl := decl.(type) {
        case *ast.GenDecl:
            for _, spec := range decl.Specs {
                typ, ok := spec.(*ast.TypeSpec)
                if !ok || typ.Name.Name != "iDefaultUserRepository" { continue }
                for _, method := range typ.Type.(*ast.InterfaceType).Methods.List {
                    for _, name := range method.Names { signatures[name.Name] = true }
                }
            }
        case *ast.FuncDecl:
            if decl.Recv == nil { continue }
            recv, ok := decl.Recv.List[0].Type.(*ast.StarExpr)
            if !ok { continue }
            name, ok := recv.X.(*ast.Ident)
            if ok && name.Name == "defaultUserRepository" { implementations[decl.Name.Name] = true }
        }
    }
    contract := reflect.TypeOf((*mongoplus.Contract[model.UserEntity,int64])(nil)).Elem()
	if !signatures["SensitiveEq"] || !implementations["SensitiveEq"] {
		t.Fatal("敏感字段条件必须通过生成的 Repository 接口可用")
	}
    for i:=0;i<contract.NumMethod();i++ {
        method:=contract.Method(i)
        if method.PkgPath!="" { continue }
        if !signatures[method.Name] || !implementations[method.Name] {
            t.Errorf("公共方法没有显式生成接口或实现: %%s",method.Name)
        }
    }
}

// 与 gorm-plus 一样，在业务结构体上声明相同签名即可覆盖基础方法。
func (r *customerUserRepository) FindOne(ctx context.Context, query ...mongoplus.QueryOption) (*model.UserEntity,error) {
    if r.dependency != nil { return &model.UserEntity{Username:*r.dependency},nil }
    return &model.UserEntity{Username:"overridden"},nil
}

func TestDefaultConnectionAndOverride(t *testing.T) {
    client,err:=mongo.Connect(options.Client().ApplyURI("mongodb://127.0.0.1:1"))
    if err!=nil { t.Fatal(err) }
    defer client.Disconnect(context.Background())
    db:=mongoplus.NewDatabase(client.Database("default"))
    mongoplus.Use(db)
    defer mongoplus.Use(nil)
    users:=NewUserRepository()
    got,err:=users.FindOne(context.Background())
    if err!=nil || got.Username!="overridden" { t.Fatalf("业务覆盖未生效: %%+v %%v",got,err) }
    if users.Native().Database().Name()!="default" { t.Fatal("无参构造没有使用默认连接") }
    explicit:=NewUserRepository(mongoplus.NewDatabase(client.Database("explicit")))
    if explicit.Native().Database().Name()!="explicit" { t.Fatal("显式连接没有优先于默认连接") }
    name:="injected"
    custom:=users.(*customerUserRepository)
    custom.dependency=&name
    bound:=users.WithTx(nil)
    got,err=bound.FindOne(context.Background())
    if err!=nil || got.Username!=name { t.Fatalf("绑定丢失业务覆盖或依赖: %%+v %%v",got,err) }
    if bound==users || custom.Repository==bound.(*customerUserRepository).Repository { t.Fatal("绑定未复制仓储") }
    if _,err:=bound.Count(context.Background()); !errors.Is(err,mongoplus.ErrUnmanagedTransaction) { t.Fatalf("无效绑定静默回退: %%v",err) }
}

func TestGeneratedTransactionBinding(t *testing.T) {
    uri:=os.Getenv("MONGOPLUS_TEST_URI")
    if uri=="" { t.Skip("需要临时 MongoDB 副本集") }
    ctx,cancel:=context.WithTimeout(context.Background(),20*time.Second)
    defer cancel()
    db,err:=mongoplus.Connect(ctx,mongoplus.Config{URI:uri,Database:"mongo_plus_generated_"+bson.NewObjectID().Hex()})
    if err!=nil { t.Fatal(err) }
    defer db.Close(context.Background())
    defer db.Native().Drop(context.Background())
    users:=NewUserRepository(db)
    if err:=users.Create(ctx,&model.UserEntity{Username:"seed"});err!=nil { t.Fatal(err) }
    name:="injected"
    users.(*customerUserRepository).dependency=&name
    rollback:=errors.New("rollback")
    var escaped UserRepository
    err=mongoplus.TransactionCtx(ctx,db,func(tx context.Context)error{
        escaped=users.WithTx(tx)
        row:=&model.UserEntity{Username:"pending"}
        if err:=escaped.Create(ctx,row);err!=nil{return err}
        if got,err:=escaped.FindById(ctx,row.ID);err!=nil||got.Username!="pending" { return fmt.Errorf("事务读取失败: %%+v %%v",got,err) }
        if n,err:=users.Count(ctx);err!=nil||n!=1{return fmt.Errorf("原仓储受影响: %%d %%v",n,err)}
        if got,err:=escaped.FindOne(ctx);err!=nil||got.Username!=name{return fmt.Errorf("业务扩展丢失: %%+v %%v",got,err)}
        return rollback
    })
    if !errors.Is(err,rollback){t.Fatal(err)}
    if n,err:=users.Count(ctx);err!=nil||n!=1{t.Fatalf("未回滚: %%d %%v",n,err)}
    if _,err:=escaped.Count(ctx);!errors.Is(err,mongoplus.ErrTxDone){t.Fatalf("生命周期检查失败: %%v",err)}
}
`, c.ImportPath("model"))
	if err := os.WriteFile(filepath.Join(c.RepoPath, "repository_contract_test.go"), []byte(usage), 0600); err != nil {
		t.Fatal(err)
	}
	compileFixture(t, dir)
}
