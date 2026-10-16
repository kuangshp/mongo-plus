package mongoplus

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/event"
)

func TestCommandRedactionAndLimits(t *testing.T) {
	raw := logRaw(t, bson.D{{Key: "find", Value: "users"}, {Key: "filter", Value: bson.D{
		{Key: "password", Value: "secret-pass"}, {Key: "email", Value: "private@example.com"}, {Key: "profile", Value: bson.D{{Key: "nickname", Value: "private-name"}}}, {Key: "tags", Value: bson.A{bson.M{"mobile": "13800000000"}}}, {Key: "status", Value: 1},
	}}})
	cfg := LogConfig{SensitiveFields: []string{"profile.nickname"}, MaxLength: 4096}
	got := safeCommand(raw, cfg)
	for _, secret := range []string{"secret-pass", "private@example.com", "private-name", "13800000000"} {
		if strings.Contains(got, secret) {
			t.Errorf("敏感值泄露: %s", got)
		}
	}
	if !strings.Contains(got, "users") || !strings.Contains(got, "REDACTED") {
		t.Fatal(got)
	}
	cfg.HideParameters = true
	cfg.MaxLength = 128
	got = safeCommand(logRaw(t, bson.M{"find": "users", "filter": bson.M{"name": "business-value"}}), cfg)
	if strings.Contains(got, "business-value") || !strings.Contains(got, "users") {
		t.Fatal(got)
	}
	got = safeCommand(logRaw(t, bson.M{"find": "users", "comment": strings.Repeat("汉字", 1000)}), LogConfig{MaxLength: 128})
	if len(got) > 128 || !utf8.ValidString(got) || !strings.Contains(got, "truncated") {
		t.Fatalf("截断无效: %d %s", len(got), got)
	}
}

func TestCommandErrorDoesNotEchoSecretsByDefault(t *testing.T) {
	var out bytes.Buffer
	monitor, err := NewCommandLogger(LogConfig{Level: LogError, Writer: &out})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	monitor.Started(ctx, &event.CommandStartedEvent{CommandName: "insert", RequestID: 1, Command: logRaw(t, bson.M{"insert": "users", "documents": bson.A{bson.M{"password": "secret-pass"}}})})
	monitor.Failed(ctx, &event.CommandFailedEvent{CommandFinishedEvent: event.CommandFinishedEvent{RequestID: 1, CommandName: "insert"}, Failure: errors.New("duplicate key secret-pass")})
	if strings.Contains(out.String(), "secret-pass") || !strings.Contains(out.String(), "ERROR") {
		t.Fatal(out.String())
	}
}
