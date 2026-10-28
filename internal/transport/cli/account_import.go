package cli

import (
	"context"
	"fmt"
	"net/http"
	"os"

	"github.com/moment/moment/internal/archive"
	"github.com/spf13/cobra"
)

func registerAccountImport(application *Application, appVersion string) {
	application.MustRegisterLeaf(LeafSpec{
		Path: []string{"user", "import"}, ArgsUsage: "<archive.zip>", Short: "Restore personal content into the selected account",
		Long:    "Preview or append a personal export. Existing posts, comments, accounts, profiles and credentials are retained. Repeated restores skip content already imported from the same source.",
		Inputs:  "One personal export ZIP. Direct mode requires --as and a stopped server; API mode always selects the authenticated account. --dry-run validates and previews without adding content.",
		Output:  "Archive hash, target account, incoming counts, added counts and skipped counts in the normal human/JSON envelope.",
		Errors:  "Unsupported personal export versions, malformed archives and changed input are refused. A serving direct instance returns INSTANCE_IN_USE. Cancelled imports do not publish partial database content.",
		Safety:  "Additive only. Cannot replace site data, change credentials or grant the source account's role. Confirmation binds the archive hash before a second verified upload.",
		Example: "moment --as writer user import ./personal.zip --data-dir ./moment-data --dry-run --json\nmoment --url http://127.0.0.1:18080 --token-file ./account.token user import ./personal.zip --yes --json",
		Args:    cobra.ExactArgs(1), Capability: CapabilityBoth,
		Policy: SafetyPolicy{Destructive: true, SupportsDryRun: true, DeferredConfirmation: true, Confirmation: "Add personal content to this account?"},
		Run: func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
			return runAccountImport(ctx, invocation, input, appVersion)
		},
	})
}

func runAccountImport(ctx context.Context, invocation Invocation, input CommandInput, version string) (Result, error) {
	archivePath := input.Args()[0]
	initial, err := inspectImportArchivePath(archivePath)
	if err != nil {
		return Result{}, backupImportCLIError(err)
	}
	var core *archive.Exporter
	accountID := ""
	if invocation.Mode() == ModeDirect {
		actor, err := directContentActor(ctx)
		if err != nil {
			return Result{}, err
		}
		accountID = actor.ID
		transport, err := invocation.DirectTransport()
		if err != nil {
			return Result{}, err
		}
		core, err = archive.NewExporter(archive.Options{DataDir: transport.DataDirectory(), AppVersion: version})
		if err != nil {
			return Result{}, err
		}
	}
	call := func(dry bool, hash string) (archive.AccountImportResult, error) {
		if core != nil {
			file, err := os.Open(archivePath)
			if err != nil {
				return archive.AccountImportResult{}, err
			}
			defer func() { _ = file.Close() }()
			return core.ImportAccount(ctx, file, accountID, archive.AccountImportOptions{DryRun: dry, Offline: true, ExpectedSHA: hash})
		}
		endpoint := "/api/v1/account/import/plan"
		identity := importArchiveIdentity{}
		if !dry {
			endpoint = "/api/v1/account/import/restore"
			identity = importArchiveIdentity{info: initial, size: initial.Size(), sha256: hash}
		}
		body, _, err := uploadImportArchive(ctx, invocation, archivePath, endpoint, http.StatusOK, hash, identity)
		if err != nil {
			return archive.AccountImportResult{}, err
		}
		var value archive.AccountImportResult
		_, _, err = decodeStrictAPISuccess(body, &value)
		if err == nil && (value.DryRun != dry || len(value.ArchiveSHA256) != 64 || value.AccountID == "") {
			err = invalidAPIResponse()
		}
		return value, err
	}
	plan, err := call(true, "")
	if err != nil {
		return Result{}, backupImportCLIError(err)
	}
	if invocation.Safety().DryRun {
		return Result{Data: plan, Text: "Personal import preview; existing data is preserved."}, nil
	}
	if err = invocation.ConfirmScope(fmt.Sprintf("Add %d posts and %d comments from archive %s to this account, preserving existing content?", plan.Counts.Entries, plan.Counts.Comments, plan.ArchiveSHA256)); err != nil {
		return Result{}, err
	}
	restored, err := call(false, plan.ArchiveSHA256)
	if err != nil {
		return Result{}, backupImportCLIError(err)
	}
	return Result{Data: restored, Text: "Personal content restored; existing data preserved."}, nil
}
