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

// UpdateById 显式更新指定字段，零值同样生效；不会修改主键、租户或创建时间。
func (r *Repository[T, K]) UpdateById(ctx context.Context, id K, opts ...UpdateOption) (*mongo.UpdateResult, error) {
	if err := r.validID(id); err != nil {
		return nil, err
	}
	return r.update(ctx, []QueryOption{idOption(id)}, flattenUpdates(opts), false)
}

// UpdateByWrapper 按条件更新；全量更新必须显式使用 AllowAll。
func (r *Repository[T, K]) UpdateByWrapper(ctx context.Context, fn func(*Wrapper), opts ...UpdateOption) (*mongo.UpdateResult, error) {
	w := Query()
	if fn != nil {
		fn(w)
	}
	return r.update(ctx, []QueryOption{w.Build()}, flattenUpdates(opts), true)
}

// UpdateMapById 为可信业务代码提供 map 更新；更推荐 DAO 字段的 Set 方法。
func (r *Repository[T, K]) UpdateMapById(ctx context.Context, id K, values map[string]any) (*mongo.UpdateResult, error) {
	if err := r.validID(id); err != nil {
		return nil, err
	}
	ops := make([]UpdateOp, 0, len(values))
	for path, value := range values {
		ops = append(ops, UpdateOp{Operator: "$set", Path: path, Value: value})
	}
	return r.update(ctx, []QueryOption{idOption(id)}, ops, false)
}

func (r *Repository[T, K]) update(ctx context.Context, opts []QueryOption, ops []UpdateOp, many bool) (result *mongo.UpdateResult, err error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return nil, err
	}
	defer cleanup()
	end := r.observe(ctx, "Update")
	defer func() { end(err) }()
	o := mergeOptions(opts)
	if err = validateWriteQuery(o); err != nil {
		return nil, err
	}
	filter, err := r.filter(ctx, o, true)
	if err != nil {
		return nil, err
	}
	update, err := r.prepareUpdate(ctx, ops)
	if err != nil {
		return nil, err
	}
	defer r.invalidate(ctx)
	if many {
		result, err = r.collection.UpdateMany(ctx, filter, update, repositoryWriteOptions(options.UpdateMany(), o))
	} else {
		result, err = r.collection.UpdateOne(ctx, filter, update, repositoryWriteOptions(options.UpdateOne(), o))
	}
	if err == nil && (result == nil || !result.Acknowledged) {
		err = ErrUnacknowledgedWrite
	}
	return result, err
}

// 普通写入没有返回游标或单条领取语义；拒绝不适用的选项，避免无意扩大写入范围。
func validateWriteQuery(o QueryOption) error {
	if err := o.validate(); err != nil {
		return err
	}
	if len(o.sort) > 0 || len(o.projection) > 0 || o.skip != 0 || o.limit != 0 || o.cacheTTL != 0 || o.singleFlight || o.batchSize != 0 || o.allowDiskUse != nil {
		return fmt.Errorf("普通更新/删除不支持排序、投影、分页、缓存或游标选项；单条排序写入请用原子接口")
	}
	return nil
}

// 四种驱动写入 Builder 使用相同选项，软删除也复用此处，防止某条路径遗漏透传。
type repositoryWriteBuilder[B any] interface {
	SetCollation(*options.Collation) B
	SetHint(any) B
	SetLet(any) B
	SetComment(any) B
}

func repositoryWriteOptions[B repositoryWriteBuilder[B]](builder B, o QueryOption) B {
	if o.collation != nil {
		builder.SetCollation(o.collation)
	}
	if o.hint != nil {
		builder.SetHint(o.hint)
	}
	if o.let != nil {
		builder.SetLet(o.let)
	}
	if o.comment != nil {
		builder.SetComment(o.comment)
	}
	return builder
}

func appendUpdate(update bson.D, op, path string, value any) bson.D {
	for i, item := range update {
		if item.Key == op {
			fields := append(bson.D(nil), item.Value.(bson.D)...)
			for j, e := range fields {
				if e.Key == path {
					fields[j].Value = value
					update[i].Value = fields
					return update
				}
			}
			update[i].Value = append(fields, bson.E{Key: path, Value: value})
			return update
		}
	}
	return append(update, bson.E{Key: op, Value: bson.D{{Key: path, Value: value}}})
}

// auditUpdate 先改写所有自动填充字段，再统一检查最终路径，避免明文绕过敏感插件。
func (r *Repository[T, K]) auditUpdate(ctx context.Context, update bson.D) (bson.D, error) {
	ops := []UpdateOp{}
	if r.config.Timestamps {
		ops = append(ops, UpdateOp{Operator: "$set", Path: "updated_at", Value: time.Now().UTC()})
	}
	filled := map[string]bool{}
	if r.db != nil && r.db.autofill != nil {
		for _, item := range r.db.autofill.OnUpdate(ctx) {
			if err := r.checkAutoFillAssignment(item.Path); err != nil {
				return nil, err
			}
			filled[item.Path] = true
			ops = append(ops, UpdateOp{Operator: "$set", Path: item.Path, Value: item.Value})
		}
	}
	if r.config.UpdatedByField != "" && !filled[r.config.UpdatedByField] {
		value := ctx.Value(actorKey{})
		if value == nil {
			return nil, fmt.Errorf("缺少操作人信息")
		}
		ops = append(ops, UpdateOp{Operator: "$set", Path: r.config.UpdatedByField, Value: value})
	}
	if r.db != nil && r.db.sensitive != nil {
		var err error
		ops, err = r.db.sensitive.rewrite(ops, reflect.TypeFor[T]())
		if err != nil {
			return nil, err
		}
	}
	for _, op := range ops {
		update = appendUpdate(update, op.Operator, op.Path, op.Value)
	}
	final := []UpdateOp{}
	for _, op := range update {
		for _, item := range op.Value.(bson.D) {
			final = append(final, UpdateOp{Operator: op.Key, Path: item.Key, Value: item.Value})
		}
	}
	return buildUpdate(final)
}

func (r *Repository[T, K]) checkAutoFillAssignment(path string) error {
	if err := r.checkTenantAssignment(path); err != nil {
		return err
	}
	protected := []string{"_id", "deleted_at", "created_at", "updated_at"}
	return guardDALPath(path, protected)
}

func (r *Repository[T, K]) tenantProtectedFields() []string {
	fields := []string{}
	if r.config.TenantField != "" {
		fields = append(fields, r.config.TenantField)
	}
	if r.db != nil && r.db.tenant != nil {
		fields = append(fields, r.db.tenant.ProtectedFields(r.config.Name, r.config.TenantField)...)
	}
	return fields
}

func (r *Repository[T, K]) checkTenantAssignment(path string) error {
	for _, protected := range r.tenantProtectedFields() {
		if path == protected || stringsOverlap(path, protected) {
			return fmt.Errorf("自动填充不能修改受保护的租户字段 %q", path)
		}
	}
	return nil
}

// DeleteById 删除文档；启用逻辑删除时只设置 deleted_at。
func (r *Repository[T, K]) DeleteById(ctx context.Context, id K, opts ...DeleteOption) (int64, error) {
	if err := r.validID(id); err != nil {
		return 0, err
	}
	if physicalDelete(opts) {
		return r.HardDeleteById(ctx, id)
	}
	return r.delete(ctx, []QueryOption{idOption(id)}, false, false)
}

// DeleteByIdList 批量删除指定主键；空列表不执行写入，任一非法 ID 使整次调用失败。
func (r *Repository[T, K]) DeleteByIdList(ctx context.Context, ids []K, opts ...DeleteOption) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}
	for _, id := range ids {
		if err := r.validID(id); err != nil {
			return 0, err
		}
	}
	w := Query().Where(field.New[K]("_id").In(ids...))
	hard := physicalDelete(opts)
	if hard {
		w.WithDeleted()
	}
	return r.delete(ctx, []QueryOption{w.Build()}, true, hard)
}

// DeleteByCondition 以 AND 合并业务条件，默认遵循逻辑删除配置；空条件返回 ErrUnsafeWrite。
func (r *Repository[T, K]) DeleteByCondition(ctx context.Context, conditions ...Condition) (int64, error) {
	return r.delete(ctx, []QueryOption{Query().Where(conditions...).Build()}, true, false)
}

// DeleteByWrapper 按条件删除，默认执行逻辑删除。
func (r *Repository[T, K]) DeleteByWrapper(ctx context.Context, fn func(*Wrapper), opts ...DeleteOption) (int64, error) {
	w := Query()
	if fn != nil {
		fn(w)
	}
	hard := physicalDelete(opts)
	if hard {
		w.WithDeleted()
	}
	return r.delete(ctx, []QueryOption{w.Build()}, true, hard)
}

// HardDeleteById 明确执行物理删除，可删除已经逻辑删除的文档；租户规则仍生效。
func (r *Repository[T, K]) HardDeleteById(ctx context.Context, id K) (int64, error) {
	if err := r.validID(id); err != nil {
		return 0, err
	}
	return r.delete(ctx, []QueryOption{idOption(id), Query().WithDeleted().Build()}, false, true)
}

// HardDeleteByWrapper 明确按条件物理删除。
func (r *Repository[T, K]) HardDeleteByWrapper(ctx context.Context, fn func(*Wrapper)) (int64, error) {
	w := Query().WithDeleted()
	if fn != nil {
		fn(w)
	}
	return r.delete(ctx, []QueryOption{w.Build()}, true, true)
}

func (r *Repository[T, K]) delete(ctx context.Context, opts []QueryOption, many, hard bool) (count int64, err error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	end := r.observe(ctx, "Delete")
	defer func() { end(err) }()
	o := mergeOptions(opts)
	if err = validateWriteQuery(o); err != nil {
		return 0, err
	}
	filter, err := r.filter(ctx, o, true)
	if err != nil {
		return 0, err
	}
	defer r.invalidate(ctx)
	if r.config.SoftDelete && !hard {
		update, err := r.auditUpdate(ctx, bson.D{{Key: "$set", Value: bson.D{{Key: "deleted_at", Value: time.Now().UTC()}}}})
		if err != nil {
			return 0, err
		}
		var result *mongo.UpdateResult
		if many {
			result, err = r.collection.UpdateMany(ctx, filter, update, repositoryWriteOptions(options.UpdateMany(), o))
		} else {
			result, err = r.collection.UpdateOne(ctx, filter, update, repositoryWriteOptions(options.UpdateOne(), o))
		}
		if err != nil {
			return 0, err
		}
		if result == nil || !result.Acknowledged {
			return 0, ErrUnacknowledgedWrite
		}
		return result.ModifiedCount, nil
	}
	var result *mongo.DeleteResult
	if many {
		result, err = r.collection.DeleteMany(ctx, filter, repositoryWriteOptions(options.DeleteMany(), o))
	} else {
		result, err = r.collection.DeleteOne(ctx, filter, repositoryWriteOptions(options.DeleteOne(), o))
	}
	if err != nil {
		return 0, err
	}
	if result == nil || !result.Acknowledged {
		return 0, ErrUnacknowledgedWrite
	}
	return result.DeletedCount, nil
}

// RestoreById 恢复逻辑删除记录，普通更新不能修改 deleted_at。
func (r *Repository[T, K]) RestoreById(ctx context.Context, id K) (int64, error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return 0, err
	}
	defer cleanup()
	if !r.config.SoftDelete {
		return 0, fmt.Errorf("集合未启用逻辑删除")
	}
	if err := r.validID(id); err != nil {
		return 0, err
	}
	filter, err := r.filter(ctx, mergeOptions([]QueryOption{idOption(id), Query().OnlyDeleted().Build()}), true)
	if err != nil {
		return 0, err
	}
	update, err := r.auditUpdate(ctx, bson.D{{Key: "$set", Value: bson.D{{Key: "deleted_at", Value: nil}}}})
	if err != nil {
		return 0, err
	}
	defer r.invalidate(ctx)
	result, err := r.collection.UpdateOne(ctx, filter, update)
	if err != nil {
		return 0, err
	}
	if result == nil || !result.Acknowledged {
		return 0, ErrUnacknowledgedWrite
	}
	return result.ModifiedCount, nil
}

// FindListByWrapper 通过条件构造器查询列表。
func (r *Repository[T, K]) FindListByWrapper(ctx context.Context, fn func(*Wrapper), opts ...QueryOption) ([]*T, error) {
	w := Query()
	if fn != nil {
		fn(w)
	}
	return r.FindList(ctx, append([]QueryOption{w.Build()}, opts...)...)
}

// FindOneWrapper 通过条件构造器查询单条。
func (r *Repository[T, K]) FindOneWrapper(ctx context.Context, fn func(*Wrapper), opts ...QueryOption) (*T, error) {
	w := Query()
	if fn != nil {
		fn(w)
	}
	return r.FindOne(ctx, append([]QueryOption{w.Build()}, opts...)...)
}

// FindPageByWrapper 通过条件构造器查询分页。
func (r *Repository[T, K]) FindPageByWrapper(ctx context.Context, page, size int64, fn func(*Wrapper), opts ...QueryOption) ([]*T, int64, error) {
	w := Query()
	if fn != nil {
		fn(w)
	}
	return r.FindPage(ctx, page, size, append([]QueryOption{w.Build()}, opts...)...)
}

// CountByWrapper 按条件统计。
func (r *Repository[T, K]) CountByWrapper(ctx context.Context, fn func(*Wrapper), opts ...QueryOption) (int64, error) {
	w := Query()
	if fn != nil {
		fn(w)
	}
	return r.Count(ctx, append([]QueryOption{w.Build()}, opts...)...)
}

// ExistsByWrapper 按条件检查存在性。
func (r *Repository[T, K]) ExistsByWrapper(ctx context.Context, fn func(*Wrapper), opts ...QueryOption) (bool, error) {
	w := Query()
	if fn != nil {
		fn(w)
	}
	return r.Exists(ctx, append([]QueryOption{w.Build()}, opts...)...)
}
