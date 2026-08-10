package cli

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/buildinfo"
	"github.com/moment/moment/internal/runtimefs"
	ginhttp "github.com/moment/moment/internal/transport/http/gin"
)

func momentTestApplication() *Application {
	return NewMoment(buildinfo.Info{Version: "test", Commit: "test", BuildDate: "test"})
}

func commandData(t *testing.T, output commandOutput) map[string]any {
	t.Helper()
	if output.exit != ExitSuccess {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", output.exit, output.stdout, output.stderr)
	}
	decoded := decodeOneDocument(t, output.stdout)
	return decoded["data"].(map[string]any)
}

func TestOwnerDirectCommandsInitializeInspectRotateAndDoNotLeak(t *testing.T) {
	application := momentTestApplication()
	dataDir := t.TempDir()
	initialized := commandData(t, execute(application, "", "owner", "init", "--data-dir", dataDir, "--json"))
	token, _ := initialized["token"].(string)
	if token == "" || initialized["session_epoch"] != float64(1) {
		t.Fatalf("initialized = %#v", initialized)
	}
	secretContents, err := os.ReadFile(filepath.Join(dataDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(secretContents), token) {
		t.Fatal("raw token leaked to secrets.json")
	}

	statusOutput := execute(application, "", "owner", "status", "--data-dir", dataDir, "--json")
	status := commandData(t, statusOutput)
	if status["initialized"] != true || status["session_epoch"] != float64(1) {
		t.Fatalf("status = %#v", status)
	}
	if strings.Contains(statusOutput.stdout, token) || strings.Contains(statusOutput.stderr, token) {
		t.Fatal("status leaked owner token")
	}

	duplicate := execute(application, "", "owner", "init", "--data-dir", dataDir, "--json")
	if duplicate.exit != ExitConflict || decodeOneDocument(t, duplicate.stdout)["error"].(map[string]any)["code"] != "OWNER_ALREADY_INITIALIZED" {
		t.Fatalf("duplicate = %#v", duplicate)
	}

	runtimeState, err := runtimefs.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := runtimeState.Locks.Acquire(context.Background(), runtimefs.ServeLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	blocked := execute(application, "", "owner", "rotate-token", "--data-dir", dataDir, "--json")
	if blocked.exit != ExitConflict || decodeOneDocument(t, blocked.stdout)["error"].(map[string]any)["code"] != "INSTANCE_IN_USE" {
		t.Fatalf("blocked = %#v", blocked)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}

	rotatedOutput := execute(application, "", "owner", "rotate-token", "--data-dir", dataDir, "--json", "--log-level", "debug", "--log-format", "json")
	rotated := commandData(t, rotatedOutput)
	newToken, _ := rotated["token"].(string)
	if newToken == "" || newToken == token || rotated["session_epoch"] != float64(2) {
		t.Fatalf("rotated = %#v", rotated)
	}
	if strings.Contains(rotatedOutput.stderr, token) || strings.Contains(rotatedOutput.stderr, newToken) {
		t.Fatalf("stderr leaked token: %q", rotatedOutput.stderr)
	}
	statusAfter := execute(application, "", "owner", "status", "--data-dir", dataDir, "--json")
	if strings.Contains(statusAfter.stdout, token) || strings.Contains(statusAfter.stdout, newToken) {
		t.Fatal("status after rotation leaked a token")
	}
}

func TestOwnerAPIModeTokenSourcesParityAndLiveRotation(t *testing.T) {
	dataDir := t.TempDir()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(runtimeState.Root, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	engine, err := ginhttp.New(ginhttp.Config{
		Assets:    fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Moment")}},
		OwnerAuth: manager,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	application := momentTestApplication()

	direct := commandData(t, execute(application, "", "owner", "status", "--data-dir", dataDir, "--json"))
	t.Setenv("MOMENT_TOKEN", credential.Token)
	apiFromEnvironment := commandData(t, execute(application, "", "--url", server.URL, "owner", "status", "--json"))
	if !reflect.DeepEqual(direct, apiFromEnvironment) {
		t.Fatalf("direct = %#v, api = %#v", direct, apiFromEnvironment)
	}

	tokenFile := filepath.Join(t.TempDir(), "owner.token")
	if err := os.WriteFile(tokenFile, []byte(credential.Token+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOMENT_TOKEN", "wrong-environment-token")
	fromFile := commandData(t, execute(application, "", "--url", server.URL, "--token-file", tokenFile, "owner", "status", "--json"))
	if !reflect.DeepEqual(direct, fromFile) {
		t.Fatalf("token-file status = %#v", fromFile)
	}
	if err := os.WriteFile(tokenFile, []byte("wrong-file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOMENT_TOKEN", credential.Token)
	fileWins := execute(application, "", "--url", server.URL, "--token-file", tokenFile, "owner", "status", "--json")
	if fileWins.exit != ExitUnauthorized || decodeOneDocument(t, fileWins.stdout)["error"].(map[string]any)["code"] != "UNAUTHORIZED" {
		t.Fatalf("explicit token-file did not take precedence: %#v", fileWins)
	}

	if err := os.WriteFile(tokenFile, []byte(credential.Token), 0o600); err != nil {
		t.Fatal(err)
	}
	rotation := commandData(t, execute(application, "", "--url", server.URL, "--token-file", tokenFile, "owner", "rotate-token", "--json"))
	newToken, _ := rotation["token"].(string)
	if newToken == "" || newToken == credential.Token || rotation["session_epoch"] != float64(2) {
		t.Fatalf("rotation = %#v", rotation)
	}
	oldToken := execute(application, "", "--url", server.URL, "--token-file", tokenFile, "owner", "status", "--json")
	if oldToken.exit != ExitUnauthorized {
		t.Fatalf("old token exit = %d, stdout = %s", oldToken.exit, oldToken.stdout)
	}
	if err := os.WriteFile(tokenFile, []byte(newToken), 0o600); err != nil {
		t.Fatal(err)
	}
	live := commandData(t, execute(application, "", "--url", server.URL, "--token-file", tokenFile, "owner", "status", "--json"))
	if live["session_epoch"] != float64(2) {
		t.Fatalf("live status = %#v", live)
	}
}

func TestOwnerCommandModeMatrixCredentialFlagsAndTokenFileFailures(t *testing.T) {
	application := momentTestApplication()
	unsupported := execute(application, "", "--url", "http://127.0.0.1:8080", "owner", "init", "--json")
	if unsupported.exit != ExitInvalidInput || decodeOneDocument(t, unsupported.stdout)["error"].(map[string]any)["code"] != "MODE_UNSUPPORTED" {
		t.Fatalf("unsupported = %#v", unsupported)
	}
	plaintextFlag := execute(application, "", "--url", "http://127.0.0.1:8080", "--token", "secret", "owner", "status", "--json")
	if plaintextFlag.exit != ExitInvalidInput || !strings.Contains(plaintextFlag.stdout, "unknown flag") {
		t.Fatalf("plaintext flag = %#v", plaintextFlag)
	}
	t.Setenv("MOMENT_TOKEN", "")
	missing := execute(application, "", "--url", "http://127.0.0.1:8080", "owner", "status", "--json")
	if missing.exit != ExitUnauthorized || decodeOneDocument(t, missing.stdout)["error"].(map[string]any)["code"] != "OWNER_TOKEN_REQUIRED" {
		t.Fatalf("missing = %#v", missing)
	}
	unreadable := execute(application, "", "--url", "http://127.0.0.1:8080", "--token-file", filepath.Join(t.TempDir(), "missing"), "owner", "status", "--json")
	if unreadable.exit != ExitIO || decodeOneDocument(t, unreadable.stdout)["error"].(map[string]any)["code"] != "TOKEN_FILE_UNREADABLE" {
		t.Fatalf("unreadable = %#v", unreadable)
	}
}

func TestOwnerLeafHelpDocumentsRunnableAgentContract(t *testing.T) {
	application := momentTestApplication()
	for _, args := range [][]string{{"owner", "init", "--help"}, {"owner", "status", "--help"}, {"owner", "rotate-token", "--help"}} {
		output := execute(application, "", args...)
		if output.exit != ExitSuccess || output.stderr != "" {
			t.Fatalf("args %q output = %#v", args, output)
		}
		for _, contract := range []string{"INPUTS", "OUTPUT", "ERRORS", "Examples:"} {
			if !strings.Contains(output.stdout, contract) {
				t.Fatalf("args %q missing %q:\n%s", args, contract, output.stdout)
			}
		}
		if args[1] != "init" && (!strings.Contains(output.stdout, "--token-file") || !strings.Contains(output.stdout, "MOMENT_TOKEN")) {
			t.Fatalf("args %q do not document safe API token sources:\n%s", args, output.stdout)
		}
	}
}

func TestSecretsJSONDoesNotContainJSONEncodedRawToken(t *testing.T) {
	application := momentTestApplication()
	dataDir := t.TempDir()
	output := execute(application, "", "owner", "init", "--data-dir", dataDir, "--json")
	data := commandData(t, output)
	encodedToken, err := json.Marshal(data["token"])
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(filepath.Join(dataDir, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(contents), strings.Trim(string(encodedToken), `"`)) {
		t.Fatal("raw token persisted")
	}
}
