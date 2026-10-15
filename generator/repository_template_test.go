package generator

import (
	"fmt"
	"os"
	"path/filepath"
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
	usage := fmt.Sprintf(`package repository

import (
    "context"
    "go/ast"
    "go/parser"
    "go/token"
    "reflect"
    "testing"
    mongoplus "github.com/kuangshp/mongo-plus"
    model %q
    "go.mongodb.org/mongo-driver/v2/mongo"
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
}
`, c.ImportPath("model"))
	if err := os.WriteFile(filepath.Join(c.RepoPath, "repository_contract_test.go"), []byte(usage), 0600); err != nil {
		t.Fatal(err)
	}
	compileFixture(t, dir)
}
