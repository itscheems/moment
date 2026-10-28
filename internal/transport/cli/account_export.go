package cli

import (
	"context"
	"io"
	"strings"

	"github.com/moment/moment/internal/archive"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

func registerAccountExport(application *Application, appVersion string) {
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"user", "export"}, ArgsUsage: "[username]", Short: "Export one account's own data",
		Long:    "Create a personal data archive, distinct from a site backup. Direct mode requires a username and filesystem authority; API mode exports only the authenticated account and takes no username.",
		Inputs:  "Required --output path. --overwrite explicitly replaces an existing output file.",
		Output:  "Path, archive_bytes, archive_sha256, format_version. The file uses moment-account-export-v3 and restores through user import, separately from site backups.",
		Errors:  "ACCOUNT_NOT_FOUND for an unknown local account; unauthorized API requests fail; invalid or incomplete downloads are never published.",
		Example: "moment user export cheems --data-dir ./moment-data --output ./my-data.zip --json",
		Args:    cobra.RangeArgs(0, 1), Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			flags.String("output", "", "destination ZIP path")
			flags.Bool("overwrite", false, "replace an existing output file")
		},
		Run: func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
			return runAccountExport(ctx, invocation, input, appVersion)
		},
	})
}

func runAccountExport(ctx context.Context, invocation Invocation, input CommandInput, appVersion string) (Result, error) {
	output, err := input.String("output")
	if err != nil {
		return Result{}, err
	}
	overwrite, err := input.Bool("overwrite")
	if err != nil {
		return Result{}, err
	}
	if strings.TrimSpace(output) == "" {
		return Result{}, invalidArgument("--output is required", "provide a destination ZIP file")
	}
	var producer func(context.Context, io.Writer) (archive.Summary, error)
	if invocation.Mode() == ModeDirect {
		if len(input.Args()) != 1 {
			return Result{}, invalidArgument("direct account export requires a username", "run moment user list --json")
		}
		target, err := invocation.DirectTransport()
		if err != nil {
			return Result{}, err
		}
		if err := ensureBackupOutputOutsideDataDir(output, target.DataDirectory()); err != nil {
			return Result{}, err
		}
		service, database, err := openDirectAccounts(ctx, invocation)
		if err != nil {
			return Result{}, err
		}
		user, lookupErr := service.AccountByUsername(ctx, input.Args()[0])
		closeErr := database.Close()
		if lookupErr != nil {
			return Result{}, accountCLIError(lookupErr)
		}
		if closeErr != nil {
			return Result{}, closeErr
		}
		exporter, err := archive.NewExporter(archive.Options{DataDir: target.DataDirectory(), AppVersion: appVersion})
		if err != nil {
			return Result{}, err
		}
		producer = func(ctx context.Context, writer io.Writer) (archive.Summary, error) {
			return exporter.ExportAccount(ctx, writer, user.ID)
		}
	} else {
		if len(input.Args()) != 0 {
			return Result{}, invalidArgument("API account export cannot select another user", "omit the username to export your authenticated account")
		}
		producer = func(ctx context.Context, writer io.Writer) (archive.Summary, error) {
			return downloadNamedExport(ctx, invocation, writer, "/api/v1/account/export", archive.AccountFormatHeaderValue)
		}
	}
	summary, err := archive.WriteAccountFile(ctx, output, overwrite, producer)
	if err != nil {
		return Result{}, backupCLIError(err)
	}
	return backupResult(summary), nil
}
