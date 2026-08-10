package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

type commandOutput struct {
	exit   ExitCode
	stdout string
	stderr string
}

func newTestApplication() *Application {
	baseTime := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	var clockCalls atomic.Int64
	application := New(Config{
		BinaryName: "moment",
		Short:      "Test Moment commands",
		Long:       "Test Moment commands.",
		Now: func() time.Time {
			return baseTime.Add(time.Duration(clockCalls.Add(1)-1) * 12 * time.Millisecond)
		},
		RequestID: func() string { return "req_test" },
	})
	application.MustRegisterGroup(GroupSpec{Name: "fixture", Short: "Exercise the CLI contract", Long: "Exercise the CLI contract."})
	return application
}

func registerFixture(application *Application, name string, capability Capability, run Handler) {
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"fixture", name},
		Short:      "Run a fixture command",
		Long:       "Run a deterministic fixture command.",
		Inputs:     "No positional input.",
		Output:     "A fixture result.",
		Errors:     "Stable fixture errors.",
		Example:    "moment fixture " + name + " --json",
		Capability: capability,
		Run:        run,
	})
}

func execute(application *Application, input string, args ...string) commandOutput {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exitCode := application.Execute(context.Background(), args, Streams{
		In:  strings.NewReader(input),
		Out: &stdout,
		Err: &stderr,
	})
	return commandOutput{exit: exitCode, stdout: stdout.String(), stderr: stderr.String()}
}

func decodeOneDocument(t *testing.T, document string) map[string]any {
	t.Helper()
	decoder := json.NewDecoder(strings.NewReader(document))
	var decoded map[string]any
	if err := decoder.Decode(&decoded); err != nil {
		t.Fatalf("decode stdout as JSON: %v\nstdout: %q", err, document)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		t.Fatalf("stdout contains more than one JSON document: %v\nstdout: %q", err, document)
	}
	return decoded
}

func TestJSONSuccessKeepsDiagnosticsOnStderr(t *testing.T) {
	application := newTestApplication()
	registerFixture(application, "inspect", CapabilityBoth, func(_ context.Context, invocation Invocation, _ CommandInput) (Result, error) {
		invocation.Log("debug", "fixture detail")
		return Result{Data: map[string]any{"mode": invocation.Mode()}, Meta: map[string]any{"replayed": false}}, nil
	})

	output := execute(application, "", "fixture", "inspect", "--json", "--log-level", "debug")
	if output.exit != ExitSuccess {
		t.Fatalf("exit = %d, stderr = %q", output.exit, output.stderr)
	}
	decoded := decodeOneDocument(t, output.stdout)
	if ok, _ := decoded["ok"].(bool); !ok {
		t.Fatalf("ok = %#v", decoded["ok"])
	}
	data := decoded["data"].(map[string]any)
	if data["mode"] != string(ModeDirect) {
		t.Fatalf("mode = %#v", data["mode"])
	}
	meta := decoded["meta"].(map[string]any)
	if meta["request_id"] != "req_test" || meta["duration_ms"] != float64(12) || meta["replayed"] != false {
		t.Fatalf("meta = %#v", meta)
	}
	if strings.Contains(output.stdout, "fixture detail") {
		t.Fatalf("debug log leaked to stdout: %q", output.stdout)
	}
	if output.stderr != "debug: fixture detail\n" {
		t.Fatalf("stderr = %q", output.stderr)
	}
}

func TestHandlerReceivesReadOnlyArgumentsAndFlags(t *testing.T) {
	application := newTestApplication()
	application.MustRegisterLeaf(LeafSpec{
		Path:      []string{"fixture", "echo"},
		ArgsUsage: "<value>",
		Short:     "Echo parsed fixture input",
		Long:      "Echo parsed fixture input.",
		Inputs:    "One value and an optional --label.",
		Output:    "The parsed input values.",
		Errors:    "Invalid arguments return INVALID_ARGUMENT.",
		Example:   "moment fixture echo value --label demo --json",
		Args:      cobra.ExactArgs(1),
		Flags: func(flags *pflag.FlagSet) {
			flags.String("label", "", "fixture label")
		},
		Capability: CapabilityBoth,
		Run: func(_ context.Context, _ Invocation, input CommandInput) (Result, error) {
			label, err := input.String("label")
			if err != nil {
				return Result{}, err
			}
			return Result{Data: map[string]any{
				"args":          input.Args(),
				"label":         label,
				"label_changed": input.Changed("label"),
			}}, nil
		},
	})

	output := execute(application, "", "fixture", "echo", "value", "--label", "demo", "--json")
	if output.exit != ExitSuccess {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", output.exit, output.stdout, output.stderr)
	}
	data := decodeOneDocument(t, output.stdout)["data"].(map[string]any)
	if data["label"] != "demo" || data["label_changed"] != true {
		t.Fatalf("data = %#v", data)
	}
	args := data["args"].([]any)
	if len(args) != 1 || args[0] != "value" {
		t.Fatalf("args = %#v", args)
	}
}

func TestStableErrorAndExitMapping(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantCode string
		wantExit ExitCode
	}{
		{name: "not found", err: NewCommandError("POST_NOT_FOUND", "missing", "list posts", ExitNotFound), wantCode: "POST_NOT_FOUND", wantExit: ExitNotFound},
		{name: "validation", err: NewCommandError("CONTENT_EMPTY", "empty", "provide content", ExitConflict), wantCode: "CONTENT_EMPTY", wantExit: ExitConflict},
		{name: "unauthorized", err: NewCommandError("UNAUTHORIZED", "denied", "authenticate", ExitUnauthorized), wantCode: "UNAUTHORIZED", wantExit: ExitUnauthorized},
		{name: "filesystem", err: &fs.PathError{Op: "open", Path: "/missing", Err: fs.ErrNotExist}, wantCode: "IO_ERROR", wantExit: ExitIO},
		{name: "integrity", err: NewCommandError("DATA_INTEGRITY_FAILED", "invalid", "run doctor", ExitIntegrity), wantCode: "DATA_INTEGRITY_FAILED", wantExit: ExitIntegrity},
		{name: "network", err: &net.DNSError{Err: "unavailable", Name: "moment.invalid", IsTemporary: true}, wantCode: "DEPENDENCY_UNAVAILABLE", wantExit: ExitUnavailable},
		{name: "cancelled", err: context.Canceled, wantCode: "CANCELLED", wantExit: ExitTimeout},
		{name: "internal", err: errors.New("private failure detail"), wantCode: "INTERNAL", wantExit: ExitInternal},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			application := newTestApplication()
			registerFixture(application, "fail", CapabilityBoth, func(_ context.Context, _ Invocation, _ CommandInput) (Result, error) {
				return Result{}, test.err
			})

			output := execute(application, "", "fixture", "fail", "--json")
			if output.exit != test.wantExit {
				t.Fatalf("exit = %d, want %d; stdout = %q", output.exit, test.wantExit, output.stdout)
			}
			decoded := decodeOneDocument(t, output.stdout)
			commandError := decoded["error"].(map[string]any)
			if commandError["code"] != test.wantCode {
				t.Fatalf("code = %#v, want %q", commandError["code"], test.wantCode)
			}
			if commandError["suggestion"] == "" {
				t.Fatal("suggestion is empty")
			}
			if strings.Contains(output.stdout, "private failure detail") {
				t.Fatalf("private internal cause leaked: %q", output.stdout)
			}
		})
	}
}

func TestArgumentFailuresUseExitTwoAndMachineOutput(t *testing.T) {
	application := newTestApplication()
	registerFixture(application, "inspect", CapabilityBoth, func(_ context.Context, _ Invocation, _ CommandInput) (Result, error) {
		return Result{}, nil
	})

	for _, args := range [][]string{
		{"unknown", "--json"},
		{"fixture", "inspect", "extra", "--json"},
		{"fixture", "inspect", "--unknown", "--json"},
		{"fixture", "inspect", "--timeout", "invalid", "--json"},
		{"fixture", "inspect", "--help", "--json"},
	} {
		output := execute(application, "", args...)
		if output.exit != ExitInvalidInput {
			t.Fatalf("args %q: exit = %d, stdout = %q", args, output.exit, output.stdout)
		}
		decoded := decodeOneDocument(t, output.stdout)
		if decoded["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
			t.Fatalf("args %q: stdout = %q", args, output.stdout)
		}
	}
}

func TestTimeoutCancelsBoundedOperation(t *testing.T) {
	application := newTestApplication()
	registerFixture(application, "wait", CapabilityBoth, func(ctx context.Context, _ Invocation, _ CommandInput) (Result, error) {
		<-ctx.Done()
		return Result{}, ctx.Err()
	})

	output := execute(application, "", "fixture", "wait", "--timeout", "5ms", "--json")
	if output.exit != ExitTimeout {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", output.exit, output.stdout, output.stderr)
	}
	decoded := decodeOneDocument(t, output.stdout)
	if decoded["error"].(map[string]any)["code"] != "TIMEOUT" {
		t.Fatalf("stdout = %q", output.stdout)
	}
}

func TestModeSelectionAndUnsupportedCapabilities(t *testing.T) {
	application := newTestApplication()
	called := false
	registerFixture(application, "local", CapabilityDirectOnly, func(_ context.Context, _ Invocation, _ CommandInput) (Result, error) {
		called = true
		return Result{}, nil
	})

	output := execute(application, "", "--url", "http://127.0.0.1:8080", "fixture", "local", "--json")
	if output.exit != ExitInvalidInput || called {
		t.Fatalf("exit = %d, called = %t, stdout = %q", output.exit, called, output.stdout)
	}
	decoded := decodeOneDocument(t, output.stdout)
	errorData := decoded["error"].(map[string]any)
	if errorData["code"] != "MODE_UNSUPPORTED" {
		t.Fatalf("error = %#v", errorData)
	}
	details := errorData["details"].(map[string]any)
	if details["mode"] != string(ModeAPI) || details["command"] != "fixture local" {
		t.Fatalf("details = %#v", details)
	}
}

func TestDirectAndAPITargetsExposeOnlyConnectionConfiguration(t *testing.T) {
	application := newTestApplication()
	registerFixture(application, "target", CapabilityBoth, func(_ context.Context, invocation Invocation, _ CommandInput) (Result, error) {
		switch invocation.Mode() {
		case ModeDirect:
			target, err := invocation.DirectTransport()
			if err != nil {
				return Result{}, err
			}
			return Result{Data: map[string]any{"mode": target.Mode(), "data_dir": target.DataDirectory()}}, nil
		case ModeAPI:
			target, err := invocation.APITransport()
			if err != nil {
				return Result{}, err
			}
			return Result{Data: map[string]any{"mode": target.Mode(), "url": target.BaseURL().String(), "token_file": target.TokenFile()}}, nil
		default:
			return Result{}, errors.New("unexpected mode")
		}
	})

	direct := execute(application, "", "--data-dir", "/tmp/moment-fixture", "fixture", "target", "--json")
	if direct.exit != ExitSuccess {
		t.Fatalf("direct exit = %d, stdout = %q", direct.exit, direct.stdout)
	}
	directData := decodeOneDocument(t, direct.stdout)["data"].(map[string]any)
	if directData["data_dir"] != "/tmp/moment-fixture" || directData["mode"] != string(ModeDirect) {
		t.Fatalf("direct data = %#v", directData)
	}

	api := execute(application, "", "--url", "https://moment.test/base", "--token-file", "/tmp/token", "fixture", "target", "--json")
	if api.exit != ExitSuccess {
		t.Fatalf("api exit = %d, stdout = %q", api.exit, api.stdout)
	}
	apiData := decodeOneDocument(t, api.stdout)["data"].(map[string]any)
	if apiData["url"] != "https://moment.test/base" || apiData["token_file"] != "/tmp/token" || apiData["mode"] != string(ModeAPI) {
		t.Fatalf("api data = %#v", apiData)
	}
}

func TestConflictingOrInvalidTransportFlagsFailBeforeHandler(t *testing.T) {
	application := newTestApplication()
	registerFixture(application, "target", CapabilityBoth, func(_ context.Context, _ Invocation, _ CommandInput) (Result, error) {
		t.Fatal("handler must not run")
		return Result{}, nil
	})

	for _, args := range [][]string{
		{"--url", "http://127.0.0.1:8080", "--data-dir", "/tmp/moment", "fixture", "target", "--json"},
		{"--url", "file:///tmp/socket", "fixture", "target", "--json"},
		{"--token-file", "/tmp/token", "fixture", "target", "--json"},
	} {
		output := execute(application, "", args...)
		if output.exit != ExitInvalidInput {
			t.Fatalf("args %q: exit = %d, stdout = %q", args, output.exit, output.stdout)
		}
		decoded := decodeOneDocument(t, output.stdout)
		if decoded["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
			t.Fatalf("args %q: stdout = %q", args, output.stdout)
		}
	}
}

func TestSafetyFlagsAreScopedAndNoInputNeverPrompts(t *testing.T) {
	application := newTestApplication()
	var mutations atomic.Int64
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"fixture", "prune"},
		Short:      "Prune fixture data",
		Long:       "Prune fixture data.",
		Inputs:     "No positional input.",
		Output:     "The planned or completed mutation.",
		Errors:     "Confirmation failures.",
		Safety:     "Requires confirmation; dry-run is read-only.",
		Example:    "moment fixture prune --dry-run --json",
		Capability: CapabilityDirectOnly,
		Policy: SafetyPolicy{
			Destructive:    true,
			SupportsDryRun: true,
			Confirmation:   "Delete fixture data?",
		},
		Run: func(_ context.Context, invocation Invocation, _ CommandInput) (Result, error) {
			if !invocation.Safety().DryRun {
				mutations.Add(1)
			}
			return Result{Data: map[string]any{"dry_run": invocation.Safety().DryRun}}, nil
		},
	})
	registerFixture(application, "inspect", CapabilityBoth, func(_ context.Context, _ Invocation, _ CommandInput) (Result, error) {
		return Result{}, nil
	})

	blocked := execute(application, "", "fixture", "prune", "--no-input", "--json")
	if blocked.exit != ExitInvalidInput || mutations.Load() != 0 || blocked.stderr != "" {
		t.Fatalf("blocked = %#v, mutations = %d", blocked, mutations.Load())
	}
	if decodeOneDocument(t, blocked.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("stdout = %q", blocked.stdout)
	}

	dryRun := execute(application, "", "fixture", "prune", "--dry-run", "--json")
	if dryRun.exit != ExitSuccess || mutations.Load() != 0 {
		t.Fatalf("dry run = %#v, mutations = %d", dryRun, mutations.Load())
	}
	if decodeOneDocument(t, dryRun.stdout)["data"].(map[string]any)["dry_run"] != true {
		t.Fatalf("stdout = %q", dryRun.stdout)
	}

	confirmed := execute(application, "", "fixture", "prune", "--yes", "--json")
	if confirmed.exit != ExitSuccess || mutations.Load() != 1 {
		t.Fatalf("confirmed = %#v, mutations = %d", confirmed, mutations.Load())
	}

	wrongScope := execute(application, "", "fixture", "inspect", "--dry-run", "--json")
	if wrongScope.exit != ExitInvalidInput {
		t.Fatalf("wrong-scope exit = %d, stdout = %q", wrongScope.exit, wrongScope.stdout)
	}
}

func TestYesDoesNotBypassHandlerSafetyBoundary(t *testing.T) {
	application := newTestApplication()
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"fixture", "guarded"},
		Short:      "Exercise a safety boundary",
		Long:       "Exercise a safety boundary.",
		Inputs:     "No positional input.",
		Output:     "No data when the scope is unsafe.",
		Errors:     "UNSAFE_SCOPE when the target cannot be mutated.",
		Safety:     "Confirmation never bypasses the target guard.",
		Example:    "moment fixture guarded --yes --json",
		Capability: CapabilityDirectOnly,
		Policy:     SafetyPolicy{Destructive: true},
		Run: func(_ context.Context, invocation Invocation, _ CommandInput) (Result, error) {
			if !invocation.Safety().Yes {
				t.Fatal("expected confirmation state")
			}
			return Result{}, NewCommandError("UNSAFE_SCOPE", "the target scope is unsafe", "choose an explicitly marked development instance", ExitConflict)
		},
	})

	output := execute(application, "", "fixture", "guarded", "--yes", "--json")
	if output.exit != ExitConflict {
		t.Fatalf("exit = %d, stdout = %q", output.exit, output.stdout)
	}
	if decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "UNSAFE_SCOPE" {
		t.Fatalf("stdout = %q", output.stdout)
	}
}

func TestHumanConfirmationUsesStderrAndDeclinesByDefault(t *testing.T) {
	application := newTestApplication()
	called := false
	application.MustRegisterLeaf(LeafSpec{
		Path:       []string{"fixture", "delete"},
		Short:      "Delete fixture data",
		Long:       "Delete fixture data.",
		Inputs:     "No positional input.",
		Output:     "Deletion status.",
		Errors:     "Confirmation failures.",
		Safety:     "Requires confirmation.",
		Example:    "moment fixture delete --yes",
		Capability: CapabilityDirectOnly,
		Policy:     SafetyPolicy{Destructive: true, Confirmation: "Delete fixture data?"},
		Run: func(_ context.Context, _ Invocation, _ CommandInput) (Result, error) {
			called = true
			return Result{Text: "deleted"}, nil
		},
	})

	output := execute(application, "n\n", "fixture", "delete")
	if output.exit != ExitConflict || called || output.stdout != "" {
		t.Fatalf("output = %#v, called = %t", output, called)
	}
	if !strings.HasPrefix(output.stderr, "Delete fixture data? [y/N] CONFIRMATION_DECLINED:") {
		t.Fatalf("stderr = %q", output.stderr)
	}
}

func TestJSONOutputHasNoANSIWhenNoColorIsSet(t *testing.T) {
	t.Setenv("NO_COLOR", "1")
	application := newTestApplication()
	registerFixture(application, "inspect", CapabilityBoth, func(_ context.Context, invocation Invocation, _ CommandInput) (Result, error) {
		invocation.Log("info", "plain diagnostic")
		return Result{Data: map[string]bool{"ok": true}}, nil
	})

	output := execute(application, "", "fixture", "inspect", "--json")
	if output.exit != ExitSuccess {
		t.Fatalf("exit = %d", output.exit)
	}
	if strings.Contains(output.stdout+output.stderr, "\x1b[") {
		t.Fatalf("ANSI output detected: stdout=%q stderr=%q", output.stdout, output.stderr)
	}
	decodeOneDocument(t, output.stdout)
}

func TestUnencodableResultBecomesOneInternalErrorDocument(t *testing.T) {
	application := newTestApplication()
	registerFixture(application, "invalid-result", CapabilityBoth, func(_ context.Context, _ Invocation, _ CommandInput) (Result, error) {
		return Result{Data: make(chan int)}, nil
	})

	output := execute(application, "", "fixture", "invalid-result", "--json")
	if output.exit != ExitInternal {
		t.Fatalf("exit = %d, stdout = %q", output.exit, output.stdout)
	}
	decoded := decodeOneDocument(t, output.stdout)
	if decoded["error"].(map[string]any)["code"] != "INTERNAL" {
		t.Fatalf("stdout = %q", output.stdout)
	}
}

func TestUnencodableErrorDetailsBecomeOneInternalErrorDocument(t *testing.T) {
	application := newTestApplication()
	registerFixture(application, "invalid-error", CapabilityBoth, func(_ context.Context, _ Invocation, _ CommandInput) (Result, error) {
		return Result{}, NewCommandError("BROKEN_FIXTURE", "broken", "inspect the fixture", ExitConflict).
			WithDetails(map[string]any{"invalid": make(chan int)})
	})

	output := execute(application, "", "fixture", "invalid-error", "--json")
	if output.exit != ExitInternal {
		t.Fatalf("exit = %d, stdout = %q", output.exit, output.stdout)
	}
	decoded := decodeOneDocument(t, output.stdout)
	if decoded["error"].(map[string]any)["code"] != "INTERNAL" {
		t.Fatalf("stdout = %q", output.stdout)
	}
}

func TestMalformedPublicErrorIsSanitized(t *testing.T) {
	application := newTestApplication()
	registerFixture(application, "invalid-error", CapabilityBoth, func(_ context.Context, _ Invocation, _ CommandInput) (Result, error) {
		return Result{}, NewCommandError("not stable", "", "", ExitSuccess)
	})

	output := execute(application, "", "fixture", "invalid-error", "--json")
	if output.exit != ExitInternal {
		t.Fatalf("exit = %d, stdout = %q", output.exit, output.stdout)
	}
	commandError := decodeOneDocument(t, output.stdout)["error"].(map[string]any)
	if commandError["code"] != "INTERNAL" || strings.Contains(output.stdout, "not stable") {
		t.Fatalf("stdout = %q", output.stdout)
	}
}

func TestRegistrationRejectsIncompleteLeafContracts(t *testing.T) {
	application := newTestApplication()
	base := LeafSpec{
		Path:       []string{"fixture", "valid"},
		Short:      "Valid fixture",
		Long:       "Run a valid fixture.",
		Inputs:     "No positional input.",
		Output:     "A fixture result.",
		Errors:     "Stable fixture errors.",
		Example:    "moment fixture valid",
		Capability: CapabilityBoth,
		Run: func(context.Context, Invocation, CommandInput) (Result, error) {
			return Result{}, nil
		},
	}

	tests := []struct {
		name   string
		mutate func(*LeafSpec)
	}{
		{name: "missing path", mutate: func(spec *LeafSpec) { spec.Path = nil }},
		{name: "missing detailed help", mutate: func(spec *LeafSpec) { spec.Output = "" }},
		{name: "missing help", mutate: func(spec *LeafSpec) { spec.Example = "" }},
		{name: "missing handler", mutate: func(spec *LeafSpec) { spec.Run = nil }},
		{name: "missing modes", mutate: func(spec *LeafSpec) { spec.Capability = 0 }},
		{name: "dry-run on safe command", mutate: func(spec *LeafSpec) { spec.Policy.SupportsDryRun = true }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			spec := base
			test.mutate(&spec)
			if err := application.RegisterLeaf(spec); err == nil {
				t.Fatal("expected registration error")
			}
		})
	}
}

func TestRegistrationRejectsDuplicateAndUnownedPaths(t *testing.T) {
	application := newTestApplication()
	leaf := LeafSpec{
		Path:       []string{"fixture", "inspect"},
		Short:      "Inspect a fixture",
		Long:       "Inspect a fixture.",
		Inputs:     "No positional input.",
		Output:     "A fixture result.",
		Errors:     "Stable fixture errors.",
		Example:    "moment fixture inspect",
		Capability: CapabilityBoth,
		Run: func(context.Context, Invocation, CommandInput) (Result, error) {
			return Result{}, nil
		},
	}
	if err := application.RegisterLeaf(leaf); err != nil {
		t.Fatal(err)
	}
	if err := application.RegisterLeaf(leaf); err == nil {
		t.Fatal("expected duplicate leaf error")
	}
	leaf.Path = []string{"missing", "inspect"}
	if err := application.RegisterLeaf(leaf); err == nil {
		t.Fatal("expected unregistered group error")
	}
	leaf.Path = []string{"fixture"}
	if err := application.RegisterLeaf(leaf); err == nil {
		t.Fatal("expected group conflict error")
	}
}

func TestAPITargetInterfacePreservesParsedURL(t *testing.T) {
	target := apiTarget{baseURL: &url.URL{Scheme: "https", Host: "moment.test"}, tokenFile: "/tmp/token"}
	var transport APITransport = target
	if transport.Mode() != ModeAPI || transport.BaseURL().String() != "https://moment.test" || transport.TokenFile() != "/tmp/token" {
		t.Fatalf("transport = %#v", transport)
	}
}
