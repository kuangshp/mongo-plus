package mongoplus

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"sync/atomic"
)

// DataSourceGroupConfig 是一组主库和从库。连接由调用方创建，从库 URI 应带有 secondary 读偏好。
// 没有从库时读操作使用主库。
type DataSourceGroupConfig struct {
	Master *Database
	Slaves []*Database
}

// DataSourceManager 按上下文选择命名数据源，并在主库与从库之间分流。
type DataSourceManager struct {
	mu      sync.RWMutex
	groups  map[string]*dataSourceGroup
	defName string
}

type dataSourceGroup struct {
	master *Database
	slaves []*Database
	next   atomic.Uint64
}

type dataSourceNameKey struct{}
type dataSourceReadKey struct{}
type dataSourceWriteKey struct{}

// NewDataSourceManager 创建独立管理器。进程内也可以直接使用 DS。
func NewDataSourceManager() *DataSourceManager {
	return &DataSourceManager{groups: map[string]*dataSourceGroup{}}
}

// DS 是进程内默认的多数据源管理器。
var DS = NewDataSourceManager()

// Register 注册命名数据源。第一个注册的名称成为 Auto 的默认数据源。
func (m *DataSourceManager) Register(name string, cfg DataSourceGroupConfig) error {
	if m == nil {
		return fmt.Errorf("数据源管理器不能为空")
	}
	if name == "" || cfg.Master == nil {
		return fmt.Errorf("数据源名称和主库不能为空")
	}
	group := &dataSourceGroup{master: cfg.Master}
	for _, slave := range cfg.Slaves {
		if slave != nil {
			group.slaves = append(group.slaves, slave)
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.groups == nil {
		m.groups = map[string]*dataSourceGroup{}
	}
	m.groups[name] = group
	if m.defName == "" {
		m.defName = name
	}
	return nil
}

// SetDefault 指定 Auto 在上下文没有数据源名时使用的名称。
func (m *DataSourceManager) SetDefault(name string) {
	m.mu.Lock()
	m.defName = name
	m.mu.Unlock()
}

// Auto 读取上下文中的数据源名和读写标记。没有标记时走主库，读标记走从库。
func (m *DataSourceManager) Auto(ctx context.Context) (*Database, error) {
	name := DSNameFrom(ctx)
	if name == "" {
		m.mu.RLock()
		name = m.defName
		m.mu.RUnlock()
	}
	if name == "" {
		return nil, fmt.Errorf("未找到数据源名且未设置默认数据源")
	}
	if DSIsRead(ctx) && !DSIsWrite(ctx) {
		return m.Read(name)
	}
	return m.Write(name)
}

// Write 返回主库。
func (m *DataSourceManager) Write(name string) (*Database, error) {
	group, err := m.group(name)
	if err != nil {
		return nil, err
	}
	return group.master, nil
}

// Read 按轮询返回从库；没有从库时返回主库。
func (m *DataSourceManager) Read(name string) (*Database, error) {
	group, err := m.group(name)
	if err != nil {
		return nil, err
	}
	if len(group.slaves) == 0 {
		return group.master, nil
	}
	index := group.next.Add(1) - 1
	return group.slaves[index%uint64(len(group.slaves))], nil
}

// Names 返回已注册的数据源名。
func (m *DataSourceManager) Names() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	names := make([]string, 0, len(m.groups))
	for name := range m.groups {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// Ping 检查每个主库和从库。键格式为 name:master、name:slave0。
func (m *DataSourceManager) Ping(ctx context.Context) map[string]error {
	m.mu.RLock()
	snapshot := make(map[string]*dataSourceGroup, len(m.groups))
	for name, group := range m.groups {
		snapshot[name] = group
	}
	m.mu.RUnlock()
	result := map[string]error{}
	for name, group := range snapshot {
		result[name+":master"] = pingDatabase(ctx, group.master)
		for i, slave := range group.slaves {
			result[fmt.Sprintf("%s:slave%d", name, i)] = pingDatabase(ctx, slave)
		}
	}
	return result
}

// Close 关闭管理器里出现过的 Client。同一个 Client 只关闭一次。
func (m *DataSourceManager) Close(ctx context.Context) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	seen := map[*Database]struct{}{}
	var first error
	for _, group := range m.groups {
		nodes := append([]*Database{group.master}, group.slaves...)
		for _, node := range nodes {
			if node == nil {
				continue
			}
			if _, ok := seen[node]; ok {
				continue
			}
			seen[node] = struct{}{}
			if node.native == nil {
				continue
			}
			if err := node.Close(ctx); err != nil && first == nil {
				first = err
			}
		}
	}
	return first
}

func (m *DataSourceManager) group(name string) (*dataSourceGroup, error) {
	m.mu.RLock()
	group := m.groups[name]
	m.mu.RUnlock()
	if group == nil {
		return nil, fmt.Errorf("数据源 %q 未注册，已注册: %v", name, m.Names())
	}
	return group, nil
}

func pingDatabase(ctx context.Context, db *Database) error {
	if db == nil || db.native == nil {
		return fmt.Errorf("数据库未连接")
	}
	return db.native.Client().Ping(ctx, nil)
}

// DSWithName 将数据源名写入上下文。
func DSWithName(ctx context.Context, name string) context.Context {
	return context.WithValue(resolveContext(ctx), dataSourceNameKey{}, name)
}

// DSNameFrom 读取数据源名。
func DSNameFrom(ctx context.Context) string {
	name, _ := resolveContext(ctx).Value(dataSourceNameKey{}).(string)
	return name
}

// DSWithRead 标记后续 Auto 选择从库。
func DSWithRead(ctx context.Context) context.Context {
	return context.WithValue(resolveContext(ctx), dataSourceReadKey{}, true)
}

// DSWithWrite 标记后续 Auto 选择主库。同时存在时写标记优先。
func DSWithWrite(ctx context.Context) context.Context {
	return context.WithValue(resolveContext(ctx), dataSourceWriteKey{}, true)
}

// DSIsRead 判断上下文是否标记为读。
func DSIsRead(ctx context.Context) bool {
	read, _ := resolveContext(ctx).Value(dataSourceReadKey{}).(bool)
	return read
}

// DSIsWrite 判断上下文是否标记为写。
func DSIsWrite(ctx context.Context) bool {
	write, _ := resolveContext(ctx).Value(dataSourceWriteKey{}).(bool)
	return write
}
