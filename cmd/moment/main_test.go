package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/runtimefs"
	"github.com/moment/moment/internal/webassets"
)

func TestServeBinaryStructuredLogsAndGracefulSignal(t *testing.T) {
	dataDir := filepath.Join(t.TempDir(), "data")
	binary := os.Getenv("MOMENT_SMOKE_BINARY")
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "moment")
		build := exec.Command("go", "build", "-trimpath", "-o", binary, ".")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build binary: %v\n%s", err, output)
		}
	} else {
		absoluteBinary, err := filepath.Abs(binary)
		if err != nil {
			t.Fatal(err)
		}
		binary = absoluteBinary
	}
	initCommand := exec.Command(binary, "init", "--username", "writer", "--data-dir", dataDir, "--json")
	initOutput, err := initCommand.Output()
	if err != nil {
		t.Fatalf("account init: %v", err)
	}
	var initialized struct {
		Data struct {
			Token      string `json:"token"`
			Credential struct {
				ID string `json:"id"`
			} `json:"credential"`
		} `json:"data"`
	}
	if err := json.Unmarshal(initOutput, &initialized); err != nil {
		t.Fatalf("decode account init: %v; output=%s", err, initOutput)
	}
	if initialized.Data.Token == "" || initialized.Data.Credential.ID == "" {
		t.Fatalf("account init result = %#v", initialized.Data)
	}
	originalToken := initialized.Data.Token
	instance, err := runtimefs.Open(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	address := availableAddress(t)
	command := exec.Command(binary,
		"--data-dir", dataDir,
		"serve",
		"--listen", address,
		"--log-format", "json",
	)
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	configureInterruptProcess(command)
	if err := command.Start(); err != nil {
		t.Fatal(err)
	}
	stopped := false
	defer func() {
		if !stopped && command.Process != nil {
			_ = command.Process.Kill()
			_, _ = command.Process.Wait()
		}
	}()

	client := &http.Client{Timeout: 200 * time.Millisecond}
	deadline := time.Now().Add(5 * time.Second)
	for {
		response, requestErr := client.Get("http://" + address + "/healthz")
		if requestErr == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				break
			}
		}
		if time.Now().After(deadline) {
			_ = command.Process.Kill()
			_ = command.Wait()
			stopped = true
			t.Fatalf("server did not become healthy: %v; stderr=%s", requestErr, stderr.String())
		}
		time.Sleep(20 * time.Millisecond)
	}
	readyResponse, err := client.Get("http://" + address + "/readyz")
	if err != nil {
		t.Fatalf("readiness request failed: %v", err)
	}
	_ = readyResponse.Body.Close()
	if readyResponse.StatusCode != http.StatusOK {
		t.Fatalf("readiness status = %d, want %d; stderr=%s", readyResponse.StatusCode, http.StatusOK, stderr.String())
	}
	shellResponse, err := client.Get("http://" + address + "/")
	if err != nil {
		t.Fatalf("embedded Web request failed: %v", err)
	}
	shellBody, readErr := io.ReadAll(io.LimitReader(shellResponse.Body, (1<<20)+1))
	_ = shellResponse.Body.Close()
	if readErr != nil {
		t.Fatal(readErr)
	}
	expectedShell, err := webassets.Public()
	if err != nil {
		t.Fatal(err)
	}
	if shellResponse.StatusCode != http.StatusOK || !bytes.Contains(shellBody, []byte(`<div id="root"></div>`)) ||
		!strings.HasPrefix(shellResponse.Header.Get("Content-Type"), "text/html") ||
		!strings.Contains(shellResponse.Header.Get("Content-Security-Policy"), "default-src 'self'") {
		t.Fatalf("embedded Web shell status=%d content-type=%q CSP=%q root=%v", shellResponse.StatusCode, shellResponse.Header.Get("Content-Type"), shellResponse.Header.Get("Content-Security-Policy"), bytes.Contains(shellBody, []byte(`<div id="root"></div>`)))
	}
	if err := fs.WalkDir(expectedShell, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || path == "index.html" {
			return walkErr
		}
		expectedAsset, readErr := fs.ReadFile(expectedShell, path)
		if readErr != nil {
			return readErr
		}
		assetResponse, requestErr := client.Get("http://" + address + "/" + path)
		if requestErr != nil {
			return requestErr
		}
		actualAsset, readErr := io.ReadAll(io.LimitReader(assetResponse.Body, int64(len(expectedAsset))+1))
		_ = assetResponse.Body.Close()
		if readErr != nil {
			return readErr
		}
		if assetResponse.StatusCode != http.StatusOK || !bytes.Equal(actualAsset, expectedAsset) ||
			assetResponse.Header.Get("Content-Type") == "" || assetResponse.Header.Get("X-Content-Type-Options") != "nosniff" {
			t.Fatalf("embedded asset %s status=%d content-type=%q bytes_match=%v", path, assetResponse.StatusCode, assetResponse.Header.Get("Content-Type"), bytes.Equal(actualAsset, expectedAsset))
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	directCreate := exec.Command(binary, "post", "create", "--as", "writer", "--data-dir", dataDir, "--content", "binary parity", "--idempotency-key", "binary-post-001", "--json")
	directCreateOutput, err := directCreate.Output()
	if err != nil {
		t.Fatalf("direct post create: %v output=%s", err, directCreateOutput)
	}
	var directPost struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(directCreateOutput, &directPost); err != nil {
		t.Fatalf("decode direct post create: %v output=%s", err, directCreateOutput)
	}
	postID, _ := directPost.Data["id"].(string)
	// kind is on the wire now that a post can be a moment or an article.
	if postID == "" || directPost.Data["kind"] != "moment" {
		t.Fatalf("direct post DTO = %#v", directPost.Data)
	}
	apiGetPost := exec.Command(binary, "--url", "http://"+address, "post", "get", postID, "--json")
	apiGetOutput, err := apiGetPost.Output()
	if err != nil {
		t.Fatalf("API post get: %v output=%s", err, apiGetOutput)
	}
	var apiPost struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(apiGetOutput, &apiPost); err != nil || !reflect.DeepEqual(directPost.Data, apiPost.Data) {
		t.Fatalf("direct/API get parity: decode=%v direct=%#v API=%#v", err, directPost.Data, apiPost.Data)
	}
	apiUpdatePost := exec.Command(binary, "--url", "http://"+address, "post", "update", postID, "--content", "binary API update", "--json")
	apiUpdatePost.Env = append(os.Environ(), "MOMENT_TOKEN="+originalToken)
	apiUpdateOutput, err := apiUpdatePost.Output()
	if err != nil {
		t.Fatalf("API post update: %v output=%s", err, apiUpdateOutput)
	}
	var updatedPost struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(apiUpdateOutput, &updatedPost); err != nil || updatedPost.Data["content_md"] != "binary API update" {
		t.Fatalf("decode API post update: %v output=%s", err, apiUpdateOutput)
	}
	directGetPost := exec.Command(binary, "post", "get", postID, "--data-dir", dataDir, "--json")
	directGetOutput, err := directGetPost.Output()
	if err != nil {
		t.Fatalf("direct post get: %v output=%s", err, directGetOutput)
	}
	var directUpdated struct {
		Data map[string]any `json:"data"`
	}
	if err := json.Unmarshal(directGetOutput, &directUpdated); err != nil || !reflect.DeepEqual(updatedPost.Data, directUpdated.Data) {
		t.Fatalf("API/direct update parity: decode=%v API=%#v direct=%#v", err, updatedPost.Data, directUpdated.Data)
	}

	directRotate := exec.Command(binary, "auth", "create-token", "--username", "writer", "--data-dir", dataDir, "--json")
	directOutput, directErr := directRotate.Output()
	var directExit *exec.ExitError
	if !errors.As(directErr, &directExit) || directExit.ExitCode() != 4 || !strings.Contains(string(directOutput), `"code":"INSTANCE_IN_USE"`) {
		t.Fatalf("direct rotation while serving err=%v output=%s", directErr, directOutput)
	}

	apiStatus := exec.Command(binary, "--url", "http://"+address, "auth", "status", "--json")
	apiStatus.Env = append(os.Environ(), "MOMENT_TOKEN="+originalToken)
	statusOutput, err := apiStatus.Output()
	if err != nil || !strings.Contains(string(statusOutput), `"password_enabled":false`) {
		t.Fatalf("API status err=%v output=%s", err, statusOutput)
	}
	apiRotate := exec.Command(binary, "--url", "http://"+address, "auth", "create-token", "--json")
	apiRotate.Env = append(os.Environ(), "MOMENT_TOKEN="+originalToken)
	rotationOutput, err := apiRotate.Output()
	if err != nil {
		t.Fatalf("API rotation: %v output=%s", err, rotationOutput)
	}
	var rotated struct {
		Data struct {
			Token      string `json:"token"`
			Credential struct {
				ID string `json:"id"`
			} `json:"credential"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rotationOutput, &rotated); err != nil {
		t.Fatalf("decode API rotation: %v; output=%s", err, rotationOutput)
	}
	if rotated.Data.Token == "" || rotated.Data.Token == originalToken || rotated.Data.Credential.ID == "" {
		t.Fatalf("API rotation result = %#v", rotated.Data)
	}
	revoke := exec.Command(binary, "--url", "http://"+address, "auth", "revoke-token", initialized.Data.Credential.ID, "--yes", "--json")
	revoke.Env = append(os.Environ(), "MOMENT_TOKEN="+rotated.Data.Token)
	if output, err := revoke.Output(); err != nil {
		t.Fatalf("revoke original credential: %v %s", err, output)
	}
	request, err := http.NewRequest(http.MethodGet, "http://"+address+"/api/v1/account/security", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+originalToken)
	oldResponse, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = oldResponse.Body.Close()
	if oldResponse.StatusCode != http.StatusUnauthorized {
		t.Fatalf("old token status = %d", oldResponse.StatusCode)
	}
	request, err = http.NewRequest(http.MethodGet, "http://"+address+"/api/v1/account/security", nil)
	if err != nil {
		t.Fatal(err)
	}
	request.Header.Set("Authorization", "Bearer "+rotated.Data.Token)
	newResponse, err := client.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	_ = newResponse.Body.Close()
	if newResponse.StatusCode != http.StatusOK {
		t.Fatalf("new token status = %d", newResponse.StatusCode)
	}
	if err := interruptProcess(command); err != nil {
		t.Fatal(err)
	}
	waitResult := make(chan error, 1)
	go func() { waitResult <- command.Wait() }()
	select {
	case err := <-waitResult:
		stopped = true
		if err != nil {
			t.Fatalf("server exit: %v; stdout=%s stderr=%s", err, stdout.String(), stderr.String())
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop after interrupt")
	}
	if stdout.String() != "Moment server stopped\n" {
		t.Fatalf("stdout = %q", stdout.String())
	}
	events := map[string]bool{}
	for lineNumber, line := range strings.Split(strings.TrimSpace(stderr.String()), "\n") {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("log line %d is not JSON: %v; %q", lineNumber+1, err, line)
		}
		name, _ := event["event"].(string)
		events[name] = true
	}
	for _, name := range []string{"server_started", "http_request", "server_stopping", "server_stopped"} {
		if !events[name] {
			t.Fatalf("missing %s log: %s", name, stderr.String())
		}
	}
	if strings.Contains(stderr.String(), originalToken) || strings.Contains(stderr.String(), rotated.Data.Token) {
		t.Fatalf("server logs leaked account token: %s", stderr.String())
	}

	lockContext, cancelLock := context.WithTimeout(context.Background(), time.Second)
	lease, err := instance.Locks.Acquire(lockContext, runtimefs.ServeLock, runtimefs.Exclusive)
	cancelLock()
	if err != nil {
		t.Fatalf("serve lock remained held: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestExplicitInitializationAndBrokenPipeBinaryContracts(t *testing.T) {
	binary := os.Getenv("MOMENT_SMOKE_BINARY")
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "moment")
		build := exec.Command("go", "build", "-trimpath", "-o", binary, ".")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build binary: %v\n%s", err, output)
		}
	}

	runJSON := func(args ...string) (map[string]any, []byte, error) {
		t.Helper()
		command := exec.Command(binary, args...)
		output, err := command.Output()
		if len(output) == 0 || output[len(output)-1] != '\n' {
			t.Fatalf("%v did not write one newline-terminated JSON document: %q", args, output)
		}
		decoder := json.NewDecoder(bytes.NewReader(output))
		var document map[string]any
		if decodeErr := decoder.Decode(&document); decodeErr != nil {
			t.Fatalf("decode %v: %v output=%q", args, decodeErr, output)
		}
		var extra any
		if decodeErr := decoder.Decode(&extra); !errors.Is(decodeErr, io.EOF) {
			t.Fatalf("%v wrote extra JSON: %v output=%q", args, decodeErr, output)
		}
		return document, output, err
	}

	aliasDir := filepath.Join(t.TempDir(), "alias")
	alias, _, err := runJSON("init", "--username", "writer", "--data-dir", aliasDir, "--json")
	if err != nil || alias["ok"] != true {
		t.Fatalf("init alias err=%v document=%#v", err, alias)
	}
	credential := alias["data"].(map[string]any)
	if credential["credential"] == nil || credential["token"] == "" {
		t.Fatalf("init credential = %#v", credential)
	}
	duplicate, _, err := runJSON("init", "--username", "writer", "--data-dir", aliasDir, "--json")
	var duplicateExit *exec.ExitError
	if !errors.As(err, &duplicateExit) || duplicateExit.ExitCode() != 4 ||
		duplicate["error"].(map[string]any)["code"] != "SIGNING_ALREADY_INITIALIZED" {
		t.Fatalf("duplicate err=%v document=%#v", err, duplicate)
	}
	unsupported, _, err := runJSON("--url", "http://127.0.0.1:1", "init", "--json")
	var unsupportedExit *exec.ExitError
	if !errors.As(err, &unsupportedExit) || unsupportedExit.ExitCode() != 2 ||
		unsupported["error"].(map[string]any)["code"] != "MODE_UNSUPPORTED" {
		t.Fatalf("unsupported err=%v document=%#v", err, unsupported)
	}

	if runtime.GOOS == "windows" {
		return
	}
	assertBrokenPipe := func(wantExit int, args ...string) {
		t.Helper()
		readEnd, writeEnd, pipeErr := os.Pipe()
		if pipeErr != nil {
			t.Fatal(pipeErr)
		}
		if pipeErr := readEnd.Close(); pipeErr != nil {
			t.Fatal(pipeErr)
		}
		command := exec.Command(binary, args...)
		var stderr bytes.Buffer
		command.Stdout = writeEnd
		command.Stderr = &stderr
		runErr := command.Run()
		_ = writeEnd.Close()
		var exitError *exec.ExitError
		if !errors.As(runErr, &exitError) || exitError.ExitCode() != wantExit {
			t.Fatalf("broken pipe %v err=%v stderr=%q", args, runErr, stderr.String())
		}
		if strings.Contains(stderr.String(), "account-token") || strings.Contains(stderr.String(), credential["token"].(string)) {
			t.Fatalf("broken pipe %v leaked sensitive text: %q", args, stderr.String())
		}
	}
	assertBrokenPipe(10, "backup", "import", "--help")
	assertBrokenPipe(6, "backup", "export", "--data-dir", aliasDir, "--stdout")
}

func availableAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}
