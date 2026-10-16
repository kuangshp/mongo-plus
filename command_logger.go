package mongoplus

import (
	"context"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
)

type commandLogKey struct {
	connection string
	request    int64
}

type commandLogEntry struct {
	caller, command string
}

// NewCommandLogger 创建官方驱动的命令监控器；通过 ClientOptions.SetMonitor 安装。
// Connect 根据 Config.Log 自动安装。已创建的 Client 无法补装，需在连接前配置。
// 同一个监控器可以供多个 Client 使用；默认关闭，不替换 Database.Observer。
func NewCommandLogger(cfg LogConfig) (*event.CommandMonitor, error) {
	if cfg.Format != "" && cfg.Format != "shell" && cfg.Format != "json" {
		return nil, fmt.Errorf("不支持的命令日志格式 %q，可选 shell、json", cfg.Format)
	}
	if cfg.Replayable && (cfg.Format == "json" || cfg.HideParameters) {
		return nil, fmt.Errorf("可复制执行日志需要 format=shell 且 hideParameters=false")
	}
	switch cfg.Level {
	case "", LogSilent, LogError, LogWarn, LogInfo:
	default:
		return nil, fmt.Errorf("不支持的命令日志级别 %q，可选 silent、error、warn、info", cfg.Level)
	}
	if cfg.SlowThresholdMS < 0 || cfg.SlowThresholdMS > int64((1<<63-1)/time.Millisecond) {
		return nil, fmt.Errorf("slowThresholdMs 必须为可表示的非负毫秒数")
	}
	if cfg.MaxLength < 0 || cfg.MaxLength > 1<<20 || cfg.MaxLength > 0 && cfg.MaxLength < 128 {
		return nil, fmt.Errorf("日志 maxLength 必须为 0 或 128 到 1048576 字节")
	}
	if cfg.MaxLength == 0 {
		cfg.MaxLength = 4096
	}
	cfg.SensitiveFields = append([]string(nil), cfg.SensitiveFields...)
	if cfg.Level == "" || cfg.Level == LogSilent {
		return nil, nil
	}
	if cfg.SlowThresholdMS == 0 {
		cfg.SlowThresholdMS = 200
	}
	if cfg.Writer == nil {
		cfg.Writer = os.Stdout
	}
	logger := log.New(cfg.Writer, "", log.LstdFlags)
	var pending sync.Map
	finish := func(e event.CommandFinishedEvent, reply bson.Raw, err error) {
		key := commandLogKey{e.ConnectionID, e.RequestID}
		value, ok := pending.LoadAndDelete(key)
		if !ok {
			return
		}
		entry := value.(commandLogEntry)
		if err == nil {
			err = commandReplyError(reply)
		}
		slow := e.Duration >= time.Duration(cfg.SlowThresholdMS)*time.Millisecond
		if cfg.Level == LogError && err == nil || cfg.Level == LogWarn && err == nil && !slow {
			return
		}
		status, color := "", "\x1b[32m"
		if slow {
			status, color = " [SLOW]", "\x1b[33m"
		}
		if err != nil {
			detail := "command failed"
			if cfg.IncludeErrorDetails {
				detail = truncateCommand(singleLine(err.Error()), min(cfg.MaxLength, 1024))
			}
			status, color = " [ERROR] "+detail, "\x1b[31m"
		}
		metrics := fmt.Sprintf("[%.3fms] [rows:%s]%s", float64(e.Duration)/float64(time.Millisecond), commandRows(reply), status)
		if cfg.Colorful {
			metrics = color + metrics + "\x1b[0m"
		}
		// 路径不插入 ANSI 转义符，供终端/IDE 按 file:line 识别跳转。
		logger.Printf("%s\n%s %s", entry.caller, metrics, entry.command)
	}
	return &event.CommandMonitor{
		Started: func(ctx context.Context, e *event.CommandStartedEvent) {
			if !loggedCommand(e.CommandName) {
				return
			}
			caller, _ := ctx.Value(commandCallerKey{}).(string)
			if caller == "" {
				caller = commandCaller()
			}
			var command string
			if cfg.Format == "json" {
				command = singleLine(e.DatabaseName) + "." + e.CommandName + " " + safeCommand(e.Command, cfg)
			} else {
				// 多数据源下仍标注实际数据库；db 始终代表这次命令所在数据库。
				command = "[db:" + singleLine(e.DatabaseName) + "]\n" + shellCommand(e.Command, cfg)
				if !cfg.Replayable {
					command = "[parameters:redacted] " + command
				}
			}
			pending.Store(commandLogKey{e.ConnectionID, e.RequestID}, commandLogEntry{caller: caller, command: command})
		},
		Succeeded: func(_ context.Context, e *event.CommandSucceededEvent) {
			finish(e.CommandFinishedEvent, e.Reply, nil)
		},
		Failed: func(_ context.Context, e *event.CommandFailedEvent) {
			finish(e.CommandFinishedEvent, nil, e.Failure)
		},
	}, nil
}

func loggedCommand(name string) bool {
	switch name {
	case "find", "getMore", "aggregate", "insert", "update", "delete", "findAndModify", "count", "distinct", "bulkWrite", "commitTransaction", "abortTransaction":
		return true
	}
	return false
}

func commandCaller() string {
	var pcs [64]uintptr
	n := runtime.Callers(2, pcs[:])
	frames := runtime.CallersFrames(pcs[:n])
	for {
		frame, more := frames.Next()
		if commandBusinessFrame(frame) {
			return fmt.Sprintf("%s:%d", frame.File, frame.Line)
		}
		if !more {
			break
		}
	}
	return "unknown:0"
}

type commandCallerKey struct{}

// 读缓存的 singleflight 在新 goroutine 中执行命令，提前保存业务入口以保留调用位置。
func cachedCommandContext(ctx context.Context, o QueryOption) context.Context {
	if o.cacheTTL <= 0 && !o.singleFlight {
		return ctx
	}
	return context.WithValue(ctx, commandCallerKey{}, commandCaller())
}

func commandBusinessFrame(f runtime.Frame) bool {
	if f.File == "" || strings.HasSuffix(f.File, "_gen.go") {
		return false
	}
	if strings.HasPrefix(f.Function, "go.mongodb.org/mongo-driver/") || strings.HasPrefix(f.Function, "golang.org/x/sync/") || strings.HasPrefix(f.File, filepath.Join(runtime.GOROOT(), "src")+string(filepath.Separator)) {
		return false
	}
	const module = "github.com/kuangshp/mongo-plus"
	// 跳过本库执行器，但保留业务示例与测试的调用位置。
	if !strings.HasSuffix(f.File, "_test.go") && (strings.HasPrefix(f.Function, module+".") || strings.HasPrefix(f.Function, module+"/internal/")) {
		return false
	}
	return true
}

func commandRows(reply bson.Raw) string {
	if n, ok := reply.Lookup("n").AsInt64OK(); ok {
		return fmt.Sprint(n)
	}
	if n, ok := reply.Lookup("lastErrorObject", "n").AsInt64OK(); ok {
		return fmt.Sprint(n)
	}
	for _, name := range []string{"firstBatch", "nextBatch"} {
		if batch, ok := reply.Lookup("cursor", name).ArrayOK(); ok {
			if values, err := batch.Values(); err == nil {
				return fmt.Sprint(len(values))
			}
		}
	}
	if values, ok := reply.Lookup("values").ArrayOK(); ok {
		if items, err := values.Values(); err == nil {
			return fmt.Sprint(len(items))
		}
	}
	if v, err := reply.LookupErr("value"); err == nil {
		if v.Type == bson.TypeNull {
			return "0"
		}
		if v.Type == bson.TypeEmbeddedDocument {
			return "1"
		}
	}
	return "-"
}

// MongoDB 的命令成功事件仍可能带有单条写入错误或写确认错误。
func commandReplyError(reply bson.Raw) error {
	if failures, ok := reply.Lookup("writeErrors").ArrayOK(); ok {
		if values, err := failures.Values(); err == nil && len(values) > 0 {
			return fmt.Errorf("writeErrors: %s", failures.String())
		}
	}
	if failure, ok := reply.Lookup("writeConcernError").DocumentOK(); ok {
		return fmt.Errorf("writeConcernError: %s", failure.String())
	}
	return nil
}

func singleLine(s string) string {
	return strings.NewReplacer("\r", "\\r", "\n", "\\n", "\x1b", "\\u001b").Replace(s)
}
