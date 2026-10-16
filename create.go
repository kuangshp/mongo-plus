package mongoplus

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/kuangshp/mongo-plus/plugin"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// preparedCreate 保留原始 Go 类型，由 Collection 使用自身的 Registry/BSONOptions 编码。
type preparedCreate[T any] struct {
	entity   *T
	document any
}

// managedField 设置自动字段。只复制被修改的 inline 指针链，不修改调用者的实体。
func managedField(v reflect.Value, path string, value any) (bool, error) {
	return managedFieldDepth(v, path, value, 0)
}

func managedFieldDepth(v reflect.Value, path string, value any, depth int) (bool, error) {
	if depth > 64 {
		return true, fmt.Errorf("自动字段 %s 的 inline 嵌套过深或包含循环", path)
	}
	typ := v.Type()
	for i := 0; i < typ.NumField(); i++ {
		f := typ.Field(i)
		if !f.IsExported() {
			continue
		}
		parts := strings.Split(f.Tag.Get("bson"), ",")
		if parts[0] == "-" {
			continue
		}
		inline := false
		for _, part := range parts[1:] {
			inline = inline || part == "inline"
		}
		if inline {
			field := v.Field(i)
			if field.Kind() == reflect.Pointer && field.Type().Elem().Kind() == reflect.Struct {
				copy := reflect.New(field.Type().Elem())
				if !field.IsNil() {
					copy.Elem().Set(field.Elem())
				}
				found, err := managedFieldDepth(copy.Elem(), path, value, depth+1)
				if found {
					field.Set(copy)
					return true, err
				}
			} else if field.Kind() == reflect.Struct {
				if found, err := managedFieldDepth(field, path, value, depth+1); found {
					return true, err
				}
			}
			continue
		}
		name := parts[0]
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		if name != path {
			continue
		}
		field := v.Field(i)
		if value == nil {
			if field.Kind() != reflect.Pointer && field.Kind() != reflect.Interface {
				return true, fmt.Errorf("自动字段 %s 必须允许 null", path)
			}
			field.SetZero()
			return true, nil
		}
		source := reflect.ValueOf(value)
		if source.Type().AssignableTo(field.Type()) {
			field.Set(source)
			return true, nil
		}
		if field.Kind() == reflect.Pointer && source.Type().AssignableTo(field.Type().Elem()) {
			pointer := reflect.New(field.Type().Elem())
			pointer.Elem().Set(source)
			field.Set(pointer)
			return true, nil
		}
		return true, fmt.Errorf("自动字段 %s 类型不匹配: %s -> %s", path, source.Type(), field.Type())
	}
	return false, nil
}

func setBSONField(fields bson.D, key string, value any) bson.D {
	for i, item := range fields {
		if item.Key == key {
			fields[i].Value = value
			return fields
		}
	}
	return append(fields, bson.E{Key: key, Value: value})
}

func (r *Repository[T, K]) prepareCreate(ctx context.Context, entity *T, id any) (preparedCreate[T], error) {
	if entity == nil {
		return preparedCreate[T]{}, fmt.Errorf("实体不能为空")
	}
	copy := new(T)
	*copy = *entity
	value := reflect.ValueOf(copy).Elem()
	fields := bson.D{{Key: "_id", Value: id}}
	if r.config.SoftDelete {
		fields = append(fields, bson.E{Key: "deleted_at", Value: nil})
	}
	if r.config.Timestamps {
		now := time.Now().UTC().Truncate(time.Millisecond) // 与 BSON datetime 存储精度一致。
		fields = append(fields, bson.E{Key: "created_at", Value: now}, bson.E{Key: "updated_at", Value: now})
	}
	if !plugin.TenantSkipped(ctx) {
		if r.db != nil && r.db.tenant != nil {
			values, _, err := r.db.tenant.CreateValues(ctx, r.config.Name, r.config.TenantField)
			if errors.Is(err, plugin.ErrTenantRequired) {
				return preparedCreate[T]{}, ErrTenantRequired
			}
			if err != nil {
				return preparedCreate[T]{}, err
			}
			for path, tenantValue := range values {
				fields = setBSONField(fields, path, tenantValue)
			}
		} else if r.config.TenantField != "" {
			fields = append(fields, bson.E{Key: r.config.TenantField, Value: ctx.Value(tenantKey{})})
		}
	}
	for _, path := range []string{r.config.CreatedByField, r.config.UpdatedByField} {
		if path != "" {
			actor := ctx.Value(actorKey{})
			if actor == nil {
				return preparedCreate[T]{}, fmt.Errorf("缺少操作人信息")
			}
			fields = setBSONField(fields, path, actor)
		}
	}
	if r.db != nil && r.db.autofill != nil {
		for _, item := range r.db.autofill.OnCreate(ctx) {
			if err := r.checkTenantAssignment(item.Path); err != nil {
				return preparedCreate[T]{}, err
			}
			fields = setBSONField(fields, item.Path, item.Value)
		}
	}
	extra := bson.M{}
	for _, f := range fields {
		found, err := managedField(value, f.Key, f.Value)
		if err != nil {
			return preparedCreate[T]{}, err
		}
		if !found {
			extra[f.Key] = f.Value
		}
	}
	if r.db != nil && r.db.sensitive != nil {
		if err := r.db.sensitive.seal(copy); err != nil {
			return preparedCreate[T]{}, err
		}
	}
	var document any = copy
	if len(extra) > 0 {
		// 兼容只在数据库维护的自动字段；正常生成的实体直接交给驱动编码。
		document = struct {
			Entity *T     `bson:",inline"`
			Extra  bson.M `bson:",inline"`
		}{copy, extra}
	}
	return preparedCreate[T]{entity: copy, document: document}, nil
}

func (r *Repository[T, K]) createID(entity *T) (any, error) {
	if entity == nil {
		return nil, fmt.Errorf("实体不能为空")
	}
	id := reflect.ValueOf(entity).Elem().FieldByName("ID").Interface().(K)
	if r.config.ID.Generation == IDGenerationIncrement {
		return int64(0), nil // 在检查自动字段之后统一分配。
	}
	if reflect.ValueOf(id).IsZero() && r.config.ID.Generation == IDGenerationAuto {
		return bson.NewObjectID(), nil
	}
	if err := r.validID(id); err != nil {
		return nil, err
	}
	return id, nil
}

// Create 新增实体；插入获得确认后回填主键和自动字段。
func (r *Repository[T, K]) Create(ctx context.Context, entity *T) (err error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	end := r.observe(ctx, "Create")
	defer func() { end(err) }()
	if _, err = r.filter(ctx, QueryOption{}, false); err != nil {
		return err
	}
	id, err := r.createID(entity)
	if err != nil {
		return err
	}
	prepared, err := r.prepareCreate(ctx, entity, id)
	if err != nil {
		return err
	}
	if r.config.ID.Generation == IDGenerationIncrement {
		first, err := r.sequenceRange(ctx, 1)
		if err != nil {
			return err
		}
		reflect.ValueOf(prepared.entity).Elem().FieldByName("ID").SetInt(first)
	}
	defer r.invalidate(ctx)
	result, err := r.collection.InsertOne(ctx, prepared.document)
	if err != nil {
		return err
	}
	if !result.Acknowledged {
		return ErrUnacknowledgedWrite
	}
	*entity = *prepared.entity
	return nil
}

const createBatchSize = 1000

// CreateBatch 每块最多 1000 条，有序批量插入；每块只分配一次自增号码区间。
// 非事务下可能部分成功；只回填已获确认的成功前缀。需整批回滚时使用 Transaction。
func (r *Repository[T, K]) CreateBatch(ctx context.Context, entities []*T) (err error) {
	ctx, cleanup, err := r.operationContext(ctx)
	if err != nil {
		return err
	}
	defer cleanup()
	end := r.observe(ctx, "CreateBatch")
	defer func() { end(err) }()
	if len(entities) == 0 {
		return nil
	}
	if _, err = r.filter(ctx, QueryOption{}, false); err != nil {
		return err
	}
	for offset := 0; offset < len(entities); offset += createBatchSize {
		if err := ctx.Err(); err != nil {
			return err
		}
		chunk := entities[offset:min(offset+createBatchSize, len(entities))]
		prepared := make([]preparedCreate[T], len(chunk))
		documents := make([]any, len(chunk))
		for i, entity := range chunk {
			id, err := r.createID(entity)
			if err != nil {
				return fmt.Errorf("第 %d 条文档: %w", offset+i+1, err)
			}
			prepared[i], err = r.prepareCreate(ctx, entity, id)
			if err != nil {
				return fmt.Errorf("第 %d 条文档: %w", offset+i+1, err)
			}
			documents[i] = prepared[i].document
		}
		if r.config.ID.Generation == IDGenerationIncrement {
			first, err := r.sequenceRange(ctx, int64(len(chunk)))
			if err != nil {
				return err
			}
			for i := range prepared {
				reflect.ValueOf(prepared[i].entity).Elem().FieldByName("ID").SetInt(first + int64(i))
			}
		}
		result, insertErr := r.collection.InsertMany(ctx, documents, options.InsertMany().SetOrdered(true))
		r.invalidate(ctx) // 包括部分成功和结果不确定的情况。
		confirmed := 0
		if result != nil && result.Acknowledged {
			if insertErr == nil {
				confirmed = len(chunk)
			} else {
				var bulk mongo.BulkWriteException
				if errors.As(insertErr, &bulk) && bulk.WriteConcernError == nil && len(bulk.WriteErrors) > 0 {
					confirmed = min(len(result.InsertedIDs), len(chunk))
					for _, e := range bulk.WriteErrors {
						confirmed = min(confirmed, e.Index)
					}
				}
			}
		}
		for i := 0; i < confirmed; i++ {
			*chunk[i] = *prepared[i].entity
		}
		if insertErr != nil {
			return fmt.Errorf("批量插入（本块从第 %d 条开始）: %w", offset+1, insertErr)
		}
		if result == nil || !result.Acknowledged {
			return ErrUnacknowledgedWrite
		}
	}
	return nil
}
