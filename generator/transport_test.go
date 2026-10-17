package generator

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kuangshp/mongo-plus/schema"
)

func TestDTOAndVOGeneration(t *testing.T) {
	c, dir := fixture(t)
	c.DtoPath, c.VoPath = "internal/requests", "internal/views"
	c.Tenant = &schema.Tenant{Name: "tenant_id", Type: "int64"}
	for name, definition := range map[string]string{
		"user":      `{"fields":[{"name":"username","type":"string","required":true},{"name":"status","type":"int64","required":true},{"name":"enabled","type":"bool","required":true},{"name":"tags","type":"[]string","required":true},{"name":"profile","type":"object","fields":[{"name":"city","type":"string","required":true}]},{"name":"items","type":"[]object","fields":[{"name":"price","type":"decimal128"},{"name":"owner","type":"objectId"}]},{"name":"joined_at","type":"datetime"},{"name":"amount","type":"decimal128"},{"name":"extra","type":"json"},{"name":"maybe","type":"int64","nullable":true}]}`,
		"event":     `{"id":{"type":"objectId"},"fields":[]}`,
		"token":     `{"id":{"type":"string"},"fields":[]}`,
		"manual":    `{"id":{"type":"int64","generation":"manual"},"fields":[]}`,
		"manualoid": `{"id":{"type":"objectId","generation":"manual"},"fields":[]}`,
	} {
		if err := os.WriteFile(filepath.Join(c.SchemaDir, name+".json"), []byte(definition), 0600); err != nil {
			t.Fatal(err)
		}
	}
	files, err := Generate(c)
	if err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if !strings.Contains(file, "internal/") {
			continue
		}
		data, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		text := string(data)
		if strings.Contains(file, "requests") {
			for _, forbidden := range []string{"CreatedAt", "UpdatedAt", "DeletedAt", "TenantID"} {
				if strings.Contains(text, forbidden) {
					t.Fatalf("DTO 暴露自动维护字段 %s: %s", forbidden, file)
				}
			}
		} else if strings.Contains(text, "DeletedAt") {
			t.Fatalf("VO 暴露逻辑删除字段: %s", file)
		}
		custom := append(data, []byte("\n// 保留自定义业务字段和备注。\n")...)
		if err := os.WriteFile(file, custom, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := Generate(c); err != nil {
		t.Fatal(err)
	}
	for _, file := range files {
		if strings.Contains(file, "internal/") {
			data, _ := os.ReadFile(file)
			if !strings.Contains(string(data), "保留自定义业务字段") {
				t.Fatalf("覆盖 DTO/VO: %s", file)
			}
		}
	}
	// 在独立消费者模块执行真实 JSON 解码、参数校验与 BSON 结果映射。
	test := "package fixture_test\n" + `
import (
 "encoding/json"
 "reflect"
 "testing"
 "time"
 dto "example.com/fixture/internal/requests"
 vo "example.com/fixture/internal/views"
 "github.com/kuangshp/mongo-plus/validation"
 "go.mongodb.org/mongo-driver/v2/bson"
)
func TestDTO(t *testing.T) {
 var req dto.ModifyUserDTO
 data := []byte("{\"id\":\"9007199254740993\",\"username\":123,\"status\":\"0\",\"enabled\":false,\"tags\":[],\"amount\":12345678901234567890.123456789,\"items\":[{\"price\":12.20,\"owner\":\"507f1f77bcf86cd799439011\"}],\"joined_at\":\"2026-10-15 13:14:15\"}")
 if err := json.Unmarshal(data, &req); err != nil {t.Fatal(err)}
 if req.ID != 9007199254740993 || req.Username != "123" || req.Status == nil || *req.Status != 0 || req.Enabled == nil || *req.Enabled || req.Amount != "12345678901234567890.123456789" || req.Items[0].Price != "12.20" {t.Fatalf("转换错误: %+v",req)}
 if err := validation.Validate(&req); err != nil {t.Fatal(err)}
 req.Status = nil
 if err := validation.Validate(&req); err == nil {t.Fatal("漏检必填数字")}
 var absent dto.ModifyUserDTO
 if err := json.Unmarshal([]byte("{\"username\":\"u\",\"status\":0,\"enabled\":false,\"tags\":[]}"), &absent); err != nil {t.Fatal(err)}
 if err := validation.Validate(&absent); err == nil {t.Fatal("修改 ID 未校验")}
 var nested dto.CreateUserDTO
 if err := json.Unmarshal([]byte("{\"username\":\"u\",\"status\":0,\"enabled\":false,\"tags\":[],\"profile\":{}}"), &nested); err != nil {t.Fatal(err)}
 if err := validation.Validate(&nested); err == nil {t.Fatal("漏检嵌套必填字段")}
 var event dto.ModifyEventDTO
 if err := json.Unmarshal([]byte("{\"id\":\"507f1f77bcf86cd799439011\"}"), &event); err != nil {t.Fatal(err)}
 if err := validation.Validate(&event); err != nil {t.Fatal(err)}
 event.ID = "broken"
 if err := validation.Validate(&event); err == nil {t.Fatal("ObjectID 未校验")}
 for _, item := range []any{dto.CreateTokenDTO{}, dto.CreateManualDTO{}, dto.CreateManualoidDTO{}} {
  if err := validation.Validate(item); err == nil {t.Fatalf("手动主键未校验: %T",item)}
 }
 for _, item := range []any{dto.CreateUserDTO{}, dto.CreateEventDTO{}} {
  if _, found := reflect.TypeOf(item).FieldByName("ID"); found {t.Fatalf("新增请求暴露自动主键: %T",item)}
 }
}
func TestVO(t *testing.T) {
 id := bson.NewObjectID()
 amount, err := bson.ParseDecimal128("12345678901234567890.123456789")
 if err != nil {t.Fatal(err)}
 now := time.Now().Truncate(time.Millisecond)
 data, err := bson.Marshal(bson.M{"_id":int64(42),"username":"u","profile":bson.M{"city":"上海"},"items":bson.A{bson.M{"price":amount,"owner":id}},"created_at":now,"tenant_id":int64(7)})
 if err != nil {t.Fatal(err)}
 var row vo.UserVO
 if err := bson.Unmarshal(data,&row); err != nil {t.Fatal(err)}
 if row.ID != 42 || row.Profile.City != "上海" || row.Items[0].Price != amount || row.Items[0].Owner != id || !row.CreatedAt.Equal(now) || row.TenantID != 7 {t.Fatalf("BSON 映射错误: %+v",row)}
 var event vo.EventVO
 data, _ = bson.Marshal(bson.M{"_id":id})
 if err := bson.Unmarshal(data,&event); err != nil || event.ID != id {t.Fatalf("VO 主键类型错误: %+v %v",event,err)}
}
`
	if err := os.WriteFile(filepath.Join(dir, "transport_test.go"), []byte(test), 0600); err != nil {
		t.Fatal(err)
	}
	compileFixture(t, dir)
}

func TestTransportForJSONModelAndOptIn(t *testing.T) {
	c, dir := fixture(t)
	if err := os.WriteFile(filepath.Join(c.SchemaDir, "document.json"), []byte(`{"fields":[{"name":"title","type":"string","required":true},{"name":"address","type":"object","fields":[{"name":"city","type":"string"}]}]}`), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := Generate(c); err != nil {
		t.Fatal(err)
	}
	for _, pkg := range []string{"dto", "vo"} {
		if _, err := os.Stat(filepath.Join(c.Output, pkg)); !os.IsNotExist(err) {
			t.Fatalf("未配置 %s 却生成了目录", pkg)
		}
	}
	c.DtoPath, c.VoPath = "query/dto", "query/vo"
	if _, err := Generate(c); err != nil {
		t.Fatal(err)
	}
	compileFixture(t, dir)
}
