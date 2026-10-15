package generator

import (
	"bytes"
	"strings"

	"github.com/kuangshp/mongo-plus/schema"
)

// DTO/VO 是业务可修改的初始模板，与 Repository 扩展一样仅首次创建。
func editableSource(pkg, body string, imports map[string]string) ([]byte, error) {
	data, err := source(pkg, body, imports)
	if err != nil {
		return nil, err
	}
	return bytes.Replace(data, []byte(generated), []byte("// 由 mongoplus 首次创建；后续生成不覆盖，可按业务调整字段和校验。\n"), 1), nil
}

func renderDTO(e schema.Entity, c schema.Config) ([]byte, error) {
	body, err := executeTemplate("dto_template.txt", dtoData(e))
	if err != nil {
		return nil, err
	}
	return editableSource(c.PackageName("dto"), body, map[string]string{"decimaljson": "github.com/kuangshp/mongo-plus/decimaljson"})
}

func dtoType(f schema.Field, nested string, id bool) string {
	base := strings.TrimPrefix(f.Type, "[]")
	typ := base
	switch base {
	case "objectId", "datetime", "decimal128":
		typ = "string"
	case "object":
		typ = nested
	case "json":
		typ = "map[string]any"
	case "bytes":
		typ = "[]byte"
	}
	if strings.HasPrefix(f.Type, "[]") {
		typ = "[]" + typ
	}
	// 必填数字/bool 用指针区分缺失与合法的 0/false；集合允许显式提交空集合。
	// 可选嵌套对象用指针避免未提交时触发其内部必填校验。
	if f.Nullable || (!id && f.Required && typ != "string") || base == "object" && !strings.HasPrefix(f.Type, "[]") {
		typ = "*" + typ
	}
	return typ
}

func renderVO(e schema.Entity, modelImport string, c schema.Config) ([]byte, error) {
	body, err := executeTemplate("vo_template.txt", voData(e))
	if err != nil {
		return nil, err
	}
	return editableSource(c.PackageName("vo"), body, map[string]string{"model": modelImport})
}
