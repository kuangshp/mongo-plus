package generator

import (
	"bytes"
	"embed"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"text/template"

	"github.com/kuangshp/mongo-plus/schema"
)

//go:embed template/*.txt
var templateFiles embed.FS

var (
	templatesOnce sync.Once
	templates     *template.Template
	templatesErr  error
)

func parsedTemplates() (*template.Template, error) {
	templatesOnce.Do(func() {
		templates, templatesErr = template.New("mongoplus").Funcs(template.FuncMap{
			"quote": strconv.Quote,
		}).ParseFS(templateFiles, "template/*.txt")
	})
	return templates, templatesErr
}

func executeTemplate(name string, data any) (string, error) {
	set, err := parsedTemplates()
	if err != nil {
		return "", err
	}
	var buf bytes.Buffer
	if err = set.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("渲染模板 %s: %w", name, err)
	}
	return buf.String(), nil
}

type taggedField struct {
	GoName, GoType, BSON, JSON, Comment string
}

type nestedType struct {
	Name, Comment string
	Fields        []taggedField
}

type entityView struct {
	Name, Comment string
	Fields        []taggedField
	Nested        []nestedType
}

type daoFieldView struct {
	Name, Type, Constructor, Path string
}

type daoView struct {
	Name, Comment string
	Fields        []daoFieldView
}

type repositoryView struct {
	Name, Entity, IDType, Collection     string
	IDTypeConst, IDGenConst, TenantField string
	BlockSize                            int64
	SoftDelete, Timestamps               bool
}

type dtoFieldView struct {
	GoName, GoType, JSON, Rules, Comment string
}

type dtoBlock struct {
	Name, Doc string
	Fields    []dtoFieldView
	Decode    bool
	Nested    []dtoBlock
}

type dtoView struct {
	Blocks []dtoBlock
}

type voView struct {
	Name, Comment string
	Fields        []taggedField
}

func entityData(e schema.Entity) entityView {
	root := e.Entity
	view := entityView{Name: root, Comment: comment(e.Comment)}
	for _, f := range effectiveFields(e) {
		view.Fields = append(view.Fields, entityField(f, root+f.GoName, true))
		view.Nested = append(view.Nested, nestedTypes(f, root+f.GoName)...)
	}
	return view
}

func entityField(f schema.Field, nested string, top bool) taggedField {
	bsonName := f.Name
	if top && (f.Name == "_id" || (!f.Required && !f.Nullable && (strings.HasPrefix(f.Type, "[]") || f.Type == "json" || f.Type == "bytes"))) {
		bsonName += ",omitempty"
	}
	jsonName := f.Name
	if f.Name == "_id" {
		jsonName = "id"
	}
	return taggedField{GoName: f.GoName, GoType: goType(f, nested), BSON: bsonName, JSON: jsonName, Comment: comment(f.Comment)}
}

func nestedTypes(f schema.Field, name string) []nestedType {
	if f.NativeGoType != "" || strings.TrimPrefix(f.Type, "[]") != "object" {
		return nil
	}
	current := nestedType{Name: name, Comment: comment(f.Comment)}
	var nested []nestedType
	for _, child := range f.Fields {
		current.Fields = append(current.Fields, entityField(child, name+child.GoName, false))
		nested = append(nested, nestedTypes(child, name+child.GoName)...)
	}
	return append([]nestedType{current}, nested...)
}

func daoData(e schema.Entity) (daoView, error) {
	root := e.Entity
	view := daoView{Name: root, Comment: comment(e.Comment)}
	names := map[string]bool{}
	var add func(schema.Field, string, string, string) error
	add = func(f schema.Field, name, path, nested string) error {
		if names[name] {
			return fmt.Errorf("DAO 字段名称冲突: %s", name)
		}
		names[name] = true
		typ := goType(f, nested)
		if f.NativeGoType == "" && strings.Contains(typ, root) {
			typ = strings.ReplaceAll(typ, root, "model."+root)
		}
		var ft, ctor string
		switch {
		case f.Type == "string" && !f.Nullable && f.NativeGoType == "":
			ft, ctor = "field.String", "field.NewString"
		case (f.Type == "int32" || f.Type == "int64" || f.Type == "float64") && !f.Nullable:
			ft, ctor = "field.Numeric["+typ+"]", "field.NewNumeric["+typ+"]"
		case strings.HasPrefix(f.Type, "[]") && strings.HasPrefix(typ, "[]") && !f.Nullable:
			elem := strings.TrimPrefix(typ, "[]")
			ft, ctor = "field.Array["+elem+"]", "field.NewArray["+elem+"]"
		default:
			ft, ctor = "field.Field["+typ+"]", "field.New["+typ+"]"
		}
		view.Fields = append(view.Fields, daoFieldView{Name: name, Type: ft, Constructor: ctor, Path: path})
		for _, child := range f.Fields {
			if err := add(child, name+child.GoName, path+"."+child.Name, nested+child.GoName); err != nil {
				return err
			}
		}
		return nil
	}
	for _, f := range effectiveFields(e) {
		if err := add(f, f.GoName, f.Name, root+f.GoName); err != nil {
			return daoView{}, err
		}
	}
	return view, nil
}

func repositoryData(e schema.Entity) repositoryView {
	return repositoryView{
		Name:        strings.TrimSuffix(e.Entity, "Entity"),
		Entity:      e.Entity,
		IDType:      goScalar(string(e.ID.Type)),
		Collection:  e.Collection,
		IDTypeConst: map[string]string{"objectId": "IDTypeObjectID", "int64": "IDTypeInt64", "string": "IDTypeString"}[string(e.ID.Type)],
		IDGenConst:  map[string]string{"auto": "IDGenerationAuto", "increment": "IDGenerationIncrement", "manual": "IDGenerationManual"}[string(e.ID.Generation)],
		BlockSize:   e.ID.BlockSize,
		SoftDelete:  *e.SoftDelete,
		Timestamps:  *e.Timestamps,
		TenantField: e.Runtime().TenantField,
	}
}

func dtoData(e schema.Entity) dtoView {
	name := strings.TrimSuffix(e.Entity, "Entity")
	doc := comment(e.Comment) + "；修改 DTO 表示完整业务字段提交，不是 PATCH。"
	blocks := make([]dtoBlock, 0, 2)
	for _, action := range []string{"Create", "Modify"} {
		root := action + name + "DTO"
		block := dtoBlock{Name: root, Doc: doc, Decode: true, Nested: dtoNested(e.Fields, root)}
		if action == "Modify" || e.ID.Generation == "manual" {
			id := schema.Field{Name: "id", GoName: "ID", Type: string(e.ID.Type), Required: true, Comment: "主键"}
			block.Fields = append(block.Fields, dtoField(id, root+"ID", true))
		}
		for _, f := range e.Fields {
			block.Fields = append(block.Fields, dtoField(f, root+f.GoName, false))
		}
		blocks = append(blocks, block)
	}
	return dtoView{Blocks: blocks}
}

func dtoNested(fields []schema.Field, prefix string) []dtoBlock {
	blocks := []dtoBlock{}
	for _, f := range fields {
		if strings.TrimPrefix(f.Type, "[]") != "object" {
			continue
		}
		name := prefix + f.GoName
		block := dtoBlock{Name: name, Doc: comment(f.Comment) + "。", Nested: dtoNested(f.Fields, name)}
		for _, child := range f.Fields {
			block.Fields = append(block.Fields, dtoField(child, name+child.GoName, false))
		}
		blocks = append(blocks, block)
	}
	return blocks
}

func dtoField(f schema.Field, nested string, id bool) dtoFieldView {
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
	return dtoFieldView{GoName: f.GoName, GoType: dtoType(f, nested, id), JSON: f.Name, Rules: rules, Comment: note}
}

func voData(e schema.Entity) voView {
	name := strings.TrimSuffix(e.Entity, "Entity") + "VO"
	view := voView{Name: name, Comment: comment(e.Comment)}
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
		view.Fields = append(view.Fields, taggedField{GoName: f.GoName, GoType: typ, BSON: f.Name, JSON: jsonName, Comment: comment(f.Comment)})
	}
	return view
}
