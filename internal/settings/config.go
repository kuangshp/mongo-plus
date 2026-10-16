// Package settings 保存运行时与生成器共享的配置类型，避免两个入口互相依赖。
package settings

import (
	"fmt"
	"io"
	"strings"
	"time"
)

// IDType 表示主键类型；字符串底层类型保持 YAML/JSON 配置格式不变。
type IDType string

const (
	IDTypeObjectID IDType = "objectId" // 官方 bson.ObjectID。
	IDTypeInt64    IDType = "int64"    // 64 位整数。
	IDTypeString   IDType = "string"   // 字符串。
)

// IDGeneration 表示主键生成方式。
type IDGeneration string

const (
	IDGenerationAuto      IDGeneration = "auto"      // ObjectID 自动生成。
	IDGenerationIncrement IDGeneration = "increment" // int64 自增分配。
	IDGenerationManual    IDGeneration = "manual"    // 由业务提供主键。
)

// IDConfig 配置主键；通常只设置 Type，集合的 _id 始终映射为 Go 的 ID 字段。
// Generation 省略时按类型推导：objectId 自动生成、int64 自增、string 由业务提供。
type IDConfig struct {
	Type       IDType       `json:"type" yaml:"type"`
	Generation IDGeneration `json:"generation" yaml:"generation"`
	// BlockSize 可选；int64 自增时预分配号码数，0/1 每次按需申请。
	BlockSize int64 `json:"blockSize,omitempty" yaml:"blockSize,omitempty"`
}

// Normalize 补全并校验主键规则。
func (c IDConfig) Normalize() (IDConfig, error) {
	if c.Type == "" {
		c.Type = IDTypeObjectID
	}
	switch strings.ToLower(string(c.Type)) {
	case "objectid":
		c.Type = IDTypeObjectID
		if c.Generation == "" {
			c.Generation = IDGenerationAuto
		}
	case "int64":
		c.Type = IDTypeInt64
		if c.Generation == "" {
			c.Generation = IDGenerationIncrement
		}
	case "string":
		c.Type = IDTypeString
		if c.Generation == "" {
			c.Generation = IDGenerationManual
		}
	default:
		return c, fmt.Errorf("不支持的主键类型 %q", c.Type)
	}
	if c.BlockSize < 0 || c.BlockSize > 1_000_000 || (c.BlockSize > 0 && c.Generation != IDGenerationIncrement) {
		return c, fmt.Errorf("blockSize 仅用于 int64 自增，范围为 0 到 1000000")
	}
	if c.Generation == IDGenerationManual || (c.Type == IDTypeObjectID && c.Generation == IDGenerationAuto) || (c.Type == IDTypeInt64 && c.Generation == IDGenerationIncrement) {
		return c, nil
	}
	return c, fmt.Errorf("主键类型 %s 不支持生成方式 %q", c.Type, c.Generation)
}

// CollectionConfig 是生成 Repository 携带的集合规则。
type CollectionConfig struct {
	Name           string   // 实际集合名，Repository 不会再次转换单复数。
	ID             IDConfig // 主键类型、生成方式与可选的自增区间预分配。
	SoftDelete     bool     // true 时普通删除设置 deleted_at，普通查询排除已删除文档。
	Timestamps     bool     // true 时创建和更新自动维护对应的时间字段。
	TenantField    string   // 可选租户 BSON 字段，从 WithTenant 的 Context 中读取值。
	CreatedByField string   // 可选创建人 BSON 字段，创建时从 WithActor 读取。
	UpdatedByField string   // 可选更新人 BSON 字段，创建和更新时从 WithActor 读取。
}

// ReservedCollection 保护序列表和迁移控制集合，避免被普通业务仓储误修改。
func ReservedCollection(name string) bool {
	switch name {
	case "__mongo_plus_sequences", "__mongo_plus_migrations", "__mongo_plus_migration_locks":
		return true
	}
	return false
}

// Config 是连接配置；URI 可通过业务项目自己的配置系统注入。
type Config struct {
	URI         string        `json:"uri" yaml:"uri"`                 // 必填连接地址；认证、副本集等参数通过 URI 配置。
	Database    string        `json:"database" yaml:"database"`       // 必填数据库名，集合由各 Repository 单独指定。
	MaxPoolSize uint64        `json:"maxPoolSize" yaml:"maxPoolSize"` // Client 最大连接数；0 使用驱动默认值。
	Timeout     time.Duration `json:"-" yaml:"-"`                     // Go 中使用 10*time.Second 等带单位的值；0 不设置操作超时。
	Log         LogConfig     `json:"log,optional" yaml:"log"`        // 命令日志独立配置，默认关闭；连接创建后不能补装监控器。
}

// LogLevel 控制 MongoDB 命令日志；零值关闭。
type LogLevel string

const (
	LogSilent LogLevel = "silent"
	LogError  LogLevel = "error"
	LogWarn   LogLevel = "warn"
	LogInfo   LogLevel = "info"
)

// LogConfig 配置驱动命令日志。info 输出全部，warn 输出慢命令和错误，error 仅输出错误。
// 命令参数按字段脱敏，可进一步隐藏全部值；Writer 仅通过 Go 设置，默认输出到 os.Stdout。
type LogConfig struct {
	Level               LogLevel  `json:"level,optional" yaml:"level"`                             // 空值或 silent 关闭；info 输出全部命令。
	SlowThresholdMS     int64     `json:"slowThresholdMs,optional" yaml:"slowThresholdMs"`         // 0 使用 200ms。
	Colorful            bool      `json:"colorful,optional" yaml:"colorful"`                       // 是否输出 ANSI 颜色；日志文件通常保持 false。
	Writer              io.Writer `json:"-" yaml:"-"`                                              // Go 中可指定输出流；nil 使用标准输出，无法通过 YAML 注入。
	HideParameters      bool      `json:"hideParameters,optional" yaml:"hideParameters"`           // 隐藏全部参数值；保留命令名称、集合和字段结构。
	SensitiveFields     []string  `json:"sensitiveFields,optional" yaml:"sensitiveFields"`         // 追加脱敏字段或点路径；默认敏感字段始终脱敏。
	MaxLength           int       `json:"maxLength,optional" yaml:"maxLength"`                     // 命令内容字节上限，0 默认 4096，至少 128。
	IncludeErrorDetails bool      `json:"includeErrorDetails,optional" yaml:"includeErrorDetails"` // 默认关闭，防止服务端错误回显敏感值；仅诊断时开启。
}
