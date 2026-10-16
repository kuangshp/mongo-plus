package mongoplus

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"reflect"
	"strings"

	"github.com/kuangshp/mongo-plus/field"
)

// SensitiveReturnMode 控制读出敏感字段后的展示形式。
type SensitiveReturnMode uint8

const (
	SensitiveReturnMasked SensitiveReturnMode = iota
	SensitiveReturnPlain
	SensitiveReturnCipher
)

// SensitiveMaskConfig 配置脱敏保留的前后长度。
type SensitiveMaskConfig struct {
	Prefix      int
	Suffix      int
	Replacement string
}

// SensitiveFieldConfig 描述一个敏感字段。明文建议使用 bson:"-"，密文和索引字段写入文档。
type SensitiveFieldConfig struct {
	PlainField    string
	CipherField   string
	IndexField    string
	PlainPath     string
	CipherPath    string
	IndexPath     string
	EncryptAtRest bool
	Normalize     func(string) string
	ReturnMode    SensitiveReturnMode
	// ReturnModeResolver 按请求决定展示形式，优先于 ReturnMode，低于上下文覆盖。
	ReturnModeResolver func(context.Context) SensitiveReturnMode
	Mask               SensitiveMaskConfig
}

// SensitiveConfig 配置敏感字段。常规场景提供至少 16 字节的 Key 和 Fields。
type SensitiveConfig struct {
	Key           []byte
	EncryptionKey []byte
	IndexKey      []byte
	Fields        []SensitiveFieldConfig
}

// SensitivePlugin 在写入时生成密文和盲索引，在读出时按权限解密或脱敏。
type SensitivePlugin struct {
	cfg   SensitiveConfig
	aead  cipher.AEAD
	byKey map[string]SensitiveFieldConfig
}

type sensitiveModeKey struct{}

// RegisterSensitive 在数据库上启用敏感字段处理，并返回可用于构造等值条件的插件。
func RegisterSensitive(db *Database, cfg SensitiveConfig) (*SensitivePlugin, error) {
	if db == nil {
		return nil, fmt.Errorf("敏感字段数据库不能为空")
	}
	plugin, err := NewSensitivePlugin(cfg)
	if err != nil {
		return nil, err
	}
	if db.sensitive != nil {
		return nil, fmt.Errorf("敏感字段已经注册")
	}
	db.sensitive = plugin
	return plugin, nil
}

// NewSensitivePlugin 创建插件。只使用 Key 时会派生独立的加密密钥和索引密钥。
func NewSensitivePlugin(cfg SensitiveConfig) (*SensitivePlugin, error) {
	if len(cfg.Key) > 0 {
		if len(cfg.Key) < 16 {
			return nil, errors.New("敏感字段 Key 不能少于 16 字节")
		}
		cfg.EncryptionKey = deriveSensitiveKey(cfg.Key, "mongo-plus/encryption")
		cfg.IndexKey = deriveSensitiveKey(cfg.Key, "mongo-plus/blind-index")
	}
	block, err := aes.NewCipher(cfg.EncryptionKey)
	if err != nil {
		return nil, fmt.Errorf("敏感字段 EncryptionKey 无效: %w", err)
	}
	if len(cfg.IndexKey) < 16 {
		return nil, errors.New("敏感字段 IndexKey 不能少于 16 字节")
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	plugin := &SensitivePlugin{cfg: cfg, aead: aead, byKey: map[string]SensitiveFieldConfig{}}
	for i := range cfg.Fields {
		item := cfg.Fields[i]
		if item.PlainField == "" || item.CipherField == "" || item.IndexField == "" {
			return nil, errors.New("PlainField、CipherField 和 IndexField 不能为空")
		}
		if item.Normalize == nil {
			item.Normalize = strings.TrimSpace
		}
		if item.Mask.Replacement == "" {
			item.Mask.Replacement = "*"
		}
		if item.Mask.Prefix == 0 && item.Mask.Suffix == 0 {
			item.Mask.Prefix, item.Mask.Suffix = 3, 4
		}
		if item.PlainPath == "" {
			item.PlainPath = toSnakeCase(item.PlainField)
		}
		if item.CipherPath == "" {
			item.CipherPath = toSnakeCase(item.CipherField)
		}
		if item.IndexPath == "" {
			item.IndexPath = toSnakeCase(item.IndexField)
		}
		cfg.Fields[i] = item
		plugin.byKey[item.PlainField] = item
		plugin.byKey[item.IndexField] = item
		plugin.byKey[item.PlainPath] = item
		plugin.byKey[item.IndexPath] = item
	}
	plugin.cfg.Fields = cfg.Fields
	return plugin, nil
}

// PhoneField 返回手机号的默认字段名、盲索引和脱敏规则。
func PhoneField(name string) SensitiveFieldConfig {
	return SensitiveFieldConfig{
		PlainField:  name,
		CipherField: name + "Cipher",
		IndexField:  name + "Index",
		Normalize:   normalizePhone,
		ReturnMode:  SensitiveReturnMasked,
		Mask:        SensitiveMaskConfig{Prefix: 3, Suffix: 4, Replacement: "*"},
	}
}

// WithSensitivePlaintext 让本次读取返回解密后的明文。
func WithSensitivePlaintext(ctx context.Context) context.Context {
	return context.WithValue(resolveContext(ctx), sensitiveModeKey{}, SensitiveReturnPlain)
}

// WithSensitiveCiphertext 让本次读取返回数据库中的存储值。
func WithSensitiveCiphertext(ctx context.Context) context.Context {
	return context.WithValue(resolveContext(ctx), sensitiveModeKey{}, SensitiveReturnCipher)
}

// WithSensitiveMasked 让本次读取返回脱敏值。
func WithSensitiveMasked(ctx context.Context) context.Context {
	return context.WithValue(resolveContext(ctx), sensitiveModeKey{}, SensitiveReturnMasked)
}

// Eq 用盲索引构造等值条件，logical 传业务字段名，例如 Phone。
func (p *SensitivePlugin) Eq(logical, value string) Condition {
	item, ok := p.byKey[logical]
	if !ok {
		item = PhoneField(logical)
	}
	if item.Normalize == nil {
		item.Normalize = strings.TrimSpace
	}
	if item.IndexPath == "" {
		item.IndexPath = toSnakeCase(item.IndexField)
	}
	return field.New[string](item.IndexPath).Eq(blindIndex(p.cfg.IndexKey, item.Normalize(value)))
}

// PhoneEq 构造手机号等值条件。
func (p *SensitivePlugin) PhoneEq(phone string) Condition {
	return p.Eq("Phone", phone)
}

// IndexValue 返回盲索引，供已经持有索引字段的查询使用。
func (p *SensitivePlugin) IndexValue(logical, value string) string {
	item, ok := p.byKey[logical]
	if !ok {
		item = PhoneField(logical)
	}
	if item.Normalize == nil {
		item.Normalize = strings.TrimSpace
	}
	return blindIndex(p.cfg.IndexKey, item.Normalize(value))
}

func (p *SensitivePlugin) seal(value any) error {
	return walkSensitive(reflect.ValueOf(value), func(item reflect.Value) error {
		for _, fieldCfg := range p.fieldsForValue(item) {
			plain, ok := sensitiveString(item, fieldCfg.PlainField)
			if !ok || plain == "" {
				continue
			}
			plain = fieldCfg.Normalize(plain)
			stored := plain
			if fieldCfg.EncryptAtRest {
				var err error
				stored, err = p.encrypt(plain)
				if err != nil {
					return err
				}
			}
			if err := setSensitiveString(item, fieldCfg.CipherField, stored); err != nil {
				return err
			}
			if err := setSensitiveString(item, fieldCfg.IndexField, blindIndex(p.cfg.IndexKey, plain)); err != nil {
				return err
			}
		}
		return nil
	})
}

func (p *SensitivePlugin) reveal(ctx context.Context, value any) error {
	return walkSensitive(reflect.ValueOf(value), func(item reflect.Value) error {
		for _, fieldCfg := range p.fieldsForValue(item) {
			stored, ok := sensitiveString(item, fieldCfg.CipherField)
			if !ok || stored == "" {
				continue
			}
			mode := fieldCfg.ReturnMode
			if fieldCfg.ReturnModeResolver != nil {
				mode = fieldCfg.ReturnModeResolver(ctx)
			}
			if contextMode, ok := ctx.Value(sensitiveModeKey{}).(SensitiveReturnMode); ok {
				mode = contextMode
			}
			output := stored
			if mode != SensitiveReturnCipher {
				plain := stored
				if fieldCfg.EncryptAtRest {
					var err error
					plain, err = p.decrypt(stored)
					if err != nil {
						return err
					}
				}
				output = plain
				if mode == SensitiveReturnMasked {
					output = maskSensitive(plain, fieldCfg.Mask)
				}
			}
			if err := setSensitiveString(item, fieldCfg.PlainField, output); err != nil {
				return err
			}
		}
		return nil
	})
}

func (p *SensitivePlugin) rewrite(ops []field.UpdateOp) ([]field.UpdateOp, error) {
	if p == nil {
		return ops, nil
	}
	rewritten := make([]field.UpdateOp, 0, len(ops))
	for _, op := range ops {
		item, ok := p.byKey[op.Path]
		if !ok || op.Operator != "$set" || op.Path != item.PlainPath {
			rewritten = append(rewritten, op)
			continue
		}
		text, ok := op.Value.(string)
		if !ok {
			return nil, fmt.Errorf("敏感字段 %s 只能更新字符串", op.Path)
		}
		text = item.Normalize(text)
		stored := text
		if item.EncryptAtRest {
			var err error
			stored, err = p.encrypt(text)
			if err != nil {
				return nil, err
			}
		}
		rewritten = append(rewritten,
			field.UpdateOp{Operator: "$set", Path: item.CipherPath, Value: stored},
			field.UpdateOp{Operator: "$set", Path: item.IndexPath, Value: blindIndex(p.cfg.IndexKey, text)},
		)
	}
	return rewritten, nil
}

func (p *SensitivePlugin) fieldsForValue(value reflect.Value) []SensitiveFieldConfig {
	fields := append([]SensitiveFieldConfig(nil), p.cfg.Fields...)
	seen := map[string]struct{}{}
	for _, item := range fields {
		seen[item.PlainField] = struct{}{}
	}
	for i := 0; i < value.NumField(); i++ {
		structField := value.Type().Field(i)
		if _, ok := seen[structField.Name]; ok {
			continue
		}
		tag := structField.Tag.Get("mongoplus")
		if tag == "" {
			tag = structField.Tag.Get("gormplus")
		}
		options := sensitiveTag(tag)
		if options["cipher"] == "" || options["index"] == "" {
			continue
		}
		item := PhoneField(structField.Name)
		item.CipherField = options["cipher"]
		item.IndexField = options["index"]
		item.EncryptAtRest = strings.EqualFold(options["encrypt"], "true")
		item.PlainPath = bsonName(structField)
		item.CipherPath = toSnakeCase(item.CipherField)
		item.IndexPath = toSnakeCase(item.IndexField)
		fields = append(fields, item)
	}
	return fields
}

func (p *SensitivePlugin) encrypt(plain string) (string, error) {
	nonce := make([]byte, p.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return "", err
	}
	sealed := p.aead.Seal(nonce, nonce, []byte(plain), nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (p *SensitivePlugin) decrypt(encoded string) (string, error) {
	data, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil || len(data) < p.aead.NonceSize() {
		return "", errors.New("敏感字段密文无效")
	}
	plain, err := p.aead.Open(nil, data[:p.aead.NonceSize()], data[p.aead.NonceSize():], nil)
	if err != nil {
		return "", errors.New("敏感字段解密失败")
	}
	return string(plain), nil
}

func deriveSensitiveKey(master []byte, purpose string) []byte {
	mac := hmac.New(sha256.New, master)
	_, _ = mac.Write([]byte(purpose))
	return mac.Sum(nil)
}

func blindIndex(key []byte, value string) string {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

func normalizePhone(value string) string {
	value = strings.NewReplacer(" ", "", "-", "", "(", "", ")", "").Replace(strings.TrimSpace(value))
	value = strings.TrimPrefix(value, "+86")
	return value
}

func toSnakeCase(value string) string {
	var result strings.Builder
	for i, char := range value {
		if i > 0 && char >= 'A' && char <= 'Z' {
			result.WriteByte('_')
		}
		result.WriteRune(char)
	}
	return strings.ToLower(result.String())
}

func maskSensitive(value string, cfg SensitiveMaskConfig) string {
	runes := []rune(value)
	if cfg.Prefix+cfg.Suffix >= len(runes) {
		return strings.Repeat(cfg.Replacement, len(runes))
	}
	return string(runes[:cfg.Prefix]) + strings.Repeat(cfg.Replacement, len(runes)-cfg.Prefix-cfg.Suffix) + string(runes[len(runes)-cfg.Suffix:])
}

func sensitiveTag(tag string) map[string]string {
	options := map[string]string{}
	for _, item := range strings.Split(tag, ";") {
		parts := strings.SplitN(item, ":", 2)
		if len(parts) == 2 {
			options[strings.TrimSpace(parts[0])] = strings.TrimSpace(parts[1])
		}
	}
	return options
}

func bsonName(field reflect.StructField) string {
	name := strings.Split(field.Tag.Get("bson"), ",")[0]
	if name == "" || name == "-" {
		return toSnakeCase(field.Name)
	}
	return name
}

func walkSensitive(value reflect.Value, fn func(reflect.Value) error) error {
	for value.IsValid() && (value.Kind() == reflect.Pointer || value.Kind() == reflect.Interface) {
		if value.IsNil() {
			return nil
		}
		value = value.Elem()
	}
	switch value.Kind() {
	case reflect.Struct:
		return fn(value)
	case reflect.Slice, reflect.Array:
		for i := 0; i < value.Len(); i++ {
			if err := walkSensitive(value.Index(i), fn); err != nil {
				return err
			}
		}
	}
	return nil
}

func sensitiveString(value reflect.Value, name string) (string, bool) {
	field := value.FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.String {
		return "", false
	}
	return field.String(), true
}

func setSensitiveString(value reflect.Value, name, content string) error {
	field := value.FieldByName(name)
	if !field.IsValid() || field.Kind() != reflect.String || !field.CanSet() {
		return fmt.Errorf("敏感字段 %s 不存在、不是 string 或不可写", name)
	}
	field.SetString(content)
	return nil
}
