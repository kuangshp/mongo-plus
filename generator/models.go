package generator

import "github.com/kuangshp/mongo-plus/schema"

// GenerateModels 为手写 Entity 生成 DAO 与 Repository，保留原始 Entity 文件。
func GenerateModels(c schema.Config, models ...schema.Model) ([]string, error) {
	if err := c.Normalize(); err != nil {
		return nil, err
	}
	entities, err := schema.FromModels(c, models...)
	if err != nil {
		return nil, err
	}
	return GenerateEntities(c, entities)
}
