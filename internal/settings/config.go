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
	Name           string
	ID             IDConfig
	SoftDelete     bool
	Timestamps     bool
	TenantField    string
	CreatedByField string
	UpdatedByField string
}

// Config 是连接配置；URI 可通过业务项目自己的配置系统注入。
type Config struct {
	URI         string        `json:"uri" yaml:"uri"`
	Database    string        `json:"database" yaml:"database"`
	MaxPoolSize uint64        `json:"maxPoolSize" yaml:"maxPoolSize"`
	Timeout     time.Duration `json:"-" yaml:"-"`
	Log         LogConfig     `json:"log,optional" yaml:"log"`
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
// 日志含实际命令参数；Writer 仅通过 Go 设置，默认输出到 os.Stdout。
type LogConfig struct {
	Level           LogLevel  `json:"level,optional" yaml:"level"`
	SlowThresholdMS int64     `json:"slowThresholdMs,optional" yaml:"slowThresholdMs"` // 0 使用 200ms。
	Colorful        bool      `json:"colorful,optional" yaml:"colorful"`
	Writer          io.Writer `json:"-" yaml:"-"`
}
