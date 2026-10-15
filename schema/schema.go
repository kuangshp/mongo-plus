// Package schema 读取项目配置和 JSON 实体定义，统一为生成与数据库同步使用的元数据。
package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/token"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/kuangshp/mongo-plus/internal/settings"
	"go.mongodb.org/mongo-driver/v2/bson"
	"gopkg.in/yaml.v3"
)

// Config 是 mongo-plus.yaml；全局主键只需配置一次。
type Config struct {
	// Package 与 gorm-plus 的 GeneratorConfig.Package 含义相同；Module 为旧配置兼容字段。
	Package   string `yaml:"package" json:"package"`
	Module    string `yaml:"module" json:"module"`
	SchemaDir string `yaml:"schemaDir" json:"schemaDir"`
	Output    string `yaml:"output" json:"output"`
	// Pluralize 为 true 时给自动推导的集合名追加 s；false（默认）不追加。
	Pluralize bool `yaml:"pluralize" json:"pluralize"`
	// 可分别指定输出位置；未填写时沿用 Output 下的 model/dao/repository。
	ModelPkgPath string `yaml:"model_pkg_path" json:"modelPkgPath"`
	OutPath      string `yaml:"out_path" json:"outPath"`
	RepoPath     string `yaml:"repo_path" json:"repoPath"`
	// DTO/VO 与 gorm-plus 一样按需生成；留空时不生成，首次创建后保留业务修改。
	DtoPath    string            `yaml:"dto_path" json:"dtoPath"`
	VoPath     string            `yaml:"vo_path" json:"voPath"`
	ID         settings.IDConfig `yaml:"id" json:"id"`
	SoftDelete *bool             `yaml:"softDelete" json:"softDelete"`
	Timestamps *bool             `yaml:"timestamps" json:"timestamps"`
	Tenant     *Tenant           `yaml:"tenant" json:"tenant"`
	MongoDB    settings.Config   `yaml:"mongodb" json:"mongodb"`
	base       string
}

// Tenant 声明自动注入的租户字段。
type Tenant struct {
	Name string `json:"name" yaml:"name"`
	Type string `json:"type" yaml:"type"`
}

// Entity 对应一个 schema/*.json 文件；ID/SoftDelete/Timestamps 可以覆盖全局默认。
type Entity struct {
	Entity        string             `json:"entity,omitempty"`
	Collection    string             `json:"collection,omitempty"`
	Comment       string             `json:"comment,omitempty"`
	ID            *settings.IDConfig `json:"id,omitempty"`
	SoftDelete    *bool              `json:"softDelete,omitempty"`
	Timestamps    *bool              `json:"timestamps,omitempty"`
	Fields        []Field            `json:"fields"`
	Indexes       []Index            `json:"indexes,omitempty"`
	Tenant        *Tenant            `json:"tenant,omitempty"`
	Source        string             `json:"-"`
	ExternalModel bool               `json:"-"`
	ModelPackage  string             `json:"-"`
}

// Field 用 name/type 声明字段，其他配置均可省略。
type Field struct {
	NativeGoType string  `json:"-"`
	Name         string  `json:"name"`
	Type         string  `json:"type"`
	Comment      string  `json:"comment,omitempty"`
	GoName       string  `json:"goName,omitempty"`
	Required     bool    `json:"required,omitempty"`
	Unique       bool    `json:"unique,omitempty"`
	Nullable     bool    `json:"nullable,omitempty"`
	Fields       []Field `json:"fields,omitempty"`
}

// IndexField 保留复合索引键的顺序，不能用无序 map 替代。
type IndexField struct {
	Name  string `json:"name"`
	Order int    `json:"order"`
}

// Index 定义复合/唯一索引；ActiveOnly 仅约束未逻辑删除的文档。
type Index struct {
	Name               string       `json:"name"`
	Fields             []IndexField `json:"fields"`
	Unique             bool         `json:"unique,omitempty"`
	Sparse             bool         `json:"sparse,omitempty"`
	ActiveOnly         bool         `json:"activeOnly,omitempty"`
	ExpireAfterSeconds *int32       `json:"expireAfterSeconds,omitempty"`
}

// LoadConfig 读取配置；路径相对配置文件，环境变量只在连接配置中展开。
func LoadConfig(path string) (Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Config{}, err
	}
	decoder := yaml.NewDecoder(bytes.NewReader(data))
	decoder.KnownFields(true)
	var c Config
	if err = decoder.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if err = decoder.Decode(&extra); err != io.EOF {
		return c, fmt.Errorf("配置文件只能包含一个 YAML 文档")
	}
	c.base, err = filepath.Abs(filepath.Dir(path))
	if err != nil {
		return c, err
	}
	c.MongoDB.URI = os.ExpandEnv(c.MongoDB.URI)
	c.MongoDB.Database = os.ExpandEnv(c.MongoDB.Database)
	// 区分省略数据源与显式填写了空配置；环境变量展开后为空不能悄悄跳过同步。
	var sections map[string]yaml.Node
	if err = yaml.Unmarshal(data, &sections); err != nil {
		return c, err
	}
	if node, exists := sections["mongodb"]; exists && node.Tag != "!!null" && !c.HasMongoDB() {
		return c, fmt.Errorf("已配置 mongodb，uri、database 不能为空")
	}
	if err = c.Normalize(); err != nil {
		return c, err
	}
	return c, nil
}

// HasMongoDB 表示生成器是否提供数据源；未提供时只生成代码。
func (c Config) HasMongoDB() bool { return c.MongoDB != (settings.Config{}) }

// Normalize 补全路径和全局默认；逻辑删除、时间字段默认开启。
func (c *Config) Normalize() error {
	if c.base == "" {
		var err error
		c.base, err = os.Getwd()
		if err != nil {
			return err
		}
	}
	if c.Package != "" {
		if c.Module != "" && c.Module != c.Package {
			return fmt.Errorf("package 与 module 必须保持一致")
		}
		c.Module = c.Package
	}
	if c.Module == "" || strings.ContainsAny(c.Module, " \t\n\\\"`") || strings.HasPrefix(c.Module, "/") {
		return fmt.Errorf("Package（兼容 YAML 的 module）必须是业务项目的 Go module 路径")
	}
	if c.HasMongoDB() && (strings.TrimSpace(c.MongoDB.URI) == "" || strings.TrimSpace(c.MongoDB.Database) == "") {
		return fmt.Errorf("已配置 mongodb，uri、database 不能为空")
	}
	if c.SchemaDir == "" {
		c.SchemaDir = "schema"
	}
	if c.Output == "" {
		c.Output = "query"
	}
	if !filepath.IsAbs(c.SchemaDir) {
		c.SchemaDir = filepath.Join(c.base, c.SchemaDir)
	}
	if !filepath.IsAbs(c.Output) {
		c.Output = filepath.Join(c.base, c.Output)
	}
	rel, err := filepath.Rel(c.base, c.Output)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) || rel == "." {
		return fmt.Errorf("output 必须是业务项目中的子目录")
	}
	seen := map[string]bool{}
	for _, target := range []struct {
		path  *string
		child string
	}{{&c.ModelPkgPath, "model"}, {&c.OutPath, "dao"}, {&c.RepoPath, "repository"}, {&c.DtoPath, "dto"}, {&c.VoPath, "vo"}} {
		if *target.path == "" && (target.child == "dto" || target.child == "vo") {
			continue
		}
		if *target.path == "" {
			*target.path = filepath.Join(c.Output, target.child)
		}
		if !filepath.IsAbs(*target.path) {
			*target.path = filepath.Join(c.base, *target.path)
		}
		*target.path = filepath.Clean(*target.path)
		rel, err := filepath.Rel(c.base, *target.path)
		if err != nil || rel == "." || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return fmt.Errorf("%s 输出必须是业务项目中的子目录", target.child)
		}
		name := filepath.Base(*target.path)
		if !token.IsIdentifier(name) || token.Lookup(name).IsKeyword() || name == "_" {
			return fmt.Errorf("输出目录 %q 不是有效 Go 包名", name)
		}
		if seen[*target.path] {
			return fmt.Errorf("Entity、DAO、Repository、DTO、VO 输出目录不能相同")
		}
		seen[*target.path] = true
	}
	c.ID, err = c.ID.Normalize()
	if err != nil {
		return err
	}
	if c.SoftDelete == nil {
		c.SoftDelete = Bool(true)
	}
	if c.Timestamps == nil {
		c.Timestamps = Bool(true)
	}
	if c.Tenant != nil {
		if err = validateTenant(c.Tenant); err != nil {
			return err
		}
	}
	return nil
}

// BaseDir 返回配置文件所在目录。
func (c Config) BaseDir() string { return c.base }

// ImportPath 返回输出目录内包的导入路径。
func (c Config) ImportPath(pkg string) string {
	rel, _ := filepath.Rel(c.base, c.PackageDir(pkg))
	return c.Module + "/" + filepath.ToSlash(rel)
}

// PackageDir 返回数据层输出目录；应先 Normalize。未启用的 DTO/VO 返回空字符串。
func (c Config) PackageDir(pkg string) string {
	switch pkg {
	case "model":
		return c.ModelPkgPath
	case "dao":
		return c.OutPath
	case "repository":
		return c.RepoPath
	case "dto":
		return c.DtoPath
	case "vo":
		return c.VoPath
	}
	return filepath.Join(c.Output, pkg)
}

// PackageName 与输出目录保持一致，例如 ModelPkgPath 为 internal/dal/entity 时使用 entity 包。
func (c Config) PackageName(pkg string) string { return filepath.Base(c.PackageDir(pkg)) }

// Bool 创建可覆盖全局默认的布尔指针。
func Bool(value bool) *bool { return &value }

var namePattern = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*$`)

// LoadEntities 读取所有 JSON 文件，不连接数据库；任何无效文件都会终止生成。
func LoadEntities(c Config) ([]Entity, error) {
	paths, err := filepath.Glob(filepath.Join(c.SchemaDir, "*.json"))
	if err != nil {
		return nil, err
	}
	if len(paths) == 0 {
		return nil, fmt.Errorf("%s 中没有 JSON 实体定义", c.SchemaDir)
	}
	sort.Strings(paths)
	entities := []Entity{}
	names := map[string]bool{}
	collections := map[string]bool{}
	for _, path := range paths {
		data, err := os.ReadFile(path)
		if err != nil {
			return nil, err
		}
		if err = RejectDuplicateKeys(data); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		var entity Entity
		if err = decoder.Decode(&entity); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		entity.Source = path
		name := strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
		if err = entity.Normalize(c, name); err != nil {
			return nil, fmt.Errorf("%s: %w", path, err)
		}
		if names[strings.ToLower(entity.Entity)] || collections[entity.Collection] {
			return nil, fmt.Errorf("实体或集合重复: %s", entity.Entity)
		}
		names[strings.ToLower(entity.Entity)] = true
		collections[entity.Collection] = true
		entities = append(entities, entity)
	}
	return entities, nil
}

// Normalize 合并全局默认，并校验字段、索引和生成名称冲突。
func (e *Entity) Normalize(c Config, filename string) error {
	if e.Entity == "" {
		e.Entity = GoName(SnakeName(filename)) + "Entity"
	}
	if !token.IsIdentifier(e.Entity) || !token.IsExported(e.Entity) {
		return fmt.Errorf("无效 Entity 名称 %q", e.Entity)
	}
	if e.Collection == "" {
		e.Collection = SnakeName(filename)
		if c.Pluralize {
			e.Collection += "s"
		}
	}
	if e.Collection == "" || strings.ContainsAny(e.Collection, "\x00$") || strings.HasPrefix(e.Collection, "system.") || e.Collection == "__mongo_plus_sequences" {
		return fmt.Errorf("无效集合名称 %q", e.Collection)
	}
	id := c.ID
	if e.ID != nil {
		if e.ID.Type != "" && e.ID.Type != id.Type {
			id.Generation = ""
			id.Type = e.ID.Type
		}
		if e.ID.Generation != "" {
			id.Generation = e.ID.Generation
		}
	}
	var err error
	id, err = id.Normalize()
	if err != nil {
		return err
	}
	e.ID = &id
	if e.SoftDelete == nil {
		e.SoftDelete = Bool(*c.SoftDelete)
	}
	if e.Timestamps == nil {
		e.Timestamps = Bool(*c.Timestamps)
	}
	if e.Tenant == nil && c.Tenant != nil {
		copy := *c.Tenant
		e.Tenant = &copy
	}
	if e.Tenant != nil {
		if err = validateTenant(e.Tenant); err != nil {
			return err
		}
	}
	reserved := map[string]bool{"_id": true}
	goReserved := map[string]bool{"ID": true}
	if *e.SoftDelete {
		reserved["deleted_at"] = true
		goReserved["DeletedAt"] = true
	}
	if *e.Timestamps {
		reserved["created_at"] = true
		reserved["updated_at"] = true
		goReserved["CreatedAt"] = true
		goReserved["UpdatedAt"] = true
	}
	if e.Tenant != nil {
		reserved[e.Tenant.Name] = true
		goReserved[GoName(e.Tenant.Name)] = true
	}
	if err = normalizeFields(e.Fields, reserved, goReserved); err != nil {
		return err
	}
	// unique 简写转换为标准索引；删除记录默认仍占用唯一值。
	var addUnique func([]Field, string)
	addUnique = func(fields []Field, prefix string) {
		for _, f := range fields {
			path := prefix + f.Name
			if f.Unique {
				wanted := Index{Name: "uk_" + strings.ReplaceAll(path, ".", "_"), Fields: []IndexField{{path, 1}}, Unique: true}
				found := false
				for _, existing := range e.Indexes {
					if existing.Name == wanted.Name && equalIndex(expectedIndex(existing), expectedIndex(wanted)) {
						found = true
						break
					}
				}
				if !found {
					e.Indexes = append(e.Indexes, wanted)
				}
			}
			addUnique(f.Fields, path+".")
		}
	}
	addUnique(e.Fields, "")
	names := map[string]bool{}
	for _, index := range e.Indexes {
		if index.Name == "" || strings.ContainsRune(index.Name, '\x00') || index.Name == "_id_" || names[index.Name] || len(index.Fields) == 0 {
			return fmt.Errorf("索引名称为空、重复或无效: %q", index.Name)
		}
		names[index.Name] = true
		seen := map[string]bool{}
		for _, f := range index.Fields {
			if !e.HasField(f.Name) || seen[f.Name] || (f.Order != 1 && f.Order != -1) {
				return fmt.Errorf("索引 %s 引用无效字段 %q", index.Name, f.Name)
			}
			seen[f.Name] = true
		}
		if index.Sparse && index.ActiveOnly {
			return fmt.Errorf("索引 %s 不能同时设置 sparse 和 activeOnly", index.Name)
		}
		if index.ActiveOnly && !*e.SoftDelete {
			return fmt.Errorf("activeOnly 索引需要启用逻辑删除")
		}
		if index.ExpireAfterSeconds != nil && (*index.ExpireAfterSeconds < 0 || len(index.Fields) != 1 || e.fieldType(index.Fields[0].Name) != "datetime") {
			return fmt.Errorf("TTL 索引需要一个 datetime 字段和非负秒数")
		}
	}
	return nil
}

func validateTenant(t *Tenant) error {
	if !namePattern.MatchString(t.Name) || t.Name == "_id" || t.Name == "deleted_at" || t.Name == "created_at" || t.Name == "updated_at" {
		return fmt.Errorf("无效租户字段")
	}
	switch t.Type {
	case "int64", "string", "objectId":
		return nil
	}
	return fmt.Errorf("租户字段只支持 int64/string/objectId")
}

func normalizeFields(fields []Field, names, goNames map[string]bool) error {
	for i := range fields {
		f := &fields[i]
		if !namePattern.MatchString(f.Name) || names[f.Name] {
			return fmt.Errorf("字段名称无效或重复 %q", f.Name)
		}
		names[f.Name] = true
		if f.GoName == "" {
			f.GoName = GoName(f.Name)
		}
		if !token.IsIdentifier(f.GoName) || !token.IsExported(f.GoName) || goNames[f.GoName] {
			return fmt.Errorf("Go 字段名称无效或冲突 %q", f.GoName)
		}
		goNames[f.GoName] = true
		base := strings.TrimPrefix(f.Type, "[]")
		switch base {
		case "string", "int32", "int64", "bool", "float64", "objectId", "datetime", "decimal128", "bytes", "json", "object":
		default:
			return fmt.Errorf("字段 %s 不支持类型 %q", f.Name, f.Type)
		}
		if base == "object" {
			if len(f.Fields) == 0 {
				return fmt.Errorf("对象字段 %s 缺少 fields", f.Name)
			}
			if err := normalizeFields(f.Fields, map[string]bool{}, map[string]bool{}); err != nil {
				return err
			}
		} else if len(f.Fields) > 0 {
			return fmt.Errorf("非对象字段 %s 不能包含 fields", f.Name)
		}
	}
	return nil
}

// GoName 将 snake_case 转为 Go 名称，保留 ID、URL 等常见缩写。
func GoName(name string) string {
	words := strings.FieldsFunc(name, func(r rune) bool { return r == '_' || r == '-' })
	for i, word := range words {
		switch strings.ToUpper(word) {
		case "ID", "URL", "IP", "HTTP", "API", "UUID":
			words[i] = strings.ToUpper(word)
		default:
			if word != "" {
				words[i] = strings.ToUpper(word[:1]) + word[1:]
			}
		}
	}
	return strings.Join(words, "")
}

// HasField 检查索引字段是否在定义中，支持嵌套点路径。
func (e Entity) HasField(path string) bool { return e.fieldType(path) != "" }

func (e Entity) fieldType(path string) string {
	if path == "_id" {
		return e.ID.Type
	}
	if path == "deleted_at" && *e.SoftDelete {
		return "datetime"
	}
	if (path == "created_at" || path == "updated_at") && *e.Timestamps {
		return "datetime"
	}
	if e.Tenant != nil && path == e.Tenant.Name {
		return e.Tenant.Type
	}
	parts := strings.Split(path, ".")
	fields := e.Fields
	for i, part := range parts {
		found := false
		for _, f := range fields {
			if f.Name == part {
				if i == len(parts)-1 {
					return f.Type
				}
				fields = f.Fields
				found = true
				break
			}
		}
		if !found {
			return ""
		}
	}
	return ""
}

// Runtime 返回生成 Repository 使用的运行配置。
func (e Entity) Runtime() settings.CollectionConfig {
	c := settings.CollectionConfig{Name: e.Collection, ID: *e.ID, SoftDelete: *e.SoftDelete, Timestamps: *e.Timestamps}
	if e.Tenant != nil {
		c.TenantField = e.Tenant.Name
	}
	return c
}

// Validator 将定义转换成 MongoDB 原生 JSON Schema；required 只表示字段存在。
func (e Entity) Validator() bson.D {
	properties, required := properties(e.Fields)
	properties = append(properties, bson.E{Key: "_id", Value: bson.D{{Key: "bsonType", Value: bsonType(e.ID.Type)}}})
	required = append(required, "_id")
	if *e.SoftDelete {
		properties = append(properties, bson.E{Key: "deleted_at", Value: bson.D{{Key: "bsonType", Value: bson.A{"date", "null"}}}})
	}
	if *e.Timestamps {
		for _, name := range []string{"created_at", "updated_at"} {
			properties = append(properties, bson.E{Key: name, Value: bson.D{{Key: "bsonType", Value: "date"}}})
			required = append(required, name)
		}
	}
	if e.Tenant != nil {
		properties = append(properties, bson.E{Key: e.Tenant.Name, Value: bson.D{{Key: "bsonType", Value: bsonType(e.Tenant.Type)}}})
		required = append(required, e.Tenant.Name)
	}
	return bson.D{{Key: "$jsonSchema", Value: bson.D{{Key: "bsonType", Value: "object"}, {Key: "required", Value: required}, {Key: "properties", Value: properties}}}}
}

func properties(fields []Field) (bson.D, []string) {
	props := bson.D{}
	required := []string{}
	for _, f := range fields {
		rule := fieldRule(f)
		if f.Comment != "" {
			rule = append(rule, bson.E{Key: "description", Value: f.Comment})
		}
		props = append(props, bson.E{Key: f.Name, Value: rule})
		if f.Required {
			required = append(required, f.Name)
		}
	}
	return props, required
}

func fieldRule(f Field) bson.D {
	base := f.Type
	var typ any = bsonType(base)
	if strings.HasPrefix(base, "[]") {
		typ = "array"
		base = strings.TrimPrefix(base, "[]")
	}
	if f.Nullable {
		typ = bson.A{typ, "null"}
	}
	rule := bson.D{{Key: "bsonType", Value: typ}}
	if strings.HasPrefix(f.Type, "[]") {
		copy := f
		copy.Type = base
		copy.Nullable = false
		rule = append(rule, bson.E{Key: "items", Value: fieldRule(copy)})
	} else if base == "object" {
		props, required := properties(f.Fields)
		rule = append(rule, bson.E{Key: "properties", Value: props})
		if len(required) > 0 {
			rule = append(rule, bson.E{Key: "required", Value: required})
		}
	}
	return rule
}

func bsonType(typ string) string {
	switch typ {
	case "int32":
		return "int"
	case "int64":
		return "long"
	case "float64":
		return "double"
	case "datetime":
		return "date"
	case "decimal128":
		return "decimal"
	case "bytes":
		return "binData"
	case "json":
		return "object"
	}
	return typ
}

// RejectDuplicateKeys 拒绝重复 JSON 键，避免配置静默覆盖。
func RejectDuplicateKeys(data []byte) error {
	d := json.NewDecoder(bytes.NewReader(data))
	d.UseNumber()
	var walk func() error
	walk = func() error {
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for d.More() {
				key, err := d.Token()
				if err != nil {
					return err
				}
				name := key.(string)
				if seen[name] {
					return fmt.Errorf("重复 JSON 键 %q", name)
				}
				seen[name] = true
				if err = walk(); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err = walk(); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("无效 JSON")
		}
		_, err = d.Token()
		return err
	}
	if err := walk(); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("JSON 文件只能包含一个值")
	}
	return nil
}
