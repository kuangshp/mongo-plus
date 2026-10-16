package mongoplus

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/kuangshp/mongo-plus/field"
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
}

// NewRepository 创建仓储；T 必须为具有 ID 字段的结构体。
func NewRepository[T any, K ID](db *Database, c CollectionConfig) (*Repository[T, K], error) {
	if db == nil || db.native == nil {
		return nil, fmt.Errorf("数据库不能为空")
	}
	if c.Name == "" || c.Name == "__mongo_plus_sequences" {
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
	return &Repository[T, K]{db: db, collection: db.native.Collection(c.Name), config: c}, nil
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
	if err := o.validate(); err != nil {
		return nil, err
	}
	conditions := append([]Condition(nil), o.conditions...)
	if write && !o.allowAll {
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
	if r.config.TenantField != "" {
		value := ctx.Value(tenantKey{})
		if value == nil || reflect.ValueOf(value).IsZero() {
			return nil, ErrTenantRequired
		}
		conditions = append(conditions, field.New[any](r.config.TenantField).Eq(value))
	}
	if r.config.SoftDelete {
		switch o.deleted {
		case 0:
			conditions = append(conditions, field.New[any]("deleted_at").IsNull())
		case 2:
			conditions = append(conditions, field.And(field.New[any]("deleted_at").Exists(true), field.New[any]("deleted_at").Neq(nil)))
		}
	}
	if r.db != nil && r.db.permission != nil {
		extra, err := r.db.permission.apply(ctx, r.config.Name)
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
	find := options.FindOne().SetSkip(o.skip)
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
	ctx = resolveContext(ctx)
	o := mergeOptions(opts)
	filter, err := r.filter(ctx, o, false)
	if err != nil {
		return err
	}
	find := options.Find().SetBatchSize(100).SetSkip(o.skip)
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
	filter, err := r.filter(ctx, mergeOptions(opts), false)
	if err != nil {
		return 0, err
	}
	return r.collection.CountDocuments(ctx, filter)
}

// Exists 判断是否存在，避免为存在性检查计算整个集合数量。
func (r *Repository[T, K]) exists(ctx context.Context, opts ...QueryOption) (bool, error) {
	filter, err := r.filter(ctx, mergeOptions(opts), false)
	if err != nil {
		return false, err
	}
	err = r.collection.FindOne(ctx, filter, options.FindOne().SetProjection(bson.D{{Key: "_id", Value: 1}})).Err()
	if err == mongo.ErrNoDocuments {
		return false, nil
	}
	return err == nil, err
}

// FindPage 返回页码分页和精确总数；两次查询默认不保证来自同一快照。
func (r *Repository[T, K]) FindPage(ctx context.Context, page, size int64, opts ...QueryOption) ([]*T, int64, error) {
	o := mergeOptions(opts)
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
