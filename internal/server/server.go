// Package server owns the production net/http lifecycle and runtime serve lock.
package server

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/moment/moment/internal/runtimefs"
)

// LogFunc receives bounded lifecycle fields selected by the server.
type LogFunc func(level, event string, fields map[string]any)

// Timeouts bounds headers, request IO, idle connections, and shutdown.
type Timeouts struct {
	ReadHeader time.Duration
	Read       time.Duration
	Write      time.Duration
	Idle       time.Duration
	Shutdown   time.Duration
}

// Config defines one production server process. Listener is a deterministic
// test seam; production callers supply Address instead.
type Config struct {
	Runtime *runtimefs.Runtime
	Handler http.Handler
	// PrepareLocked runs while Import→Serve→Maintenance→Media are owned. It
	// performs crash recovery, resolves CURRENT, and installs the initial live
	// bundle without racing a direct import. The server then releases every
	// lock except Serve before opening its listener.
	PrepareLocked func(context.Context, *runtimefs.LeaseSet) error
	// AfterLock runs after serve.lock is owned and before the listener opens.
	// It is used for state that must not race offline instance mutation.
	AfterLock      func(context.Context) error
	Address        string
	Listener       net.Listener
	Timeouts       Timeouts
	MaxHeaderBytes int
	Log            LogFunc
}

// Run serves until the parent context is cancelled or the listener fails. It
// owns serve.lock for the complete listening lifetime.
func Run(ctx context.Context, config Config) (runErr error) {
	if config.Runtime == nil || config.Runtime.Locks == nil {
		return errors.New("resolved runtime is required")
	}
	if config.Handler == nil {
		return errors.New("HTTP handler is required")
	}
	if config.Address == "" {
		config.Address = "127.0.0.1:8080"
	}
	config.Timeouts = completeTimeouts(config.Timeouts)
	if config.MaxHeaderBytes <= 0 {
		config.MaxHeaderBytes = 1 << 20
	}
	if config.Log == nil {
		config.Log = func(string, string, map[string]any) {}
	}

	var held *runtimefs.LeaseSet
	var err error
	if config.PrepareLocked != nil {
		held, err = config.Runtime.Locks.AcquireSet(ctx,
			runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive},
			runtimefs.LockRequest{Name: runtimefs.ServeLock, Mode: runtimefs.Exclusive},
			runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Exclusive},
			runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Exclusive},
		)
		if err != nil {
			return fmt.Errorf("acquire startup lock set: %w", err)
		}
		if err := config.PrepareLocked(ctx, held); err != nil {
			_ = held.Release()
			return fmt.Errorf("prepare server under startup locks: %w", err)
		}
		for _, name := range []runtimefs.LockName{runtimefs.MediaLock, runtimefs.MaintenanceLock, runtimefs.ImportLock} {
			if err := held.ReleaseLock(name); err != nil {
				_ = held.Release()
				return fmt.Errorf("release startup %s lock: %w", name, err)
			}
		}
	} else {
		held, err = config.Runtime.Locks.AcquireSet(ctx, runtimefs.LockRequest{Name: runtimefs.ServeLock, Mode: runtimefs.Exclusive})
		if err != nil {
			return fmt.Errorf("acquire serve lock: %w", err)
		}
		if config.AfterLock != nil {
			if err := config.AfterLock(ctx); err != nil {
				_ = held.Release()
				return fmt.Errorf("prepare server after acquiring serve lock: %w", err)
			}
		}
	}
	defer func() { runErr = errors.Join(runErr, held.Release()) }()

	listener := config.Listener
	if listener == nil {
		listener, err = net.Listen("tcp", config.Address)
		if err != nil {
			return fmt.Errorf("listen on %s: %w", config.Address, err)
		}
	}

	requestContext, cancelRequests := context.WithCancel(context.Background())
	defer cancelRequests()
	httpServer := &http.Server{
		Handler:           config.Handler,
		ReadHeaderTimeout: config.Timeouts.ReadHeader,
		ReadTimeout:       config.Timeouts.Read,
		WriteTimeout:      config.Timeouts.Write,
		IdleTimeout:       config.Timeouts.Idle,
		MaxHeaderBytes:    config.MaxHeaderBytes,
		BaseContext: func(net.Listener) context.Context {
			return requestContext
		},
		ErrorLog: log.New(io.Discard, "", 0),
	}
	httpServer.RegisterOnShutdown(cancelRequests)

	address := listener.Addr().String()
	config.Log("info", "server_started", map[string]any{"address": address})
	if !isLoopbackListener(listener.Addr()) {
		config.Log("warn", "server_public_bind", map[string]any{
			"address": address,
			"warning": "configure a TLS reverse proxy before exposing Moment",
		})
	}
	serveResult := make(chan error, 1)
	go func() {
		serveResult <- httpServer.Serve(listener)
	}()

	select {
	case err := <-serveResult:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve HTTP: %w", err)
	case <-ctx.Done():
		config.Log("info", "server_stopping", map[string]any{"reason": "context_cancelled"})
		shutdownContext, cancelShutdown := context.WithTimeout(context.Background(), config.Timeouts.Shutdown)
		defer cancelShutdown()
		if err := httpServer.Shutdown(shutdownContext); err != nil {
			_ = httpServer.Close()
			return fmt.Errorf("shutdown HTTP server: %w", err)
		}
		if err := <-serveResult; err != nil && !errors.Is(err, http.ErrServerClosed) {
			return fmt.Errorf("serve HTTP during shutdown: %w", err)
		}
		config.Log("info", "server_stopped", map[string]any{"reason": "context_cancelled"})
		return nil
	}
}

func completeTimeouts(timeouts Timeouts) Timeouts {
	if timeouts.ReadHeader == 0 {
		timeouts.ReadHeader = 5 * time.Second
	}
	if timeouts.Read == 0 {
		timeouts.Read = 30 * time.Second
	}
	if timeouts.Write == 0 {
		timeouts.Write = 30 * time.Second
	}
	if timeouts.Idle == 0 {
		timeouts.Idle = 2 * time.Minute
	}
	if timeouts.Shutdown == 0 {
		timeouts.Shutdown = 10 * time.Second
	}
	return timeouts
}

func isLoopbackListener(address net.Addr) bool {
	tcpAddress, ok := address.(*net.TCPAddr)
	return ok && tcpAddress.IP.IsLoopback()
}
