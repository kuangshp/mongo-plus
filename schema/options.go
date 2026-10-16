package schema

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math/big"
	"sort"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
)

// CollectionOptions 是普通集合的声明选项。创建后不可自动更改的选项发生漂移时，Plan 要求显式迁移。
// TimeSeries、Clustered、View 等特殊集合使用 migration.Run 的原生回调建立，避免隐式改变存储结构。
type CollectionOptions struct {
	Collation                    *options.Collation `json:"collation,omitempty"`
	Capped                       bool               `json:"capped,omitempty"`
	SizeInBytes                  int64              `json:"sizeInBytes,omitempty"`
	MaxDocuments                 int64              `json:"maxDocuments,omitempty"`
	ChangeStreamPreAndPostImages *bool              `json:"changeStreamPreAndPostImages,omitempty"`
	ValidationLevel              string             `json:"validationLevel,omitempty"`
	ValidationAction             string             `json:"validationAction,omitempty"`
}

func (e *Entity) normalizeCollectionOptions() error {
	if e.Options == nil {
		return nil
	}
	o := e.Options
	if o.SizeInBytes < 0 || o.MaxDocuments < 0 || (o.Capped && o.SizeInBytes <= 0) || (!o.Capped && (o.SizeInBytes != 0 || o.MaxDocuments != 0)) {
		return fmt.Errorf("capped 集合需要正 sizeInBytes，普通集合不能设置容量参数")
	}
	if o.SizeInBytes > 1<<50 {
		return fmt.Errorf("capped sizeInBytes 不能超过 1 PB")
	}
	if o.Collation != nil && o.Collation.Locale == "" {
		return fmt.Errorf("集合 collation.locale 不能为空")
	}
	switch o.ValidationLevel {
	case "", "strict", "moderate", "off":
	default:
		return fmt.Errorf("无效 validationLevel %q", o.ValidationLevel)
	}
	switch o.ValidationAction {
	case "", "error", "warn":
	default:
		return fmt.Errorf("无效 validationAction %q", o.ValidationAction)
	}
	return nil
}
func (e Entity) validationMode() (string, string) {
	level, action := "strict", "error"
	if e.Options != nil {
		if e.Options.ValidationLevel != "" {
			level = e.Options.ValidationLevel
		}
		if e.Options.ValidationAction != "" {
			action = e.Options.ValidationAction
		}
	}
	return level, action
}
func (e Entity) createOptions() *options.CreateCollectionOptionsBuilder {
	level, action := e.validationMode()
	opts := options.CreateCollection().SetValidator(e.Validator()).SetValidationLevel(level).SetValidationAction(action)
	if e.Options != nil {
		o := e.Options
		if o.Collation != nil {
			opts.SetCollation(o.Collation)
		}
		if o.Capped {
			opts.SetCapped(true).SetSizeInBytes(cappedStorageSize(o.SizeInBytes))
			if o.MaxDocuments > 0 {
				opts.SetMaxDocuments(o.MaxDocuments)
			}
		}
		if o.ChangeStreamPreAndPostImages != nil {
			opts.SetChangeStreamPreAndPostImages(bson.D{{Key: "enabled", Value: *o.ChangeStreamPreAndPostImages}})
		}
	}
	return opts
}

// MongoDB 将 capped 容量向上取整到 256 字节；创建和漂移比较必须使用同一规范值。
func cappedStorageSize(size int64) int64 {
	if size <= 0 || size > 1<<50 {
		return size // 非法范围由 Normalize 拒绝，此处不做可能溢出的运算。
	}
	return ((size-1)/256 + 1) * 256
}

func normalizeIndexOrder(value any) (any, error) {
	switch v := value.(type) {
	case int:
		if v == 1 || v == -1 {
			return v, nil
		}
	case int32:
		if v == 1 || v == -1 {
			return int(v), nil
		}
	case int64:
		if v == 1 || v == -1 {
			return int(v), nil
		}
	case float64:
		if v == 1 || v == -1 {
			return int(v), nil
		}
	case string:
		switch v {
		case "text", "hashed", "2d", "2dsphere":
			return v, nil
		}
	}
	return nil, fmt.Errorf("索引 order 必须为 1/-1 或 text/hashed/2d/2dsphere")
}

func indexKeys(index Index) bson.D {
	keys := bson.D{}
	for _, f := range index.Fields {
		keys = append(keys, bson.E{Key: f.Name, Value: f.Order})
	}
	return keys
}
func (e Entity) effectiveIndex(index Index) Index {
	if index.Collation == nil && e.Options != nil {
		index.Collation = e.Options.Collation
	}
	return index
}

// collationSpec 补齐服务器默认值，忽略 ICU 的只读 version 字段，避免每次 Plan 都产生伪冲突。
func collationSpec(doc bson.D) bson.D {
	values := map[string]any{}
	for _, e := range doc {
		values[e.Key] = e.Value
	}
	locale, _ := values["locale"].(string)
	if locale == "" || locale == "simple" {
		return nil
	}
	defaults := bson.D{{Key: "locale", Value: locale}, {Key: "caseLevel", Value: false}, {Key: "caseFirst", Value: "off"}, {Key: "strength", Value: 3}, {Key: "numericOrdering", Value: false}, {Key: "alternate", Value: "non-ignorable"}, {Key: "maxVariable", Value: "punct"}, {Key: "normalization", Value: false}, {Key: "backwards", Value: false}}
	for i, e := range defaults {
		if v, ok := values[e.Key]; ok {
			defaults[i].Value = v
		}
	}
	return defaults
}
func collationDocument(c *options.Collation) bson.D {
	if c == nil || c.Locale == "simple" {
		return nil
	}
	strength := c.Strength
	if strength == 0 {
		strength = 3
	}
	first := c.CaseFirst
	if first == "" {
		first = "off"
	}
	alternate := c.Alternate
	if alternate == "" {
		alternate = "non-ignorable"
	}
	variable := c.MaxVariable
	if variable == "" {
		variable = "punct"
	}
	return collationSpec(bson.D{{Key: "locale", Value: c.Locale}, {Key: "caseLevel", Value: c.CaseLevel}, {Key: "caseFirst", Value: first}, {Key: "strength", Value: strength}, {Key: "numericOrdering", Value: c.NumericOrdering}, {Key: "alternate", Value: alternate}, {Key: "maxVariable", Value: variable}, {Key: "normalization", Value: c.Normalization}, {Key: "backwards", Value: c.Backwards}})
}
func canonicalDocument(doc any) []byte {
	raw, err := bson.MarshalExtJSON(doc, false, false)
	if err != nil {
		return nil
	}
	var value any
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if decoder.Decode(&value) != nil {
		return nil
	}
	var normalize func(any) any
	normalize = func(v any) any {
		switch x := v.(type) {
		case json.Number:
			if n, ok := new(big.Rat).SetString(string(x)); ok && n.IsInt() {
				return json.Number(n.Num().String())
			}
		case map[string]any:
			for k, item := range x {
				x[k] = normalize(item)
			}
		case []any:
			for i, item := range x {
				x[i] = normalize(item)
			}
		}
		return v
	}
	canonical, _ := json.Marshal(normalize(value))
	return canonical
}
func sortedDocument(values bson.M) bson.D {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	doc := bson.D{}
	for _, k := range keys {
		doc = append(doc, bson.E{Key: k, Value: values[k]})
	}
	return doc
}

func normalizeIndexWeight(value any) (int, error) {
	var n int64
	switch v := value.(type) {
	case int:
		n = int64(v)
	case int32:
		n = int64(v)
	case int64:
		n = v
	case float64:
		if v != float64(int64(v)) {
			return 0, fmt.Errorf("文本权重必须为正整数")
		}
		n = int64(v)
	default:
		return 0, fmt.Errorf("文本权重必须为正整数")
	}
	if n < 1 || n > 99999 {
		return 0, fmt.Errorf("文本权重范围为 1 到 99999")
	}
	return int(n), nil
}
