package mongoplus

import (
	"context"
	"fmt"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
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
	ctx = resolveContext(ctx)
	end := r.observe(ctx, "Update")
	defer func() { end(err) }()
	filter, err := r.filter(ctx, mergeOptions(opts), true)
	if err != nil {
		return nil, err
	}
	protected := []string{"_id"}
	if r.config.SoftDelete {
		protected = append(protected, "deleted_at")
	}
	if r.config.Timestamps {
		protected = append(protected, "created_at", "updated_at")
	}
	for _, p := range []string{r.config.TenantField, r.config.CreatedByField, r.config.UpdatedByField} {
		if p != "" {
			protected = append(protected, p)
		}
	}
	if r.db != nil && r.db.sensitive != nil {
		ops, err = r.db.sensitive.rewrite(ops)
		if err != nil {
			return nil, err
		}
	}
	update, err := buildUpdate(ops, protected...)
	if err != nil {
		return nil, err
	}
	update, err = r.auditUpdate(ctx, update)
	if err != nil {
		return nil, err
	}
	defer r.invalidate(ctx)
	if many {
		result, err = r.collection.UpdateMany(ctx, filter, update)
	} else {
		result, err = r.collection.UpdateOne(ctx, filter, update)
	}
	if err == nil && (result == nil || !result.Acknowledged) {
		err = ErrUnacknowledgedWrite
	}
	return result, err
}

func appendUpdate(update bson.D, op, path string, value any) bson.D {
	for i, item := range update {
		if item.Key == op {
			update[i].Value = append(item.Value.(bson.D), bson.E{Key: path, Value: value})
			return update
		}
	}
	return append(update, bson.E{Key: op, Value: bson.D{{Key: path, Value: value}}})
}

func (r *Repository[T, K]) auditUpdate(ctx context.Context, update bson.D) (bson.D, error) {
	if r.config.Timestamps {
		update = appendUpdate(update, "$set", "updated_at", time.Now().UTC())
	}
	if r.config.UpdatedByField != "" {
		value := ctx.Value(actorKey{})
		if value == nil {
			return nil, fmt.Errorf("缺少操作人信息")
		}
		update = appendUpdate(update, "$set", r.config.UpdatedByField, value)
	}
	return update, nil
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
	ctx = resolveContext(ctx)
	end := r.observe(ctx, "Delete")
	defer func() { end(err) }()
	filter, err := r.filter(ctx, mergeOptions(opts), true)
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
			result, err = r.collection.UpdateMany(ctx, filter, update)
		} else {
			result, err = r.collection.UpdateOne(ctx, filter, update)
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
		result, err = r.collection.DeleteMany(ctx, filter)
	} else {
		result, err = r.collection.DeleteOne(ctx, filter)
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
	ctx = resolveContext(ctx)
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
