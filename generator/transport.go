package generator

import (
	"bytes"
	"fmt"
	"strconv"
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
	name := strings.TrimSuffix(e.Entity, "Entity")
	var body strings.Builder
	for _, action := range []string{"Create", "Modify"} {
		root := action + name + "DTO"
		fmt.Fprintf(&body, "// %s %s；修改 DTO 表示完整业务字段提交，不是 PATCH。\ntype %s struct {\n", root, comment(e.Comment), root)
		// 自动主键由 Repository 填充；手动主键的新增请求也需要 ID。
		if action == "Modify" || e.ID.Generation == "manual" {
			id := schema.Field{Name: "id", GoName: "ID", Type: e.ID.Type, Required: true, Comment: "主键"}
			renderDTOField(&body, id, root+"ID", true)
		}
		for _, f := range e.Fields {
			renderDTOField(&body, f, root+f.GoName, false)
		}
		body.WriteString("}\n")
		// 每种 DTO 单独定义 UnmarshalJSON，避免嵌入带解码方法的 Create DTO 吞掉 ID。
		fmt.Fprintf(&body, "// UnmarshalJSON 兼容数字和数字字符串，保留数值原文。\nfunc(value *%s) UnmarshalJSON(data []byte) error {\ntype plain %s\nreturn decimaljson.Unmarshal(data, (*plain)(value))\n}\n", root, root)
		for _, f := range e.Fields {
			renderDTONested(&body, f, root+f.GoName)
		}
	}
	return editableSource(c.PackageName("dto"), body.String(), map[string]string{"decimaljson": "github.com/kuangshp/mongo-plus/decimaljson"})
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

func renderDTOField(out *strings.Builder, f schema.Field, nested string, id bool) {
	rules := "omitempty"
	if f.Required && !f.Nullable {
		rules = "required"
	}
	base := strings.TrimPrefix(f.Type, "[]")
	if strings.HasPrefix(f.Type, "[]") {
		rules += ",dive"
	}
	switch base {
	case "objectId":
		rules += ",objectId"
	case "datetime":
		rules += ",dateTime"
	}
	note := comment(f.Comment)
	switch base {
	case "objectId":
		note += "；24 位十六进制字符串，入库前用 bson.ObjectIDFromHex 解析"
	case "datetime":
		note += "；格式 2006-01-02 15:04:05，入库前按业务时区解析"
	case "decimal128":
		note += "；数字文本，入库前用 bson.ParseDecimal128 解析并校验业务精度"
	}
	fmt.Fprintf(out, "%s %s `json:%s validate:%s` // %s\n", f.GoName, dtoType(f, nested, id), strconv.Quote(f.Name), strconv.Quote(rules), note)
}

func renderDTONested(out *strings.Builder, f schema.Field, name string) {
	if strings.TrimPrefix(f.Type, "[]") != "object" {
		return
	}
	fmt.Fprintf(out, "// %s %s。\ntype %s struct {\n", name, comment(f.Comment), name)
	for _, child := range f.Fields {
		renderDTOField(out, child, name+child.GoName, false)
	}
	out.WriteString("}\n")
	for _, child := range f.Fields {
		renderDTONested(out, child, name+child.GoName)
	}
}

func renderVO(e schema.Entity, modelImport string, c schema.Config) ([]byte, error) {
	name := strings.TrimSuffix(e.Entity, "Entity") + "VO"
	var body strings.Builder
	fmt.Fprintf(&body, "// %s %s查询结果；可用 Scan/ScanByPage 直接填充，按需裁剪敏感字段。\ntype %s struct {\n", name, comment(e.Comment), name)
	for _, f := range effectiveFields(e) {
		if *e.SoftDelete && f.Name == "deleted_at" {
			continue
		}
		typ := goType(f, e.Entity+f.GoName)
		if f.NativeGoType == "" && strings.TrimPrefix(f.Type, "[]") == "object" {
			typ = strings.ReplaceAll(typ, e.Entity, "model."+e.Entity)
		}
		jsonName := f.Name
		if f.Name == "_id" {
			jsonName = "id"
		}
		fmt.Fprintf(&body, "%s %s `bson:%s json:%s` // %s\n", f.GoName, typ, strconv.Quote(f.Name), strconv.Quote(jsonName), comment(f.Comment))
	}
	body.WriteString("}\n")
	return editableSource(c.PackageName("vo"), body.String(), map[string]string{"model": modelImport})
}
