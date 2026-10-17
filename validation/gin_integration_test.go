package validation

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuangshp/mongo-plus/generator"
	"github.com/kuangshp/mongo-plus/internal/settings"
	"github.com/kuangshp/mongo-plus/schema"
)

// TestGinIntegration 在临时模块中编译文档原文，验证真实 Gin 绑定只校验一次。
// Gin 仅供该临时模块使用，不成为 mongo-plus 的依赖。
func TestGinIntegration(t *testing.T) {
	if os.Getenv("MONGOPLUS_GIN_INTEGRATION") != "1" {
		t.Skip("set MONGOPLUS_GIN_INTEGRATION=1 to run Gin integration")
	}
	root, err := filepath.Abs("..")
	if err != nil {
		t.Fatal(err)
	}
	document, err := os.ReadFile(filepath.Join(root, "docs/validation.md"))
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":              fmt.Sprintf("module flycos\n\ngo 1.25.5\n\nrequire (\n github.com/gin-gonic/gin v1.12.0\n github.com/kuangshp/mongo-plus v0.0.0\n)\nreplace github.com/kuangshp/mongo-plus => %s\n", root),
		"validators/rules.go": ginProjectRules,
		"gin_test.go":         ginBindingTests,
	}
	for _, name := range []string{"utils/validator.go", "initialize/validate.go"} {
		_, rest, ok := strings.Cut(string(document), "<!-- gin-example: "+name+" -->")
		if !ok {
			t.Fatalf("missing documented example %s", name)
		}
		_, rest, ok = strings.Cut(rest, "```go\n")
		if !ok {
			t.Fatalf("missing Go code for %s", name)
		}
		source, _, ok := strings.Cut(rest, "\n```")
		if !ok {
			t.Fatalf("unterminated Go example %s", name)
		}
		files[name] = source + "\n"
	}
	for name, source := range files {
		path := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	// 使用真实生成器输出 DTO，再编译 README 的 Gin 控制器，避免手写替代标签。
	if err := os.Mkdir(filepath.Join(dir, "schema"), 0700); err != nil {
		t.Fatal(err)
	}
	for name, definition := range map[string]string{
		"user":  `{"fields":[{"name":"username","type":"string","required":true},{"name":"status","type":"int64"}]}`,
		"flags": `{"fields":[{"name":"status","type":"int64","required":true},{"name":"enabled","type":"bool","required":true},{"name":"profile","type":"object","fields":[{"name":"city","type":"string","required":true}]},{"name":"tags","type":"[]object","fields":[{"name":"name","type":"string","required":true}]},{"name":"owner","type":"objectId"},{"name":"joined_at","type":"datetime"}]}`,
	} {
		if err := os.WriteFile(filepath.Join(dir, "schema", name+".json"), []byte(definition), 0600); err != nil {
			t.Fatal(err)
		}
	}
	cfg := schema.Config{ProjectDir: dir, Package: "flycos", ModelPkgPath: "internal/dal/entity", OutPath: "internal/dal/dao", RepoPath: "internal/dal/repository", DtoPath: "internal/dal/dto", VoPath: "internal/dal/vo", ID: settings.IDConfig{Type: settings.IDTypeInt64}}
	if _, err := generator.Generate(cfg); err != nil {
		t.Fatal(err)
	}
	readme, err := os.ReadFile(filepath.Join(root, "README.md"))
	if err != nil {
		t.Fatal(err)
	}
	_, controllerBlock, ok := strings.Cut(string(readme), "<!-- gin-dto-controller -->")
	if !ok {
		t.Fatal("缺少 README Gin 控制器示例")
	}
	_, controllerBlock, ok = strings.Cut(controllerBlock, "```go\n")
	if !ok {
		t.Fatal("缺少 Gin 控制器 Go 代码块")
	}
	controller, _, ok := strings.Cut(controllerBlock, "\n```")
	if !ok {
		t.Fatal("不完整的 Gin 控制器代码块")
	}
	apiDir := filepath.Join(dir, "internal/api/user")
	if err := os.MkdirAll(apiDir, 0700); err != nil {
		t.Fatal(err)
	}
	for path, source := range map[string]string{
		filepath.Join(apiDir, "user.go"):            strings.ReplaceAll(controller, "example.com/shop", "flycos"),
		filepath.Join(dir, "utils/response.go"):     ginProjectResponses,
		filepath.Join(dir, "generated_dto_test.go"): ginGeneratedDTOTests,
	} {
		if err := os.WriteFile(path, []byte(source), 0600); err != nil {
			t.Fatal(err)
		}
	}
	command := exec.Command("go", "test", "-mod=mod", "-race", "./...")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("documented Gin integration failed: %v\n%s", err, output)
	}
}

const ginProjectResponses = `package utils
import (
 "net/http"
 "github.com/gin-gonic/gin"
)
func Success(ctx *gin.Context, data any) { ctx.JSON(http.StatusOK,gin.H{"code":0,"message":"请求成功","result":data}) }
func Fail(ctx *gin.Context, message string) { ctx.JSON(http.StatusOK,gin.H{"code":1,"message":message,"result":nil}) }
`

const ginGeneratedDTOTests = `package fixture
import (
 "context"
 "encoding/json"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
 userapi "flycos/internal/api/user"
 "flycos/internal/dal/dto"
 "flycos/internal/dal/entity"
 "flycos/internal/dal/repository"
 "flycos/initialize"
 "flycos/utils"
 "github.com/gin-gonic/gin"
)
type requestKey struct{}
type fakeUserRepository struct {
 repository.UserRepository
 calls int
 username string
 status int64
 contextValue any
}
func (r *fakeUserRepository) Create(ctx context.Context, record *entity.UserEntity) error {
 r.calls++;r.username=record.Username;r.status=record.Status;r.contextValue=ctx.Value(requestKey{})
 record.ID=42
 return nil
}
func TestGeneratedDTOAndREADMEController(t *testing.T) {
 gin.SetMode(gin.TestMode)
 if err:=initialize.InitValidate();err!=nil {t.Fatal(err)}
 repo:=&fakeUserRepository{}
 api:=userapi.NewUser(repo)
 router:=gin.New()
 router.POST("/users",api.Create)
 router.POST("/flags",func(ctx *gin.Context){
  var req dto.CreateFlagsDTO
  if err:=ctx.ShouldBindJSON(&req);err!=nil {utils.Fail(ctx,utils.ShowErrorMessage(err));return}
  utils.Success(ctx,req)
 })
 router.POST("/modify",func(ctx *gin.Context){
  var req dto.ModifyUserDTO
  if err:=ctx.ShouldBindJSON(&req);err!=nil {utils.Fail(ctx,utils.ShowErrorMessage(err));return}
  utils.Success(ctx,req)
 })
 send:=func(path,body string) map[string]any {
  request:=httptest.NewRequest(http.MethodPost,path,strings.NewReader(body))
  request.Header.Set("Content-Type","application/json")
  request=request.WithContext(context.WithValue(request.Context(),requestKey{},"request-context"))
  result:=httptest.NewRecorder();router.ServeHTTP(result,request)
  if result.Code!=http.StatusOK {t.Fatalf("错误响应状态: %d %s",result.Code,result.Body.String())}
  var response map[string]any
  if err:=json.Unmarshal(result.Body.Bytes(),&response);err!=nil {t.Fatal(err)}
  return response
 }
 if out:=send("/users","{\"username\":123,\"status\":\"9007199254740993\"}");out["code"]!=float64(0) {t.Fatal(out)}
 if repo.calls!=1||repo.username!="123"||repo.status!=9007199254740993||repo.contextValue!="request-context" {t.Fatalf("DTO 转换、仓储注入或上下文错误: %+v",repo)}
 for _,body:=range []string{"{}","{\"username\":null}","{\"username\":\"u\",\"status\":\"bad\"}","{\"username\":\"u\",\"status\":\"9223372036854775808\"}","{"} {
  out:=send("/users",body)
  if out["code"]!=float64(1)||out["message"]=="" {t.Fatalf("请求绑定/校验未生效: %s %v",body,out)}
 }
 if repo.calls!=1 {t.Fatal("错误请求仍调用仓储")}
 out:=send("/users","{}")
 if !strings.Contains(out["message"].(string),"username")||!strings.Contains(out["message"].(string),"必填") {t.Fatalf("中文必填提示未生效: %v",out)}
 if out:=send("/flags","{\"status\":\"0\",\"enabled\":false,\"tags\":[],\"owner\":\"507f1f77bcf86cd799439011\",\"joined_at\":\"project-time\"}");out["code"]!=float64(0) {t.Fatalf("合法 0/false 或自定义校验被拒绝: %v",out)}
 for _,item:=range []struct{body,field string}{
  {"{\"enabled\":false}","status"},
  {"{\"status\":0}","enabled"},
  {"{\"status\":0,\"enabled\":false,\"profile\":{}}","city"},
  {"{\"status\":0,\"enabled\":false,\"tags\":[{}]}","name"},
  {"{\"status\":0,\"enabled\":false,\"owner\":\"bad\"}","owner"},
  {"{\"status\":0,\"enabled\":false,\"joined_at\":\"bad\"}","joined_at"},
 } {
  out:=send("/flags",item.body)
  if out["code"]!=float64(1)||!strings.Contains(out["message"].(string),item.field) {t.Fatalf("嵌套/数组/自定义字段校验失效: %s %v",item.body,out)}
 }
 if out:=send("/modify","{\"id\":\"12\",\"username\":\"u\",\"status\":\"0\"}");out["code"]!=float64(0)||out["result"].(map[string]any)["id"]!=float64(12) {t.Fatalf("修改 DTO ID 转换失败: %v",out)}
 if out:=send("/modify","{\"username\":\"u\"}");out["code"]!=float64(1)||!strings.Contains(out["message"].(string),"id") {t.Fatalf("修改 DTO ID 未校验: %v",out)}
}
`

// 以下项目规则仅用于验证文档装配和同名覆盖，不复制参考项目的业务实现。
const ginProjectRules = `package validators
import (
 "sync/atomic"
 "github.com/go-playground/validator/v10"
)
var MobileCalls atomic.Int64
func ValidateMobile(fl validator.FieldLevel) bool { MobileCalls.Add(1); return fl.Field().String()=="project-phone" }
func ValidateEmail(fl validator.FieldLevel) bool { return fl.Field().String()=="project-email" }
func ValidateDate(fl validator.FieldLevel) bool { return fl.Field().String()=="project-date" }
func ValidateDateTime(fl validator.FieldLevel) bool { return fl.Field().String()=="project-time" }
func ValidateName(fl validator.FieldLevel) bool { return fl.Field().String()=="project-name" }
func ValidateId(fl validator.FieldLevel) bool { return fl.Field().String()=="project-id" }
func ValidateCronSpec(fl validator.FieldLevel) bool { return fl.Field().String()=="project-cron" }
`

const ginBindingTests = "package fixture\n" + `
import (
 "encoding/json"
 "errors"
 "fmt"
 "net/http"
 "net/http/httptest"
 "strings"
 "testing"
 "flycos/initialize"
 "flycos/utils"
 "flycos/validators"
 "github.com/gin-gonic/gin"
 "github.com/kuangshp/mongo-plus/validation"
)
type Request struct {
 Mobile string ` + "`json:\"mobile\" binding:\"required,mobile\"`" + `
 Date string ` + "`json:\"date\" binding:\"required,date\"`" + `
 Time string ` + "`json:\"time\" binding:\"required,dateTime\"`" + `
 Email string ` + "`json:\"email\" binding:\"required,myEmail\"`" + `
 Name string ` + "`json:\"name\" binding:\"required,checkName\"`" + `
 ID string ` + "`json:\"id\" binding:\"required,chineseId\"`" + `
 Cron string ` + "`json:\"cron\" binding:\"omitempty,cronspec\"`" + `
 Common string ` + "`json:\"common\" binding:\"required,checkMobile\"`" + `
 Ignored string ` + "`json:\"ignored\" validate:\"required\"`" + `
}
func TestDocumentedGinBinding(t *testing.T) {
 gin.SetMode(gin.TestMode)
 if err:=initialize.InitValidate();err!=nil {t.Fatal(err)}
 // 重复启动调用不能重复添加翻译，也不覆盖第一份规则。
 if err:=utils.InitValidation(validation.Rule{Tag:"mobile",Message:"bad",Validate:validators.ValidateEmail});err!=nil {t.Fatal(err)}
 router:=gin.New()
 var lastErr error
 router.POST("/users",func(ctx *gin.Context){
  var req Request
  if err:=ctx.ShouldBindJSON(&req);err!=nil {
   lastErr=err
   ctx.JSON(http.StatusOK,gin.H{"code":1,"message":utils.ShowErrorMessage(err)})
   return
  }
  ctx.JSON(http.StatusOK,gin.H{"code":0})
 })
 valid:=map[string]string{"mobile":"project-phone","date":"project-date","time":"project-time","email":"project-email","name":"project-name","id":"project-id","cron":"project-cron","common":"13800138000"}
 send:=func(body string) map[string]any {
  req:=httptest.NewRequest(http.MethodPost,"/users",strings.NewReader(body));req.Header.Set("Content-Type","application/json")
  w:=httptest.NewRecorder();router.ServeHTTP(w,req)
  var out map[string]any
  if err:=json.Unmarshal(w.Body.Bytes(),&out);err!=nil {t.Fatal(err)}
  return out
 }
 body,_:=json.Marshal(valid)
 validators.MobileCalls.Store(0)
 if out:=send(string(body));out["code"]!=float64(0) {t.Fatal(out)}
 if got:=validators.MobileCalls.Load();got!=1 {t.Fatalf("validated %d times, want once",got)}
 for field,message:=range map[string]string{
  "mobile":"mobile 非法的手机号码!","date":"date 时间格式非法必须为2006-01-02格式!",
  "time":"time 时间格式非法必须为2006-01-02 15:04:05格式!","email":"email 邮箱号码非法!",
  "name":"name用户名长度必须是3-12位字符","id":"id身份证号码格式错误",
  "cron":"cron定时规则格式非法，需为6段cron表达式(秒 分 时 日 月 周)",
 } {
  original:=valid[field];valid[field]="invalid";body,_:=json.Marshal(valid)
  out:=send(string(body));valid[field]=original
  if out["code"]!=float64(1)||out["message"]!=message {t.Fatalf("%s: %v",field,out)}
  wrapped:=fmt.Errorf("wrapped: %w",lastErr)
  if utils.ShowErrorMessage(wrapped)!=message {t.Fatal("wrapped validation error lost translation")}
 }
 // required 由 Gin 的 binding 引擎执行，且没有 validate 标签的二次校验。
 if out:=send("{}");out["code"]!=float64(1)||!strings.Contains(out["message"].(string),"mobile") {t.Fatal(out)}
 send("{")
 if utils.ShowErrorMessage(lastErr)!=lastErr.Error() {t.Fatal("JSON parse error was translated as validation error")}
 parseErr:=errors.New("bad input")
 if utils.ShowErrorMessage(parseErr)!="bad input"||utils.ShowErrorMessage(nil)!="" {t.Fatal("unexpected generic error translation")}
}
`
