package schema

import (
	"bytes"
	"context"
	"fmt"
	"github.com/kuangshp/mongo-plus/migration"
	"reflect"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// Change 描述计划中的非破坏性结构操作，不包含删除字段或索引。
type Change struct {
	Collection      string   `json:"collection"`
	Create          bool     `json:"createCollection"`
	UpdateValidator bool     `json:"updateValidator"`
	CreateIndexes   []string `json:"createIndexes"`
}

type existingIndex struct {
	Name    string `bson:"name"`
	Key     bson.D `bson:"key"`
	Unique  bool   `bson:"unique"`
	Sparse  bool   `bson:"sparse"`
	Partial bson.D `bson:"partialFilterExpression"`
	TTL     *int32 `bson:"expireAfterSeconds"`
}

// Plan 比较集合、校验器和索引；检测冲突时在任何写操作前返回错误。
func Plan(ctx context.Context, db *mongo.Database, entities []Entity) ([]Change, error) {
	changes := []Change{}
	for _, e := range entities {
		cursor, err := db.ListCollections(ctx, bson.D{{Key: "name", Value: e.Collection}})
		if err != nil {
			return nil, err
		}
		var infos []struct {
			Name    string `bson:"name"`
			Options struct {
				Validator        bson.D `bson:"validator"`
				ValidationLevel  string `bson:"validationLevel"`
				ValidationAction string `bson:"validationAction"`
			} `bson:"options"`
		}
		err = cursor.All(ctx, &infos)
		cursor.Close(ctx)
		if err != nil {
			return nil, err
		}
		change := Change{Collection: e.Collection, CreateIndexes: []string{}}
		var indexes []existingIndex
		if len(infos) == 0 {
			change.Create = true
		} else {
			current := infos[0].Options
			// 未返回的选项采用服务端默认值 strict/error。
			change.UpdateValidator = !equalDocument(current.Validator, e.Validator()) ||
				(current.ValidationLevel != "" && current.ValidationLevel != "strict") ||
				(current.ValidationAction != "" && current.ValidationAction != "error")
			cursor, err = db.Collection(e.Collection).Indexes().List(ctx)
			if err != nil {
				return nil, err
			}
			err = cursor.All(ctx, &indexes)
			cursor.Close(ctx)
			if err != nil {
				return nil, err
			}
		}
		for _, index := range e.Indexes {
			wanted := expectedIndex(index)
			found := false
			for _, old := range indexes {
				if old.Name == wanted.Name || equalKeys(old.Key, wanted.Key) {
					if !equalIndex(old, wanted) {
						return nil, fmt.Errorf("集合 %s 索引 %s 与现有索引 %s 冲突，请显式迁移", e.Collection, wanted.Name, old.Name)
					}
					found = true
					break
				}
			}
			if !found {
				change.CreateIndexes = append(change.CreateIndexes, index.Name)
			}
		}
		if change.Create || change.UpdateValidator || len(change.CreateIndexes) > 0 {
			changes = append(changes, change)
		}
	}
	return changes, nil
}

// Apply 同步创建集合、校验规则和缺失索引，不删除已有数据/索引。
// 这些管理命令不是一个原子事务；失败后可修复原因并重新执行 Plan/Apply。
func Apply(ctx context.Context, db *mongo.Database, entities []Entity) ([]Change, error) {
	var changes []Change
	err := migration.WithLock(ctx, db, func(lockedCtx context.Context) error {
		var applyErr error
		changes, applyErr = apply(lockedCtx, db, entities)
		return applyErr
	})
	return changes, err
}

func apply(ctx context.Context, db *mongo.Database, entities []Entity) ([]Change, error) {
	changes, err := Plan(ctx, db, entities)
	if err != nil {
		return nil, err
	}
	for _, change := range changes {
		var entity Entity
		for _, e := range entities {
			if e.Collection == change.Collection {
				entity = e
				break
			}
		}
		if change.Create {
			err = db.CreateCollection(ctx, entity.Collection, options.CreateCollection().SetValidator(entity.Validator()).SetValidationLevel("strict").SetValidationAction("error"))
			if err != nil {
				return changes, fmt.Errorf("创建集合 %s: %w", entity.Collection, err)
			}
		} else if change.UpdateValidator {
			err = db.RunCommand(ctx, bson.D{{Key: "collMod", Value: entity.Collection}, {Key: "validator", Value: entity.Validator()}, {Key: "validationLevel", Value: "strict"}, {Key: "validationAction", Value: "error"}}).Err()
			if err != nil {
				return changes, fmt.Errorf("同步集合 %s 校验规则: %w", entity.Collection, err)
			}
		}
		for _, name := range change.CreateIndexes {
			for _, index := range entity.Indexes {
				if index.Name != name {
					continue
				}
				wanted := expectedIndex(index)
				opts := options.Index().SetName(name).SetUnique(index.Unique).SetSparse(index.Sparse)
				if index.ActiveOnly {
					opts.SetPartialFilterExpression(wanted.Partial)
				}
				if index.ExpireAfterSeconds != nil {
					opts.SetExpireAfterSeconds(*index.ExpireAfterSeconds)
				}
				_, err = db.Collection(entity.Collection).Indexes().CreateOne(ctx, mongo.IndexModel{Keys: wanted.Key, Options: opts})
				if err != nil {
					return changes, fmt.Errorf("创建 %s.%s 索引: %w", entity.Collection, name, err)
				}
			}
		}
	}
	return changes, nil
}

func expectedIndex(index Index) existingIndex {
	wanted := existingIndex{Name: index.Name, Unique: index.Unique, Sparse: index.Sparse, TTL: index.ExpireAfterSeconds}
	for _, f := range index.Fields {
		wanted.Key = append(wanted.Key, bson.E{Key: f.Name, Value: f.Order})
	}
	if index.ActiveOnly {
		wanted.Partial = bson.D{{Key: "deleted_at", Value: bson.D{{Key: "$eq", Value: nil}}}}
	}
	return wanted
}

func equalIndex(a, b existingIndex) bool {
	return equalKeys(a.Key, b.Key) && a.Unique == b.Unique && a.Sparse == b.Sparse && reflect.DeepEqual(a.TTL, b.TTL) && equalDocument(a.Partial, b.Partial)
}

func equalKeys(a, b bson.D) bool {
	if len(a) != len(b) {
		return false
	}
	for i, e := range a {
		if e.Key != b[i].Key {
			return false
		}
		if fmt.Sprint(e.Value) != fmt.Sprint(b[i].Value) {
			return false
		}
	}
	return true
}

func equalDocument(a, b bson.D) bool {
	if len(a) == 0 && len(b) == 0 {
		return true
	}
	// 文档中的 required/property 顺序不影响校验意义；本库输出顺序稳定。
	aa, e1 := bson.MarshalExtJSON(a, false, false)
	bb, e2 := bson.MarshalExtJSON(b, false, false)
	return e1 == nil && e2 == nil && bytes.Equal(aa, bb)
}
