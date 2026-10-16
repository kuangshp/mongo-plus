package mongoplus

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/kuangshp/mongo-plus/field"
)

const sensitiveArrayToken = "\x00" // BSON 字段名不能含 NUL，避免与合法字段名冲突。

// owner 是根实体到包含敏感字段的结构体的下标链；遍历时自动展开切片/数组。
// 三个叶子下标都相对于该结构体，避免在 nil 的匿名指针上调用 FieldByName。
type sensitiveBinding struct {
	field                                                SensitiveFieldConfig
	owner, plain, cipher, index                          []int
	plainPattern, goPattern, cipherPattern, indexPattern []string
}

type sensitiveMetadata struct {
	fields   []SensitiveFieldConfig
	bindings []sensitiveBinding
	dynamic  [][]int // map/interface 中的运行时类型不能静态改写，写前检查是否夹带敏感实体。
	err      error
}

func (p *SensitivePlugin) fieldsForType(typ reflect.Type) ([]SensitiveFieldConfig, error) {
	model := p.modelForType(typ)
	return model.fields, model.err
}

// 元数据只按类型编译一次，新增、读取、更新和等值查询使用同一份完整路径。
func (p *SensitivePlugin) modelForType(typ reflect.Type) *sensitiveMetadata {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if cached, ok := p.metadata.Load(typ); ok {
		return cached.(*sensitiveMetadata)
	}
	model := &sensitiveMetadata{}
	model.err = p.compileSensitiveModel(typ, model)
	actual, _ := p.metadata.LoadOrStore(typ, model)
	return actual.(*sensitiveMetadata)
}

type sensitiveRef struct {
	indexes                []int
	goPattern, bsonPattern []string
	field                  reflect.StructField
}

// resolveSensitiveRef 同时解析 Go 路径和 BSON 路径，包含匿名字段及 inline 规则。
// 内部数组占位标记不会进入实际数据库查询。
func resolveSensitiveRef(root reflect.Type, path string) (sensitiveRef, bool, error) {
	var ref sensitiveRef
	typ := root
	parts := strings.Split(path, ".")
	for n, part := range parts {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
			if typ.Kind() != reflect.Pointer {
				ref.goPattern = append(ref.goPattern, sensitiveArrayToken)
				ref.bsonPattern = append(ref.bsonPattern, sensitiveArrayToken)
			}
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct {
			return ref, false, nil
		}
		f, ok := typ.FieldByName(part)
		if !ok {
			return ref, false, nil
		}
		for j, index := range f.Index {
			for typ.Kind() == reflect.Pointer {
				typ = typ.Elem()
			}
			if typ.Kind() != reflect.Struct {
				return ref, false, fmt.Errorf("敏感字段 %s 的嵌入路径无效", path)
			}
			current := typ.Field(index)
			if !current.IsExported() {
				return ref, false, fmt.Errorf("敏感字段 %s 必须可导出", path)
			}
			ref.indexes = append(ref.indexes, index)
			ref.goPattern = append(ref.goPattern, current.Name)
			name, inline, ignored := sensitiveBSONField(current)
			last := n == len(parts)-1 && j == len(f.Index)-1
			if ignored && !last {
				return ref, false, fmt.Errorf("敏感字段 %s 位于不持久化的对象中", path)
			}
			if !inline {
				if ignored {
					name = toSnakeCase(current.Name)
				}
				ref.bsonPattern = append(ref.bsonPattern, name)
			}
			typ = current.Type
			ref.field = current
		}
	}
	return ref, true, nil
}

func sensitiveBSONField(f reflect.StructField) (name string, inline, ignored bool) {
	parts := strings.Split(f.Tag.Get("bson"), ",")
	name = parts[0]
	ignored = name == "-"
	for _, option := range parts[1:] {
		inline = inline || option == "inline"
	}
	if name == "" {
		name = strings.ToLower(f.Name)
	}
	return
}

func (p *SensitivePlugin) compileSensitiveModel(root reflect.Type, model *sensitiveMetadata) error {
	if root.Kind() != reflect.Struct {
		return nil
	}
	seen := map[string]bool{}
	add := func(cfg SensitiveFieldConfig, explicit [3]bool, optional bool) error {
		plain, found, err := resolveSensitiveRef(root, cfg.PlainField)
		if err != nil {
			return err
		}
		if !found && optional {
			return nil
		} // 数据库插件允许同一配置用于其他集合。
		if !found {
			return fmt.Errorf("敏感明文字段 %s 不存在", cfg.PlainField)
		}
		canonical := sensitivePatternPath(plain.goPattern)
		if seen[canonical] {
			return nil
		} // 显式 Fields 优先于标签。
		cipher, cipherOK, err := resolveSensitiveRef(root, cfg.CipherField)
		if err != nil {
			return err
		}
		index, indexOK, err := resolveSensitiveRef(root, cfg.IndexField)
		if err != nil {
			return err
		}
		if plain.field.Type.Kind() != reflect.String || !cipherOK || !indexOK ||
			cipher.field.Type.Kind() != reflect.String || index.field.Type.Kind() != reflect.String {
			return fmt.Errorf("敏感字段 %s 的明文、密文和索引必须是可导出的 string 字段", cfg.PlainField)
		}
		_, _, cipherIgnored := sensitiveBSONField(cipher.field)
		_, _, indexIgnored := sensitiveBSONField(index.field)
		_, _, plainIgnored := sensitiveBSONField(plain.field)
		if cfg.EncryptAtRest && !plainIgnored {
			return fmt.Errorf("加密字段 %s 的明文必须配置 bson:\"-\"，防止明文同时持久化", canonical)
		}
		if cipherIgnored || indexIgnored {
			return fmt.Errorf("敏感字段 %s 的密文和索引不能配置 bson:\"-\"", canonical)
		}
		owner := plain.indexes[:len(plain.indexes)-1]
		if !sensitiveIndexPrefix(cipher.indexes, owner) || !sensitiveIndexPrefix(index.indexes, owner) {
			return fmt.Errorf("敏感字段 %s 的密文和索引必须位于同一对象", canonical)
		}
		if countSensitiveArrays(plain.goPattern) != countSensitiveArrays(cipher.goPattern) || countSensitiveArrays(plain.goPattern) != countSensitiveArrays(index.goPattern) {
			return fmt.Errorf("敏感字段 %s 的密文和索引不能跨越额外数组", canonical)
		}
		binding := sensitiveBinding{
			owner:         append([]int(nil), owner...),
			plain:         append([]int(nil), plain.indexes[len(owner):]...),
			cipher:        append([]int(nil), cipher.indexes[len(owner):]...),
			index:         append([]int(nil), index.indexes[len(owner):]...),
			plainPattern:  plain.bsonPattern,
			goPattern:     plain.goPattern,
			cipherPattern: cipher.bsonPattern,
			indexPattern:  index.bsonPattern,
		}
		patterns := []*[]string{&binding.plainPattern, &binding.cipherPattern, &binding.indexPattern}
		paths := []string{cfg.PlainPath, cfg.CipherPath, cfg.IndexPath}
		for i := range patterns {
			if explicit[i] {
				// 创建直接由驱动按实体 BSON 标签编码，配置不能另指一个存储位置。
				// bson:"-" 的明文是虚拟查询别名，可以显式命名，但密文/索引必须真实持久化。
				_, _, plainIgnored := sensitiveBSONField(plain.field)
				if i != 0 || !plainIgnored {
					if paths[i] != sensitivePatternPath(*patterns[i]) {
						return fmt.Errorf("敏感字段 %s 的显式路径 %q 与实体 BSON 路径 %q 不一致", canonical, paths[i], sensitivePatternPath(*patterns[i]))
					}
				} else {
					*patterns[i], err = explicitSensitivePattern(paths[i], *patterns[i])
					if err != nil {
						return err
					}
				}
			}
			if !field.ValidPath(sensitivePatternPath(*patterns[i])) {
				return fmt.Errorf("敏感字段 %s 的 BSON 路径无效", canonical)
			}
		}
		dimensions := countSensitiveArrays(binding.goPattern)
		if countSensitiveArrays(binding.plainPattern) != dimensions || countSensitiveArrays(binding.cipherPattern) != dimensions || countSensitiveArrays(binding.indexPattern) != dimensions {
			return fmt.Errorf("敏感字段 %s 的明文、密文和索引必须具有相同的数组层级", canonical)
		}
		cfg.PlainField = canonical
		cfg.CipherField = sensitivePatternPath(cipher.goPattern)
		cfg.IndexField = sensitivePatternPath(index.goPattern)
		cfg.PlainPath = sensitivePatternPath(binding.plainPattern)
		cfg.CipherPath = sensitivePatternPath(binding.cipherPattern)
		cfg.IndexPath = sensitivePatternPath(binding.indexPattern)
		paths = []string{cfg.PlainPath, cfg.CipherPath, cfg.IndexPath}
		for i, path := range paths {
			for _, other := range paths[:i] {
				if path == other || stringsOverlap(path, other) {
					return fmt.Errorf("敏感字段 %s 的路径 %q 与 %q 重叠", canonical, path, other)
				}
			}
			for _, existing := range model.fields {
				for _, other := range []string{existing.PlainPath, existing.CipherPath, existing.IndexPath} {
					if path == other || stringsOverlap(path, other) {
						return fmt.Errorf("敏感字段 %s 与 %s 的路径 %q 和 %q 重叠", canonical, existing.PlainField, path, other)
					}
				}
			}
		}
		binding.field = cfg
		seen[canonical] = true
		model.bindings = append(model.bindings, binding)
		model.fields = append(model.fields, cfg)
		return nil
	}
	for _, cfg := range p.cfg.Fields {
		if err := add(cfg, p.explicitPaths[cfg.PlainField], true); err != nil {
			return err
		}
	}
	stack := map[reflect.Type]bool{}
	var scan func(reflect.Type, []string) error
	scan = func(typ reflect.Type, path []string) error {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
			typ = typ.Elem()
		}
		if typ.Kind() == reflect.Map {
			if p.hasSensitiveType(typ.Elem()) {
				return fmt.Errorf("敏感字段 %s 暂不支持 map 容器，请使用结构体或切片", strings.Join(path, "."))
			}
		}
		if typ.Kind() == reflect.Map || typ.Kind() == reflect.Interface {
			ref, _, err := resolveSensitiveRef(root, strings.Join(path, "."))
			if err != nil {
				return err
			}
			model.dynamic = append(model.dynamic, ref.indexes)
			return nil
		}
		if typ.Kind() != reflect.Struct {
			return nil
		}
		if stack[typ] {
			if p.hasSensitiveType(typ) {
				return fmt.Errorf("敏感字段 %s 不支持递归实体类型", strings.Join(path, "."))
			}
			return nil
		}
		stack[typ] = true
		defer delete(stack, typ)
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			parts := append(append([]string(nil), path...), f.Name)
			tag := f.Tag.Get("mongoplus")
			if tag == "" {
				tag = f.Tag.Get("gormplus")
			}
			options := sensitiveTag(tag)
			if options["cipher"] != "" || options["index"] != "" {
				plainPath := strings.Join(parts, ".")
				// 已显式配置的字段不再解释标签，保持覆盖优先级。
				if !seen[plainPath] {
					if options["cipher"] == "" || options["index"] == "" {
						return fmt.Errorf("敏感字段 %s 必须同时配置 cipher 和 index", plainPath)
					}
					cfg := PhoneField(f.Name)
					cfg.PlainField = plainPath
					cfg.CipherField = strings.Join(append(append([]string(nil), path...), options["cipher"]), ".")
					cfg.IndexField = strings.Join(append(append([]string(nil), path...), options["index"]), ".")
					cfg.EncryptAtRest = strings.EqualFold(options["encrypt"], "true")
					if err := add(cfg, [3]bool{}, false); err != nil {
						return err
					}
				}
			}
			_, _, ignored := sensitiveBSONField(f)
			if !ignored {
				if err := scan(f.Type, parts); err != nil {
					return err
				}
			}
		}
		return nil
	}
	return scan(root, nil)
}

func countSensitiveArrays(pattern []string) int {
	count := 0
	for _, token := range pattern {
		if token == sensitiveArrayToken {
			count++
		}
	}
	return count
}

func hasSensitiveTag(typ reflect.Type, seen map[reflect.Type]bool) bool {
	if seen[typ] {
		return false
	}
	seen[typ] = true
	switch typ.Kind() {
	case reflect.Pointer, reflect.Slice, reflect.Array, reflect.Map:
		return hasSensitiveTag(typ.Elem(), seen)
	case reflect.Struct:
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			for _, key := range []string{"mongoplus", "gormplus"} {
				opts := sensitiveTag(f.Tag.Get(key))
				if opts["cipher"] != "" || opts["index"] != "" {
					return true
				}
			}
			_, _, ignored := sensitiveBSONField(f)
			if !ignored && hasSensitiveTag(f.Type, seen) {
				return true
			}
		}
	}
	return false
}

func (p *SensitivePlugin) hasSensitiveType(typ reflect.Type) bool {
	if hasSensitiveTag(typ, map[reflect.Type]bool{}) {
		return true
	}
	for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array {
		typ = typ.Elem()
	}
	for _, cfg := range p.cfg.Fields {
		if _, found, _ := resolveSensitiveRef(typ, cfg.PlainField); found {
			return true
		}
	}
	return false
}

func sensitiveIndexPrefix(path, prefix []int) bool {
	if len(path) <= len(prefix) {
		return false
	}
	for i := range prefix {
		if path[i] != prefix[i] {
			return false
		}
	}
	return true
}

func sensitiveDefaultPath(path string) string {
	parts := strings.Split(path, ".")
	for i := range parts {
		parts[i] = toSnakeCase(parts[i])
	}
	return strings.Join(parts, ".")
}

func sensitivePatternPath(pattern []string) string {
	parts := make([]string, 0, len(pattern))
	for _, part := range pattern {
		if part != sensitiveArrayToken {
			parts = append(parts, part)
		}
	}
	return strings.Join(parts, ".")
}

func explicitSensitivePattern(path string, actual []string) ([]string, error) {
	parts := strings.Split(path, ".")
	result := []string{}
	position := 0
	for _, part := range actual {
		if part == sensitiveArrayToken {
			if len(parts) < position {
				return nil, fmt.Errorf("敏感字段 %s 的数组路径无效", path)
			}
			result = append(result, sensitiveArrayToken)
			continue
		}
		if position >= len(parts) {
			break
		}
		result = append(result, parts[position])
		position++
	}
	result = append(result, parts[position:]...)
	return result, nil
}

// nil 指针表示可选对象不存在，安全跳过；数组和切片逐个处理。
func visitSensitiveOwners(value reflect.Value, path []int, fn func(reflect.Value) error) error {
	depth := 0
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		depth++
		if depth > 64 {
			return fmt.Errorf("敏感字段指针链过深或包含循环")
		}
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	if !value.IsValid() {
		return nil
	}
	if value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
		for i := 0; i < value.Len(); i++ {
			if err := visitSensitiveOwners(value.Index(i), path, fn); err != nil {
				return err
			}
		}
		return nil
	}
	if len(path) == 0 {
		return fn(value)
	}
	return visitSensitiveOwners(value.Field(path[0]), path[1:], fn)
}

func sensitiveValueAt(value reflect.Value, path []int) (reflect.Value, bool) {
	for _, index := range path {
		for value.Kind() == reflect.Pointer {
			if value.IsNil() {
				return reflect.Value{}, false
			}
			value = value.Elem()
		}
		value = value.Field(index)
	}
	return value, true
}

func sensitiveStringAt(value reflect.Value, path []int) (string, bool) {
	v, ok := sensitiveValueAt(value, path)
	if !ok || v.Kind() != reflect.String {
		return "", false
	}
	return v.String(), true
}

func setSensitiveStringAt(value reflect.Value, path []int, text string) error {
	v, ok := sensitiveValueAt(value, path)
	if !ok || v.Kind() != reflect.String || !v.CanSet() {
		return fmt.Errorf("敏感字段存储目标不存在或不可写")
	}
	v.SetString(text)
	return nil
}

// 只复制敏感字段写入会穿过的指针和切片，保留其他字段的类型、编码器和资源语义。
// 在 seal 之前完成，InsertOne/InsertMany 失败时不会污染调用方的实体或共享切片。
func (p *SensitivePlugin) copyWriteFields(value any) error {
	return walkSensitive(reflect.ValueOf(value), func(root reflect.Value) error {
		model := p.modelForType(root.Type())
		if model.err != nil {
			return model.err
		}
		if err := p.checkSensitiveDynamic(root, model, 0); err != nil {
			return err
		}
		for _, binding := range model.bindings {
			for _, leaf := range [][]int{binding.cipher, binding.index} {
				path := append(append([]int(nil), binding.owner...), leaf...)
				if err := copySensitivePath(root, path); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (p *SensitivePlugin) checkSensitiveDynamic(root reflect.Value, model *sensitiveMetadata, depth int) error {
	for _, path := range model.dynamic {
		if err := visitSensitiveOwners(root, path, func(value reflect.Value) error {
			return p.rejectDynamicSensitive(value, depth+1)
		}); err != nil {
			return err
		}
	}
	return nil
}

// 动态容器保留普通 BSON 数据，但不静默接受需要加密的运行时实体。
// 此类输入必须改为静态结构体/切片，使新增、更新和查询能共享同一份元数据。
func (p *SensitivePlugin) rejectDynamicSensitive(value reflect.Value, depth int) error {
	if depth > 64 {
		return fmt.Errorf("敏感字段动态容器过深或包含循环")
	}
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
		depth++
		if depth > 64 {
			return fmt.Errorf("敏感字段动态容器过深或包含循环")
		}
	}
	if !value.IsValid() {
		return nil
	}
	switch value.Kind() {
	case reflect.Struct:
		model := p.modelForType(value.Type())
		if model.err != nil {
			return model.err
		}
		if len(model.bindings) > 0 {
			return fmt.Errorf("动态 map/interface 不支持敏感实体 %s，请使用静态结构体或切片", value.Type())
		}
		return p.checkSensitiveDynamic(value, model, depth+1)
	case reflect.Map:
		iter := value.MapRange()
		for iter.Next() {
			if err := p.rejectDynamicSensitive(iter.Value(), depth+1); err != nil {
				return err
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := p.rejectDynamicSensitive(value.Index(i), depth+1); err != nil {
				return err
			}
		}
	}
	return nil
}

func copySensitivePath(value reflect.Value, path []int) error {
	if len(path) == 0 {
		return nil
	}
	if value.Kind() == reflect.Pointer {
		if value.IsNil() {
			return nil
		}
		if !value.CanSet() {
			return fmt.Errorf("敏感字段指针不可复制")
		}
		copy := reflect.New(value.Type().Elem())
		copy.Elem().Set(value.Elem())
		value.Set(copy)
		return copySensitivePath(value.Elem(), path)
	}
	if value.Kind() == reflect.Slice || value.Kind() == reflect.Array {
		if value.Kind() == reflect.Slice {
			if value.IsNil() {
				return nil
			}
			copy := reflect.MakeSlice(value.Type(), value.Len(), value.Len())
			reflect.Copy(copy, value)
			value.Set(copy)
		}
		for i := 0; i < value.Len(); i++ {
			if err := copySensitivePath(value.Index(i), path); err != nil {
				return err
			}
		}
		return nil
	}
	return copySensitivePath(value.Field(path[0]), path[1:])
}
