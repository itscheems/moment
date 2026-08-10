package observability

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestJSONLoggerRedactsSensitiveFieldsAndFiltersLevels(t *testing.T) {
	var output bytes.Buffer
	logger, err := New(Config{
		Level:  "info",
		Format: "json",
		Writer: &output,
		Now: func() time.Time {
			return time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	logger.Event("debug", "hidden", Fields{"method": "GET"})
	logger.Event("info", "HTTP Request\n", Fields{
		"method":        "POST",
		"route":         "/api/v1/posts",
		"request_body":  "private post",
		"authorization": "Bearer owner-token",
		"nested": map[string]any{
			"cookie": "session-secret",
		},
		"items": []any{map[string]any{"token": "nested-token"}},
		"event": "caller-cannot-override",
	})

	if strings.Contains(output.String(), "private post") || strings.Contains(output.String(), "owner-token") || strings.Contains(output.String(), "session-secret") || strings.Contains(output.String(), "nested-token") || strings.Contains(output.String(), "caller-cannot-override") {
		t.Fatalf("sensitive value leaked: %s", output.String())
	}
	lines := strings.Split(strings.TrimSpace(output.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("lines = %d, output = %q", len(lines), output.String())
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &event); err != nil {
		t.Fatalf("decode event: %v", err)
	}
	if event["event"] != "http_request" || event["request_body"] != "[REDACTED]" {
		t.Fatalf("event = %#v", event)
	}
	nested := event["nested"].(map[string]any)
	if nested["cookie"] != "[REDACTED]" {
		t.Fatalf("nested = %#v", nested)
	}
}

func TestLoggerRejectsUnknownConfiguration(t *testing.T) {
	if _, err := New(Config{Level: "trace", Format: "text"}); err == nil {
		t.Fatal("expected invalid level error")
	}
	if _, err := New(Config{Level: "info", Format: "yaml"}); err == nil {
		t.Fatal("expected invalid format error")
	}
}
