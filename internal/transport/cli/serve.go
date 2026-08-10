package cli

import (
	"context"
	"errors"
	"strings"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/buildinfo"
	"github.com/moment/moment/internal/runtimefs"
	"github.com/moment/moment/internal/server"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	"github.com/moment/moment/internal/webassets"
	"github.com/spf13/pflag"
)

func registerServe(application *Application, info buildinfo.Info) {
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
		Run: func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
			return runServe(ctx, invocation, input, info)
		},
	})
}

func runServe(ctx context.Context, invocation Invocation, input CommandInput, info buildinfo.Info) (Result, error) {
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
	runtime, err := runtimefs.OpenForCoordination(target.DataDirectory())
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
	assets, err := webassets.Public()
	if err != nil {
		return Result{}, NewCommandError(
			"WEB_ASSETS_UNAVAILABLE",
			"the embedded web application could not be opened",
			"rebuild the Moment binary and retry",
			ExitInternal,
		).WithCause(err)
	}
	authManager, err := auth.NewManager(runtime.Root, auth.Options{})
	if err != nil {
		return Result{}, NewCommandError(
			"OWNER_AUTH_INVALID",
			"owner authentication configuration is invalid",
			"inspect the selected data directory and retry",
			ExitIntegrity,
		).WithCause(err)
	}
	exporter, err := archive.NewExporter(archive.Options{DataDir: runtime.Root, AppVersion: info.Version})
	if err != nil {
		return Result{}, err
	}
	committer, err := archive.NewCommitter(archive.CommitOptions{DataDir: runtime.Root, AppVersion: info.Version})
	if err != nil {
		return Result{}, err
	}
	board := server.NewSwitchboard()
	host := &liveServerHost{
		board: board, auth: authManager, assets: assets, trustedProxies: trustedProxies,
		secureCookies: !runtime.IsDevelopment(), log: invocation.LogEvent, exporter: exporter,
	}
	host.committer = &hostedImportCommitter{core: committer, host: host}
	retentionContext, cancelRetention := context.WithCancel(ctx)
	defer cancelRetention()
	go committer.RunRetention(retentionContext)
	defer func() { _ = host.CloseActive() }()
	if err := server.Run(ctx, server.Config{
		Runtime: runtime, Handler: board,
		PrepareLocked: func(prepareCtx context.Context, held *runtimefs.LeaseSet) error {
			if err := runtime.RefreshCurrent(held); err != nil {
				return err
			}
			if err := committer.RecoverWithinLocks(prepareCtx, runtime, held); err != nil {
				return err
			}
			if err := runtime.RefreshCurrent(held); err != nil {
				return err
			}
			if err := host.Activate(prepareCtx, runtime); err != nil {
				return err
			}
			if err := authManager.Load(); err != nil {
				_ = host.CloseActive()
				return err
			}
			board.EndMaintenance()
			return nil
		},
		Address: listenAddress, Log: invocation.LogEvent,
	}); err != nil {
		switch {
		case errors.Is(err, sqlitestore.ErrNotReady), errors.Is(err, sqlitestore.ErrMigrationDrift),
			errors.Is(err, sqlitestore.ErrSchemaTooNew), errors.Is(err, sqlitestore.ErrSchemaIncomplete),
			errors.Is(err, sqlitestore.ErrFTSUnavailable):
			return Result{}, NewCommandError(
				"DATABASE_NOT_READY",
				"the current Moment database could not become ready",
				"inspect the selected data directory and migration history, then retry",
				ExitIntegrity,
			).WithCause(err)
		case errors.Is(err, auth.ErrNotInitialized):
			return Result{}, NewCommandError(
				"OWNER_NOT_INITIALIZED",
				"owner authentication is not initialized",
				"run 'moment owner init --data-dir <path>' while the server is stopped",
				ExitConflict,
			).WithCause(err)
		case errors.Is(err, auth.ErrInvalidSecrets), errors.Is(err, auth.ErrSecretPermissions):
			return Result{}, NewCommandError(
				"OWNER_SECRETS_INVALID",
				"owner authentication secrets are invalid or insecure",
				"restore a protected secrets.json or inspect the selected data directory",
				ExitIntegrity,
			).WithCause(err)
		}
		return Result{}, err
	}
	return Result{
		Data: map[string]any{"stopped": true},
		Text: "Moment server stopped",
	}, nil
}
