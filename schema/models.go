package schema

import (
	"fmt"
	"reflect"
	"strings"
	"time"

	mongoplus "github.com/kuangshp/mongo-plus"
	"go.mongodb.org/mongo-driver/v2/bson"
)

// Model 提供手写 Entity 和可选的集合覆盖；Value 传实体零值即可。
type Model struct {
	Value      any
	Collection string
	ID         *mongoplus.IDConfig
	SoftDelete *bool
	Timestamps *bool
	Comment    string
}

// FromModels 读取手写实体，不连接数据库，不覆盖实体源码。
func FromModels(c Config, models ...Model) ([]Entity, error) {
	if err := c.Normalize(); err != nil {
		return nil, err
	}
	result := []Entity{}
	names := map[string]bool{}
	collections := map[string]bool{}
	for _, m := range models {
		typ := reflect.TypeOf(m.Value)
		if typ == nil {
			return nil, fmt.Errorf("Model 不能为空")
		}
		if typ.Kind() == reflect.Pointer {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct || typ.Name() == "" || typ.PkgPath() == "" {
			return nil, fmt.Errorf("Model 必须是命名结构体")
		}
		e := Entity{Entity: typ.Name(), Collection: m.Collection, ID: m.ID, SoftDelete: m.SoftDelete, Timestamps: m.Timestamps, Comment: m.Comment, ExternalModel: true, ModelPackage: typ.PkgPath()}
		soft := *c.SoftDelete
		if m.SoftDelete != nil {
			soft = *m.SoftDelete
		}
		timestamps := *c.Timestamps
		if m.Timestamps != nil {
			timestamps = *m.Timestamps
		}
		fields, err := reflectFields(typ, typ.PkgPath(), map[reflect.Type]bool{})
		if err != nil {
			return nil, err
		}
		idFound := false
		for _, f := range fields {
			if f.Name == "_id" {
				if f.GoName != "ID" {
					return nil, fmt.Errorf("主键 Go 字段必须命名 ID")
				}
				idFound = true
				continue
			}
			if soft && f.Name == "deleted_at" {
				if f.Type != "datetime" || !f.Nullable {
					return nil, fmt.Errorf("DeletedAt 必须为 *time.Time")
				}
				continue
			}
			if timestamps && (f.Name == "created_at" || f.Name == "updated_at") {
				if f.Type != "datetime" || f.Nullable {
					return nil, fmt.Errorf("时间字段必须为 time.Time")
				}
				continue
			}
			if c.Tenant != nil && f.Name == c.Tenant.Name {
				if f.Type != c.Tenant.Type {
					return nil, fmt.Errorf("租户类型与配置不一致")
				}
				continue
			}
			e.Fields = append(e.Fields, f)
		}
		if !idFound {
			return nil, fmt.Errorf("%s 缺少 bson:\"_id\" 主键字段", typ.Name())
		}
		if err = e.Normalize(c, strings.ToLower(strings.TrimSuffix(typ.Name(), "Entity"))); err != nil {
			return nil, err
		}
		id, _ := typ.FieldByName("ID")
		if id.Type != idGoType(e.ID.Type) {
			return nil, fmt.Errorf("%s.ID 与主键配置不一致，请设置实体 ID 覆盖", typ.Name())
		}
		if soft && !hasBSONField(fields, "deleted_at") {
			return nil, fmt.Errorf("启用逻辑删除的手写实体需要 DeletedAt *time.Time")
		}
		if timestamps && (!hasBSONField(fields, "created_at") || !hasBSONField(fields, "updated_at")) {
			return nil, fmt.Errorf("启用时间填充的手写实体需要 CreatedAt/UpdatedAt")
		}
		if c.Tenant != nil && !hasBSONField(fields, c.Tenant.Name) {
			return nil, fmt.Errorf("手写实体缺少租户字段")
		}
		if names[e.Entity] || collections[e.Collection] {
			return nil, fmt.Errorf("重复实体或集合")
		}
		names[e.Entity] = true
		collections[e.Collection] = true
		result = append(result, e)
	}
	return result, nil
}

func hasBSONField(fields []Field, name string) bool {
	for _, f := range fields {
		if f.Name == name {
			return true
		}
	}
	return false
}

func idGoType(name string) reflect.Type {
	switch name {
	case "objectId":
		return reflect.TypeOf(bson.ObjectID{})
	case "int64":
		return reflect.TypeOf(int64(0))
	default:
		return reflect.TypeOf("")
	}
}

func reflectFields(typ reflect.Type, pkg string, stack map[reflect.Type]bool) ([]Field, error) {
	if stack[typ] {
		return nil, fmt.Errorf("不支持递归 Entity 类型 %s", typ)
	}
	stack[typ] = true
	defer delete(stack, typ)
	result := []Field{}
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		tag := strings.Split(f.Tag.Get("bson"), ",")
		if tag[0] == "-" {
			continue
		}
		for _, opt := range tag[1:] {
			if opt == "inline" {
				return nil, fmt.Errorf("手写 Entity 暂不支持 bson inline，请显式声明字段")
			}
		}
		name := tag[0]
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		item := Field{Name: name, GoName: f.Name, Comment: f.Tag.Get("comment")}
		for _, rule := range strings.Split(f.Tag.Get("mongoplus"), ",") {
			if rule == "required" {
				item.Required = true
			}
			if rule == "unique" {
				item.Unique = true
			}
		}
		t := f.Type
		if t.Kind() == reflect.Pointer {
			item.Nullable = true
			t = t.Elem()
		}
		isArray := t.Kind() == reflect.Slice && t != reflect.TypeOf([]byte{})
		if isArray {
			t = t.Elem()
			if t.Kind() == reflect.Pointer {
				return nil, fmt.Errorf("手写实体暂不支持指针数组元素 %s", f.Name)
			}
		}
		switch t {
		case reflect.TypeOf(bson.ObjectID{}):
			item.Type = "objectId"
		case reflect.TypeOf(bson.Decimal128{}):
			item.Type = "decimal128"
		case reflect.TypeOf(time.Time{}):
			item.Type = "datetime"
		case reflect.TypeOf([]byte{}):
			item.Type = "bytes"
		case reflect.TypeOf(bson.M{}):
			item.Type = "json"
		default:
			switch t.Kind() {
			case reflect.String:
				item.Type = "string"
			case reflect.Int32:
				item.Type = "int32"
			case reflect.Int64:
				item.Type = "int64"
			case reflect.Bool:
				item.Type = "bool"
			case reflect.Float64:
				item.Type = "float64"
			case reflect.Struct:
				item.Type = "object"
				nested, err := reflectFields(t, pkg, stack)
				if err != nil {
					return nil, err
				}
				item.Fields = nested
			default:
				return nil, fmt.Errorf("不支持手写字段 %s 的类型 %s", f.Name, f.Type)
			}
		}
		if t.PkgPath() != "" && t.PkgPath() != pkg && t.PkgPath() != "time" && t.PkgPath() != "go.mongodb.org/mongo-driver/v2/bson" {
			return nil, fmt.Errorf("字段 %s 引用了外部命名类型，请在项目 Model 包声明", f.Name)
		}
		if t.PkgPath() == pkg {
			item.NativeGoType = "model." + t.Name()
		}
		if isArray {
			item.Type = "[]" + item.Type
			if item.NativeGoType != "" {
				item.NativeGoType = "[]" + item.NativeGoType
			}
		}
		if item.Nullable && item.NativeGoType != "" {
			item.NativeGoType = "*" + item.NativeGoType
		}
		result = append(result, item)
	}
	return result, nil
}
