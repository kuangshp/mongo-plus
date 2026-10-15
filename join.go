package mongoplus

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

type joinStep struct {
	source     Source
	local      field.Named
	foreign    field.Named
	alias      string
	mode       string
	option     QueryOption
	conditions []Condition
	group      *groupSpec
	unwind     *unwindSpec
	sort       bson.D
}

// Aggregation 是只读查询、关联与分组统计构造器，每次查询创建一个，不跨 goroutine 修改。
type Aggregation struct {
	ctx           context.Context
	source        collectionSource
	option        QueryOption
	steps         []joinStep
	project       bson.D
	selectAliases bson.D
	err           error
}

// From 从仓储开始关联查询；每个集合继续使用自己的租户与逻辑删除规则。
func From(source Source, opts ...QueryOption) *Aggregation {
	return &Aggregation{source: getSource(source), option: mergeOptions(opts)}
}

// WithContext 从仓储创建独立查询链；沿用传入的租户、取消信号和事务上下文。
// 每次调用都会创建新构造器，不会修改 Repository 或其他查询链。
func (r *Repository[T, K]) WithContext(ctx context.Context) *Aggregation {
	return From(r).WithContext(ctx)
}

// WithContext 为当前查询绑定执行上下文，返回新构造器。
// 链式 Scan/ScanOne/ScanByPage 必须先绑定上下文，不会自动回退到后台 Context。
func (q *Aggregation) WithContext(ctx context.Context) *Aggregation {
	if q == nil {
		return nil
	}
	copy := *q
	copy.ctx = resolveContext(ctx)
	copy.steps = append([]joinStep(nil), q.steps...)
	for i := range copy.steps {
		if group := copy.steps[i].group; group != nil {
			cloned := *group
			cloned.metrics = append([]aggregateMetric(nil), group.metrics...)
			copy.steps[i].group = &cloned
		}
	}
	copy.option.sort = append(bson.D(nil), q.option.sort...)
	copy.option.projection = append(bson.D(nil), q.option.projection...)
	copy.project = append(bson.D(nil), q.project...)
	copy.selectAliases = append(bson.D(nil), q.selectAliases...)
	return &copy
}

// Lookup 将匹配的关联文档保存为数组；不展开主记录，适合一对多。
// opts 仅作用于关联集合，可以设置过滤、排序、每个主记录的关联条数和投影。
func (q *Aggregation) Lookup(source Source, local, foreign field.Named, alias string, opts ...QueryOption) *Aggregation {
	return q.addJoin(source, local, foreign, alias, "array", opts)
}

// LeftJoin 关联后展开结果；没有匹配时保留主记录，多个匹配会产生多行。
func (q *Aggregation) LeftJoin(source Source, local, foreign field.Named, alias string, opts ...QueryOption) *Aggregation {
	return q.addJoin(source, local, foreign, alias, "left", opts)
}

// InnerJoin 关联后展开结果，只返回存在匹配的记录。
func (q *Aggregation) InnerJoin(source Source, local, foreign field.Named, alias string, opts ...QueryOption) *Aggregation {
	return q.addJoin(source, local, foreign, alias, "inner", opts)
}

func (q *Aggregation) addJoin(source Source, local, foreign field.Named, alias, mode string, opts []QueryOption) *Aggregation {
	q.steps = append(q.steps, joinStep{source: source, local: local, foreign: foreign, alias: alias, mode: mode, option: mergeOptions(opts)})
	return q
}

// Where 在当前位置追加过滤；放在 Join 后可通过字段.At("别名") 查询关联字段。
func (q *Aggregation) Where(conditions ...Condition) *Aggregation {
	q.steps = append(q.steps, joinStep{conditions: append([]Condition(nil), conditions...)})
	return q
}

// WhereIf 条件成立时追加过滤，避免为可选参数手写 BSON。
func (q *Aggregation) WhereIf(ok bool, conditions ...Condition) *Aggregation {
	if ok {
		q.Where(conditions...)
	}
	return q
}

// WhereOrGroup 追加 AND (... OR ...) 条件组。
func (q *Aggregation) WhereOrGroup(conditions ...Condition) *Aggregation {
	return q.Where(field.Or(conditions...))
}

// Order 设置最终结果排序；分页时补充主键与展开后的关联主键作为稳定排序键。
func (q *Aggregation) Order(order ...bson.E) *Aggregation {
	q.option.sort = append(q.option.sort, order...)
	return q
}

// Select 指定结果字段；关联字段使用 DAO 字段的 At 方法添加别名。
func (q *Aggregation) Select(fields ...field.Named) *Aggregation {
	for _, f := range fields {
		name, err := namedPath(f)
		if err != nil {
			q.err = err
			return q
		}
		if aliased, ok := f.(interface{ Alias() string }); ok {
			if !validAggregateAlias(aliased.Alias()) {
				q.err = fmt.Errorf("无效投影别名 %q", aliased.Alias())
				return q
			}
			q.selectAliases = append(q.selectAliases, bson.E{Key: aliased.Alias(), Value: "$" + name})
		} else {
			q.option.projection = append(q.option.projection, bson.E{Key: name, Value: 1})
		}
	}
	return q
}

// Project 设置原生结果投影，可将 "$department.name" 映射为扁平 VO 字段。
// 表达式必须由可信业务代码构造；不与 Select 同时使用。
func (q *Aggregation) Project(projection bson.D) *Aggregation {
	q.project = append(bson.D(nil), projection...)
	return q
}

// Limit 限制最终行数；关联子集合条数使用 Join 的查询选项单独限制。
func (q *Aggregation) Limit(limit int64) *Aggregation { q.option.limit = limit; return q }

// Offset 设置最终行偏移；ScanByPage 请使用其 page、size 参数。
func (q *Aggregation) Offset(offset int64) *Aggregation { q.option.skip = offset; return q }

// WithDeleted 仅放开主集合的逻辑删除过滤，关联集合仍默认过滤删除数据。
func (q *Aggregation) WithDeleted() *Aggregation { q.option.deleted = 1; return q }

// OnlyDeleted 仅选择主集合已删除记录。
func (q *Aggregation) OnlyDeleted() *Aggregation { q.option.deleted = 2; return q }

// Build 返回带完整隔离条件的聚合管道，用于调试或显式原生执行。
func (q *Aggregation) Build(ctx context.Context) (mongo.Pipeline, error) {
	ctx = resolveContext(ctx)
	base, _, err := q.buildBase(ctx, false)
	if err != nil {
		return nil, err
	}
	return q.finalStages(base, q.option.sort, q.option.skip, q.option.limit), nil
}

func (q *Aggregation) buildBase(ctx context.Context, paged bool) (mongo.Pipeline, bson.D, error) {
	if q == nil {
		return nil, nil, fmt.Errorf("关联查询不能为空")
	}
	if q.err != nil {
		return nil, nil, q.err
	}
	if q.source.err != nil {
		return nil, nil, q.source.err
	}
	if q.source.collection == nil || q.source.filter == nil || q.source.model == nil {
		return nil, nil, fmt.Errorf("关联查询缺少来源仓储，请使用 From 创建")
	}
	if err := aggregateOption(q.option); err != nil {
		return nil, nil, err
	}
	if len(q.project) > 0 && (len(q.option.projection) > 0 || len(q.selectAliases) > 0) {
		return nil, nil, fmt.Errorf("Project 与 Select 不能混用")
	}
	if len(q.selectAliases) > 0 {
		for _, p := range q.option.projection {
			if p.Key != "_id" && p.Value == 0 {
				return nil, nil, fmt.Errorf("As 别名投影不能与 Omit 混用")
			}
		}
	}
	if paged && (q.option.limit != 0 || q.option.skip != 0) {
		return nil, nil, fmt.Errorf("ScanByPage 使用 page、size，不应再设置 Limit 或 Offset")
	}
	for _, p := range q.project {
		if !field.ValidPath(p.Key) {
			return nil, nil, fmt.Errorf("无效投影字段 %q", p.Key)
		}
	}
	filter, err := q.source.filter(ctx, q.option, false)
	if err != nil {
		return nil, nil, err
	}
	stages := mongo.Pipeline{}
	if len(filter) > 0 {
		stages = append(stages, bson.D{{Key: "$match", Value: filter}})
	}
	aliases := map[string]collectionSource{}
	shape := newAggregateShape(q.source, aliases)
	stable := append(bson.D(nil), q.option.sort...)
	if paged {
		stable = stableSort(stable, "_id")
	}
	for _, step := range q.steps {
		if step.group != nil {
			groupStages, err := step.group.build(shape)
			if err != nil {
				return nil, nil, err
			}
			stages = append(stages, groupStages...)
			stable = append(bson.D(nil), q.option.sort...)
			if paged {
				stable = stableSort(stable, "_id")
			}
			continue
		}
		if step.unwind != nil {
			stage, err := step.unwind.build(shape)
			if err != nil {
				return nil, nil, err
			}
			stages = append(stages, stage)
			if paged {
				stable = stableSort(stable, step.unwind.index)
			}
			continue
		}
		if step.mode == "sort" {
			if len(step.sort) == 0 {
				return nil, nil, fmt.Errorf("Sort 至少需要一个排序字段")
			}
			seen := map[string]bool{}
			for _, order := range step.sort {
				if !field.ValidPath(order.Key) || seen[order.Key] || (order.Value != 1 && order.Value != -1) {
					return nil, nil, fmt.Errorf("Sort 只支持有效字段的升序/降序")
				}
				seen[order.Key] = true
				if _, ok := shape.resolve(order.Key); !ok {
					return nil, nil, fmt.Errorf("排序字段 %q 不属于当前文档", order.Key)
				}
			}
			stages = append(stages, bson.D{{Key: "$sort", Value: step.sort}})
			shape.ordered = true
			continue
		}
		if step.mode == "" {
			condition := field.And(step.conditions...)
			if condition.Err != nil {
				return nil, nil, condition.Err
			}
			stages = append(stages, bson.D{{Key: "$match", Value: condition.Document}})
			continue
		}
		if shape.grouped {
			return nil, nil, fmt.Errorf("请在 GroupBy/统计函数之前完成关联查询")
		}
		foreign := getSource(step.source)
		if foreign.err != nil {
			return nil, nil, foreign.err
		}
		if foreign.collection.Database().Client() != q.source.collection.Database().Client() || foreign.collection.Database().Name() != q.source.collection.Database().Name() {
			return nil, nil, fmt.Errorf("关联集合必须使用同一 Client 和数据库")
		}
		if !validAggregateAlias(step.alias) {
			return nil, nil, fmt.Errorf("关联别名必须是有效的顶层字段名")
		}
		if _, ok := aliases[step.alias]; ok {
			return nil, nil, fmt.Errorf("关联别名重复 %q", step.alias)
		}
		if _, exists := bsonPathType(q.source.model, step.alias); exists || reservedAlias(q.source.config, step.alias) {
			return nil, nil, fmt.Errorf("关联别名 %q 覆盖了主集合字段", step.alias)
		}
		localPath, err := namedPath(step.local)
		if err != nil {
			return nil, nil, err
		}
		foreignPath, err := namedPath(step.foreign)
		if err != nil {
			return nil, nil, err
		}
		localType, ok := shape.resolve(localPath)
		if !ok {
			return nil, nil, fmt.Errorf("关联字段 %q 不属于主集合或已声明的关联结果", localPath)
		}
		foreignType, ok := bsonPathType(foreign.model, foreignPath)
		if !ok {
			return nil, nil, fmt.Errorf("关联字段 %q 不属于 %s", foreignPath, foreign.config.Name)
		}
		if !compatibleJoinTypes(localType, foreignType) {
			return nil, nil, fmt.Errorf("关联字段 BSON 类型不兼容: %s 与 %s", localType, foreignType)
		}
		if err = aggregateOption(step.option); err != nil {
			return nil, nil, err
		}
		if paged && step.mode != "array" && excludesID(step.option.projection) {
			return nil, nil, fmt.Errorf("展开关联的分页需要保留关联集合 _id")
		}
		lookup, err := lookupStage(ctx, foreign, step, localPath, foreignPath)
		if err != nil {
			return nil, nil, err
		}
		stages = append(stages, lookup)
		if step.mode != "array" {
			stages = append(stages, bson.D{{Key: "$unwind", Value: bson.D{{Key: "path", Value: "$" + step.alias}, {Key: "preserveNullAndEmptyArrays", Value: step.mode == "left"}}}})
			if paged {
				stable = stableSort(stable, step.alias+"._id")
			}
		}
		aliases[step.alias] = foreign
		shape.overrides[step.alias] = foreign.model
		if step.mode == "array" {
			shape.overrides[step.alias] = reflect.SliceOf(foreign.model)
		}
		shape.ordered = false
	}
	return stages, stable, nil
}

func lookupStage(ctx context.Context, source collectionSource, step joinStep, localPath, foreignPath string) (bson.D, error) {
	filter, err := source.filter(ctx, step.option, false)
	if err != nil {
		return nil, err
	}
	// 不将缺失、null 或空引用数组当作一个可关联的业务键。
	guard := Condition{Document: bson.D{{Key: "$expr", Value: bson.D{{Key: "$and", Value: bson.A{
		bson.D{{Key: "$ne", Value: bson.A{"$$mp_local", nil}}},
		bson.D{{Key: "$ne", Value: bson.A{"$$mp_local", bson.A{}}}},
	}}}}}}
	condition := field.And(Condition{Document: filter}, field.New[any](foreignPath).Neq(nil), guard)
	if condition.Err != nil {
		return nil, condition.Err
	}
	pipeline := mongo.Pipeline{bson.D{{Key: "$match", Value: condition.Document}}}
	sort := append(bson.D(nil), step.option.sort...)
	if len(sort) > 0 || step.option.limit > 0 || step.option.skip > 0 {
		sort = stableSort(sort, "_id")
	}
	if len(sort) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$sort", Value: sort}})
	}
	if step.option.skip > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$skip", Value: step.option.skip}})
	}
	if step.option.limit > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$limit", Value: step.option.limit}})
	}
	if len(step.option.projection) > 0 {
		pipeline = append(pipeline, bson.D{{Key: "$project", Value: step.option.projection}})
	}
	return bson.D{{Key: "$lookup", Value: bson.D{
		{Key: "from", Value: source.config.Name}, {Key: "localField", Value: localPath}, {Key: "foreignField", Value: foreignPath},
		{Key: "let", Value: bson.D{{Key: "mp_local", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$" + localPath, nil}}}}}},
		{Key: "pipeline", Value: pipeline}, {Key: "as", Value: step.alias},
	}}}, nil
}

func (q *Aggregation) finalStages(stages mongo.Pipeline, sort bson.D, skip, limit int64) mongo.Pipeline {
	if len(sort) > 0 {
		stages = append(stages, bson.D{{Key: "$sort", Value: sort}})
	}
	if skip > 0 {
		stages = append(stages, bson.D{{Key: "$skip", Value: skip}})
	}
	if limit > 0 {
		stages = append(stages, bson.D{{Key: "$limit", Value: limit}})
	}
	projection := append(append(bson.D(nil), q.option.projection...), q.selectAliases...)
	if len(q.project) > 0 {
		projection = q.project
	}
	if len(projection) > 0 {
		stages = append(stages, bson.D{{Key: "$project", Value: projection}})
	}
	hidden := bson.A{}
	for _, step := range q.steps {
		if step.unwind != nil {
			hidden = append(hidden, step.unwind.index)
		}
	}
	if len(hidden) > 0 {
		stages = append(stages, bson.D{{Key: "$unset", Value: hidden}})
	}
	return stages
}

func aggregateOption(o QueryOption) error {
	if err := o.validate(); err != nil {
		return err
	}
	if o.cacheTTL > 0 || o.singleFlight {
		return fmt.Errorf("关联查询不使用单集合缓存或 SingleFlight，请移除这些选项")
	}
	return nil
}

func namedPath(f field.Named) (string, error) {
	if f == nil || (reflect.ValueOf(f).Kind() == reflect.Pointer && reflect.ValueOf(f).IsNil()) {
		return "", fmt.Errorf("关联字段不能为空")
	}
	name := f.Name()
	if !field.ValidPath(name) {
		return "", fmt.Errorf("无效关联字段 %q", name)
	}
	return name, nil
}

func reservedAlias(c CollectionConfig, name string) bool {
	return name == "_id" || name == "deleted_at" || name == "created_at" || name == "updated_at" || name == c.TenantField || name == c.CreatedByField || name == c.UpdatedByField
}

func compatibleJoinTypes(a, b reflect.Type) bool {
	base := func(t reflect.Type) reflect.Type {
		for t.Kind() == reflect.Pointer || (t.Kind() == reflect.Slice && t.Elem().Kind() != reflect.Uint8) {
			t = t.Elem()
		}
		return t
	}
	a = base(a)
	b = base(b)
	if a == b {
		return true
	}
	numeric := func(k reflect.Kind) bool {
		switch k {
		case reflect.Int, reflect.Int32, reflect.Int64, reflect.Float32, reflect.Float64:
			return true
		}
		return false
	}
	return (a.Kind() == reflect.String && b.Kind() == reflect.String) || (numeric(a.Kind()) && numeric(b.Kind()))
}

func stableSort(sort bson.D, path string) bson.D {
	for _, s := range sort {
		if s.Key == path {
			return sort
		}
	}
	return append(sort, bson.E{Key: path, Value: 1})
}

func excludesID(projection bson.D) bool {
	for _, p := range projection {
		if p.Key == "_id" && p.Value == 0 {
			return true
		}
	}
	return false
}

func (q *Aggregation) observe(ctx context.Context, operation string) func(error) {
	start := time.Now()
	return func(err error) {
		if q != nil && q.source.db != nil && q.source.db.Observer != nil {
			q.source.db.Observer(ctx, Observation{Collection: q.source.config.Name, Operation: operation, Duration: time.Since(start), Err: err})
		}
	}
}

// Scan 执行关联查询并直接解码到业务 VO，默认绕过单集合读缓存。
// 函数入口保留编译期结果类型；也可使用 WithContext(ctx).Scan(&rows)。
func Scan[R any](ctx context.Context, q *Aggregation) (result []R, err error) {
	result = []R{}
	err = q.scan(ctx, &result)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// ScanOne 获取关联查询的第一行；不存在时返回 ErrNotFound。
func ScanOne[R any](ctx context.Context, q *Aggregation) (*R, error) {
	if q == nil {
		return nil, fmt.Errorf("关联查询不能为空")
	}
	copy := *q
	copy.option.limit = 1
	list, err := Scan[R](ctx, &copy)
	if err != nil {
		return nil, err
	}
	if len(list) == 0 {
		return nil, ErrNotFound
	}
	return &list[0], nil
}

// ScanByPage 对关联和过滤后的最终行分页，分别执行总数和列表聚合。
// LeftJoin/InnerJoin 展开为多行时，total 是展开后的行数；Lookup 数组不增加主记录行数。
// 两次查询默认不保证同一快照；需要一致快照时使用 snapshot 读关注的事务。
func ScanByPage[R any](ctx context.Context, q *Aggregation, page, size int64) (result []R, total int64, err error) {
	result = []R{}
	total, err = q.scanByPage(ctx, &result, page, size)
	if err != nil {
		return nil, 0, err
	}
	return result, total, nil
}

// Scan 将查询结果写入非 nil 的切片指针，如 &[]UserDepartmentVO{}。
// 先通过 Repository.WithContext(ctx) 或 From(...).WithContext(ctx) 绑定上下文。
func (q *Aggregation) Scan(dest any) error {
	ctx, err := q.executionContext()
	if err != nil {
		return err
	}
	return q.scan(ctx, dest)
}

// ScanOne 将首行写入结构体或 map 指针；无匹配时返回 ErrNotFound，不修改目标。
func (q *Aggregation) ScanOne(dest any) error {
	ctx, err := q.executionContext()
	if err != nil {
		return err
	}
	value := reflect.ValueOf(dest)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() ||
		(value.Elem().Kind() != reflect.Struct && value.Elem().Kind() != reflect.Map) {
		return fmt.Errorf("ScanOne 结果必须是非 nil 的结构体或 map 指针")
	}
	copy := *q
	copy.option.limit = 1
	list := reflect.New(reflect.SliceOf(value.Elem().Type()))
	if err := copy.scan(ctx, list.Interface()); err != nil {
		return err
	}
	if list.Elem().Len() == 0 {
		return ErrNotFound
	}
	value.Elem().Set(list.Elem().Index(0))
	return nil
}

// ScanByPage 将当前页写入切片指针，返回 (total, err)；page 从 1 开始。
// 空结果与超出末页返回非 nil 空切片；超出末页仍保留真实 total。
func (q *Aggregation) ScanByPage(dest any, page, size int64) (int64, error) {
	ctx, err := q.executionContext()
	if err != nil {
		return 0, err
	}
	return q.scanByPage(ctx, dest, page, size)
}

func (q *Aggregation) executionContext() (context.Context, error) {
	if q == nil {
		return nil, fmt.Errorf("关联查询不能为空")
	}
	if q.ctx == nil {
		return nil, fmt.Errorf("请先调用 WithContext(ctx) 绑定查询上下文")
	}
	return q.ctx, nil
}

func scanDestination(dest any) (reflect.Value, error) {
	value := reflect.ValueOf(dest)
	if !value.IsValid() || value.Kind() != reflect.Pointer || value.IsNil() || value.Elem().Kind() != reflect.Slice {
		return reflect.Value{}, fmt.Errorf("查询结果必须是非 nil 的切片指针")
	}
	return value.Elem(), nil
}

func (q *Aggregation) scan(ctx context.Context, dest any) (err error) {
	ctx = resolveContext(ctx)
	end := q.observe(ctx, "Scan")
	defer func() { end(err) }()
	value, err := scanDestination(dest)
	if err != nil {
		return err
	}
	pipeline, err := q.Build(ctx)
	if err != nil {
		return err
	}
	cursor, err := q.source.collection.Aggregate(ctx, pipeline, options.Aggregate().SetAllowDiskUse(true))
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	err = cursor.All(ctx, dest)
	if err == nil && value.IsNil() {
		value.Set(reflect.MakeSlice(value.Type(), 0, 0))
	}
	return err
}

type aggregatePageCount struct {
	Value int64 `bson:"value"`
}

func (q *Aggregation) scanByPage(ctx context.Context, dest any, page, size int64) (total int64, err error) {
	ctx = resolveContext(ctx)
	end := q.observe(ctx, "ScanByPage")
	defer func() { end(err) }()
	value, err := scanDestination(dest)
	if err != nil {
		return 0, err
	}
	base, sort, err := q.buildBase(ctx, true)
	if err != nil {
		return 0, err
	}
	offset, limit := DbPage(page, size)
	// 分开计数和读取，避免 $facet 将整页打包成一个 BSON 文档及其不可落盘的内存限制。
	total, err = q.countRows(ctx, base)
	if err != nil {
		return 0, err
	}
	list := q.finalStages(append(mongo.Pipeline(nil), base...), sort, offset, limit)
	cursor, err := q.source.collection.Aggregate(ctx, list, options.Aggregate().SetAllowDiskUse(true))
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)
	// 继续使用 Client 自身的解码配置；只在整页读取成功后替换调用者的结果。
	items := reflect.New(value.Type())
	if err = cursor.All(ctx, items.Interface()); err != nil {
		return 0, err
	}
	if items.Elem().IsNil() {
		items.Elem().Set(reflect.MakeSlice(value.Type(), 0, 0))
	}
	value.Set(items.Elem())
	return total, nil
}

func (q *Aggregation) countRows(ctx context.Context, base mongo.Pipeline) (int64, error) {
	pipeline := append(append(mongo.Pipeline(nil), base...), bson.D{{Key: "$count", Value: "value"}})
	cursor, err := q.source.collection.Aggregate(ctx, pipeline, options.Aggregate().SetAllowDiskUse(true))
	if err != nil {
		return 0, err
	}
	defer cursor.Close(ctx)
	var count aggregatePageCount
	if cursor.Next(ctx) {
		if err = cursor.Decode(&count); err != nil {
			return 0, err
		}
	}
	return count.Value, cursor.Err()
}
