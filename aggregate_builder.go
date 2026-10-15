package mongoplus

import (
	"fmt"
	"reflect"
	"strings"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type aggregateMetric struct {
	op    string
	field field.Named
	alias string
}

type groupSpec struct {
	keys    []field.Named
	metrics []aggregateMetric
}

type unwindSpec struct {
	field    field.Named
	preserve bool
	index    string
}

// GroupBy 按一个或多个字段分组；无参数表示对全部匹配记录统计。
// 分组字段默认以路径最后一段作为结果名（_id 映射为 id），可用字段.As("别名") 覆盖。
// 分组键会同时保留在 _id 文档中，并展开为顶层结果字段，方便直接映射到 VO。
func (q *Aggregation) GroupBy(keys ...field.Named) *Aggregation {
	q.steps = append(q.steps, joinStep{mode: "group", group: &groupSpec{keys: append([]field.Named(nil), keys...)}})
	return q
}

// Count 为当前分组增加记录数；这是统计字段，不是立即执行的 Repository.Count(ctx)。
func (q *Aggregation) Count(alias string) *Aggregation { return q.metric("$count", nil, alias) }

// Sum 对数值字段求和；支持整数、浮点与 Decimal128，不会将字符串当作数值。
func (q *Aggregation) Sum(f field.Named, alias string) *Aggregation {
	return q.metric("$sum", f, alias)
}

// Avg 对数值字段求平均；普通数值通常返回 double，Decimal128 沿用 MongoDB 精度规则。
func (q *Aggregation) Avg(f field.Named, alias string) *Aggregation {
	return q.metric("$avg", f, alias)
}

// Min 返回当前分组的最小值，支持数值、日期等可比较字段。
func (q *Aggregation) Min(f field.Named, alias string) *Aggregation {
	return q.metric("$min", f, alias)
}

// Max 返回当前分组的最大值。
func (q *Aggregation) Max(f field.Named, alias string) *Aggregation {
	return q.metric("$max", f, alias)
}

// First 返回分组中第一条记录的字段值；必须在分组前通过 Sort 指定顺序。
func (q *Aggregation) First(f field.Named, alias string) *Aggregation {
	return q.metric("$first", f, alias)
}

// Last 返回分组中最后一条记录的字段值；必须在分组前通过 Sort 指定顺序。
func (q *Aggregation) Last(f field.Named, alias string) *Aggregation {
	return q.metric("$last", f, alias)
}

// Push 将字段值收集为数组，保留重复值；有顺序要求时先 Sort。
func (q *Aggregation) Push(f field.Named, alias string) *Aggregation {
	return q.metric("$push", f, alias)
}

// AddToSet 将字段值收集为去重数组，结果元素顺序不保证。
func (q *Aggregation) AddToSet(f field.Named, alias string) *Aggregation {
	return q.metric("$addToSet", f, alias)
}

// StdDevPop 计算总体标准差。
func (q *Aggregation) StdDevPop(f field.Named, alias string) *Aggregation {
	return q.metric("$stdDevPop", f, alias)
}

// StdDevSamp 计算样本标准差；有效样本不足两个时结果为 null。
func (q *Aggregation) StdDevSamp(f field.Named, alias string) *Aggregation {
	return q.metric("$stdDevSamp", f, alias)
}

func (q *Aggregation) metric(op string, f field.Named, alias string) *Aggregation {
	if len(q.steps) == 0 || q.steps[len(q.steps)-1].group == nil {
		for _, step := range q.steps {
			if step.group != nil {
				q.err = fmt.Errorf("统计函数需紧接 GroupBy 或其他统计函数，不能放在 Having/Where 等后面；二次分组请显式调用 GroupBy")
				return q
			}
		}
		q.GroupBy()
	}
	group := q.steps[len(q.steps)-1].group
	group.metrics = append(group.metrics, aggregateMetric{op: op, field: f, alias: alias})
	return q
}

// Having 过滤分组结果；字段使用结果名，如 field.NewNumeric[int64]("count").Gt(1)。
func (q *Aggregation) Having(conditions ...Condition) *Aggregation {
	for _, step := range q.steps {
		if step.group != nil {
			return q.Where(conditions...)
		}
	}
	q.err = fmt.Errorf("Having 必须在 GroupBy 或统计函数之后调用")
	return q
}

// HavingIf 条件成立时过滤分组结果。
func (q *Aggregation) HavingIf(ok bool, conditions ...Condition) *Aggregation {
	if ok {
		return q.Having(conditions...)
	}
	return q
}

// Sort 在当前位置排序，适合 GroupBy 前为 First/Last/Push 指定输入顺序。
// Order 始终对最终结果排序；需要稳定 First/Last 时，应同时传主键作为并列排序键。
func (q *Aggregation) Sort(order ...bson.E) *Aggregation {
	q.steps = append(q.steps, joinStep{mode: "sort", sort: append(bson.D(nil), order...)})
	return q
}

// Unwind 将数组拆成多条记录，可在分组前统计每个元素。
// preserveEmpty 默认 false；传 true 时保留缺失、null 和空数组记录。
func (q *Aggregation) Unwind(f field.Named, preserveEmpty ...bool) *Aggregation {
	if len(preserveEmpty) > 1 {
		q.err = fmt.Errorf("Unwind 最多接受一个 preserveEmpty 参数")
		return q
	}
	preserve := len(preserveEmpty) == 1 && preserveEmpty[0]
	q.steps = append(q.steps, joinStep{mode: "unwind", unwind: &unwindSpec{field: f, preserve: preserve, index: fmt.Sprintf("__mp_unwind_%d", len(q.steps))}})
	return q
}

func validAggregateAlias(name string) bool {
	return field.ValidPath(name) && !strings.Contains(name, ".") && !strings.HasPrefix(name, "__mp_") && name != "_id"
}

type aggregateShape struct {
	source    collectionSource
	aliases   map[string]collectionSource
	fields    map[string]reflect.Type
	overrides map[string]reflect.Type
	grouped   bool
	ordered   bool
}

func newAggregateShape(source collectionSource, aliases map[string]collectionSource) *aggregateShape {
	return &aggregateShape{source: source, aliases: aliases, overrides: map[string]reflect.Type{}}
}

func (s *aggregateShape) resolve(path string) (reflect.Type, bool) {
	if typ, ok := s.overrides[path]; ok {
		return typ, true
	}
	parts := strings.Split(path, ".")
	for i := len(parts) - 1; i > 0; i-- {
		if typ, ok := s.overrides[strings.Join(parts[:i], ".")]; ok {
			return aggregatePathType(typ, strings.Join(parts[i:], "."))
		}
	}
	if s.grouped {
		if typ, ok := s.fields[path]; ok {
			return typ, true
		}
		if prefix, rest, ok := strings.Cut(path, "."); ok {
			if typ, exists := s.fields[prefix]; exists {
				return aggregatePathType(typ, rest)
			}
		}
		return nil, false
	}
	if prefix, rest, ok := strings.Cut(path, "."); ok {
		if source, exists := s.aliases[prefix]; exists {
			return aggregatePathType(source.model, rest)
		}
	}
	return aggregatePathType(s.source.model, path)
}

// 与普通字段查找不同，保留穿过数组路径的数组类型，避免 Sum("items.price") 静默忽略数组。
func aggregatePathType(typ reflect.Type, path string) (reflect.Type, bool) {
	typ = indirectType(typ)
	if typ.Kind() == reflect.Slice {
		if typ.Elem().Kind() == reflect.Uint8 {
			return nil, false
		}
		elem, ok := aggregatePathType(typ.Elem(), path)
		if !ok {
			return nil, false
		}
		return reflect.SliceOf(elem), true
	}
	if typ.Kind() != reflect.Struct {
		return nil, false
	}
	part, rest, nested := strings.Cut(path, ".")
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		name := strings.Split(f.Tag.Get("bson"), ",")[0]
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		if name != part {
			continue
		}
		if !nested {
			return f.Type, true
		}
		return aggregatePathType(f.Type, rest)
	}
	return nil, false
}

func (s *aggregateShape) named(f field.Named) (string, reflect.Type, error) {
	path, err := namedPath(f)
	if err != nil {
		return "", nil, err
	}
	typ, ok := s.resolve(path)
	if !ok {
		return "", nil, fmt.Errorf("聚合字段 %q 不属于当前文档", path)
	}
	return path, typ, nil
}

func aggregateKeyName(f field.Named, path string) string {
	if aliased, ok := f.(interface{ Alias() string }); ok {
		return aliased.Alias()
	}
	parts := strings.Split(path, ".")
	name := parts[len(parts)-1]
	if name == "_id" {
		return "id"
	}
	return name
}

func (g *groupSpec) build(shape *aggregateShape) (mongo.Pipeline, error) {
	keys, flatten := bson.D{}, bson.D{}
	fields := map[string]reflect.Type{}
	add := func(name string, typ reflect.Type) error {
		if !validAggregateAlias(name) {
			return fmt.Errorf("无效聚合结果名 %q", name)
		}
		if _, exists := fields[name]; exists {
			return fmt.Errorf("聚合结果名重复 %q，请用 As 或不同统计别名区分", name)
		}
		fields[name] = typ
		return nil
	}
	for _, f := range g.keys {
		path, typ, err := shape.named(f)
		if err != nil {
			return nil, err
		}
		name := aggregateKeyName(f, path)
		if err := add(name, typ); err != nil {
			return nil, err
		}
		keys = append(keys, bson.E{Key: name, Value: bson.D{{Key: "$ifNull", Value: bson.A{"$" + path, nil}}}})
		flatten = append(flatten, bson.E{Key: name, Value: "$_id." + name})
	}
	var key any
	if len(keys) > 0 {
		key = keys
	}
	group := bson.D{{Key: "_id", Value: key}}
	for _, metric := range g.metrics {
		var expression any = 1
		typ := reflect.TypeOf(int64(0))
		op := metric.op
		if op == "$count" {
			op = "$sum"
		} else {
			path, fieldType, err := shape.named(metric.field)
			if err != nil {
				return nil, err
			}
			expression, typ = "$"+path, fieldType
		}
		if metric.op == "$sum" || metric.op == "$avg" || metric.op == "$stdDevPop" || metric.op == "$stdDevSamp" {
			if !aggregateNumeric(typ) {
				return nil, fmt.Errorf("%s 仅支持数值字段；数组请先 Unwind，字符串请先在模型中使用数值类型", metric.op)
			}
			if metric.op == "$stdDevPop" || metric.op == "$stdDevSamp" || (metric.op == "$avg" && indirectType(typ) != reflect.TypeOf(bson.Decimal128{})) {
				typ = reflect.TypeOf(float64(0))
			}
		}
		if (metric.op == "$first" || metric.op == "$last") && !shape.ordered {
			return nil, fmt.Errorf("First/Last 需要在 GroupBy 前调用 Sort 指定输入顺序；Order 仅排序最终结果")
		}
		if metric.op == "$push" || metric.op == "$addToSet" {
			typ = reflect.SliceOf(typ)
		}
		if err := add(metric.alias, typ); err != nil {
			return nil, err
		}
		group = append(group, bson.E{Key: metric.alias, Value: bson.D{{Key: op, Value: expression}}})
	}
	stages := mongo.Pipeline{bson.D{{Key: "$group", Value: group}}}
	if len(flatten) > 0 {
		stages = append(stages, bson.D{{Key: "$set", Value: flatten}})
	}
	fields["_id"] = reflect.TypeOf(bson.D{})
	shape.fields, shape.overrides, shape.grouped, shape.ordered = fields, map[string]reflect.Type{}, true, false
	return stages, nil
}

func indirectType(typ reflect.Type) reflect.Type {
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	return typ
}

func aggregateNumeric(typ reflect.Type) bool {
	typ = indirectType(typ)
	if typ == reflect.TypeOf(bson.Decimal128{}) {
		return true
	}
	switch typ.Kind() {
	case reflect.Int, reflect.Int32, reflect.Int64, reflect.Float32, reflect.Float64:
		return true
	}
	return false
}

func (u *unwindSpec) build(shape *aggregateShape) (bson.D, error) {
	path, typ, err := shape.named(u.field)
	if err != nil {
		return nil, err
	}
	typ = indirectType(typ)
	if typ.Kind() != reflect.Slice || typ.Elem().Kind() == reflect.Uint8 {
		return nil, fmt.Errorf("Unwind 需要数组字段，收到 %q", path)
	}
	if _, collision := shape.resolve(u.index); collision {
		return nil, fmt.Errorf("字段 %q 与 Unwind 内部索引冲突", u.index)
	}
	shape.overrides[path] = typ.Elem()
	shape.overrides[u.index] = reflect.TypeOf(int64(0))
	return bson.D{{Key: "$unwind", Value: bson.D{{Key: "path", Value: "$" + path}, {Key: "preserveNullAndEmptyArrays", Value: u.preserve}, {Key: "includeArrayIndex", Value: u.index}}}}, nil
}
