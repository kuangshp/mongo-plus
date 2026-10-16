package mongoplus

import (
	"context"
	"fmt"
	"reflect"
	"strings"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

// Source 表示经过仓储配置的集合；生成的 Repository 接口自动具备此能力。
// 通过源仓储读取关联集合规则，避免仅传集合名而遗漏租户或逻辑删除过滤。
type Source interface {
	collectionSource() collectionSource
}

type collectionSource struct {
	db         *Database
	collection *mongo.Collection
	model      reflect.Type
	config     CollectionConfig
	tx         *transactionBinding
	filter     func(context.Context, QueryOption, bool) (bson.D, error)
	err        error
}

func (r *Repository[T, K]) collectionSource() collectionSource {
	if r == nil || r.db == nil || r.collection == nil {
		return collectionSource{err: fmt.Errorf("关联查询的 Repository 不能为空")}
	}
	return collectionSource{db: r.db, collection: r.collection, model: reflect.TypeOf((*T)(nil)).Elem(), config: r.config, tx: r.tx, filter: r.filter}
}

func getSource(source Source) collectionSource {
	if source == nil || (reflect.ValueOf(source).Kind() == reflect.Pointer && reflect.ValueOf(source).IsNil()) {
		return collectionSource{err: fmt.Errorf("关联查询的 Repository 不能为空")}
	}
	return source.collectionSource()
}

// bsonPathType 校验声明过的实体路径；数组文档路径按 MongoDB 点路径处理。
func bsonPathType(typ reflect.Type, path string) (reflect.Type, bool) {
	for _, part := range strings.Split(path, ".") {
		for typ.Kind() == reflect.Pointer || typ.Kind() == reflect.Slice {
			typ = typ.Elem()
		}
		if typ.Kind() != reflect.Struct {
			return nil, false
		}
		var next reflect.Type
		for i := 0; i < typ.NumField(); i++ {
			f := typ.Field(i)
			if !f.IsExported() {
				continue
			}
			name := strings.Split(f.Tag.Get("bson"), ",")[0]
			if name == "" {
				name = strings.ToLower(f.Name)
			}
			if name == part {
				next = f.Type
				break
			}
		}
		if next == nil {
			return nil, false
		}
		typ = next
	}
	return typ, true
}
