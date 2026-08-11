package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"sort"
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

func TestInitIsExactOwnerInitAlias(t *testing.T) {
	application := momentTestApplication()
	var alias, owner LeafSpec
	for _, leaf := range application.leaves {
		switch strings.Join(leaf.Path, " ") {
		case "init":
			alias = leaf
		case "owner init":
			owner = leaf
		}
	}
	if reflect.ValueOf(alias.Run).Pointer() != reflect.ValueOf(owner.Run).Pointer() ||
		alias.Capability != owner.Capability || alias.Policy != owner.Policy || alias.ArgsUsage != owner.ArgsUsage ||
		(alias.Args == nil) != (owner.Args == nil) || (alias.Flags == nil) != (owner.Flags == nil) {
		t.Fatalf("init alias drifted: alias=%#v owner=%#v", alias, owner)
	}
	if alias.Args != nil && reflect.ValueOf(alias.Args).Pointer() != reflect.ValueOf(owner.Args).Pointer() {
		t.Fatal("init alias argument contract drifted")
	}
	if alias.Flags != nil && reflect.ValueOf(alias.Flags).Pointer() != reflect.ValueOf(owner.Flags).Pointer() {
		t.Fatal("init alias flag contract drifted")
	}

	aliasDir := t.TempDir()
	aliasData := commandData(t, execute(application, "", "init", "--data-dir", aliasDir, "--json"))
	ownerDir := t.TempDir()
	ownerData := commandData(t, execute(application, "", "owner", "init", "--data-dir", ownerDir, "--json"))
	decodeCredential := func(data map[string]any) auth.Credential {
		t.Helper()
		encoded, err := json.Marshal(data)
		if err != nil {
			t.Fatal(err)
		}
		var credential auth.Credential
		decoder := json.NewDecoder(bytes.NewReader(encoded))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&credential); err != nil {
			t.Fatal(err)
		}
		if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
			t.Fatalf("credential DTO trailing content: %v", err)
		}
		if len(credential.Token) < 40 || !credential.Initialized || credential.SessionEpoch != 1 || credential.InitializedAt.IsZero() || credential.RotatedAt != nil {
			t.Fatalf("invalid credential DTO: %#v", credential)
		}
		return credential
	}
	aliasCredential := decodeCredential(aliasData)
	ownerCredential := decodeCredential(ownerData)
	keys := func(data map[string]any) []string {
		result := make([]string, 0, len(data))
		for key := range data {
			result = append(result, key)
		}
		sort.Strings(result)
		return result
	}
	if !reflect.DeepEqual(keys(aliasData), keys(ownerData)) || aliasCredential.Token == ownerCredential.Token {
		t.Fatalf("alias data=%#v owner data=%#v", aliasData, ownerData)
	}
	for name, output := range map[string]commandOutput{
		"owner after alias": execute(application, "", "owner", "init", "--data-dir", aliasDir, "--json"),
		"alias after owner": execute(application, "", "init", "--data-dir", ownerDir, "--json"),
	} {
		if output.exit != ExitConflict || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "OWNER_ALREADY_INITIALIZED" {
			t.Fatalf("%s = %#v", name, output)
		}
	}
	unsupported := execute(application, "", "--url", "http://127.0.0.1:1", "init", "--json")
	if unsupported.exit != ExitInvalidInput || decodeOneDocument(t, unsupported.stdout)["error"].(map[string]any)["code"] != "MODE_UNSUPPORTED" {
		t.Fatalf("API alias = %#v", unsupported)
	}
	help := execute(application, "", "init", "--help")
	if help.exit != ExitSuccess || help.stderr != "" || !strings.Contains(help.stdout, "exact public alias") ||
		!strings.Contains(help.stdout, "moment init --data-dir ./moment-data --json") {
		t.Fatalf("alias help = %#v", help)
	}
}

func TestInitAliasesDoNotMutateAServiceOwnedInstance(t *testing.T) {
	application := momentTestApplication()
	dataDir := t.TempDir()
	commandData(t, execute(application, "", "owner", "init", "--data-dir", dataDir, "--json"))
	runtimeState, err := runtimefs.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	beforeStatus, err := auth.ReadStatus(runtimeState.Root)
	if err != nil {
		t.Fatal(err)
	}
	serve, err := runtimeState.Locks.Acquire(context.Background(), runtimefs.ServeLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serve.Release() }()
	beforeTree := fingerprintOwnerInstance(t, dataDir)

	for _, command := range [][]string{{"init"}, {"owner", "init"}} {
		args := append(append([]string(nil), command...), "--data-dir", dataDir, "--json")
		output := execute(application, "", args...)
		if output.exit != ExitConflict || output.stderr != "" ||
			decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "INSTANCE_IN_USE" {
			t.Fatalf("command=%v output=%#v", command, output)
		}
		if afterTree := fingerprintOwnerInstance(t, dataDir); !reflect.DeepEqual(beforeTree, afterTree) {
			t.Fatalf("command=%v mutated service-owned tree\nbefore=%#v\nafter=%#v", command, beforeTree, afterTree)
		}
		afterStatus, err := auth.ReadStatus(runtimeState.Root)
		if err != nil || !reflect.DeepEqual(beforeStatus, afterStatus) {
			t.Fatalf("command=%v changed credential status: before=%#v after=%#v err=%v", command, beforeStatus, afterStatus, err)
		}
	}
}

func fingerprintOwnerInstance(t *testing.T, root string) map[string]string {
	t.Helper()
	fingerprint := make(map[string]string)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			fingerprint[relative] = "directory"
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fingerprint[relative] = "symlink:" + target
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fingerprint[relative] = fmt.Sprintf("file:%x", sha256.Sum256(contents))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return fingerprint
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

func TestTokenFileSourceIsBoundedAndExplicitlyPreferred(t *testing.T) {
	t.Setenv("MOMENT_TOKEN", "environment-token")
	directory := t.TempDir()
	path := filepath.Join(directory, "owner.token")
	if err := os.WriteFile(path, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := readAPIToken(apiTarget{tokenFile: path})
	if err != nil || token != "file-token" {
		t.Fatalf("explicit file token=%q err=%v", token, err)
	}

	assertCode := func(name string, err error, code string, exit ExitCode) {
		t.Helper()
		var commandErr *CommandError
		if !errors.As(err, &commandErr) || commandErr.Code != code || commandErr.ExitCode != exit {
			t.Fatalf("%s err=%#v", name, err)
		}
	}
	if err := os.WriteFile(path, []byte("two tokens"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = readAPIToken(apiTarget{tokenFile: path})
	assertCode("whitespace", err, "INVALID_ARGUMENT", ExitInvalidInput)

	oversized := filepath.Join(directory, "oversized.token")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("x"), maxTokenFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = readAPIToken(apiTarget{tokenFile: oversized})
	assertCode("oversized", err, "INVALID_ARGUMENT", ExitInvalidInput)

	_, err = readAPIToken(apiTarget{tokenFile: directory})
	assertCode("non-regular", err, "TOKEN_FILE_UNREADABLE", ExitIO)

	t.Setenv("MOMENT_TOKEN", "   ")
	_, err = readAPIToken(apiTarget{})
	assertCode("missing", err, "OWNER_TOKEN_REQUIRED", ExitUnauthorized)
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

func TestOwnerAPIStrictSuccessAndFailureEnvelopes(t *testing.T) {
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", "test-token")
	tests := []struct {
		name        string
		status      int
		contentType string
		body        string
	}{
		{name: "success unknown envelope", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":true,"session_epoch":1,"initialized_at":"2026-08-10T00:00:00Z"},"meta":{"request_id":"req"},"extra":true}`},
		{name: "success unknown DTO", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":true,"session_epoch":1,"initialized_at":"2026-08-10T00:00:00Z","secret":"no"},"meta":{"request_id":"req"}}`},
		{name: "success duplicate DTO", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":true,"session_epoch":1,"session_epoch":2,"initialized_at":"2026-08-10T00:00:00Z"},"meta":{"request_id":"req"}}`},
		{name: "success wrong content type", status: http.StatusOK, contentType: "text/plain", body: `{"data":{"initialized":true,"session_epoch":1,"initialized_at":"2026-08-10T00:00:00Z"},"meta":{"request_id":"req"}}`},
		{name: "success missing meta", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":true,"session_epoch":1,"initialized_at":"2026-08-10T00:00:00Z"}}`},
		{name: "success empty meta", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":true,"session_epoch":1,"initialized_at":"2026-08-10T00:00:00Z"},"meta":{}}`},
		{name: "success blank request ID", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":true,"session_epoch":1,"initialized_at":"2026-08-10T00:00:00Z"},"meta":{"request_id":"  "}}`},
		{name: "success wrong request ID", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":true,"session_epoch":1,"initialized_at":"2026-08-10T00:00:00Z"},"meta":{"request_id":7}}`},
		{name: "success zero status DTO", status: http.StatusOK, contentType: "application/json", body: `{"data":{},"meta":{"request_id":"req"}}`},
		{name: "success null initialized", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":null,"session_epoch":0},"meta":{"request_id":"req"}}`},
		{name: "success null session epoch", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":false,"session_epoch":null},"meta":{"request_id":"req"}}`},
		{name: "success null omitted timestamp", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":false,"session_epoch":0,"initialized_at":null},"meta":{"request_id":"req"}}`},
		{name: "success unreachable uninitialized API status", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":false,"session_epoch":0},"meta":{"request_id":"req"}}`},
		{name: "success nanosecond timestamp", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":true,"session_epoch":1,"initialized_at":"2026-08-10T00:00:00.000000001Z"},"meta":{"request_id":"req"}}`},
		{name: "success noncanonical UTC offset", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":true,"session_epoch":1,"initialized_at":"2026-08-10T00:00:00+00:00"},"meta":{"request_id":"req"}}`},
		{name: "success epoch one with rotation", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":true,"session_epoch":1,"initialized_at":"2026-08-10T00:00:00Z","rotated_at":"2026-08-10T00:01:00Z"},"meta":{"request_id":"req"}}`},
		{name: "success rotated epoch missing rotation", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":true,"session_epoch":2,"initialized_at":"2026-08-10T00:00:00Z"},"meta":{"request_id":"req"}}`},
		{name: "success rotation precedes initialization", status: http.StatusOK, contentType: "application/json", body: `{"data":{"initialized":true,"session_epoch":2,"initialized_at":"2026-08-10T00:01:00Z","rotated_at":"2026-08-10T00:00:00Z"},"meta":{"request_id":"req"}}`},
		{name: "failure trailing value", status: http.StatusUnauthorized, contentType: "application/json", body: `{"error":{"code":"UNAUTHORIZED","message":"denied","details":{},"request_id":"req"}} {}`},
		{name: "failure unknown field", status: http.StatusUnauthorized, contentType: "application/json", body: `{"error":{"code":"UNAUTHORIZED","message":"denied","details":{},"request_id":"req","token":"no"}}`},
		{name: "failure duplicate code", status: http.StatusUnauthorized, contentType: "application/json", body: `{"error":{"code":"UNAUTHORIZED","code":"INTERNAL","message":"denied","details":{},"request_id":"req"}}`},
		{name: "failure lowercase code", status: http.StatusUnauthorized, contentType: "application/json", body: `{"error":{"code":"unauthorized","message":"denied","details":{},"request_id":"req"}}`},
		{name: "failure illegal code", status: http.StatusUnauthorized, contentType: "application/json", body: `{"error":{"code":"UNAUTHORIZED!","message":"denied","details":{},"request_id":"req"}}`},
		{name: "failure missing message", status: http.StatusUnauthorized, contentType: "application/json", body: `{"error":{"code":"UNAUTHORIZED","details":{},"request_id":"req"}}`},
		{name: "failure blank message", status: http.StatusUnauthorized, contentType: "application/json", body: `{"error":{"code":"UNAUTHORIZED","message":"  ","details":{},"request_id":"req"}}`},
		{name: "failure missing details", status: http.StatusUnauthorized, contentType: "application/json", body: `{"error":{"code":"UNAUTHORIZED","message":"denied","request_id":"req"}}`},
		{name: "failure null details", status: http.StatusUnauthorized, contentType: "application/json", body: `{"error":{"code":"UNAUTHORIZED","message":"denied","details":null,"request_id":"req"}}`},
		{name: "failure missing request ID", status: http.StatusUnauthorized, contentType: "application/json", body: `{"error":{"code":"UNAUTHORIZED","message":"denied","details":{}}}`},
		{name: "failure blank request ID", status: http.StatusUnauthorized, contentType: "application/json", body: `{"error":{"code":"UNAUTHORIZED","message":"denied","details":{},"request_id":"  "}}`},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", test.contentType)
				writer.WriteHeader(test.status)
				_, _ = writer.Write([]byte(test.body))
			}))
			defer server.Close()
			output := execute(application, "", "--url", server.URL, "owner", "status", "--json")
			if output.exit != ExitUnavailable || output.stderr != "" ||
				decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" {
				t.Fatalf("output = %#v", output)
			}
		})
	}
}

func TestOwnerRotateRejectsZeroCredentialAPIResponse(t *testing.T) {
	t.Setenv("MOMENT_TOKEN", "test-token")
	tests := []string{
		`{"data":{},"meta":{"request_id":"req"}}`,
		`{"data":{"token":"0123456789abcdef0123456789abcdef","initialized":true,"session_epoch":1,"initialized_at":"2026-08-10T00:00:00Z"},"meta":{"request_id":"req"}}`,
		`{"data":{"token":"0123456789abcdef0123456789abcdef","initialized":true,"session_epoch":2,"initialized_at":"2026-08-10T00:01:00Z","rotated_at":"2026-08-10T00:00:00Z"},"meta":{"request_id":"req"}}`,
	}
	for _, body := range tests {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "application/json")
			_, _ = writer.Write([]byte(body))
		}))
		output := execute(momentTestApplication(), "", "--url", server.URL, "owner", "rotate-token", "--json")
		server.Close()
		if output.exit != ExitUnavailable || output.stderr != "" ||
			decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" {
			t.Fatalf("body=%s output=%#v", body, output)
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
