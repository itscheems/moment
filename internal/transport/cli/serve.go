package cli

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/moment/moment/internal/runtimefs"
	"github.com/moment/moment/internal/server"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	ginhttp "github.com/moment/moment/internal/transport/http/gin"
	"github.com/moment/moment/internal/webassets"
	"github.com/spf13/pflag"
)

func registerServe(application *Application) {
	application.MustRegisterLeaf(LeafSpec{
		Path:        []string{"serve"},
		Short:       "Run the Moment HTTP server",
		Long:        "Run the bounded Moment HTTP server until the process receives a cancellation signal.",
		Inputs:      "An initialized local data directory. --listen defaults to 127.0.0.1:8080; repeat --trusted-proxy only for explicit proxy CIDRs.",
		Output:      "Lifecycle logs are written to stderr in --log-format text or json. HTTP responses use the shared API envelope.",
		Errors:      "Uninitialized data, invalid proxy configuration, bind failures, and cancellation use stable CLI exit behavior.",
		Safety:      "Only direct mode is supported. Non-loopback binds emit a TLS reverse proxy warning.",
		Example:     "moment --data-dir ./moment-data serve --log-format json",
		Capability:  CapabilityDirectOnly,
		LongRunning: true,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("listen", "127.0.0.1:8080", "HTTP listen address")
			flags.StringArray("trusted-proxy", nil, "trusted proxy IP or CIDR (repeatable; disabled by default)")
		},
		Run: runServe,
	})
}

func runServe(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	target, err := invocation.DirectTransport()
	if err != nil {
		return Result{}, err
	}
	listenAddress, err := input.String("listen")
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(listenAddress) == "" {
		return Result{}, invalidArgument("--listen cannot be empty", "use an address such as 127.0.0.1:8080")
	}
	trustedProxies, err := input.StringArray("trusted-proxy")
	if err != nil {
		return Result{}, err
	}
	runtime, err := runtimefs.OpenContext(ctx, target.DataDirectory())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return Result{}, err
		}
		return Result{}, NewCommandError(
			"INSTANCE_NOT_READY",
			"the Moment data directory is not initialized and ready",
			"initialize or repair the selected --data-dir before running serve",
			ExitIO,
		).WithCause(err)
	}
	database, err := sqlitestore.Open(ctx, runtime, sqlitestore.Options{})
	if err != nil {
		return Result{}, NewCommandError(
			"DATABASE_NOT_READY",
			"the current Moment database could not become ready",
			"inspect the selected data directory and migration history, then retry",
			ExitIntegrity,
		).WithCause(err)
	}
	defer func() { _ = database.Close() }()
	assets, err := webassets.Public()
	if err != nil {
		return Result{}, NewCommandError(
			"WEB_ASSETS_UNAVAILABLE",
			"the embedded web application could not be opened",
			"rebuild the Moment binary and retry",
			ExitInternal,
		).WithCause(err)
	}
	engine, err := ginhttp.New(ginhttp.Config{
		Assets:         assets,
		Readiness:      database.Ready,
		TrustedProxies: trustedProxies,
		Log:            invocation.LogEvent,
	})
	if err != nil {
		return Result{}, invalidArgument(
			fmt.Sprintf("invalid HTTP server configuration: %v", err),
			"check --trusted-proxy values and positive request limits",
		)
	}
	if err := server.Run(ctx, server.Config{
		Runtime: runtime,
		Handler: engine,
		Address: listenAddress,
		Log:     invocation.LogEvent,
	}); err != nil {
		return Result{}, err
	}
	return Result{
		Data: map[string]any{"stopped": true},
		Text: "Moment server stopped",
	}, nil
}
