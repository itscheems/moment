package main

import (
	"bytes"
	"context"
	"encoding/json"
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
	instance, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	binary := os.Getenv("MOMENT_SMOKE_BINARY")
	if binary == "" {
		binary = filepath.Join(t.TempDir(), "moment")
		build := exec.Command("go", "build", "-trimpath", "-o", binary, ".")
		if output, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build binary: %v\n%s", err, output)
		}
	} else if binary, err = filepath.Abs(binary); err != nil {
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
