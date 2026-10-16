package mongoplus

import (
	"context"
	"fmt"
	"io/fs"
	"log"
	"math"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/kuangshp/mongo-plus/internal/settings"
	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/sync/singleflight"
)

// DAL 把查询放在 JSON 文件中，调用方式和 gorm-plus 的 SQL 文件查询对应。
// 文件描述集合、操作和 BSON，不解析 SQL。执行时会按数据库上的租户、数据权限和仓储逻辑删除追加顶层条件。$lookup 等子管道仍要自己写范围。
type DAL struct {
	provider dalProvider
	loader   DALLoader
	hooks    []DALHook
	debug    bool
	mu       sync.Mutex
	files    map[string][]byte
	group    singleflight.Group
	stop     context.CancelFunc
}

// DALLoader 读取查询文件，通常是 embed.FS。
type DALLoader interface {
	Load(name string) ([]byte, error)
}

// DALHook 在每次文件查询前后触发，可用于慢操作和链路追踪。
type DALHook interface {
	Before(ctx context.Context, file string, args []any)
	After(ctx context.Context, file string, args []any, cost time.Duration, err error)
}

// DALOption 配置 DAL 实例。
type DALOption func(*DAL)

// DALPageResult 是分页查询结果。
type DALPageResult[T any] struct {
	List  []T
	Total int64
}

// DALExecResult 是写操作的命中数量。更新使用匹配数，删除使用删除数。
type DALExecResult struct {
	RowsAffected int64
}

type dalProvider interface {
	Get(ctx context.Context) (*Database, error)
}

type fixedDALProvider struct{ db *Database }

func (p fixedDALProvider) Get(context.Context) (*Database, error) {
	if p.db == nil || p.db.native == nil {
		return nil, fmt.Errorf("DAL 数据库不能为空")
	}
	return p.db, nil
}

type dalContextKey struct{}

var defaultDAL atomic.Pointer[DAL]

// NewEmbedLoader 使用文件系统读取查询文件。配合 //go:embed 时传入 fs.Sub 后的目录。
func NewEmbedLoader(files fs.FS) DALLoader { return embedLoader{files} }

type embedLoader struct{ files fs.FS }

func (l embedLoader) Load(name string) ([]byte, error) {
	if l.files == nil {
		return nil, fmt.Errorf("查询文件目录不能为空")
	}
	data, err := fs.ReadFile(l.files, name)
	if err != nil {
		return nil, fmt.Errorf("读取查询文件 %s: %w", name, err)
	}
	return data, nil
}

// WithDALDebug 打印文件、耗时、参数个数和失败标记，不输出参数值和服务端错误详情。
func WithDALDebug(enabled bool) DALOption {
	return func(d *DAL) { d.debug = enabled }
}

// WithDALHook 追加生命周期回调。
func WithDALHook(hook DALHook) DALOption {
	return func(d *DAL) {
		if hook != nil {
			d.hooks = append(d.hooks, hook)
		}
	}
}

// WithDALCacheCleanup 定期清空文件内容缓存。程序退出时调用返回值的 Close。
func WithDALCacheCleanup(interval time.Duration) DALOption {
	return func(d *DAL) {
		if interval <= 0 {
			return
		}
		ctx, cancel := context.WithCancel(context.Background())
		d.stop = cancel
		go func() {
			ticker := time.NewTicker(interval)
			defer ticker.Stop()
			for {
				select {
				case <-ctx.Done():
					return
				case <-ticker.C:
					d.mu.Lock()
					d.files = nil
					d.mu.Unlock()
				}
			}
		}()
	}
}

// NewDal 创建 DAL 并设为进程默认实例。
func NewDal(db *Database, loader DALLoader, opts ...DALOption) (*DAL, error) {
	return NewDalWithProvider(fixedDALProvider{db}, loader, opts...)
}

// NewDalWithProvider 用自定义数据库选择器创建 DAL，可用于按上下文切换主从。
func NewDalWithProvider(provider dalProvider, loader DALLoader, opts ...DALOption) (*DAL, error) {
	if provider == nil || loader == nil {
		return nil, fmt.Errorf("DAL 数据库和文件加载器不能为空")
	}
	dal := &DAL{provider: provider, loader: loader, files: map[string][]byte{}}
	for _, opt := range opts {
		if opt != nil {
			opt(dal)
		}
	}
	defaultDAL.Store(dal)
	return dal, nil
}

// Close 停止文件缓存清理。
func (d *DAL) Close() {
	if d != nil && d.stop != nil {
		d.stop()
	}
}

// WithDALDB 让当前上下文使用指定 DAL 实例。
func WithDALDB(ctx context.Context, d *DAL) context.Context {
	return context.WithValue(resolveContext(ctx), dalContextKey{}, d)
}

// DALPreload 在启动时加载文件，路径错误会立即返回。
func DALPreload(files ...string) error {
	dal, err := resolveDAL(context.Background())
	if err != nil {
		return err
	}
	return dal.Preload(files...)
}

// Preload 预加载当前实例的文件，适用于不依赖全局默认 DAL 的多数据源项目。
// 这里只检查文件读取；带参数文件的语义在实际绑定参数时校验。
func (d *DAL) Preload(files ...string) error {
	if d == nil {
		return fmt.Errorf("DAL 不能为空")
	}
	for _, file := range files {
		if _, err := d.load(file); err != nil {
			return err
		}
	}
	return nil
}

func resolveDAL(ctx context.Context) (*DAL, error) {
	if ctx == nil {
		return nil, fmt.Errorf("DAL 操作上下文不能为空")
	}
	if dal, ok := ctx.Value(dalContextKey{}).(*DAL); ok && dal != nil {
		return dal, nil
	}
	if dal := defaultDAL.Load(); dal != nil {
		return dal, nil
	}
	return nil, fmt.Errorf("尚未初始化 DAL")
}

func (d *DAL) load(name string) ([]byte, error) {
	d.mu.Lock()
	if data, ok := d.files[name]; ok {
		d.mu.Unlock()
		return append([]byte(nil), data...), nil
	}
	d.mu.Unlock()
	value, err, _ := d.group.Do(name, func() (any, error) {
		data, err := d.loader.Load(name)
		if err != nil {
			return nil, err
		}
		d.mu.Lock()
		if d.files == nil {
			d.files = map[string][]byte{}
		}
		d.files[name] = append([]byte(nil), data...)
		d.mu.Unlock()
		return data, nil
	})
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), value.([]byte)...), nil
}

type dalCommand struct {
	collection  string
	op          string
	filter      bson.D
	sort        bson.D
	projection  bson.D
	pipeline    bson.A
	update      bson.D
	document    bson.D
	limit       *int64
	skip        *int64
	allowAll    bool
	options     bson.D
	operations  bson.A
	invalidates []string
}

func (d *DAL) command(file string, positional []any, named map[string]any) (dalCommand, error) {
	data, err := d.load(file)
	if err != nil {
		return dalCommand{}, err
	}
	var root bson.D
	if err = bson.UnmarshalExtJSON(data, false, &root); err != nil {
		return dalCommand{}, fmt.Errorf("解析查询文件 %s: %w", file, err)
	}
	args := append([]any(nil), positional...)
	replaced, err := substituteDAL(root, &args, named)
	if err != nil {
		return dalCommand{}, fmt.Errorf("查询文件 %s: %w", file, err)
	}
	if named == nil && len(args) > 0 {
		return dalCommand{}, fmt.Errorf("查询文件 %s 有 %d 个多余参数", file, len(args))
	}
	document, ok := replaced.(bson.D)
	if !ok {
		return dalCommand{}, fmt.Errorf("查询文件 %s 必须是文档", file)
	}
	return commandFromDocument(document)
}

func commandFromDocument(doc bson.D) (dalCommand, error) {
	command := dalCommand{op: "find"}
	seen := make(map[string]bool, len(doc))
	for _, item := range doc {
		if seen[item.Key] {
			return dalCommand{}, fmt.Errorf("重复查询文件属性 %s", item.Key)
		}
		seen[item.Key] = true
		switch item.Key {
		case "collection":
			command.collection, _ = item.Value.(string)
		case "op":
			text, ok := item.Value.(string)
			if !ok || text == "" {
				return dalCommand{}, fmt.Errorf("op 必须是非空字符串")
			}
			command.op = text
		case "filter":
			filter, err := dalDocument(item.Value)
			if err != nil {
				return dalCommand{}, err
			}
			command.filter = filter
		case "sort":
			sort, err := dalDocument(item.Value)
			if err != nil {
				return dalCommand{}, err
			}
			command.sort = sort
		case "projection":
			projection, err := dalDocument(item.Value)
			if err != nil {
				return dalCommand{}, err
			}
			command.projection = projection
		case "pipeline":
			pipeline, ok := item.Value.(bson.A)
			if !ok {
				return dalCommand{}, fmt.Errorf("pipeline 必须是数组")
			}
			command.pipeline = pipeline
		case "update":
			update, err := dalDocument(item.Value)
			if err != nil {
				return dalCommand{}, err
			}
			command.update = update
		case "document":
			document, err := dalDocument(item.Value)
			if err != nil {
				return dalCommand{}, err
			}
			command.document = document
		case "limit":
			limit, err := dalInt(item.Value)
			if err != nil {
				return dalCommand{}, fmt.Errorf("limit: %w", err)
			}
			command.limit = &limit
		case "skip":
			skip, err := dalInt(item.Value)
			if err != nil {
				return dalCommand{}, fmt.Errorf("skip: %w", err)
			}
			command.skip = &skip
		case "allowAll":
			value, ok := item.Value.(bool)
			if !ok {
				return dalCommand{}, fmt.Errorf("allowAll 必须是 bool")
			}
			command.allowAll = value
		case "options":
			value, err := dalDocument(item.Value)
			if err != nil {
				return dalCommand{}, fmt.Errorf("options: %w", err)
			}
			command.options = value
			keys := make(map[string]bool, len(value))
			for _, option := range value {
				if keys[option.Key] {
					return dalCommand{}, fmt.Errorf("重复 options.%s", option.Key)
				}
				keys[option.Key] = true
			}
		case "operations":
			value, ok := item.Value.(bson.A)
			if !ok {
				return dalCommand{}, fmt.Errorf("operations 必须是数组")
			}
			command.operations = value
		case "invalidates":
			value, ok := item.Value.(bson.A)
			if !ok {
				return dalCommand{}, fmt.Errorf("invalidates 必须是集合名数组")
			}
			for _, name := range value {
				text, ok := name.(string)
				if !ok || !validDALCollection(text) {
					return dalCommand{}, fmt.Errorf("invalidates 包含无效集合名")
				}
				command.invalidates = append(command.invalidates, text)
			}
		default:
			return dalCommand{}, fmt.Errorf("不支持的查询文件属性 %s", item.Key)
		}
	}
	if !validDALCollection(command.collection) {
		return dalCommand{}, fmt.Errorf("查询文件 collection 为空、无效或使用保留名称")
	}
	switch command.op {
	case "update", "updateOne", "updateMany", "delete", "deleteOne", "deleteMany":
		if len(command.filter) == 0 && !command.allowAll {
			return dalCommand{}, fmt.Errorf("写操作必须提供 filter，或在文件中设置 allowAll")
		}
	case "find", "findOne", "count", "aggregate", "insertOne", "watch", "bulkWrite":
	default:
		return dalCommand{}, fmt.Errorf("不支持的操作 %s", command.op)
	}
	return command, nil
}

func substituteDAL(value any, positional *[]any, named map[string]any) (any, error) {
	switch typed := value.(type) {
	case bson.D:
		out := make(bson.D, len(typed))
		for i, item := range typed {
			next, err := substituteDAL(item.Value, positional, named)
			if err != nil {
				return nil, err
			}
			out[i] = bson.E{Key: item.Key, Value: next}
		}
		return out, nil
	case bson.A:
		out := make(bson.A, len(typed))
		for i, item := range typed {
			next, err := substituteDAL(item, positional, named)
			if err != nil {
				return nil, err
			}
			out[i] = next
		}
		return out, nil
	case string:
		if typed == "?" {
			if named != nil {
				return nil, fmt.Errorf("命名参数文件不能使用 ?")
			}
			if len(*positional) == 0 {
				return nil, fmt.Errorf("缺少位置参数")
			}
			next := (*positional)[0]
			*positional = (*positional)[1:]
			return next, nil
		}
		if strings.HasPrefix(typed, "@") && len(typed) > 1 {
			if named == nil {
				return nil, fmt.Errorf("位置参数文件不能使用 %s", typed)
			}
			next, ok := named[typed[1:]]
			if !ok {
				return nil, fmt.Errorf("缺少命名参数 %s", typed[1:])
			}
			return next, nil
		}
		return typed, nil
	default:
		return value, nil
	}
}

func dalDocument(value any) (bson.D, error) {
	switch typed := value.(type) {
	case nil:
		return nil, nil
	case bson.D:
		return typed, nil
	default:
		raw, err := bson.Marshal(typed)
		if err != nil {
			return nil, err
		}
		var doc bson.D
		if err = bson.Unmarshal(raw, &doc); err != nil {
			return nil, err
		}
		return doc, nil
	}
}

func dalInt(value any) (int64, error) {
	switch typed := value.(type) {
	case int:
		return int64(typed), nil
	case int32:
		return int64(typed), nil
	case int64:
		return typed, nil
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) || math.Trunc(typed) != typed || typed < -0x1p63 || typed >= 0x1p63 {
			return 0, fmt.Errorf("需要 int64 范围内的整数")
		}
		return int64(typed), nil
	default:
		return 0, fmt.Errorf("需要整数，实际 %T", value)
	}
}

func countFileName(file string) string {
	slash := strings.LastIndex(file, "/")
	if slash < 0 {
		return "count_" + file
	}
	return file[:slash+1] + "count_" + file[slash+1:]
}

func (d *DAL) run(ctx context.Context, file string, args []any, fn func(context.Context) error) error {
	if binding, ok := ctx.Value(dalDatabaseKey{}).(dalDatabaseBinding); !ok || binding.dal != d {
		ctx = context.WithValue(ctx, dalDatabaseKey{}, dalDatabaseBinding{dal: d, selection: &dalDatabaseSelection{}})
	}
	for _, hook := range d.hooks {
		hook.Before(ctx, file, args)
	}
	start := time.Now()
	err := fn(ctx)
	cost := time.Since(start)
	for _, hook := range d.hooks {
		hook.After(ctx, file, args, cost, err)
	}
	if d.debug {
		log.Printf("dal file=%s cost=%s args=%d failed=%t", file, cost, len(args), err != nil)
	}
	return err
}

func validDALCollection(name string) bool {
	return strings.TrimSpace(name) != "" && !strings.ContainsRune(name, '\x00') && !settings.ReservedCollection(name)
}
