package mongoplus

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/kuangshp/mongo-plus/field"
	"github.com/kuangshp/mongo-plus/plugin"
	"go.mongodb.org/mongo-driver/v2/bson"
)

type dalUnscopedKey struct{}
type dalPhysicalDeleteKey struct{}

// DALPhysicalDelete 显式启用 DAL 的物理删除；不自动扩大租户、权限或已删除记录范围。
// 删除已逻辑删除文档时同时使用 DALUnscoped；该能力只由可信业务代码授权。
func DALPhysicalDelete(ctx context.Context) context.Context {
	return context.WithValue(resolveContext(ctx), dalPhysicalDeleteKey{}, true)
}

func dalPhysicalDelete(ctx context.Context) bool {
	physical, _ := ctx.Value(dalPhysicalDeleteKey{}).(bool)
	return physical
}

// DALUnscoped 让本次 DAL 查询包含已逻辑删除的文档，仍然保留租户和数据权限。
func DALUnscoped(ctx context.Context) context.Context {
	return context.WithValue(resolveContext(ctx), dalUnscopedKey{}, true)
}

func dalUnscoped(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	unscoped, _ := ctx.Value(dalUnscopedKey{}).(bool)
	return unscoped
}

func applyDALScope(ctx context.Context, dal *DAL, command dalCommand) (dalCommand, error) {
	db, err := dalDatabase(ctx, dal)
	if err != nil {
		return command, err
	}
	parts, assigns, protected, err := db.dalScope(ctx, command.collection)
	if err != nil {
		return command, err
	}
	filter := mergeDALFilter(nil, parts)
	switch command.op {
	case "find", "findOne", "count", "update", "updateOne", "updateMany", "delete", "deleteOne", "deleteMany":
		command.filter = mergeDALFilter(command.filter, parts)
	case "aggregate":
		if len(filter) > 0 {
			command.pipeline, err = scopeDALPipeline(command.pipeline, filter)
			if err != nil {
				return command, err
			}
		}
	case "watch":
		if len(filter) > 0 {
			prefixed, prefixErr := prefixScopeDocument(filter, "fullDocument.")
			if prefixErr != nil {
				return command, prefixErr
			}
			command.pipeline = append(bson.A{bson.D{{Key: "$match", Value: prefixed}}}, command.pipeline...)
		}
	case "insertOne":
		command.document, err = assignDALDocument(command.document, assigns)
		if err != nil {
			return command, err
		}
	case "bulkWrite":
		command.operations, err = scopeDALBulk(command.operations, filter, assigns, protected, db.collectionPolicyFor(command.collection), !dalPhysicalDelete(ctx))
		if err != nil {
			return command, err
		}
	}
	if err = guardDALUpdate(command.update, protected); err != nil {
		return command, err
	}
	policy := db.collectionPolicyFor(command.collection)
	if policy.softDelete && !dalPhysicalDelete(ctx) {
		switch command.op {
		case "deleteOne":
			command.op = "updateOne"
			command.update = softDeleteUpdate(policy.timestamps)
		case "delete", "deleteMany":
			command.op = "updateMany"
			command.update = softDeleteUpdate(policy.timestamps)
		}
	}
	return command, nil
}

func (d *Database) dalScope(ctx context.Context, collection string) ([]bson.D, bson.D, []string, error) {
	policy := d.collectionPolicyFor(collection)
	var parts []bson.D
	var assigns bson.D
	protected := []string{"_id"}
	if d.tenant != nil {
		documents, _, err := d.tenant.Conditions(ctx, collection, policy.tenantField)
		if errors.Is(err, plugin.ErrTenantRequired) {
			return nil, nil, nil, ErrTenantRequired
		}
		if err != nil {
			return nil, nil, nil, err
		}
		parts = append(parts, documents...)
		for _, name := range d.tenant.ProtectedFields(collection, policy.tenantField) {
			protected = append(protected, name)
		}
		values, _, err := d.tenant.CreateValues(ctx, collection, policy.tenantField)
		if err != nil {
			return nil, nil, nil, err
		}
		for name, value := range values {
			assigns = append(assigns, bson.E{Key: name, Value: value})
		}
	} else if policy.tenantField != "" && !plugin.TenantSkipped(ctx) {
		value := ctx.Value(tenantKey{})
		if value == nil || reflect.ValueOf(value).IsZero() {
			return nil, nil, nil, ErrTenantRequired
		}
		parts = append(parts, plugin.TenantCondition(policy.tenantField, value).Document)
		assigns = append(assigns, bson.E{Key: policy.tenantField, Value: value})
		protected = append(protected, policy.tenantField)
	}
	if policy.softDelete {
		protected = append(protected, "deleted_at")
		if !dalUnscoped(ctx) {
			parts = append(parts, field.New[any]("deleted_at").IsNull().Document)
			assigns = append(assigns, bson.E{Key: "deleted_at", Value: nil})
		}
	}
	if d.permission != nil {
		extra, err := d.permission.Apply(ctx, collection)
		if err != nil {
			return nil, nil, nil, err
		}
		if len(extra) > 0 {
			parts = append(parts, extra)
		}
	}
	return parts, assigns, protected, nil
}

func mergeDALFilter(base bson.D, extra []bson.D) bson.D {
	parts := make(bson.A, 0, len(extra)+1)
	if len(base) > 0 {
		parts = append(parts, base)
	}
	for _, item := range extra {
		if len(item) > 0 {
			parts = append(parts, item)
		}
	}
	switch len(parts) {
	case 0:
		return bson.D{}
	case 1:
		return parts[0].(bson.D)
	default:
		return bson.D{{Key: "$and", Value: parts}}
	}
}

// prefixScopeDocument 仅改写文档路径；逻辑操作符递归处理，不能把 $or 变成字段名。
// 表达式/脚本等不能安全自动重定位的权限条件明确报错，由原生扩展显式处理。
func prefixScopeDocument(doc bson.D, prefix string) (bson.D, error) {
	out := make(bson.D, 0, len(doc))
	for _, item := range doc {
		switch item.Key {
		case "$and", "$or", "$nor":
			value := reflect.ValueOf(item.Value)
			if !value.IsValid() || value.Kind() != reflect.Slice || value.Len() == 0 {
				return nil, fmt.Errorf("权限 %s 需要非空文档数组", item.Key)
			}
			list := bson.A{}
			for i := 0; i < value.Len(); i++ {
				part, err := dalDocument(value.Index(i).Interface())
				if err != nil {
					return nil, err
				}
				child, err := prefixScopeDocument(part, prefix)
				if err != nil {
					return nil, err
				}
				list = append(list, child)
			}
			out = append(out, bson.E{Key: item.Key, Value: list})
		default:
			if strings.HasPrefix(item.Key, "$") {
				return nil, fmt.Errorf("变更订阅不支持自动转换权限操作符 %s，请使用 DALNative", item.Key)
			}
			out = append(out, bson.E{Key: prefix + item.Key, Value: item.Value})
		}
	}
	return out, nil
}

// scopeDALPipeline 保留特殊首阶段；不能靠把 $match 插到所有阶段前面实现隔离。
func scopeDALPipeline(pipeline bson.A, filter bson.D) (bson.A, error) {
	if len(pipeline) > 0 {
		first, err := dalDocument(pipeline[0])
		if err != nil {
			return nil, err
		}
		if len(first) == 1 {
			switch first[0].Key {
			case "$geoNear":
				opts, err := dalDocument(first[0].Value)
				if err != nil {
					return nil, err
				}
				var query bson.D
				for _, e := range opts {
					if e.Key == "query" {
						query, err = dalDocument(e.Value)
						if err != nil {
							return nil, err
						}
					}
				}
				opts = replaceDALField(append(bson.D(nil), opts...), "query", mergeDALFilter(query, []bson.D{filter}))
				return append(bson.A{bson.D{{Key: "$geoNear", Value: opts}}}, pipeline[1:]...), nil
			case "$search", "$searchMeta", "$vectorSearch", "$documents", "$collStats", "$indexStats", "$changeStream":
				return nil, fmt.Errorf("%s 需要显式设计隔离条件，请通过 DALNative 执行", first[0].Key)
			}
		}
	}
	return append(bson.A{bson.D{{Key: "$match", Value: filter}}}, pipeline...), nil
}

func assignDALDocument(document, assigns bson.D) (bson.D, error) {
	document = append(bson.D{}, document...)
	for _, item := range assigns {
		var err error
		document, err = setDALPath(document, strings.Split(item.Key, "."), item.Value)
		if err != nil {
			return nil, err
		}
	}
	return document, nil
}

// setDALPath 按 BSON 路径创建嵌套文档，拒绝点号字面字段和重复字段造成的歧义。
func setDALPath(doc bson.D, parts []string, value any) (bson.D, error) {
	if len(parts) == 0 || !field.ValidPath(strings.Join(parts, ".")) {
		return nil, fmt.Errorf("无效自动字段路径")
	}
	doc = append(bson.D{}, doc...)
	found := -1
	for i, e := range doc {
		if strings.HasPrefix(e.Key, parts[0]+".") {
			return nil, fmt.Errorf("字段 %s 含有歧义点路径", e.Key)
		}
		if e.Key == parts[0] {
			if found >= 0 {
				return nil, fmt.Errorf("重复字段 %s", e.Key)
			}
			found = i
		}
	}
	if len(parts) > 1 {
		var child bson.D
		var err error
		if found >= 0 {
			child, err = dalDocument(doc[found].Value)
			if err != nil {
				return nil, fmt.Errorf("自动字段父路径 %s 必须是文档: %w", parts[0], err)
			}
		}
		value, err = setDALPath(child, parts[1:], value)
		if err != nil {
			return nil, err
		}
	}
	if found < 0 {
		return append(doc, bson.E{Key: parts[0], Value: value}), nil
	}
	doc[found].Value = value
	return doc, nil
}

func softDeleteUpdate(timestamps bool) bson.D {
	set := bson.D{{Key: "deleted_at", Value: time.Now().UTC()}}
	if timestamps {
		set = append(set, bson.E{Key: "updated_at", Value: time.Now().UTC()})
	}
	return bson.D{{Key: "$set", Value: set}}
}

// guardDALUpdate 同时检查写入路径和 $rename 目标，不把未知更新结构静默放行。
func guardDALUpdate(update bson.D, protected []string) error {
	for _, op := range update {
		fields, err := dalDocument(op.Value)
		if err != nil {
			return fmt.Errorf("更新 %s 必须是文档", op.Key)
		}
		for _, item := range fields {
			if err := guardDALPath(item.Key, protected); err != nil {
				return err
			}
			if op.Key == "$rename" {
				target, ok := item.Value.(string)
				if !ok {
					return fmt.Errorf("$rename 目标必须为字符串")
				}
				if err := guardDALPath(target, protected); err != nil {
					return err
				}
			}
		}
	}
	return nil
}

func guardDALPath(path string, protected []string) error {
	normalize := func(value string) string {
		parts := strings.Split(value, ".")
		out := []string{}
		for _, part := range parts {
			if strings.HasPrefix(part, "$") || sensitiveArrayIndex(part) {
				continue
			}
			out = append(out, part)
		}
		return strings.Join(out, ".")
	}
	for _, p := range protected {
		if path == p || stringsOverlap(path, p) || normalize(path) == normalize(p) || stringsOverlap(normalize(path), normalize(p)) {
			return fmt.Errorf("字段 %q 受保护或更新路径冲突", path)
		}
	}
	return nil
}

func guardDALPipeline(pipeline bson.A, protected []string) error {
	for _, raw := range pipeline {
		stage, err := dalDocument(raw)
		if err != nil || len(stage) != 1 {
			return fmt.Errorf("更新管道阶段必须是单操作符文档")
		}
		switch stage[0].Key {
		case "$set", "$addFields":
			if err := guardDALUpdate(stage, protected); err != nil {
				return err
			}
		case "$unset":
			paths := []string{}
			switch v := stage[0].Value.(type) {
			case string:
				paths = append(paths, v)
			case bson.A:
				for _, v := range v {
					p, ok := v.(string)
					if !ok {
						return fmt.Errorf("$unset 需要字符串路径")
					}
					paths = append(paths, p)
				}
			default:
				return fmt.Errorf("$unset 需要字符串或字符串数组")
			}
			for _, p := range paths {
				if err := guardDALPath(p, protected); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("受保护集合不支持更新管道阶段 %s，请使用 DALNative 显式维护集合规则", stage[0].Key)
		}
	}
	return nil
}

func scopeDALBulk(operations bson.A, filter, assigns bson.D, protected []string, policy collectionPolicy, softDelete bool) (bson.A, error) {
	scoped := make(bson.A, len(operations))
	for i, raw := range operations {
		document, err := dalDocument(raw)
		if err != nil {
			return nil, err
		}
		op := ""
		for _, item := range document {
			if item.Key == "op" {
				op, _ = item.Value.(string)
			}
		}
		switch op {
		case "insertOne":
			for i, item := range document {
				if item.Key == "document" {
					current, err := dalDocument(item.Value)
					if err != nil {
						return nil, err
					}
					document[i].Value, err = assignDALDocument(current, assigns)
					if err != nil {
						return nil, err
					}
				}
			}
		case "updateOne", "updateMany", "replaceOne", "deleteOne", "deleteMany":
			foundFilter := false
			for i, item := range document {
				switch item.Key {
				case "filter":
					foundFilter = true
					current, err := dalDocument(item.Value)
					if err != nil {
						return nil, err
					}
					document[i].Value = mergeDALFilter(current, []bson.D{filter})
				case "update":
					if pipeline, ok := item.Value.(bson.A); ok {
						if err = guardDALPipeline(pipeline, protected); err != nil {
							return nil, err
						}
					}
					if current, ok := item.Value.(bson.D); ok {
						if err = guardDALUpdate(current, protected); err != nil {
							return nil, err
						}
					}
				case "replacement":
					current, err := dalDocument(item.Value)
					if err != nil {
						return nil, err
					}
					document[i].Value, err = assignDALDocument(current, assigns)
					if err != nil {
						return nil, err
					}
				}
			}
			if !foundFilter {
				document = append(document, bson.E{Key: "filter", Value: filter})
			}
			if softDelete && policy.softDelete && (op == "deleteOne" || op == "deleteMany") {
				next := "updateMany"
				if op == "deleteOne" {
					next = "updateOne"
				}
				document = replaceDALField(document, "op", next)
				document = append(document, bson.E{Key: "update", Value: softDeleteUpdate(policy.timestamps)})
			}
		}
		scoped[i] = document
	}
	return scoped, nil
}

func replaceDALField(document bson.D, key string, value any) bson.D {
	for i, item := range document {
		if item.Key == key {
			document[i].Value = value
			return document
		}
	}
	return append(document, bson.E{Key: key, Value: value})
}
