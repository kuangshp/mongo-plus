// Package settings 保存运行时与生成器共享的配置类型，避免两个入口互相依赖。
package settings

import (
	"fmt"
	"strings"
	"time"
)

// IDConfig 配置主键类型与生成方式；集合的 _id 始终映射为 Go 的 ID 字段。
type IDConfig struct {
	Type       string `json:"type" yaml:"type"`
	Generation string `json:"generation" yaml:"generation"`
}

// Normalize 补全并校验主键规则。
func (c IDConfig) Normalize() (IDConfig, error) {
	if c.Type == "" {
		c.Type = "objectId"
	}
	switch strings.ToLower(c.Type) {
	case "objectid":
		c.Type = "objectId"
		if c.Generation == "" {
			c.Generation = "auto"
		}
	case "int64":
		c.Type = "int64"
		if c.Generation == "" {
			c.Generation = "increment"
		}
	case "string":
		c.Type = "string"
		if c.Generation == "" {
			c.Generation = "manual"
		}
	default:
		return c, fmt.Errorf("不支持的主键类型 %q", c.Type)
	}
	if c.Generation == "manual" || (c.Type == "objectId" && c.Generation == "auto") || (c.Type == "int64" && c.Generation == "increment") {
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
}
