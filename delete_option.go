package mongoplus

// DeleteOption 定义删除行为，默认遵循集合的逻辑删除配置。
type DeleteOption struct{ physical bool }

// DeleteBuilder 沿用 gorm-plus 的删除选项构造方式。
type DeleteBuilder struct{ option DeleteOption }

// Delete 创建删除选项构造器。
func Delete() *DeleteBuilder { return &DeleteBuilder{} }

// WithPhysicalDelete 明确执行物理删除，包含已经逻辑删除的文档。
func (b *DeleteBuilder) WithPhysicalDelete() *DeleteBuilder {
	b.option.physical = true
	return b
}

// Build 返回删除选项快照。
func (b *DeleteBuilder) Build() DeleteOption { return b.option }

// WithPhysicalDelete 等价于 Delete().WithPhysicalDelete().Build()。
func WithPhysicalDelete() DeleteOption { return Delete().WithPhysicalDelete().Build() }

func physicalDelete(opts []DeleteOption) bool {
	for _, opt := range opts {
		if opt.physical {
			return true
		}
	}
	return false
}
