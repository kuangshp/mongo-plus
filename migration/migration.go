// Package migration 提供独立于服务启动和代码生成的生产迁移管理。
// MongoDB DDL 不具备整体事务：本包记录版本、校验和、失败与回滚状态，
// 通过数据库级互斥锁串行执行；业务负责幂等回填及新旧版本兼容。
package migration

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync/atomic"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"go.mongodb.org/mongo-driver/v2/mongo/readconcern"
	"go.mongodb.org/mongo-driver/v2/mongo/readpref"
	"go.mongodb.org/mongo-driver/v2/mongo/writeconcern"
)

const (
	HistoryCollection = "__mongo_plus_migrations"
	LockCollection    = "__mongo_plus_migration_locks"
)

var (
	ErrLocked           = errors.New("数据库迁移正在执行或遗留锁尚未人工恢复")
	ErrChecksumMismatch = errors.New("已登记迁移的校验和与当前源码不一致")
	ErrIncomplete       = errors.New("迁移未完成，请检查部分执行结果并显式选择恢复")
	ErrInvalidOrder     = errors.New("迁移版本与已应用历史不兼容")
	ErrNoRollback       = errors.New("迁移没有显式 Down，不能自动回滚")
	ErrLockLost         = errors.New("迁移锁已被更改，拒绝释放其他执行者的锁")
)

// Migration 按 Version 字符串升序执行，推荐 YYYYMMDDHHMMSS_描述。
// Checksum 必须来自受版本控制的迁移源码，而不是函数指针或运行时间。
// Up 必须可安全重入；Down 显式定义恢复动作，不能假定数据和 DDL 自动回滚。
type Migration struct {
	Version     string
	Description string
	Checksum    string
	Up          func(context.Context, *mongo.Database) error
	Down        func(context.Context, *mongo.Database) error
}

// Options 的 ResumeFailed 只在人工确认失败迁移可重入后开启。
type Options struct{ ResumeFailed bool }

// Record 是持久化历史；失败记录保留，后续恢复会增加 Attempt。
type Record struct {
	Version     string     `bson:"_id" json:"version"`
	Description string     `bson:"description" json:"description"`
	Checksum    string     `bson:"checksum" json:"checksum"`
	Status      string     `bson:"status" json:"status"`
	Attempt     int64      `bson:"attempt" json:"attempt"`
	StartedAt   time.Time  `bson:"started_at" json:"startedAt"`
	FinishedAt  *time.Time `bson:"finished_at" json:"finishedAt,omitempty"`
	Error       string     `bson:"error" json:"error,omitempty"`
}

// LockInfo 不设置自动到期。避免长时间建索引或进程暂停时，第二执行者抢占锁。
// 崩溃恢复必须确认原进程已退出，再使用完整 Owner 值执行 Unlock。
type LockInfo struct {
	ID        string    `bson:"_id" json:"id"`
	Owner     string    `bson:"owner" json:"owner"`
	CreatedAt time.Time `bson:"created_at" json:"createdAt"`
}

type lockContextKey struct{}
type lockIdentity struct {
	client   *mongo.Client
	database string
}
type lockScope struct{ active atomic.Bool }

func attachLock(ctx context.Context, db *mongo.Database) (context.Context, *lockScope) {
	previous, _ := ctx.Value(lockContextKey{}).(map[lockIdentity]*lockScope)
	scopes := make(map[lockIdentity]*lockScope, len(previous)+1)
	for identity, scope := range previous {
		scopes[identity] = scope
	}
	scope := &lockScope{}
	scope.active.Store(true)
	scopes[lockIdentity{db.Client(), db.Name()}] = scope
	return context.WithValue(ctx, lockContextKey{}, scopes), scope
}

// WithLock 让旧 schema.Apply 等入口参与同一个数据库迁移锁。
// 在 Run/Down 回调中沿用传入的 Context 可复用锁；回调外保存的 Context 不可再用。
// 它只串行执行，不记录版本，生产发布仍应使用 Run。
func WithLock(ctx context.Context, db *mongo.Database, fn func(context.Context) error) (err error) {
	if ctx == nil || db == nil || fn == nil {
		return fmt.Errorf("迁移锁需要上下文、数据库和回调")
	}
	if mongo.SessionFromContext(ctx) != nil {
		return fmt.Errorf("结构迁移不能嵌入业务 Session")
	}
	identity := lockIdentity{db.Client(), db.Name()}
	if scopes, _ := ctx.Value(lockContextKey{}).(map[lockIdentity]*lockScope); scopes[identity] != nil {
		if !scopes[identity].active.Load() {
			return ErrLockLost
		}
		return fn(ctx)
	}
	owner, err := acquire(ctx, db)
	if err != nil {
		return err
	}
	ctx, scope := attachLock(ctx, db)
	defer func() {
		scope.active.Store(false)
		closeCtx, cancel := cleanupContext(ctx)
		defer cancel()
		err = errors.Join(err, Unlock(closeCtx, db, owner))
	}()
	return fn(ctx)
}

func Checksum(source []byte) string {
	sum := sha256.Sum256(source)
	return hex.EncodeToString(sum[:])
}

func metadata(db *mongo.Database, name string) *mongo.Collection {
	return db.Collection(name, options.Collection().SetWriteConcern(writeconcern.Majority()).SetReadConcern(readconcern.Majority()).SetReadPreference(readpref.Primary()))
}

func validate(db *mongo.Database, migrations []Migration) ([]Migration, error) {
	if db == nil {
		return nil, fmt.Errorf("迁移数据库不能为空")
	}
	ordered := append([]Migration(nil), migrations...)
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Version < ordered[j].Version })
	versionPattern := regexp.MustCompile(`^[0-9A-Za-z][0-9A-Za-z_\-]{0,127}$`)
	for i, m := range ordered {
		checksum, err := hex.DecodeString(m.Checksum)
		if !versionPattern.MatchString(m.Version) || m.Up == nil || err != nil || len(checksum) != 32 || hex.EncodeToString(checksum) != m.Checksum {
			return nil, fmt.Errorf("迁移 %q 需要合法版本、Up 和小写 SHA256 校验和", m.Version)
		}
		if i > 0 && ordered[i-1].Version == m.Version {
			return nil, fmt.Errorf("迁移版本重复: %s", m.Version)
		}
	}
	return ordered, nil
}

// History 按版本升序读取持久化历史，不执行迁移。
func History(ctx context.Context, db *mongo.Database) ([]Record, error) {
	if db == nil {
		return nil, fmt.Errorf("迁移数据库不能为空")
	}
	cursor, err := metadata(db, HistoryCollection).Find(ctx, bson.D{}, options.Find().SetSort(bson.D{{Key: "_id", Value: 1}}))
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)
	result := []Record{}
	err = cursor.All(ctx, &result)
	return result, err
}

// Plan 检查历史、校验和和执行顺序，返回尚未应用的版本，不修改数据库。
// 运行期间历史可能变化；Run 会在取得独占锁后重新检查。
func Plan(ctx context.Context, db *mongo.Database, migrations []Migration) ([]Migration, error) {
	ordered, err := validate(db, migrations)
	if err != nil {
		return nil, err
	}
	history, err := History(ctx, db)
	if err != nil {
		return nil, err
	}
	byVersion := map[string]Record{}
	known := map[string]bool{}
	for _, m := range ordered {
		known[m.Version] = true
	}
	for _, record := range history {
		if !known[record.Version] && record.Status != "rolled_back" {
			return nil, fmt.Errorf("%w: 未注册历史版本 %s", ErrInvalidOrder, record.Version)
		}
		byVersion[record.Version] = record
	}
	pending := []Migration{}
	gap := false
	for _, m := range ordered {
		record, exists := byVersion[m.Version]
		if exists && record.Checksum != m.Checksum {
			return nil, fmt.Errorf("%w: %s", ErrChecksumMismatch, m.Version)
		}
		if exists && record.Status == "applied" {
			if gap {
				return nil, fmt.Errorf("%w: 不能在 %s 之前追加旧迁移", ErrInvalidOrder, m.Version)
			}
			continue
		}
		gap = true
		pending = append(pending, m)
	}
	return pending, nil
}

// CurrentLock 返回锁持有者；无锁时返回 (nil,nil)。
func CurrentLock(ctx context.Context, db *mongo.Database) (*LockInfo, error) {
	if db == nil {
		return nil, fmt.Errorf("迁移数据库不能为空")
	}
	var lock LockInfo
	err := metadata(db, LockCollection).FindOne(ctx, bson.D{{Key: "_id", Value: "global"}}).Decode(&lock)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, nil
	}
	return &lock, err
}

func acquire(ctx context.Context, db *mongo.Database) (string, error) {
	owner := bson.NewObjectID().Hex()
	_, err := metadata(db, LockCollection).InsertOne(ctx, LockInfo{ID: "global", Owner: owner, CreatedAt: time.Now().UTC()})
	if mongo.IsDuplicateKeyError(err) {
		lock, readErr := CurrentLock(ctx, db)
		if readErr == nil && lock != nil {
			return "", fmt.Errorf("%w: owner=%s createdAt=%s", ErrLocked, lock.Owner, lock.CreatedAt.Format(time.RFC3339))
		}
		return "", ErrLocked
	}
	return owner, err
}

// Unlock 仅用于已确认原迁移进程停止后的人工恢复，不允许无条件删除锁。
func Unlock(ctx context.Context, db *mongo.Database, owner string) error {
	if db == nil || owner == "" {
		return fmt.Errorf("数据库和锁 Owner 不能为空")
	}
	result, err := metadata(db, LockCollection).DeleteOne(ctx, bson.D{{Key: "_id", Value: "global"}, {Key: "owner", Value: owner}})
	if err != nil {
		return err
	}
	if result.DeletedCount != 1 {
		return ErrLockLost
	}
	return nil
}

func cleanupContext(ctx context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
}

// Run 在独占锁下执行未应用的迁移；已应用版本按校验和验证后跳过。
// 失败/进程崩溃后不自动重跑；确认 Up 幂等及部分结果后传 ResumeFailed。
func Run(ctx context.Context, db *mongo.Database, migrations []Migration, opts Options) (err error) {
	if ctx == nil {
		return fmt.Errorf("迁移上下文不能为空")
	}
	if mongo.SessionFromContext(ctx) != nil {
		return fmt.Errorf("迁移必须使用独立 Context，不能嵌入业务 Session")
	}
	if _, err = validate(db, migrations); err != nil {
		return err
	}
	owner, err := acquire(ctx, db)
	if err != nil {
		return err
	}
	ctx, scope := attachLock(ctx, db)
	defer func() {
		scope.active.Store(false)
		closeCtx, cancel := cleanupContext(ctx)
		defer cancel()
		err = errors.Join(err, Unlock(closeCtx, db, owner))
	}()
	pending, err := Plan(ctx, db, migrations)
	if err != nil {
		return err
	}
	for _, m := range pending {
		var previous Record
		readErr := metadata(db, HistoryCollection).FindOne(ctx, bson.D{{Key: "_id", Value: m.Version}}).Decode(&previous)
		if readErr != nil && !errors.Is(readErr, mongo.ErrNoDocuments) {
			return readErr
		}
		if readErr == nil && previous.Status != "rolled_back" && !opts.ResumeFailed {
			return fmt.Errorf("%w: %s (%s)", ErrIncomplete, m.Version, previous.Status)
		}
		if readErr == nil && previous.Status != "rolled_back" && previous.Status != "running" && previous.Status != "failed" {
			return fmt.Errorf("%w: %s 的状态为 %s，不能执行 Up", ErrIncomplete, m.Version, previous.Status)
		}
		if err = execute(ctx, db, m, "running", "applied", m.Up); err != nil {
			return err
		}
	}
	return nil
}

// Rollback 只允许回滚最后一个已应用版本，且必须提供相同校验和与显式 Down。
// Down 失败保留 rollback_failed；恢复该回滚需设置 ResumeFailed。
func Rollback(ctx context.Context, db *mongo.Database, migrations []Migration, version string, opts Options) (err error) {
	if ctx == nil {
		return fmt.Errorf("迁移上下文不能为空")
	}
	if mongo.SessionFromContext(ctx) != nil {
		return fmt.Errorf("回滚迁移不能嵌入业务 Session")
	}
	ordered, err := validate(db, migrations)
	if err != nil {
		return err
	}
	owner, err := acquire(ctx, db)
	if err != nil {
		return err
	}
	ctx, scope := attachLock(ctx, db)
	defer func() {
		scope.active.Store(false)
		closeCtx, cancel := cleanupContext(ctx)
		defer cancel()
		err = errors.Join(err, Unlock(closeCtx, db, owner))
	}()
	if _, err = Plan(ctx, db, ordered); err != nil {
		return err
	}
	history, err := History(ctx, db)
	if err != nil {
		return err
	}
	var current *Record
	for i := range history {
		if history[i].Status != "rolled_back" {
			current = &history[i]
		}
	}
	if current == nil || current.Version != version {
		return ErrInvalidOrder
	}
	if current.Status != "applied" && !(opts.ResumeFailed && (current.Status == "rolling_back" || current.Status == "rollback_failed")) {
		return ErrIncomplete
	}
	for _, m := range ordered {
		if m.Version == version {
			if m.Checksum != current.Checksum {
				return ErrChecksumMismatch
			}
			if m.Down == nil {
				return ErrNoRollback
			}
			return execute(ctx, db, m, "rolling_back", "rolled_back", m.Down)
		}
	}
	return ErrInvalidOrder
}

func execute(ctx context.Context, db *mongo.Database, m Migration, running, done string, fn func(context.Context, *mongo.Database) error) (err error) {
	history := metadata(db, HistoryCollection)
	_, err = history.UpdateOne(ctx, bson.D{{Key: "_id", Value: m.Version}}, bson.D{
		{Key: "$set", Value: bson.D{{Key: "description", Value: m.Description}, {Key: "checksum", Value: m.Checksum}, {Key: "status", Value: running}, {Key: "started_at", Value: time.Now().UTC()}, {Key: "finished_at", Value: nil}, {Key: "error", Value: ""}}},
		{Key: "$inc", Value: bson.D{{Key: "attempt", Value: 1}}},
	}, options.UpdateOne().SetUpsert(true))
	if err != nil {
		return err
	}
	defer func() {
		panicValue := recover()
		if panicValue != nil {
			err = fmt.Errorf("迁移回调 panic: %v", panicValue)
		}
		status, message := done, ""
		if err != nil {
			status = "failed"
			if running == "rolling_back" {
				status = "rollback_failed"
			}
			message = err.Error()
			if len(message) > 2048 {
				message = message[:2048]
			}
		}
		closeCtx, cancel := cleanupContext(ctx)
		defer cancel()
		_, saveErr := history.UpdateOne(closeCtx, bson.D{{Key: "_id", Value: m.Version}}, bson.D{{Key: "$set", Value: bson.D{{Key: "status", Value: status}, {Key: "finished_at", Value: time.Now().UTC()}, {Key: "error", Value: message}}}})
		err = errors.Join(err, saveErr)
		if panicValue != nil {
			panic(panicValue)
		}
	}()
	err = fn(ctx, db)
	return err
}
