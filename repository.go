package mongoplus

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/internal/settings"
	"github.com/kuangshp/mongo-plus/plugin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ID 约束仓储主键，保持 FindById 等方法的编译期类型安全。
type ID interface{ int64 | string | bson.ObjectID }

// Repository 统一处理查询、逻辑删除、租户、审计字段和主键分配。
type Repository[T any, K ID] struct {
	db         *Database
	collection *mongo.Collection
	config     CollectionConfig
	tx         *transactionBinding
}

// NewRepository 创建仓储；T 必须为具有 ID 字段的结构体。
func NewRepository[T any, K ID](db *Database, c CollectionConfig) (*Repository[T, K], error) {
	if db == nil || db.native == nil {
		return nil, fmt.Errorf("数据库不能为空")
	}
	if c.Name == "" || settings.ReservedCollection(c.Name) {
		return nil, fmt.Errorf("集合名称为空或使用保留名称")
	}
	id, err := c.ID.Normalize()
	if err != nil {
		return nil, err
	}
	c.ID = id
	typ := reflect.TypeOf((*T)(nil)).Elem()
	if typ.Kind() != reflect.Struct {
		return nil, fmt.Errorf("实体类型必须为结构体")
	}
	f, ok := typ.FieldByName("ID")
	if !ok || len(f.Index) != 1 || f.Type != reflect.TypeOf((*K)(nil)).Elem() {
		return nil, fmt.Errorf("实体 ID 与仓储主键类型不一致")
	}
	expected := map[IDType]reflect.Type{IDTypeObjectID: reflect.TypeOf(bson.ObjectID{}), IDTypeInt64: reflect.TypeOf(int64(0)), IDTypeString: reflect.TypeOf("")}[id.Type]
	if f.Type != expected || strings.Split(f.Tag.Get("bson"), ",")[0] != "_id" {
		return nil, fmt.Errorf("实体 ID 与主键配置不一致")
	}
	reserved := map[string]bool{"_id": true, "deleted_at": true, "created_at": true, "updated_at": true}
	for _, path := range []string{c.TenantField, c.CreatedByField, c.UpdatedByField} {
		if path != "" && (!field.ValidPath(path) || strings.Contains(path, ".") || reserved[path]) {
			return nil, fmt.Errorf("无效保留字段配置 %q", path)
		}
		if path != "" {
			reserved[path] = true
		}
	}
	if err := db.RegisterCollections(c); err != nil {
		return nil, err
	}
	return &Repository[T, K]{db: db, collection: db.native.Collection(c.Name), config: c}, nil
}

// RegisterCollections 在启动时显式注册集合规则，DAL 不再依赖首次构造仓储的时机。
// 同名规则必须完全相同；一次调用整体校验通过后才发布，不允许后注册覆盖权限语义。
func (d *Database) RegisterCollections(configs ...CollectionConfig) error {
	if d == nil {
		return fmt.Errorf("数据库不能为空")
	}
	d.rulesMu.Lock()
	defer d.rulesMu.Unlock()
	pending := map[string]collectionPolicy{}
	for _, c := range configs {
		if !validDALCollection(c.Name) {
			return fmt.Errorf("无效集合名称 %q", c.Name)
		}
		id, err := c.ID.Normalize()
		if err != nil {
			return err
		}
		c.ID = id
		reserved := map[string]bool{"_id": true, "deleted_at": true, "created_at": true, "updated_at": true}
		for _, path := range []string{c.TenantField, c.CreatedByField, c.UpdatedByField} {
			if path != "" && (!field.ValidPath(path) || strings.Contains(path, ".") || reserved[path]) {
				return fmt.Errorf("无效保留字段配置 %q", path)
			}
			if path != "" {
				reserved[path] = true
			}
		}
		rule := collectionPolicy{config: c, softDelete: c.SoftDelete, timestamps: c.Timestamps, tenantField: c.TenantField}
		if old, ok := d.rules[c.Name]; ok && old.config != c {
			return fmt.Errorf("集合 %s 的规则与已注册配置冲突", c.Name)
		}
		if old, ok := pending[c.Name]; ok && old.config != c {
			return fmt.Errorf("集合 %s 的规则重复且不一致", c.Name)
		}
		pending[c.Name] = rule
	}
	if d.rules == nil {
		d.rules = map[string]collectionPolicy{}
	}
	for name, rule := range pending {
		d.rules[name] = rule
	}
	return nil
}

func (d *Database) collectionPolicyFor(name string) collectionPolicy {
	if d == nil {
		return collectionPolicy{}
	}
	d.rulesMu.Lock()
	defer d.rulesMu.Unlock()
	return d.rules[name]
}

// MustRepository 供经过 schema 校验的生成构造函数使用，配置错误在启动时暴露。
func MustRepository[T any, K ID](db *Database, c CollectionConfig) *Repository[T, K] {
	r, err := NewRepository[T, K](db, c)
	if err != nil {
		panic(err)
	}
	return r
}

// Native 返回官方 Collection；原生操作需要业务自行处理租户、软删除等规则。
func (r *Repository[T, K]) Native() *mongo.Collection { return r.collection }

func (r *Repository[T, K]) reveal(ctx context.Context, value any) error {
	if r == nil || r.db == nil || r.db.sensitive == nil {
		return nil
	}
	return r.db.sensitive.reveal(ctx, value)
}

func (r *Repository[T, K]) observe(ctx context.Context, operation string) func(error) {
	start := time.Now()
	return func(err error) {
		if r.db.Observer != nil {
			r.db.Observer(ctx, Observation{r.config.Name, operation, time.Since(start), err})
		}
	}
}

func (r *Repository[T, K]) filter(ctx context.Context, o QueryOption, write bool) (bson.D, error) {
	// 关联集合也必须核对绑定，不能把另一事务的仓储静默用于当前管道。
	if r.tx != nil {
		if r.tx.err != nil {
			return nil, r.tx.err
		}
		if err := r.tx.scope.check(r.db); err != nil {
			return nil, err
		}
		if mongo.SessionFromContext(ctx) != r.tx.scope.session || ctx.Value(transactionScopeKey{}) != r.tx.scope {
			return nil, ErrTransactionMismatch
		}
	}
	if err := o.validate(); err != nil {
		return nil, err
	}
	conditions := append([]Condition(nil), o.conditions...)
	if write && !o.allowAll && !plugin.GlobalAllowed(ctx) {
		has := false
		for _, c := range conditions {
			if len(c.Document) > 0 {
				has = true
			}
		}
		if !has {
			return nil, ErrUnsafeWrite
		}
	}
	tenantConditions, err := r.tenantFilters(ctx)
	if err != nil {
		return nil, err
	}
	conditions = append(conditions, tenantConditions...)
	if r.config.SoftDelete {
		switch o.deleted {
		case 0:
			conditions = append(conditions, field.New[any]("deleted_at").IsNull())
		case 2:
			conditions = append(conditions, field.And(field.New[any]("deleted_at").Exists(true), field.New[any]("deleted_at").Neq(nil)))
		}
	}
	if r.db != nil && r.db.permission != nil {
		extra, err := r.db.permission.Apply(ctx, r.config.Name)
		if err != nil {
			return nil, err
		}
		if len(extra) > 0 {
			conditions = append(conditions, Condition{Document: extra})
		}
	}
	if len(conditions) == 0 {
		return bson.D{}, nil
	}
	c := field.And(conditions...)
	return c.Document, c.Err
}

func (r *Repository[T, K]) tenantFilters(ctx context.Context) ([]Condition, error) {
	if plugin.TenantSkipped(ctx) {
		return nil, nil
	}
	if r.db != nil && r.db.tenant != nil {
		documents, _, err := r.db.tenant.Conditions(ctx, r.config.Name, r.config.TenantField)
		if errors.Is(err, plugin.ErrTenantRequired) {
			return nil, ErrTenantRequired
		}
		if err != nil {
			return nil, err
		}
		conditions := make([]Condition, 0, len(documents))
		for _, document := range documents {
			conditions = append(conditions, Condition{Document: document})
		}
		return conditions, nil
	}
	if r.config.TenantField == "" {
		return nil, nil
	}
	value := ctx.Value(tenantKey{})
	if value == nil || reflect.ValueOf(value).IsZero() {
		return nil, ErrTenantRequired
	}
	return []Condition{plugin.TenantCondition(r.config.TenantField, value)}, nil
}

func idOption[K ID](id K) QueryOption { return Query().Where(field.New[K]("_id").Eq(id)).Build() }

func (r *Repository[T, K]) validID(id K) error {
	if reflect.ValueOf(id).IsZero() {
		return ErrInvalidID
	}
	if value, ok := any(id).(int64); ok && value < 0 {
		return ErrInvalidID
	}
	return nil
}

// FindById 按主键获取未删除实体。
func (r *Repository[T, K]) FindById(ctx context.Context, id K, opts ...QueryOption) (*T, error) {
	if err := r.validID(id); err != nil {
		return nil, err
	}
	return r.FindOne(ctx, append([]QueryOption{idOption(id)}, opts...)...)
}

// FindByIdList 按主键集合查询。
func (r *Repository[T, K]) FindByIdList(ctx context.Context, ids []K, opts ...QueryOption) ([]*T, error) {
	return r.FindList(ctx, append([]QueryOption{Query().Where(field.New[K]("_id").In(ids...)).Build()}, opts...)...)
}

// FindOne 获取单条文档；不存在返回 ErrNotFound。
func (r *Repository[T, K]) findOne(ctx context.Context, opts ...QueryOption) (entity *T, err error) {
	end := r.observe(ctx, "FindOne")
	defer func() { end(err) }()
	o := mergeOptions(opts)
	filter, err := r.filter(ctx, o, false)
	if err != nil {
		return nil, err
	}
	if len(o.let) > 0 || o.batchSize > 0 || o.allowDiskUse != nil {
		o.limit = 1
		list, err := r.findList(ctx, o)
		if err != nil {
			return nil, err
		}
		if len(list) == 0 {
			return nil, ErrNotFound
		}
		return list[0], nil
	}
	find := options.FindOne().SetSkip(o.skip)
	if o.collation != nil {
		find.SetCollation(o.collation)
	}
	if o.hint != nil {
		find.SetHint(o.hint)
	}
	if o.comment != nil {
		find.SetComment(o.comment)
	}
	if len(o.sort) > 0 {
		find.SetSort(o.sort)
	}
	if len(o.projection) > 0 {
		find.SetProjection(o.projection)
	}
	entity = new(T)
	err = r.collection.FindOne(ctx, filter, find).Decode(entity)
	if err != nil {
		return nil, err
	}
	return entity, nil
}

// FindList 查询实体列表；大量导出请使用 Walk 流式读取。
func (r *Repository[T, K]) findList(ctx context.Context, opts ...QueryOption) (entities []*T, err error) {
	end := r.observe(ctx, "FindList")
	defer func() { end(err) }()
	o := mergeOptions(opts)
	filter, err := r.filter(ctx, o, false)
	if err != nil {
		return nil, err
	}
	find := options.Find().SetSkip(o.skip)
	applyFindOptions(find, o)
	if o.limit > 0 {
		find.SetLimit(o.limit)
	}
	if len(o.sort) > 0 {
		find.SetSort(o.sort)
	}
	if len(o.projection) > 0 {
		find.SetProjection(o.projection)
	}
	cursor, err := r.collection.Find(ctx, filter, find)
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	entities = []*T{}
	err = cursor.All(ctx, &entities)
	return entities, err
}

// Walk 按批次读取文档，避免将整个集合载入内存；回调错误会停止读取。
func (r *Repository[T, K]) Walk(ctx context.Context, fn func(*T) error, opts ...QueryOption) error {
	if fn == nil {
		return fmt.Errorf("Walk 回调不能为空")
	}
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	o := mergeOptions(opts)
	if o.cacheTTL > 0 || o.singleFlight {
		return fmt.Errorf("Walk 流式查询不支持缓存或请求合并")
	}
	filter, err := r.filter(ctx, o, false)
	if err != nil {
		return err
	}
	find := options.Find().SetBatchSize(100).SetSkip(o.skip)
	applyFindOptions(find, o)
	if o.limit > 0 {
		find.SetLimit(o.limit)
	}
	if len(o.sort) > 0 {
		find.SetSort(o.sort)
	}
	if len(o.projection) > 0 {
		find.SetProjection(o.projection)
	}
	cursor, err := r.collection.Find(ctx, filter, find)
	if err != nil {
		return err
	}
	defer cursor.Close(ctx)
	for cursor.Next(ctx) {
		entity := new(T)
		if err = cursor.Decode(entity); err != nil {
			return err
		}
		if err = r.reveal(ctx, entity); err != nil {
			return err
		}
		if err = fn(entity); err != nil {
			return err
		}
	}
	return cursor.Err()
}

// Count 按相同的租户与逻辑删除规则统计；忽略列表的 Limit、Offset。
func (r *Repository[T, K]) count(ctx context.Context, opts ...QueryOption) (int64, error) {
	o := mergeOptions(opts)
	filter, err := r.filter(ctx, o, false)
	if err != nil {
		return 0, err
	}
	count := options.Count()
	if o.collation != nil {
		count.SetCollation(o.collation)
	}
	if o.hint != nil {
		count.SetHint(o.hint)
	}
	if o.comment != nil {
		count.SetComment(o.comment)
	}
	if len(o.let) > 0 {
		return 0, fmt.Errorf("CountDocuments 不支持 Let")
	}
	return r.collection.CountDocuments(ctx, filter, count)
}

// Exists 判断是否存在，避免为存在性检查计算整个集合数量。
func (r *Repository[T, K]) exists(ctx context.Context, opts ...QueryOption) (bool, error) {
	o := mergeOptions(opts)
	o.projection = bson.D{{Key: "_id", Value: 1}}
	_, err := r.findOne(ctx, o)
	if errors.Is(err, ErrNotFound) {
		return false, nil
	}
	return err == nil, err
}

// FindPage 返回页码分页和精确总数；两次查询默认不保证来自同一快照。
func (r *Repository[T, K]) FindPage(ctx context.Context, page, size int64, opts ...QueryOption) ([]*T, int64, error) {
	o := mergeOptions(opts)
	var err error
	o, err = stableIDPageOption(o, r.config.ID.Type)
	if err != nil {
		return nil, 0, err
	}
	offset, limit := DbPage(page, size)
	o.skip = offset
	o.limit = limit
	hasID := false
	for _, sort := range o.sort {
		if sort.Key == "_id" {
			hasID = true
		}
	}
	if !hasID {
		o.sort = append(append(bson.D{}, o.sort...), bson.E{Key: "_id", Value: 1})
	}
	count, err := r.Count(ctx, o)
	if err != nil {
		return nil, 0, err
	}
	list, err := r.FindList(ctx, o)
	return list, count, err
}

// CursorPage 使用 _id 升序游标分页，不执行深度 skip 或总数统计。
type CursorPage[T any, K ID] struct {
	List    []*T `json:"list"`
	Next    *K   `json:"next,omitempty"`
	HasMore bool `json:"hasMore"`
}

// FindCursorPage 以最后一条 _id 继续读取；游标不是创建时间，也不代表一致性快照。
func (r *Repository[T, K]) FindCursorPage(ctx context.Context, after *K, size int64, opts ...QueryOption) (CursorPage[T, K], error) {
	o := mergeOptions(opts)
	if len(o.sort) > 0 || o.skip != 0 || len(o.projection) > 0 {
		return CursorPage[T, K]{}, fmt.Errorf("ID 游标分页不支持自定义排序、Offset 或投影")
	}
	// 字符串主键必须使用二进制全序；显式设置也覆盖集合默认 Collation。
	// 非 simple 规则会使 A/a 等不同主键比较相等，导致严格大于游标跳过记录。
	var err error
	o, err = stableIDPageOption(o, r.config.ID.Type)
	if err != nil {
		return CursorPage[T, K]{}, err
	}
	_, size = DbPage(1, size)
	o.limit = size + 1
	o.sort = bson.D{{Key: "_id", Value: 1}}
	if after != nil {
		if err := r.validID(*after); err != nil {
			return CursorPage[T, K]{}, err
		}
		o.conditions = append(o.conditions, field.New[K]("_id").Gt(*after))
	}
	list, err := r.FindList(ctx, o)
	if err != nil {
		return CursorPage[T, K]{}, err
	}
	result := CursorPage[T, K]{List: list, HasMore: int64(len(list)) > size}
	if result.HasMore {
		result.List = list[:size]
		id := reflect.ValueOf(result.List[len(result.List)-1]).Elem().FieldByName("ID").Interface().(K)
		result.Next = &id
	}
	return result, nil
}

// 所有依赖 _id 保证稳定顺序的分页入口共用该约束，避免只修游标而遗漏页码/关联分页。
func stableIDPageOption(o QueryOption, idType IDType) (QueryOption, error) {
	if idType == IDTypeString {
		if o.collation != nil && o.collation.Locale != "simple" {
			return o, fmt.Errorf("字符串 ID 稳定分页仅支持 simple Collation；语言比较请使用独立数值/ObjectID 分页键或原生扩展")
		}
		o.collation = &options.Collation{Locale: "simple"}
	}
	return o, nil
}

func applyFindOptions(find *options.FindOptionsBuilder, o QueryOption) {
	if o.collation != nil {
		find.SetCollation(o.collation)
	}
	if o.hint != nil {
		find.SetHint(o.hint)
	}
	if o.let != nil {
		find.SetLet(o.let)
	}
	if o.comment != nil {
		find.SetComment(o.comment)
	}
	if o.batchSize > 0 {
		find.SetBatchSize(o.batchSize)
	}
	if o.allowDiskUse != nil {
		find.SetAllowDiskUse(*o.allowDiskUse)
	}
}
