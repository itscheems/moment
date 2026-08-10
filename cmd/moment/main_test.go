package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/runtimefs"
)

func TestServeBinaryStructuredLogsAndGracefulSignal(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("os.Interrupt is not supported for child processes on Windows")
	}
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
	initCommand := exec.Command(binary, "owner", "init", "--data-dir", dataDir, "--json")
	initOutput, err := initCommand.Output()
	if err != nil {
		t.Fatalf("owner init: %v", err)
	}
	var initialized struct {
		Data struct {
			Token        string `json:"token"`
			SessionEpoch uint64 `json:"session_epoch"`
		} `json:"data"`
	}
	if err := json.Unmarshal(initOutput, &initialized); err != nil {
		t.Fatalf("decode owner init: %v; output=%s", err, initOutput)
	}
	if initialized.Data.Token == "" || initialized.Data.SessionEpoch != 1 {
		t.Fatalf("owner init result = %#v", initialized.Data)
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

	directRotate := exec.Command(binary, "owner", "rotate-token", "--data-dir", dataDir, "--json")
	directOutput, directErr := directRotate.Output()
	var directExit *exec.ExitError
	if !errors.As(directErr, &directExit) || directExit.ExitCode() != 4 || !strings.Contains(string(directOutput), `"code":"INSTANCE_IN_USE"`) {
		t.Fatalf("direct rotation while serving err=%v output=%s", directErr, directOutput)
	}

	apiStatus := exec.Command(binary, "--url", "http://"+address, "owner", "status", "--json")
	apiStatus.Env = append(os.Environ(), "MOMENT_TOKEN="+originalToken)
	statusOutput, err := apiStatus.Output()
	if err != nil || !strings.Contains(string(statusOutput), `"session_epoch":1`) {
		t.Fatalf("API status err=%v output=%s", err, statusOutput)
	}
	apiRotate := exec.Command(binary, "--url", "http://"+address, "owner", "rotate-token", "--json")
	apiRotate.Env = append(os.Environ(), "MOMENT_TOKEN="+originalToken)
	rotationOutput, err := apiRotate.Output()
	if err != nil {
		t.Fatalf("API rotation: %v output=%s", err, rotationOutput)
	}
	var rotated struct {
		Data struct {
			Token        string `json:"token"`
			SessionEpoch uint64 `json:"session_epoch"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rotationOutput, &rotated); err != nil {
		t.Fatalf("decode API rotation: %v; output=%s", err, rotationOutput)
	}
	if rotated.Data.Token == "" || rotated.Data.Token == originalToken || rotated.Data.SessionEpoch != 2 {
		t.Fatalf("API rotation result = %#v", rotated.Data)
	}
	request, err := http.NewRequest(http.MethodGet, "http://"+address+"/api/v1/owner/status", nil)
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
	request, err = http.NewRequest(http.MethodGet, "http://"+address+"/api/v1/owner/status", nil)
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
	if err := command.Process.Signal(os.Interrupt); err != nil {
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
		t.Fatalf("server logs leaked owner token: %s", stderr.String())
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
