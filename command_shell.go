package mongoplus

import (
	"encoding/json"
	"math"
	"strconv"
	"strings"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// shellCommand 默认使用脱敏预览；显式 Replayable 模式才使用真实参数并保留 BSON 类型。
// 可复制模式超出长度上限时整条省略，不输出一条可能被误执行的残缺语句。
func shellCommand(raw bson.Raw, cfg LogConfig) string {
	var doc bson.D
	var err error
	if cfg.Replayable {
		err = bson.Unmarshal(raw, &doc)
		for _, name := range []string{"getMore", "commitTransaction", "abortTransaction"} {
			if raw.Lookup(name).Type != 0 {
				return "// [not replayable: this command requires its original cursor or session]"
			}
		}
	} else {
		doc, err = redactedCommand(raw, cfg)
	}
	if err != nil {
		return "[command unavailable]"
	}
	call := shellCall{target: "db", method: "runCommand", args: []any{shellOptions(doc)}}
	for _, name := range []string{"insert", "find", "update", "delete", "aggregate", "findAndModify", "count", "distinct"} {
		collection, ok := shellField(doc, name).(string)
		if !ok || collection == "" {
			continue
		}
		candidate := shellCall{target: shellCollection(collection)}
		switch name {
		case "insert":
			rows, ok := shellField(doc, "documents").(bson.A)
			if !ok || len(rows) == 0 {
				break
			}
			candidate.method, candidate.args = "insertMany", []any{rows}
			options := shellOptions(doc, name, "documents")
			if len(rows) == 1 {
				candidate.method, candidate.args = "insertOne", []any{rows[0]}
				options = shellOptions(options, "ordered") // 单条插入没有批内顺序。
			}
			candidate.addOptions(options)
		case "find":
			candidate.method, candidate.args = "find", []any{shellDocumentOrEmpty(shellField(doc, "filter"))}
			projection := shellField(doc, "projection")
			options := shellOptions(doc, name, "filter", "projection", "sort", "skip", "limit", "hint", "collation", "maxTimeMS", "comment", "batchSize", "allowDiskUse")
			if projection != nil || len(options) > 0 {
				candidate.args = append(candidate.args, shellDocumentOrEmpty(projection))
			}
			candidate.addOptions(options)
			for _, key := range []string{"sort", "skip", "limit", "hint", "collation", "maxTimeMS", "comment", "batchSize", "allowDiskUse"} {
				if value := shellField(doc, key); value != nil {
					candidate.chains = append(candidate.chains, shellChain{key, shellPlainValue{value}})
				}
			}
		case "update", "delete":
			itemsKey := "updates"
			if name == "delete" {
				itemsKey = "deletes"
			}
			items, ok := shellField(doc, itemsKey).(bson.A)
			// 多个操作可能拥有不同 Hint/Collation 等选项，保留完整命令，避免误拼成单条操作。
			if !ok || len(items) != 1 {
				break
			}
			item, ok := items[0].(bson.D)
			if !ok {
				break
			}
			candidate.args = []any{shellDocumentOrEmpty(shellField(item, "q"))}
			options := shellOptions(doc, name, itemsKey, "ordered")
			if name == "update" {
				update := shellField(item, "u")
				if update == nil {
					break
				}
				candidate.method = "updateOne"
				// 只读取原始命令的结构标志，不从原始 BSON 恢复任何业务参数。
				multiValue := raw.Lookup("updates", "0", "multi")
				multi, validMulti := multiValue.BooleanOK()
				if multiValue.Type != 0 && !validMulti {
					candidate.method = ""
					break
				}
				if multi {
					candidate.method = "updateMany"
				}
				if original, ok := raw.Lookup("updates", "0", "u").DocumentOK(); ok {
					fields, _ := original.Elements()
					if len(fields) > 0 && !strings.HasPrefix(fields[0].Key(), "$") {
						if multi {
							candidate.method = "" // 无效的批量替换保留原命令，方便排查服务端错误。
							break
						}
						candidate.method = "replaceOne"
					}
				}
				candidate.args = append(candidate.args, update)
				options = append(options, shellOptions(item, "q", "u", "multi")...)
			} else {
				limit, validLimit := raw.Lookup("deletes", "0", "limit").AsInt64OK()
				if !validLimit || limit != 0 && limit != 1 {
					break
				}
				candidate.method = "deleteMany"
				if limit == 1 {
					candidate.method = "deleteOne"
				}
				options = append(options, shellOptions(item, "q", "limit")...)
			}
			candidate.addOptions(options)
		case "aggregate":
			if pipeline, ok := shellField(doc, "pipeline").(bson.A); ok {
				candidate.method, candidate.args = "aggregate", []any{pipeline}
				options := shellOptions(doc, name, "pipeline")
				// 空 cursor 是协议要求，不是业务选项；指定 batchSize 时仍保留。
				if cursor, ok := shellField(options, "cursor").(bson.D); ok && len(cursor) == 0 {
					options = shellOptions(options, "cursor")
				}
				candidate.addOptions(options)
			}
		case "findAndModify":
			candidate.method, candidate.args = "findAndModify", []any{shellDriverOptions(shellOptions(doc, name))}
		case "count":
			candidate.method, candidate.args = "countDocuments", []any{shellDocumentOrEmpty(shellField(doc, "query"))}
			if shellField(doc, "query") == nil {
				candidate.method, candidate.args = "estimatedDocumentCount", nil
			}
			candidate.addOptions(shellOptions(doc, name, "query"))
		case "distinct":
			candidate.method, candidate.args = "distinct", []any{shellField(doc, "key"), shellDocumentOrEmpty(shellField(doc, "query"))}
			candidate.addOptions(shellOptions(doc, name, "key", "query"))
		}
		if candidate.method != "" {
			call = candidate
		}
		break
	}
	w := shellWriter{limit: cfg.MaxLength, exact: cfg.Replayable}
	if w.limit == 0 {
		w.limit = 4096
	}
	w.write(call.target + "." + call.method + "(")
	for i, arg := range call.args {
		if i > 0 {
			w.write(", ")
		}
		w.value(arg, 0)
	}
	w.write(")")
	for _, chain := range call.chains {
		w.write("." + chain.method + "(")
		w.value(chain.arg, 0)
		w.write(")")
	}
	if cfg.Replayable {
		w.write(";")
	}
	result := w.String()
	if w.cut {
		if cfg.Replayable {
			return "// [statement omitted: exceeds maxLength or supported nesting; increase maxLength to copy the complete command]"
		}
		result += "...[truncated]"
	}
	return truncateCommand(result, w.limit)
}

type shellCall struct {
	target, method string
	args           []any
	chains         []shellChain
}
type shellChain struct {
	method string
	arg    any
}

func (c *shellCall) addOptions(options bson.D) {
	if len(options) > 0 {
		c.args = append(c.args, shellDriverOptions(options))
	}
}

// Shell/Node 的游标与 options 校验要求 JavaScript number；业务过滤、更新和 let 值仍保留 BSON 类型。
// 包装器只改变显示方式，不改动驱动的原始命令。
type shellPlainValue struct{ value any }

func shellDriverOptions(options bson.D) bson.D {
	result := make(bson.D, len(options))
	copy(result, options)
	for i := range result {
		switch result[i].Key {
		case "maxTimeMS", "maxAwaitTimeMS", "skip", "limit", "batchSize", "sort", "hint", "writeConcern", "collation", "cursor":
			result[i].Value = shellPlainValue{result[i].Value}
		}
	}
	return result
}

func shellField(doc bson.D, key string) any {
	for _, field := range doc {
		if field.Key == key {
			return field.Value
		}
	}
	return nil
}

// 仅移除驱动协议元数据，其余选项全部保留；未知命令仍能以 runCommand 排版。
func shellOptions(doc bson.D, skip ...string) bson.D {
	result := bson.D{}
	for _, field := range doc {
		switch field.Key {
		case "lsid", "txnNumber", "$clusterTime", "$db", "autocommit", "startTransaction", "stmtId", "stmtIds", "apiVersion", "apiStrict", "apiDeprecationErrors":
			continue
		}
		omit := false
		for _, key := range skip {
			omit = omit || field.Key == key
		}
		if !omit {
			result = append(result, field)
		}
	}
	return result
}

func shellDocumentOrEmpty(value any) any {
	if value == nil {
		return bson.D{}
	}
	return value
}

func shellIdentifier(value string) bool {
	if value == "" {
		return false
	}
	for i, r := range value {
		if r != '_' && r != '$' && !(r >= 'a' && r <= 'z') && !(r >= 'A' && r <= 'Z') && !(i > 0 && r >= '0' && r <= '9') {
			return false
		}
	}
	return true
}

func shellQuote(value string) string {
	encoded, _ := json.Marshal(value) // JSON 字符串同时安全转义换行、控制字符及 JS 分隔符。
	return string(encoded)
}

func shellCollection(name string) string {
	if shellIdentifier(name) {
		switch name {
		case "getCollection", "getSiblingDB", "runCommand", "adminCommand", "getName", "stats", "watch", "constructor", "__proto__", "toString":
		default:
			return "db." + name
		}
	}
	return "db.getCollection(" + shellQuote(name) + ")"
}

// 写入时即限制长度，避免为了打印大批量命令而构建完整的展示文本。
type shellWriter struct {
	strings.Builder
	limit int
	cut   bool
	exact bool
}

func (w *shellWriter) write(value string) {
	if w.cut {
		return
	}
	if remaining := w.limit - w.Len(); len(value) > remaining {
		w.WriteString(value[:remaining])
		w.cut = true
		return
	}
	w.WriteString(value)
}

func (w *shellWriter) indent(depth int) { w.write("\n" + strings.Repeat("  ", depth)) }

func (w *shellWriter) value(value any, depth int) {
	if w.cut {
		return
	}
	if depth > 100 {
		w.cut = true
		return
	}
	switch value := value.(type) {
	case shellPlainValue:
		exact := w.exact
		w.exact = false
		w.value(value.value, depth)
		w.exact = exact
	case nil:
		w.write("null")
	case bson.D:
		w.write("{")
		for i, field := range value {
			if w.cut {
				break
			}
			if i > 0 {
				w.write(",")
			}
			w.indent(depth + 1)
			key := shellQuote(field.Key)
			if shellIdentifier(field.Key) {
				key = field.Key
			}
			if field.Key == "__proto__" {
				key = "[" + shellQuote(field.Key) + "]"
			}
			w.write(key + ": ")
			w.value(field.Value, depth+1)
		}
		if len(value) > 0 {
			w.indent(depth)
		}
		w.write("}")
	case bson.A:
		multiline := false
		for _, item := range value {
			switch item.(type) {
			case bson.D, bson.A:
				multiline = true
			}
		}
		w.write("[")
		for i, item := range value {
			if w.cut {
				break
			}
			if i > 0 {
				w.write(",")
				if !multiline {
					w.write(" ")
				}
			}
			if multiline {
				w.indent(depth + 1)
			}
			itemDepth := depth
			if multiline {
				itemDepth++
			}
			w.value(item, itemDepth)
		}
		if multiline && len(value) > 0 {
			w.indent(depth)
		}
		w.write("]")
	case string:
		w.write(shellQuote(value))
	case bool:
		w.write(strconv.FormatBool(value))
	case int32:
		w.write(strconv.FormatInt(int64(value), 10))
	case int64:
		if w.exact || value < -9007199254740991 || value > 9007199254740991 {
			w.write("NumberLong(" + shellQuote(strconv.FormatInt(value, 10)) + ")")
		} else {
			w.write(strconv.FormatInt(value, 10))
		}
	case float64:
		if w.exact {
			w.write("Double(")
		}
		switch {
		case math.IsInf(value, 1):
			w.write("Infinity")
		case math.IsInf(value, -1):
			w.write("-Infinity")
		default:
			w.write(strconv.FormatFloat(value, 'g', -1, 64))
		}
		if w.exact {
			w.write(")")
		}
	case bson.ObjectID:
		w.write("ObjectId(" + shellQuote(value.Hex()) + ")")
	case bson.DateTime:
		w.write("ISODate(" + shellQuote(value.Time().UTC().Format(time.RFC3339Nano)) + ")")
	case bson.Decimal128:
		w.write("NumberDecimal(" + shellQuote(value.String()) + ")")
	default:
		// 其他 BSON 类型保留规范表示，避免把正则、Timestamp、MinKey 等误打印成普通对象。
		encoded, err := bson.MarshalExtJSON(bson.D{{Key: "v", Value: value}}, true, false)
		var wrapper map[string]json.RawMessage
		if err != nil || json.Unmarshal(encoded, &wrapper) != nil {
			w.write(shellQuote("[unavailable]"))
			return
		}
		w.write("EJSON.deserialize(" + string(wrapper["v"]) + ")")
	}
}
