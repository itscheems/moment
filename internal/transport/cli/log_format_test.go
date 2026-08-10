package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

func TestJSONLogFormatKeepsStructuredDiagnosticsOnStderr(t *testing.T) {
	application := newTestApplication()
	registerFixture(application, "inspect-json-log", CapabilityBoth, func(_ context.Context, invocation Invocation, _ CommandInput) (Result, error) {
		invocation.LogEvent("info", "fixture_event", map[string]any{
			"method": "GET",
			"token":  "must-not-leak",
		})
		return Result{Data: map[string]bool{"ok": true}}, nil
	})

	output := execute(application, "", "fixture", "inspect-json-log", "--json", "--log-format", "json")
	if output.exit != ExitSuccess {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", output.exit, output.stdout, output.stderr)
	}
	decodeOneDocument(t, output.stdout)
	if strings.Contains(output.stderr, "must-not-leak") {
		t.Fatalf("secret leaked: %s", output.stderr)
	}
	var event map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(output.stderr)), &event); err != nil {
		t.Fatalf("stderr is not one JSON log: %v; %q", err, output.stderr)
	}
	if event["event"] != "fixture_event" || event["method"] != "GET" || event["token"] != "[REDACTED]" {
		t.Fatalf("event = %#v", event)
	}
}

func TestInvalidLogFormatFailsBeforeHandler(t *testing.T) {
	application := newTestApplication()
	called := false
	registerFixture(application, "invalid-log", CapabilityBoth, func(_ context.Context, _ Invocation, _ CommandInput) (Result, error) {
		called = true
		return Result{}, nil
	})
	output := execute(application, "", "fixture", "invalid-log", "--log-format", "yaml", "--json")
	if output.exit != ExitInvalidInput || called {
		t.Fatalf("exit = %d, called = %t, stdout = %q", output.exit, called, output.stdout)
	}
	if decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("stdout = %q", output.stdout)
	}
}
