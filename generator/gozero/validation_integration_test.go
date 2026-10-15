package gozero

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func newServiceFixture(t *testing.T, api string) string {
	t.Helper()
	if os.Getenv("MONGOPLUS_GOCTL_INTEGRATION") != "1" {
		t.Skip("设置 MONGOPLUS_GOCTL_INTEGRATION=1 运行真实 goctl 集成测试")
	}
	if _, err := exec.LookPath("goctl"); err != nil {
		t.Fatal(err)
	}
	root, err := filepath.Abs("../..")
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	mod := fmt.Sprintf("module servicefixture\n\ngo 1.25.5\nrequire (\n github.com/kuangshp/mongo-plus v0.0.0\n github.com/zeromicro/go-zero v1.10.2\n)\nreplace github.com/kuangshp/mongo-plus => %s\n", root)
	for name, content := range map[string]string{"go.mod": mod, "sample.api": api} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0600); err != nil {
			t.Fatal(err)
		}
	}
	if output, err := Run(context.Background(), RunOptions{}, "api", "go", "--api", filepath.Join(dir, "sample.api"), "--dir", dir, "--style=goZero"); err != nil {
		t.Fatalf("生成服务失败: %v\n%s", err, output)
	}
	return dir
}

func TestGoctlValidationAndCoercionHTTP(t *testing.T) {
	spec := "syntax = \"v1\"\ntype Item {\n Quantity int64 `json:\"quantity\" validate:\"gt=0\"`\n}\ntype Input {\n ID int64 `json:\"id\" validate:\"required,gt=0\"`\n Amount string `json:\"amount\" validate:\"required,decimal\"`\n Mobile string `json:\"mobile\" validate:\"required,checkMobile\"`\n Time string `json:\"time\" validate:\"required,dateTime\"`\n DocumentID string `json:\"documentId\" validate:\"required,objectId\"`\n Items []Item `json:\"items\" validate:\"required,dive\"`\n}\nservice sample-api {\n @handler Check\n post /check (Input) returns (Input)\n}\n"
	dir := newServiceFixture(t, spec)
	logicPath := filepath.Join(dir, "internal/logic/checkLogic.go")
	logic, err := os.ReadFile(logicPath)
	if err != nil {
		t.Fatal(err)
	}
	logic = bytes.Replace(logic, []byte("// todo: add your logic here and delete this line"), []byte("resp = req"), 1)
	if err := os.WriteFile(logicPath, logic, 0600); err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(dir, "service")
	command := exec.Command("go", "build", "-mod=mod", "-o", binary, ".")
	command.Dir = dir
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("编译校验服务失败: %v\n%s", err, output)
	}
	base := startHTTPFixture(t, dir, binary, "")
	valid := map[string]any{"id": "9223372036854775807", "amount": 12.30, "mobile": "13800138000", "time": "2026-10-15 10:20:30", "documentId": "507f1f77bcf86cd799439011", "items": []map[string]any{{"quantity": "2"}}}
	send := func(input map[string]any) struct {
		Code    int64           `json:"code"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	} { t.Helper(); body, err := json.Marshal(input); if err != nil {
		t.Fatal(err)
	}; status, _, data := requestHTTPFixture(t, base, http.MethodPost, "/check", string(body), ""); var out struct {
		Code    int64           `json:"code"`
		Message string          `json:"message"`
		Result  json.RawMessage `json:"result"`
	}; if err := json.Unmarshal(data, &out); err != nil || status != http.StatusOK {
		t.Fatalf("无效 HTTP 响应: %d %s %v", status, data, err)
	}; return out }
	out := send(valid)
	var echoed struct {
		ID     int64  `json:"id"`
		Amount string `json:"amount"`
		Items  []struct {
			Quantity int64 `json:"quantity"`
		} `json:"items"`
	}
	if err := json.Unmarshal(out.Result, &echoed); err != nil || out.Code != 0 || echoed.ID != 9223372036854775807 || echoed.Amount != "12.3" || len(echoed.Items) != 1 || echoed.Items[0].Quantity != 2 {
		t.Fatalf("兼容转换或实际 Handler 失败: %+v %s %v", out, echoed.Amount, err)
	}
	for _, test := range []struct {
		field   string
		value   any
		message string
	}{
		{"id", "1.5", "id"}, {"amount", "12.345", "amount"}, {"mobile", "123", "非法的手机号码"}, {"time", "2026-02-30 10:20:30", "时间格式"}, {"documentId", "bad", "24位十六进制"}, {"items", []map[string]any{{"quantity": 0}}, "quantity"},
	} {
		t.Run(test.field, func(t *testing.T) {
			old := valid[test.field]
			valid[test.field] = test.value
			out := send(valid)
			valid[test.field] = old
			if out.Code != 1 || !strings.Contains(out.Message, test.message) {
				t.Fatalf("无效参数未被正确拒绝: %+v", out)
			}
		})
	}
}
