package mongoplus

import (
	"context"
	"fmt"
	"reflect"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/plugin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// ModifyOptions 配置原子查改/查替换；零值返回修改前的文档，与官方驱动一致。
// Upsert 不支持自增主键；手动 string/int64 主键必须在 Query 中提供唯一的 _id 等值条件。
// 返回 ErrNotFound 时可能是成功 upsert 但选择了 Before；需要新文档时设置 After。
type ModifyOptions struct {
	ReturnDocument options.ReturnDocument
	Upsert         bool
}

func (r *Repository[T, K]) protectedUpdateFields() []string {
	paths := []string{"_id"}
	if r.config.SoftDelete {
		paths = append(paths, "deleted_at")
	}
	if r.config.Timestamps {
		paths = append(paths, "created_at", "updated_at")
	}
	for _, p := range []string{r.config.CreatedByField, r.config.UpdatedByField} {
		if p != "" {
			paths = append(paths, p)
		}
	}
	return append(paths, r.tenantProtectedFields()...)
}

func (r *Repository[T, K]) prepareUpdate(ctx context.Context, ops []UpdateOp) (bson.D, error) {
	var err error
	if r.db.sensitive != nil {
		ops, err = r.db.sensitive.rewrite(ops, reflect.TypeFor[T]())
		if err != nil {
			return nil, err
		}
	}
	update, err := buildUpdate(ops, r.protectedUpdateFields()...)
	if err != nil {
		return nil, err
	}
	return r.auditUpdate(ctx, update)
}

func validateModifyQuery(o QueryOption) error {
	if o.skip != 0 || o.limit != 0 || o.cacheTTL != 0 || o.singleFlight || o.batchSize != 0 || o.allowDiskUse != nil {
		return fmt.Errorf("原子写操作不支持分页、缓存或游标选项")
	}
	return o.validate()
}

// FindOneAndUpdate 在一次服务端原子操作中按条件更新并返回文档，沿用租户、敏感字段、审计与提交后缓存失效。
// 无业务条件必须显式 AllowAll；排序可用于任务领取。并发冲突由过滤条件/事务语义决定。
func (r *Repository[T, K]) FindOneAndUpdate(ctx context.Context, query QueryOption, modify ModifyOptions, updates ...UpdateOption) (entity *T, err error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	end := r.observe(ctx, "FindOneAndUpdate")
	defer func() { end(err) }()
	if err = validateModifyQuery(query); err != nil {
		return nil, err
	}
	if modify.ReturnDocument != options.Before && modify.ReturnDocument != options.After {
		return nil, fmt.Errorf("无效 ReturnDocument")
	}
	if err = r.validateUpsertPolicy(modify.Upsert); err != nil {
		return nil, err
	}
	filter, err := r.filter(ctx, query, true)
	if err != nil {
		return nil, err
	}
	update, err := r.prepareUpdate(ctx, flattenUpdates(updates))
	if err != nil {
		return nil, err
	}
	if modify.Upsert {
		update, err = r.upsertDefaults(ctx, query, update)
		if err != nil {
			return nil, err
		}
	}
	find := options.FindOneAndUpdate().SetUpsert(modify.Upsert).SetReturnDocument(modify.ReturnDocument)
	if len(query.sort) > 0 {
		find.SetSort(query.sort)
	}
	if len(query.projection) > 0 {
		find.SetProjection(query.projection)
	}
	if query.collation != nil {
		find.SetCollation(query.collation)
	}
	if query.hint != nil {
		find.SetHint(query.hint)
	}
	if query.let != nil {
		find.SetLet(query.let)
	}
	if query.comment != nil {
		find.SetComment(query.comment)
	}
	defer r.invalidate(ctx)
	entity = new(T)
	result := r.collection.FindOneAndUpdate(ctx, filter, update, find)
	if err = result.Err(); err != nil {
		return nil, err
	}
	if !result.Acknowledged {
		return nil, ErrUnacknowledgedWrite
	}
	if err = result.Decode(entity); err != nil {
		return nil, err
	}
	if err = r.reveal(ctx, entity); err != nil {
		return nil, err
	}
	return entity, nil
}

// FindOneAndDelete 原子返回并删除首条文档；启用逻辑删除时原子设置 deleted_at 并返回删除前内容。
// 使用物理删除选项时直接调用官方 FindOneAndDelete；租户/数据权限仍然生效。
func (r *Repository[T, K]) FindOneAndDelete(ctx context.Context, query QueryOption, opts ...DeleteOption) (entity *T, err error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	end := r.observe(ctx, "FindOneAndDelete")
	defer func() { end(err) }()
	if err = validateModifyQuery(query); err != nil {
		return nil, err
	}
	if r.config.SoftDelete && !physicalDelete(opts) {
		filter, e := r.filter(ctx, query, true)
		if e != nil {
			return nil, e
		}
		update, e := r.auditUpdate(ctx, softDeleteUpdate(r.config.Timestamps))
		if e != nil {
			return nil, e
		}
		find := options.FindOneAndUpdate().SetReturnDocument(options.Before)
		if query.let != nil {
			find.SetLet(query.let)
		}
		if len(query.sort) > 0 {
			find.SetSort(query.sort)
		}
		if len(query.projection) > 0 {
			find.SetProjection(query.projection)
		}
		if query.collation != nil {
			find.SetCollation(query.collation)
		}
		if query.hint != nil {
			find.SetHint(query.hint)
		}
		if query.comment != nil {
			find.SetComment(query.comment)
		}
		defer r.invalidate(ctx)
		entity = new(T)
		res := r.collection.FindOneAndUpdate(ctx, filter, update, find)
		if err = res.Err(); err != nil {
			return nil, err
		}
		if !res.Acknowledged {
			return nil, ErrUnacknowledgedWrite
		}
		if err = res.Decode(entity); err != nil {
			return nil, err
		}
	} else {
		if physicalDelete(opts) {
			query.deleted = 1
		}
		filter, e := r.filter(ctx, query, true)
		if e != nil {
			return nil, e
		}
		find := options.FindOneAndDelete()
		if query.let != nil {
			find.SetLet(query.let)
		}
		if len(query.sort) > 0 {
			find.SetSort(query.sort)
		}
		if len(query.projection) > 0 {
			find.SetProjection(query.projection)
		}
		if query.collation != nil {
			find.SetCollation(query.collation)
		}
		if query.hint != nil {
			find.SetHint(query.hint)
		}
		if query.comment != nil {
			find.SetComment(query.comment)
		}
		defer r.invalidate(ctx)
		entity = new(T)
		res := r.collection.FindOneAndDelete(ctx, filter, find)
		if err = res.Err(); err != nil {
			return nil, err
		}
		if !res.Acknowledged {
			return nil, ErrUnacknowledgedWrite
		}
		if err = res.Decode(entity); err != nil {
			return nil, err
		}
	}
	if err = r.reveal(ctx, entity); err != nil {
		return nil, err
	}
	return entity, nil
}

// FindOneAndReplace 原子替换同主键实体；保留原 _id/创建时间/创建人，并重新应用租户、自动填充及加密。
// 内部使用更新管道保留系统字段；未获确认前不修改调用者的 replacement。
func (r *Repository[T, K]) FindOneAndReplace(ctx context.Context, query QueryOption, replacement *T, modify ModifyOptions) (entity *T, err error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	end := r.observe(ctx, "FindOneAndReplace")
	defer func() { end(err) }()
	if replacement == nil {
		return nil, fmt.Errorf("替换实体不能为空")
	}
	if err = validateModifyQuery(query); err != nil {
		return nil, err
	}
	if modify.ReturnDocument != options.Before && modify.ReturnDocument != options.After {
		return nil, fmt.Errorf("无效 ReturnDocument")
	}
	if err = r.validateUpsertPolicy(modify.Upsert); err != nil {
		return nil, err
	}
	id := reflect.ValueOf(replacement).Elem().FieldByName("ID").Interface().(K)
	if err = r.validID(id); err != nil {
		return nil, err
	}
	query.conditions = append(append([]Condition(nil), query.conditions...), field.New[K]("_id").Eq(id))
	filter, err := r.filter(ctx, query, true)
	if err != nil {
		return nil, err
	}
	if len(r.tenantProtectedFields()) > 0 && plugin.TenantSkipped(ctx) {
		return nil, fmt.Errorf("跨租户原子替换请使用 DALNative 并显式维护归属")
	}
	prepared, err := r.prepareCreate(ctx, replacement, id)
	if err != nil {
		return nil, err
	}
	preserved := bson.D{{Key: "_id", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$_id", id}}}}}
	if r.config.SoftDelete {
		preserved = append(preserved, bson.E{Key: "deleted_at", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$deleted_at", nil}}}})
	}
	if r.config.Timestamps {
		preserved = append(preserved, bson.E{Key: "created_at", Value: bson.D{{Key: "$ifNull", Value: bson.A{"$created_at", time.Now().UTC()}}}})
	}
	if r.config.CreatedByField != "" {
		preserved = append(preserved, bson.E{Key: r.config.CreatedByField, Value: bson.D{{Key: "$ifNull", Value: bson.A{"$" + r.config.CreatedByField, bson.D{{Key: "$literal", Value: ctx.Value(actorKey{})}}}}}})
	}
	pipeline := bson.A{bson.D{{Key: "$replaceWith", Value: bson.D{{Key: "$mergeObjects", Value: bson.A{bson.D{{Key: "$literal", Value: prepared.document}}, preserved}}}}}}
	audit, err := r.auditUpdate(ctx, bson.D{{Key: "$set", Value: bson.D{{Key: "__mp_replace_marker", Value: true}}}})
	if err != nil {
		return nil, err
	}
	for _, group := range audit {
		fields := bson.D{}
		for _, item := range group.Value.(bson.D) {
			if item.Key != "__mp_replace_marker" {
				fields = append(fields, item)
			}
		}
		if len(fields) > 0 { // 管道中的业务值必须使用 $literal，不能把 $ 开头的文本当作表达式。
			literal := bson.D{}
			for _, item := range fields {
				literal = append(literal, bson.E{Key: item.Key, Value: bson.D{{Key: "$literal", Value: item.Value}}})
			}
			pipeline = append(pipeline, bson.D{{Key: group.Key, Value: literal}})
		}
	}
	find := options.FindOneAndUpdate().SetUpsert(modify.Upsert).SetReturnDocument(modify.ReturnDocument)
	if query.collation != nil {
		find.SetCollation(query.collation)
	}
	if query.hint != nil {
		find.SetHint(query.hint)
	}
	if query.comment != nil {
		find.SetComment(query.comment)
	}
	if query.let != nil {
		find.SetLet(query.let)
	}
	if len(query.sort) > 0 {
		find.SetSort(query.sort)
	}
	if len(query.projection) > 0 {
		find.SetProjection(query.projection)
	}
	defer r.invalidate(ctx)
	entity = new(T)
	res := r.collection.FindOneAndUpdate(ctx, filter, pipeline, find)
	if err = res.Err(); err != nil {
		return nil, err
	}
	if !res.Acknowledged {
		return nil, ErrUnacknowledgedWrite
	}
	if err = res.Decode(entity); err != nil {
		return nil, err
	}
	if err = r.reveal(ctx, entity); err != nil {
		return nil, err
	}
	return entity, nil
}

// 不能只推进序列表：其他实例仍可能持有已预分配的号码。禁止该组合才不会污染号段。
func (r *Repository[T, K]) validateUpsertPolicy(upsert bool) error {
	if upsert && r.config.ID.Generation == IDGenerationIncrement {
		return fmt.Errorf("自增主键仓储不支持原子 Upsert；请用 Create 分配主键，或改用手动主键仓储")
	}
	return nil
}

func (r *Repository[T, K]) upsertDefaults(ctx context.Context, o QueryOption, update bson.D) (bson.D, error) {
	// MongoDB 对没有 _id 的 upsert 自动生成 ObjectID；其他主键不能静默落入错误类型。
	id, equal := queryEqualID(o.conditions)
	if equal || id != nil || r.config.ID.Type != IDTypeObjectID || r.config.ID.Generation != IDGenerationAuto {
		typed, valid := id.(K)
		if !equal || !valid || r.validID(typed) != nil {
			return nil, fmt.Errorf("upsert 的 _id 等值条件必须与仓储主键类型一致")
		}
	}
	_, assigns, _, err := r.db.dalScope(ctx, r.config.Name)
	if err != nil {
		return nil, err
	}
	if r.config.Timestamps {
		assigns = append(assigns, bson.E{Key: "created_at", Value: time.Now().UTC()})
	}
	if r.config.CreatedByField != "" {
		v := ctx.Value(actorKey{})
		if v == nil {
			return nil, fmt.Errorf("缺少操作人信息")
		}
		assigns = append(assigns, bson.E{Key: r.config.CreatedByField, Value: v})
	}
	defaults := []UpdateOp{}
	for _, item := range assigns {
		defaults = append(defaults, UpdateOp{Operator: "$setOnInsert", Path: item.Key, Value: item.Value})
	}
	if r.db.autofill != nil {
		for _, item := range r.db.autofill.OnCreate(ctx) {
			if err := r.checkAutoFillAssignment(item.Path); err != nil {
				return nil, err
			}
			defaults = append(defaults, UpdateOp{Operator: "$setOnInsert", Path: item.Path, Value: item.Value})
		}
	}
	if r.db.sensitive != nil {
		defaults, err = r.db.sensitive.rewrite(defaults, reflect.TypeFor[T]())
		if err != nil {
			return nil, err
		}
	}
	for _, item := range defaults {
		overlap := false
		for _, group := range update {
			if group.Key == "$setOnInsert" {
				continue
			}
			for _, existing := range group.Value.(bson.D) {
				if item.Path == existing.Key || stringsOverlap(item.Path, existing.Key) {
					overlap = true
				}
			}
		}
		if !overlap {
			update = appendUpdate(update, "$setOnInsert", item.Path, item.Value)
		}
	}
	ops := []UpdateOp{}
	for _, group := range update {
		for _, item := range group.Value.(bson.D) {
			ops = append(ops, UpdateOp{Operator: group.Key, Path: item.Key, Value: item.Value})
		}
	}
	return buildUpdate(ops)
}

// queryEqualID 仅接受外层 AND 下唯一的 _id 等值，OR/表达式无法证明主键类型。
func queryEqualID(conditions []Condition) (any, bool) {
	var found any
	ok := false
	valid := true
	var walk func(bson.D)
	walk = func(doc bson.D) {
		for _, item := range doc {
			if item.Key == "$and" {
				if list, yes := item.Value.(bson.A); yes {
					for _, raw := range list {
						d, e := dalDocument(raw)
						if e != nil {
							valid = false
						} else {
							walk(d)
						}
					}
				}
				continue
			}
			if item.Key != "_id" {
				continue
			}
			v := item.Value
			if d, yes := v.(bson.D); yes {
				if len(d) != 1 || d[0].Key != "$eq" {
					valid = false
					continue
				}
				v = d[0].Value
			}
			if ok && !reflect.DeepEqual(found, v) {
				valid = false
			}
			found = v
			ok = true
		}
	}
	for _, c := range conditions {
		walk(c.Document)
	}
	return found, ok && valid
}

// Distinct 返回当前租户/权限范围内的去重 BSON 值，不经过实体敏感字段解密或结果缓存。
// 如需对敏感字段分组，请显式选择盲索引字段并遵守业务授权。
func (r *Repository[T, K]) Distinct(ctx context.Context, path string, query ...QueryOption) (values []any, err error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	end := r.observe(ctx, "Distinct")
	defer func() { end(err) }()
	if !field.ValidPath(path) {
		return nil, fmt.Errorf("无效字段 %s", path)
	}
	o := mergeOptions(query)
	if len(o.sort) > 0 || len(o.projection) > 0 || o.skip != 0 || o.limit != 0 || len(o.let) > 0 || o.cacheTTL != 0 || o.singleFlight || o.batchSize != 0 || o.allowDiskUse != nil {
		return nil, fmt.Errorf("Distinct 仅支持条件、Collation、Hint、Comment 和删除范围选项")
	}
	filter, err := r.filter(ctx, o, false)
	if err != nil {
		return nil, err
	}
	find := options.Distinct()
	if o.hint != nil {
		find.SetHint(o.hint)
	}
	if o.collation != nil {
		find.SetCollation(o.collation)
	}
	if o.comment != nil {
		find.SetComment(o.comment)
	}
	err = r.collection.Distinct(ctx, path, filter, find).Decode(&values)
	return values, err
}

// EstimatedDocumentCount 返回整个集合的元数据估计，不能表示租户/软删除/权限范围数量。
// 注册了任何隔离策略时拒绝调用；此时应使用 Count。
func (r *Repository[T, K]) EstimatedDocumentCount(ctx context.Context) (count int64, err error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	if r.config.SoftDelete || r.config.TenantField != "" || r.db.tenant != nil || r.db.permission != nil {
		return 0, fmt.Errorf("隔离集合不能使用 EstimatedDocumentCount，请使用 Count")
	}
	return r.collection.EstimatedDocumentCount(ctx)
}
