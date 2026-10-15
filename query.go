package mongoplus

import (
	"fmt"
	"reflect"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type Condition = field.Condition
type UpdateOp = field.UpdateOp

// QueryOption 保存一次查询的条件；Build 后不应修改其中的 BSON 值。
type QueryOption struct {
	cacheTTL     time.Duration
	singleFlight bool
	conditions   []Condition
	sort         bson.D
	projection   bson.D
	limit        int64
	skip         int64
	deleted      int // 0: 正常记录，1: 全部，2: 仅删除记录。
	allowAll     bool
	err          error
}

// Wrapper 是每次查询新建的条件构造器，不应在 goroutine 间共享。
type Wrapper struct {
	option          QueryOption
	defaultOrders   bson.D
	singleFlightTTL time.Duration
}

// QueryOptionBuilder 沿用 gorm-plus 的查询选项构造器名称。
type QueryOptionBuilder = Wrapper

// Query 创建条件构造器。
func Query() *Wrapper { return &Wrapper{} }

// QueryOpt 是 Query 的别名，方便沿用 gorm-plus 的命名。
func QueryOpt() *Wrapper { return Query() }

// Where 追加 AND 条件。
func (w *Wrapper) Where(conditions ...Condition) *Wrapper {
	w.option.conditions = append(w.option.conditions, conditions...)
	return w
}

// WhereIf 条件成立时才追加过滤条件，零值也可显式查询。
func (w *Wrapper) WhereIf(ok bool, conditions ...Condition) *Wrapper {
	if ok {
		w.Where(conditions...)
	}
	return w
}

// WhereGroup 追加 AND 分组。
func (w *Wrapper) WhereGroup(conditions ...Condition) *Wrapper {
	return w.Where(field.And(conditions...))
}

// WhereGroupIf 条件成立时追加 AND 分组。
func (w *Wrapper) WhereGroupIf(ok bool, conditions ...Condition) *Wrapper {
	if ok {
		w.WhereGroup(conditions...)
	}
	return w
}

// OrWhereIf 以 OR 逐个追加条件；仓储的租户/删除条件仍在外层 AND。
func (w *Wrapper) OrWhereIf(ok bool, conditions ...Condition) *Wrapper {
	if !ok {
		return w
	}
	for _, condition := range conditions {
		w.orCondition(condition)
	}
	return w
}

// WhereOrGroup 追加 AND (... OR ...) 分组。
func (w *Wrapper) WhereOrGroup(conditions ...Condition) *Wrapper {
	return w.Where(field.Or(conditions...))
}

// WhereOrGroupIf 条件成立时追加 OR 分组。
func (w *Wrapper) WhereOrGroupIf(ok bool, conditions ...Condition) *Wrapper {
	if ok {
		w.WhereOrGroup(conditions...)
	}
	return w
}

// RawWhere 接入原生 BSON；只能由可信业务代码构造，不得直接传入前端 JSON。
func (w *Wrapper) RawWhere(filter bson.D) *Wrapper {
	return w.Where(Condition{Document: filter})
}

// BetweenIfNotZero 与 gorm-plus 一致：任一边界为零值时跳过整个范围。
// 单边范围或包含零值的范围应显式使用 Gte/Lte。
func (w *Wrapper) BetweenIfNotZero(f field.Named, min, max any) *Wrapper {
	if f == nil || isZeroValue(min) || isZeroValue(max) {
		return w
	}
	return w.Where(field.New[any](f.Name()).Gte(min), field.New[any](f.Name()).Lte(max))
}

func isZeroValue(value any) bool {
	if value == nil {
		return true
	}
	v := reflect.ValueOf(value)
	switch v.Kind() {
	case reflect.Pointer, reflect.Interface, reflect.Map, reflect.Slice, reflect.Chan, reflect.Func:
		if v.IsNil() {
			return true
		}
	}
	if zero, ok := value.(interface{ IsZero() bool }); ok {
		return zero.IsZero()
	}
	return v.IsZero()
}

// Like 按字面量包含匹配；空文本自动跳过。
func (w *Wrapper) Like(f field.Named, value string) *Wrapper {
	if value != "" {
		w.Where(field.NewString(f.Name()).Contains(value))
	}
	return w
}

// LikeIf 条件成立时追加包含匹配。
func (w *Wrapper) LikeIf(ok bool, f field.Named, value string) *Wrapper {
	if ok {
		w.Like(f, value)
	}
	return w
}

// LLike 匹配以 value 结尾的文本，相当于 SQL LIKE '%value' 的使用习惯。
func (w *Wrapper) LLike(f field.Named, value string) *Wrapper {
	if value != "" {
		w.Where(field.NewString(f.Name()).EndsWith(value))
	}
	return w
}

// RLike 匹配以 value 开头的文本，相当于 SQL LIKE 'value%' 的使用习惯。
func (w *Wrapper) RLike(f field.Named, value string) *Wrapper {
	if value != "" {
		w.Where(field.NewString(f.Name()).StartsWith(value))
	}
	return w
}

// Order 设置稳定排序，分页执行时自动追加 _id 作为最后排序键。
func (w *Wrapper) Order(order ...bson.E) *Wrapper {
	w.option.sort = append(w.option.sort, order...)
	return w
}

// Select 只返回指定字段。
func (w *Wrapper) Select(fields ...field.Named) *Wrapper {
	for _, f := range fields {
		if _, ok := f.(interface{ Alias() string }); ok {
			w.option.err = fmt.Errorf("As 别名请使用 Repository.WithContext(ctx).Select(...).Scan(&rows)")
			return w
		}
		w.option.projection = append(w.option.projection, bson.E{Key: f.Name(), Value: 1})
	}
	return w
}

// Omit 排除指定字段；不能与 Select 混用（_id 除外）。
func (w *Wrapper) Omit(fields ...field.Named) *Wrapper {
	for _, f := range fields {
		w.option.projection = append(w.option.projection, bson.E{Key: f.Name(), Value: 0})
	}
	return w
}

// Limit 限制查询条数；负值会在执行时返回错误。
func (w *Wrapper) Limit(value int64) *Wrapper { w.option.limit = value; return w }

// Offset 设置偏移量。
func (w *Wrapper) Offset(value int64) *Wrapper { w.option.skip = value; return w }

// WithDeleted 查询包含逻辑删除记录；不会绕过租户隔离。
func (w *Wrapper) WithDeleted() *Wrapper { w.option.deleted = 1; return w }

// OnlyDeleted 仅查询逻辑删除记录。
func (w *Wrapper) OnlyDeleted() *Wrapper { w.option.deleted = 2; return w }

// AllowAll 明确允许无业务条件的批量更新或删除，租户过滤仍然有效。
func (w *Wrapper) AllowAll() *Wrapper { w.option.allowAll = true; return w }

// Build 返回独立的选项切片副本，继续构造不会改变此前快照。
func (w *Wrapper) Build() QueryOption {
	o := w.option
	if o.cacheTTL == 0 {
		o.cacheTTL = w.singleFlightTTL
	}
	o.conditions = append([]Condition(nil), o.conditions...)
	o.sort = append(bson.D(nil), o.sort...)
	if len(o.sort) == 0 {
		o.sort = append(bson.D(nil), w.defaultOrders...)
	}
	o.projection = append(bson.D(nil), o.projection...)
	return o
}

func mergeOptions(options []QueryOption) QueryOption {
	o := QueryOption{}
	for _, next := range options {
		o.conditions = append(o.conditions, next.conditions...)
		o.sort = append(o.sort, next.sort...)
		o.projection = append(o.projection, next.projection...)
		if next.limit != 0 {
			o.limit = next.limit
		}
		if next.skip != 0 {
			o.skip = next.skip
		}
		if next.deleted != 0 {
			o.deleted = next.deleted
		}
		o.allowAll = o.allowAll || next.allowAll
		if next.cacheTTL != 0 {
			o.cacheTTL = next.cacheTTL
		}
		o.singleFlight = o.singleFlight || next.singleFlight
		if next.err != nil {
			o.err = next.err
		}
	}
	return o
}

func (o QueryOption) validate() error {
	if o.err != nil {
		return o.err
	}
	if o.cacheTTL < 0 {
		return fmt.Errorf("缓存 TTL 不能为负数")
	}
	if o.limit < 0 || o.skip < 0 {
		return fmt.Errorf("limit、offset 不能小于零")
	}
	seen := map[string]bool{}
	for _, order := range o.sort {
		if !field.ValidPath(order.Key) || seen[order.Key] || (order.Value != 1 && order.Value != -1) {
			return fmt.Errorf("无效或重复的排序字段 %q", order.Key)
		}
		seen[order.Key] = true
	}
	mode := -1
	for _, p := range o.projection {
		if !field.ValidPath(p.Key) || (p.Value != 0 && p.Value != 1) {
			return fmt.Errorf("无效投影字段 %q", p.Key)
		}
		if p.Key == "_id" {
			continue
		}
		value := p.Value.(int)
		if mode >= 0 && mode != value {
			return fmt.Errorf("Select 与 Omit 不能混用")
		}
		mode = value
	}
	for _, c := range o.conditions {
		if c.Err != nil {
			return c.Err
		}
	}
	return nil
}

// UpdateBuilder 收集显式更新操作。
type UpdateBuilder struct{ ops []UpdateOp }

// Update 创建字段更新构造器。
func Update(ops ...UpdateOp) *UpdateBuilder {
	return &UpdateBuilder{ops: append([]UpdateOp(nil), ops...)}
}

// Apply 追加类型安全字段操作，例如 dao.UserEntity.Status.Set(0)。
func (u *UpdateBuilder) Apply(ops ...UpdateOp) *UpdateBuilder {
	u.ops = append(u.ops, ops...)
	return u
}

// Set 设置原生值；需要编译期检查时优先使用字段的 Set 方法。
func (u *UpdateBuilder) Set(f field.Named, value any) *UpdateBuilder {
	return u.Apply(UpdateOp{Operator: "$set", Path: f.Name(), Value: value})
}

// Unset 删除字段。
func (u *UpdateBuilder) Unset(f field.Named) *UpdateBuilder {
	return u.Apply(UpdateOp{Operator: "$unset", Path: f.Name(), Value: ""})
}

// Build 返回可直接传给 UpdateById 的更新选项，不需要再展开。
func (u *UpdateBuilder) Build() UpdateOption { return updateColumns(append([]UpdateOp(nil), u.ops...)) }

// UpdateOption 是单字段赋值或一组字段赋值，UpdateById 会展开成具体操作。
type UpdateOption interface {
	Operations() []UpdateOp
}

type updateColumns []UpdateOp

func (c updateColumns) Operations() []UpdateOp { return append([]UpdateOp(nil), c...) }

func flattenUpdates(opts []UpdateOption) []UpdateOp {
	ops := make([]UpdateOp, 0, len(opts))
	for _, opt := range opts {
		if opt != nil {
			ops = append(ops, opt.Operations()...)
		}
	}
	return ops
}

// WithUpdateColumns 与 gorm-plus 同名，直接作为 UpdateById 的一个参数。
func WithUpdateColumns(ops ...UpdateOp) UpdateOption { return updateColumns(ops) }

func buildUpdate(ops []UpdateOp, protected ...string) (bson.D, error) {
	if len(ops) == 0 {
		return nil, fmt.Errorf("更新字段不能为空")
	}
	groups := bson.D{}
	seen := []string{}
	for _, op := range ops {
		if !field.ValidPath(op.Path) {
			return nil, fmt.Errorf("无效更新字段 %q", op.Path)
		}
		switch op.Operator {
		case "$set", "$unset", "$inc", "$push", "$pull", "$addToSet", "$setOnInsert":
		default:
			return nil, fmt.Errorf("不支持的更新操作 %q", op.Operator)
		}
		for _, path := range append(append([]string{}, protected...), seen...) {
			if op.Path == path || stringsOverlap(op.Path, path) {
				return nil, fmt.Errorf("字段 %q 受保护或更新路径冲突", op.Path)
			}
		}
		seen = append(seen, op.Path)
		index := -1
		for i, e := range groups {
			if e.Key == op.Operator {
				index = i
				break
			}
		}
		if index < 0 {
			groups = append(groups, bson.E{Key: op.Operator, Value: bson.D{}})
			index = len(groups) - 1
		}
		groups[index].Value = append(groups[index].Value.(bson.D), bson.E{Key: op.Path, Value: op.Value})
	}
	return groups, nil
}

func stringsOverlap(a, b string) bool {
	return (len(a) > len(b) && a[:len(b)] == b && a[len(b)] == '.') || (len(b) > len(a) && b[:len(a)] == a && b[len(a)] == '.')
}

// WithCache 为当前查询启用有界本机 TTL 缓存，写操作自动失效。
func (w *Wrapper) WithCache(ttl time.Duration) *Wrapper { w.option.cacheTTL = ttl; return w }

// WithSingleFlight 合并相同的并发查询；省略 TTL 或传 0 时不缓存。
// 正 TTL 与 WithCache 等价，兼容 gorm-plus.WithSingleFlight(ttl) 的用法。
func (w *Wrapper) WithSingleFlight(ttl ...time.Duration) *Wrapper {
	w.option.singleFlight = true
	if len(ttl) > 1 {
		w.option.err = fmt.Errorf("WithSingleFlight 最多接受一个 TTL")
	}
	if len(ttl) == 1 {
		w.singleFlightTTL = ttl[0]
	} else if len(ttl) == 0 {
		w.singleFlightTTL = 0
	}
	if w.singleFlightTTL < 0 {
		w.option.err = fmt.Errorf("SingleFlight TTL 不能为负数")
	}
	return w
}
