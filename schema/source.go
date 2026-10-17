package schema

import (
	"encoding/json"
	"fmt"
)

// loadedEntity 仅由 LoadEntities 创建，不能通过设置公开的 Source 路径伪造来源。
// 保存序列化快照而非 Entity 指针，避免切片、map 和指针的原地修改绕过校验。
type loadedEntity struct {
	source     string
	definition string
}

func validateLoadedEntities(entities []Entity) error {
	if len(entities) == 0 {
		return fmt.Errorf("没有 JSON 实体定义，请先编写 schema/*.json 并调用 schema.LoadEntities")
	}
	seen := make(map[string]bool, len(entities))
	for _, entity := range entities {
		if entity.loaded == nil || entity.Source != entity.loaded.source {
			return fmt.Errorf("集合 %q 必须由 JSON 文件定义，请使用 schema.LoadEntities 加载，禁止手写数据模型或元数据", entity.Collection)
		}
		definition, err := json.Marshal(entity)
		if err != nil {
			return fmt.Errorf("检查 JSON 实体 %s: %w", entity.Source, err)
		}
		if string(definition) != entity.loaded.definition {
			return fmt.Errorf("集合 %q 的元数据已被修改，请先修改 %s 再通过 schema.LoadEntities 重新加载", entity.Collection, entity.Source)
		}
		if seen[entity.Collection] {
			return fmt.Errorf("集合重复: %s", entity.Collection)
		}
		seen[entity.Collection] = true
	}
	return nil
}
