package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSwitchboardMaintenanceDrainsAndSwapsBundle(t *testing.T) {
	board := NewSwitchboard()
	entered := make(chan struct{})
	release := make(chan struct{})
	old := http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		_, _ = writer.Write([]byte("old"))
	})
	if err := board.Swap(old); err != nil {
		t.Fatal(err)
	}
	board.EndMaintenance()
	done := make(chan struct{})
	go func() {
		board.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil))
		close(done)
	}()
	<-entered
	maintenance := make(chan error, 1)
	go func() { maintenance <- board.BeginMaintenance(context.Background()) }()
	select {
	case err := <-maintenance:
		t.Fatalf("maintenance returned before admitted request drained: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	rejected := httptest.NewRecorder()
	board.ServeHTTP(rejected, httptest.NewRequest(http.MethodPost, "/api/v1/posts", strings.NewReader("{}")))
	if rejected.Code != http.StatusServiceUnavailable || !strings.Contains(rejected.Body.String(), "MAINTENANCE_MODE") {
		t.Fatalf("maintenance response=%d %s", rejected.Code, rejected.Body.String())
	}
	ready := httptest.NewRecorder()
	board.ServeHTTP(ready, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable || !strings.Contains(ready.Body.String(), "NOT_READY") {
		t.Fatalf("ready response=%d %s", ready.Code, ready.Body.String())
	}
	close(release)
	<-done
	if err := <-maintenance; err != nil {
		t.Fatal(err)
	}
	if err := board.Swap(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) { _, _ = writer.Write([]byte("new")) })); err != nil {
		t.Fatal(err)
	}
	board.EndMaintenance()
	next := httptest.NewRecorder()
	board.ServeHTTP(next, httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil))
	if next.Body.String() != "new" {
		t.Fatalf("swapped response=%q", next.Body.String())
	}
}

func TestSwitchboardCommitAndHealthBypassMaintenance(t *testing.T) {
	board := NewSwitchboard()
	if err := board.Swap(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusAccepted)
	})); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{
		"/healthz",
		"/api/v1/backups/import/commit",
		"/api/v1/backups/import/restore",
		"/api/v1/backups/import/merge/commit",
		"/api/v1/backups/import/review/replace",
		"/api/v1/backups/import/status",
	} {
		response := httptest.NewRecorder()
		board.ServeHTTP(response, httptest.NewRequest(http.MethodPost, path, nil))
		if response.Code != http.StatusAccepted {
			t.Fatalf("bypass %s status=%d", path, response.Code)
		}
	}
}

func TestSwitchboardDrainsReadinessBeforeDatabaseClosure(t *testing.T) {
	board := NewSwitchboard()
	entered := make(chan struct{})
	release := make(chan struct{})
	if err := board.Swap(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/readyz" {
			close(entered)
			<-release
		}
		writer.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatal(err)
	}
	board.EndMaintenance()
	go board.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/readyz", nil))
	<-entered
	maintenance := make(chan error, 1)
	go func() { maintenance <- board.BeginMaintenance(context.Background()) }()
	select {
	case err := <-maintenance:
		t.Fatalf("maintenance returned while readiness still used the database: %v", err)
	case <-time.After(20 * time.Millisecond):
	}
	close(release)
	if err := <-maintenance; err != nil {
		t.Fatal(err)
	}
}

func TestSwitchboardCancelledDrainRestoresAdmission(t *testing.T) {
	board := NewSwitchboard()
	entered := make(chan struct{})
	release := make(chan struct{})
	if err := board.Swap(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/slow" {
			close(entered)
			<-release
		}
		writer.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatal(err)
	}
	board.EndMaintenance()
	go board.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/slow", nil))
	<-entered
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := board.BeginMaintenance(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled maintenance error=%v", err)
	}
	if board.InMaintenance() {
		t.Fatal("cancelled maintenance left admission closed")
	}
	response := httptest.NewRecorder()
	board.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if response.Code != http.StatusNoContent {
		t.Fatalf("restored admission status=%d", response.Code)
	}
	close(release)
}

func TestReviewedReplacementDoesNotDrainItsOwnRequest(t *testing.T) {
	board := NewSwitchboard()
	if err := board.Swap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := board.BeginMaintenance(r.Context()); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		board.EndMaintenance()
		w.WriteHeader(http.StatusNoContent)
	})); err != nil {
		t.Fatal(err)
	}
	board.EndMaintenance()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	response := httptest.NewRecorder()
	board.ServeHTTP(response, httptest.NewRequest(http.MethodPost, "/api/v1/backups/import/review/replace", nil).WithContext(ctx))
	if response.Code != http.StatusNoContent {
		t.Fatalf("review replacement waited on its own admitted request: %d %s", response.Code, response.Body.String())
	}
}

func TestSwitchboardCancelsNotificationStreamsBeforeMaintenanceDrain(t *testing.T) {
	board := NewSwitchboard()
	entered := make(chan struct{})
	finished := make(chan struct{})
	if err := board.Swap(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		close(entered)
		<-request.Context().Done()
		close(finished)
	})); err != nil {
		t.Fatal(err)
	}
	board.EndMaintenance()
	go board.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/api/v1/account/notifications/stream", nil))
	<-entered
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := board.BeginMaintenance(ctx); err != nil {
		t.Fatal(err)
	}
	select {
	case <-finished:
	default:
		t.Fatal("maintenance did not close the stream")
	}
}
