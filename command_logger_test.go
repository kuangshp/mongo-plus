package mongoplus

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
	"gopkg.in/yaml.v3"
)

func logRaw(t *testing.T, value any) bson.Raw {
	t.Helper()
	raw, err := bson.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestCommandLoggerLevelsAndWriteErrors(t *testing.T) {
	for _, tc := range []struct {
		name         string
		level        LogLevel
		duration     time.Duration
		failure      error
		reply        bson.M
		want, marker string
	}{
		{"info", LogInfo, time.Millisecond, nil, bson.M{"n": 2}, "rows:2", ""},
		{"warn-fast", LogWarn, time.Millisecond, nil, bson.M{"n": 2}, "", ""},
		{"warn-slow", LogWarn, 200 * time.Millisecond, nil, bson.M{"n": 2}, "rows:2", "SLOW"},
		{"error-success", LogError, time.Second, nil, bson.M{"n": 2}, "", ""},
		{"error-failed", LogError, time.Millisecond, errors.New("network failure"), nil, "network failure", "ERROR"},
		{"write-error", LogError, time.Millisecond, nil, bson.M{"n": 0, "writeErrors": bson.A{bson.M{"code": 11000, "errmsg": "duplicate key"}}}, "duplicate key", "ERROR"},
		{"write-concern", LogWarn, time.Millisecond, nil, bson.M{"n": 1, "writeConcernError": bson.M{"errmsg": "confirmation timeout"}}, "confirmation timeout", "ERROR"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var out bytes.Buffer
			monitor, err := NewCommandLogger(LogConfig{Level: tc.level, Writer: &out, IncludeErrorDetails: true})
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			monitor.Started(ctx, &event.CommandStartedEvent{CommandName: "update", DatabaseName: "shop", ConnectionID: "server", RequestID: 1, Command: logRaw(t, bson.M{"update": "users", "updates": bson.A{bson.M{"q": bson.M{"name": "alice"}}}})})
			finish := event.CommandFinishedEvent{CommandName: "update", DatabaseName: "shop", ConnectionID: "server", RequestID: 1, Duration: tc.duration}
			if tc.failure != nil {
				monitor.Failed(ctx, &event.CommandFailedEvent{CommandFinishedEvent: finish, Failure: tc.failure})
			} else {
				monitor.Succeeded(ctx, &event.CommandSucceededEvent{CommandFinishedEvent: finish, Reply: logRaw(t, tc.reply)})
			}
			got := out.String()
			if tc.want == "" {
				if got != "" {
					t.Fatalf("unexpected log: %s", got)
				}
			} else if !strings.Contains(got, tc.want) || !strings.Contains(got, tc.marker) || !strings.Contains(got, "shop.update") || !strings.Contains(got, "alice") {
				t.Fatalf("incomplete log: %s", got)
			}
		})
	}
}

func TestCommandLoggerCallerAndConcurrentRequests(t *testing.T) {
	var out bytes.Buffer
	monitor, err := NewCommandLogger(LogConfig{Level: LogInfo, Writer: &out, Colorful: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	raw := logRaw(t, bson.M{"find": "users"})
	reply := logRaw(t, bson.M{"cursor": bson.M{"firstBatch": bson.A{bson.M{"id": 1}}}})
	_, file, line, _ := runtime.Caller(0)
	monitor.Started(ctx, &event.CommandStartedEvent{CommandName: "find", Command: raw, RequestID: 1})
	monitor.Succeeded(ctx, &event.CommandSucceededEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "find", RequestID: 1, Duration: time.Millisecond}, Reply: reply})
	if !strings.Contains(out.String(), fmt.Sprintf("%s:%d\n", file, line+1)) || !strings.Contains(out.String(), "[1.000ms] [rows:1]") || !strings.Contains(out.String(), "\x1b[") {
		t.Fatalf("caller/metrics/color missing: %s", out.String())
	}
	out.Reset()
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			conn := fmt.Sprint(i)
			monitor.Started(ctx, &event.CommandStartedEvent{CommandName: "find", DatabaseName: "db" + conn, Command: raw, ConnectionID: conn, RequestID: 1})
			monitor.Succeeded(ctx, &event.CommandSucceededEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "find", ConnectionID: conn, RequestID: 1}, Reply: reply})
		}(i)
	}
	wg.Wait()
	if got := strings.Count(out.String(), "[rows:1]"); got != 50 {
		t.Fatalf("lost concurrent commands: %d", got)
	}
	for i := 0; i < 50; i++ {
		if strings.Count(out.String(), fmt.Sprintf(" db%d.find ", i)) != 1 {
			t.Fatalf("mismatched request %d: %s", i, out.String())
		}
	}
}

func TestCommandLoggerConfiguration(t *testing.T) {
	for _, cfg := range []LogConfig{{}, {Level: LogSilent}} {
		monitor, err := NewCommandLogger(cfg)
		if err != nil || monitor != nil {
			t.Fatalf("default must be silent: %v %v", monitor, err)
		}
	}
	for _, cfg := range []LogConfig{{Level: "debug"}, {SlowThresholdMS: -1}, {SlowThresholdMS: 1<<63 - 1}} {
		if _, err := NewCommandLogger(cfg); err == nil {
			t.Fatalf("invalid config accepted: %+v", cfg)
		}
	}
	var cfg Config
	if err := yaml.Unmarshal([]byte("uri: mongodb://localhost\ndatabase: shop\nlog:\n  level: info\n  slowThresholdMs: 500\n  colorful: true\n"), &cfg); err != nil {
		t.Fatal(err)
	}
	if cfg.Log.Level != LogInfo || cfg.Log.SlowThresholdMS != 500 || !cfg.Log.Colorful {
		t.Fatalf("YAML config: %+v", cfg.Log)
	}
}

func TestCommandLoggerSkipsAuthenticationAndUnknownRequests(t *testing.T) {
	var out bytes.Buffer
	monitor, err := NewCommandLogger(LogConfig{Level: LogInfo, Writer: &out})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	monitor.Started(ctx, &event.CommandStartedEvent{CommandName: "saslStart", Command: logRaw(t, bson.M{"password": "secret"}), RequestID: 1})
	monitor.Succeeded(ctx, &event.CommandSucceededEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "saslStart", RequestID: 1}})
	monitor.Failed(ctx, &event.CommandFailedEvent{CommandFinishedEvent: event.CommandFinishedEvent{CommandName: "find", RequestID: 2}, Failure: errors.New("unmatched")})
	if out.Len() != 0 {
		t.Fatalf("unexpected log: %s", &out)
	}
}

func TestCommandRows(t *testing.T) {
	for _, tc := range []struct {
		reply bson.M
		want  string
	}{
		{bson.M{"cursor": bson.M{"nextBatch": bson.A{1, 2}}}, "2"},
		{bson.M{"cursor": bson.M{"firstBatch": bson.A{}}}, "0"},
		{bson.M{"values": bson.A{"a", "b", "c"}}, "3"},
		{bson.M{"value": nil}, "0"},
		{bson.M{"lastErrorObject": bson.M{"n": 1}, "value": nil}, "1"},
		{bson.M{"value": bson.M{"id": 1}}, "1"},
		{bson.M{"ok": 1}, "-"},
	} {
		if got := commandRows(logRaw(t, tc.reply)); got != tc.want {
			t.Fatalf("%v: got %s want %s", tc.reply, got, tc.want)
		}
	}
}
