package cli

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/moment/moment/internal/buildinfo"
	"github.com/moment/moment/internal/runtimefs"
)

func TestServeIsDocumentedDirectOnlyLifecycleCommand(t *testing.T) {
	application := NewMoment(buildinfo.Info{Version: "dev", Commit: "unknown", BuildDate: "unknown"})
	help := execute(application, "", "serve", "--help")
	if help.exit != ExitSuccess || help.stderr != "" {
		t.Fatalf("help = %#v", help)
	}
	for _, contract := range []string{
		"--listen",
		"--trusted-proxy",
		"--log-format text or json",
		"Only direct mode is supported",
		"moment --data-dir ./moment-data serve --log-format json",
	} {
		if !strings.Contains(help.stdout, contract) {
			t.Fatalf("serve help missing %q:\n%s", contract, help.stdout)
		}
	}

	unsupported := execute(application, "", "--url", "http://127.0.0.1:8080", "serve", "--json")
	if unsupported.exit != ExitInvalidInput {
		t.Fatalf("exit = %d, stdout = %q", unsupported.exit, unsupported.stdout)
	}
	commandError := decodeOneDocument(t, unsupported.stdout)["error"].(map[string]any)
	if commandError["code"] != "MODE_UNSUPPORTED" {
		t.Fatalf("error = %#v", commandError)
	}

	invalidListen := execute(application, "", "serve", "--listen", "", "--json")
	if invalidListen.exit != ExitInvalidInput {
		t.Fatalf("invalid listen exit = %d, stdout = %q", invalidListen.exit, invalidListen.stdout)
	}
	if decodeOneDocument(t, invalidListen.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("stdout = %q", invalidListen.stdout)
	}
}

func TestServeReportsDatabaseReadinessFailure(t *testing.T) {
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(runtimeState.Current.DatabasePath, []byte("not a SQLite database"), 0o600); err != nil {
		t.Fatal(err)
	}

	application := NewMoment(buildinfo.Info{Version: "dev", Commit: "unknown", BuildDate: "unknown"})
	result := execute(application, "", "--data-dir", runtimeState.Root, "serve", "--json")
	if result.exit != ExitIntegrity || result.stderr != "" {
		t.Fatalf("result = %#v", result)
	}
	commandError := decodeOneDocument(t, result.stdout)["error"].(map[string]any)
	if commandError["code"] != "DATABASE_NOT_READY" {
		t.Fatalf("error = %#v", commandError)
	}
}
