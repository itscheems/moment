package observability

import (
	"bytes"
	"encoding/json"
	"errors"
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

func TestJSONLoggerFailsClosedForTypedAndUnsupportedValues(t *testing.T) {
	type privatePayload struct {
		Token string
	}
	cyclic := map[string]any{}
	cyclic["next"] = cyclic
	var output bytes.Buffer
	logger, err := New(Config{Level: "info", Format: "json", Writer: &output})
	if err != nil {
		t.Fatal(err)
	}
	logger.Event("info", "typed_values", Fields{
		"typed_map": map[string]string{
			"method": "POST",
			"token":  "typed-map-secret",
		},
		"typed_slice": []map[string]any{{
			"route":  "/api/v1/posts",
			"cookie": "typed-slice-secret",
		}},
		"unsupported_struct":  privatePayload{Token: "struct-secret"},
		"unsupported_pointer": &privatePayload{Token: "pointer-secret"},
		"unsupported_error":   errors.New("error-secret"),
		"unsupported_bytes":   []byte("byte-secret"),
		"cyclic":              cyclic,
		"timestamp":           time.Date(2026, time.August, 10, 12, 0, 0, 0, time.FixedZone("offset", 8*60*60)),
	})

	for _, secret := range []string{"typed-map-secret", "typed-slice-secret", "struct-secret", "pointer-secret", "error-secret", "byte-secret"} {
		if strings.Contains(output.String(), secret) {
			t.Fatalf("sensitive value %q leaked: %s", secret, output.String())
		}
	}
	var event map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(output.Bytes()), &event); err != nil {
		t.Fatalf("decode event: %v; %q", err, output.String())
	}
	typedMap := event["typed_map"].(map[string]any)
	if typedMap["method"] != "POST" || typedMap["token"] != redactedValue {
		t.Fatalf("typed map = %#v", typedMap)
	}
	typedSlice := event["typed_slice"].([]any)[0].(map[string]any)
	if typedSlice["route"] != "/api/v1/posts" || typedSlice["cookie"] != redactedValue {
		t.Fatalf("typed slice = %#v", typedSlice)
	}
	for _, field := range []string{"unsupported_struct", "unsupported_pointer", "unsupported_error", "unsupported_bytes"} {
		if event[field] != redactedValue {
			t.Fatalf("%s = %#v", field, event[field])
		}
	}
	if event["timestamp"] != "2026-08-10T04:00:00Z" {
		t.Fatalf("timestamp = %#v", event["timestamp"])
	}
	if _, ok := event["cyclic"].(map[string]any); !ok {
		t.Fatalf("cyclic value was not safely traversed: %#v", event["cyclic"])
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
