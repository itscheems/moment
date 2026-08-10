package server

import (
	"context"
	"errors"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/moment/moment/internal/runtimefs"
)

func TestCancellationStopsRequestsAndReleasesServeLock(t *testing.T) {
	runtime, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	requestStarted := make(chan struct{})
	requestCancelled := make(chan struct{})
	handler := http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(requestStarted)
		<-request.Context().Done()
		close(requestCancelled)
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	var logMu sync.Mutex
	var events []string
	ctx, cancel := context.WithCancel(context.Background())
	runResult := make(chan error, 1)
	go func() {
		runResult <- Run(ctx, Config{
			Runtime:  runtime,
			Handler:  handler,
			Listener: listener,
			Timeouts: Timeouts{Shutdown: time.Second},
			Log: func(_ string, event string, _ map[string]any) {
				logMu.Lock()
				events = append(events, event)
				logMu.Unlock()
			},
		})
	}()

	responseResult := make(chan error, 1)
	go func() {
		response, err := http.Get("http://" + listener.Addr().String() + "/api/v1/wait")
		if err == nil {
			_ = response.Body.Close()
		}
		responseResult <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(2 * time.Second):
		t.Fatal("request did not start")
	}

	lockContext, cancelLock := context.WithTimeout(context.Background(), 30*time.Millisecond)
	_, lockErr := runtime.Locks.Acquire(lockContext, runtimefs.ServeLock, runtimefs.Exclusive)
	cancelLock()
	if !errors.Is(lockErr, context.DeadlineExceeded) {
		t.Fatalf("serve lock was not held: %v", lockErr)
	}

	cancel()
	select {
	case <-requestCancelled:
	case <-time.After(2 * time.Second):
		t.Fatal("request context was not cancelled")
	}
	select {
	case err := <-runResult:
		if err != nil {
			t.Fatalf("Run returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("server did not stop")
	}
	<-responseResult

	lockContext, cancelLock = context.WithTimeout(context.Background(), time.Second)
	lease, err := runtime.Locks.Acquire(lockContext, runtimefs.ServeLock, runtimefs.Exclusive)
	cancelLock()
	if err != nil {
		t.Fatalf("serve lock was not released: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	logMu.Lock()
	defer logMu.Unlock()
	want := []string{"server_started", "server_stopping", "server_stopped"}
	if len(events) < len(want) {
		t.Fatalf("events = %#v", events)
	}
	for index, event := range want {
		if events[index] != event {
			t.Fatalf("events = %#v", events)
		}
	}
}

func TestDefaultTimeoutsAreBounded(t *testing.T) {
	timeouts := completeTimeouts(Timeouts{})
	if timeouts.ReadHeader <= 0 || timeouts.Read <= 0 || timeouts.Write <= 0 || timeouts.Idle <= 0 || timeouts.Shutdown <= 0 {
		t.Fatalf("timeouts = %#v", timeouts)
	}
}

func TestLoopbackListenerDetection(t *testing.T) {
	if !isLoopbackListener(&net.TCPAddr{IP: net.ParseIP("127.0.0.1"), Port: 8080}) {
		t.Fatal("loopback address was treated as public")
	}
	if isLoopbackListener(&net.TCPAddr{IP: net.IPv4zero, Port: 8080}) {
		t.Fatal("unspecified public bind was treated as loopback")
	}
}
