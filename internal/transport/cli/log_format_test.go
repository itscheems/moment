package cli

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/moment/moment/internal/observability"
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

// A command a person typed is attributed to the terminal, and a long-running
// command can attribute an event to the entry point it is actually serving.
// `moment serve` depends on the second: it is one CLI invocation whose HTTP
// stack speaks for the browser, and recording those as cli would make the
// classification useless exactly where it is most wanted.
func TestStructuredEventsCarryTheSurfaceTheyCameFrom(t *testing.T) {
	application := newTestApplication()
	registerFixture(application, "surfaces", CapabilityBoth, func(_ context.Context, invocation Invocation, _ CommandInput) (Result, error) {
		invocation.LogEvent("info", "typed_command", map[string]any{"ok": true})
		invocation.LogEventOn(observability.SurfaceWeb, "info", "http_request", map[string]any{"ok": true})
		invocation.LogEventOn(observability.SurfaceSystem, "info", "server_started", map[string]any{"ok": true})
		return Result{Data: map[string]bool{"ok": true}}, nil
	})

	output := execute(application, "", "fixture", "surfaces", "--json", "--log-format", "json")
	if output.exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr = %q", output.exit, output.stderr)
	}
	got := map[string]string{}
	for _, line := range strings.Split(strings.TrimSpace(output.stderr), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("stderr line %q: %v", line, err)
		}
		got[event["event"].(string)], _ = event["surface"].(string)
	}
	want := map[string]string{
		"typed_command":  "cli",
		"http_request":   "web",
		"server_started": "system",
	}
	for event, surface := range want {
		if got[event] != surface {
			t.Fatalf("event %s carried surface %q, want %q (all: %#v)", event, got[event], surface, got)
		}
	}
}
