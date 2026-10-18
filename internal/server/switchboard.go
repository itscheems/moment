package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"sync"
)

// Switchboard is the stable outer HTTP handler used while import swaps the
// complete live application bundle. Ordinary admitted requests drain before
// BeginMaintenance returns; commit/status/health remain reachable.
type Switchboard struct {
	mu          sync.Mutex
	changed     chan struct{}
	handler     http.Handler
	active      int
	maintenance bool
}

// NewSwitchboard starts without a live bundle and therefore in maintenance.
func NewSwitchboard() *Switchboard {
	return &Switchboard{changed: make(chan struct{}), maintenance: true}
}

// Swap atomically selects a fully built handler.
func (board *Switchboard) Swap(handler http.Handler) error {
	if board == nil || handler == nil {
		return errors.New("live HTTP handler is required")
	}
	board.mu.Lock()
	board.handler = handler
	board.signalLocked()
	board.mu.Unlock()
	return nil
}

// BeginMaintenance closes admission and waits for all previously admitted
// ordinary requests to return.
func (board *Switchboard) BeginMaintenance(ctx context.Context) error {
	if board == nil {
		return errors.New("HTTP switchboard is required")
	}
	board.mu.Lock()
	previous := board.maintenance
	board.maintenance = true
	board.signalLocked()
	for board.active > 0 {
		changed := board.changed
		board.mu.Unlock()
		select {
		case <-ctx.Done():
			board.mu.Lock()
			board.maintenance = previous
			board.signalLocked()
			board.mu.Unlock()
			return ctx.Err()
		case <-changed:
		}
		board.mu.Lock()
	}
	board.mu.Unlock()
	return nil
}

// EndMaintenance reopens admission after the replacement bundle is ready (or
// the old bundle has been restored).
func (board *Switchboard) EndMaintenance() {
	if board == nil {
		return
	}
	board.mu.Lock()
	board.maintenance = false
	board.signalLocked()
	board.mu.Unlock()
}

// InMaintenance reports the current admission state.
func (board *Switchboard) InMaintenance() bool {
	if board == nil {
		return true
	}
	board.mu.Lock()
	defer board.mu.Unlock()
	return board.maintenance
}

func (board *Switchboard) ServeHTTP(writer http.ResponseWriter, request *http.Request) {
	board.mu.Lock()
	handler := board.handler
	bypass := maintenanceBypass(request.URL.Path)
	if handler == nil {
		board.mu.Unlock()
		maintenanceFailure(writer, request, "NOT_READY", "application dependencies are not ready")
		return
	}
	if board.maintenance && request.URL.Path == "/readyz" {
		board.mu.Unlock()
		maintenanceFailure(writer, request, "NOT_READY", "application maintenance is in progress")
		return
	}
	if board.maintenance && !bypass {
		board.mu.Unlock()
		maintenanceFailure(writer, request, "MAINTENANCE_MODE", "application maintenance is in progress")
		return
	}
	if !bypass {
		board.active++
	}
	board.mu.Unlock()
	if !bypass {
		defer func() {
			board.mu.Lock()
			board.active--
			board.signalLocked()
			board.mu.Unlock()
		}()
	}
	handler.ServeHTTP(writer, request)
}

func maintenanceBypass(path string) bool {
	return path == "/healthz" || path == "/api/v1/backups/import/commit" || path == "/api/v1/backups/import/restore" ||
		path == "/api/v1/backups/import/merge/commit" || path == "/api/v1/backups/import/review/replace" || path == "/api/v1/backups/import/status"
}

func (board *Switchboard) signalLocked() {
	close(board.changed)
	board.changed = make(chan struct{})
}

func maintenanceFailure(writer http.ResponseWriter, request *http.Request, code, message string) {
	requestID := request.Header.Get("X-Request-ID")
	if requestID == "" {
		var value [8]byte
		if _, err := rand.Read(value[:]); err == nil {
			requestID = "req_" + hex.EncodeToString(value[:])
		} else {
			requestID = "req_unavailable"
		}
	}
	writer.Header().Set("Content-Type", "application/json; charset=utf-8")
	writer.Header().Set("Cache-Control", "no-store")
	writer.Header().Set("X-Request-ID", requestID)
	writer.WriteHeader(http.StatusServiceUnavailable)
	_ = json.NewEncoder(writer).Encode(map[string]any{"error": map[string]any{
		"code": code, "message": message, "details": map[string]any{}, "request_id": requestID,
	}})
}
