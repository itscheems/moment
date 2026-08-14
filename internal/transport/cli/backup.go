package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/buildinfo"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	importInspectionTokenTTL  = 30 * time.Minute
	importInspectionClockSkew = time.Minute
)

func registerBackup(application *Application, info buildinfo.Info) {
	application.MustRegisterLeaf(LeafSpec{
		Path:   []string{"backup", "export"},
		Short:  "Export one consistent portable archive",
		Long:   "Stream Moment export format v1 from one logical SQLite and reachable-media snapshot through direct or authenticated API mode.",
		Inputs: "Choose exactly one destination: --output <zip> for atomic file publication or --stdout for raw ZIP bytes. Direct file output must be outside the selected --data-dir. --stdout is mutually exclusive with --json. API mode authenticates through MOMENT_TOKEN or --token-file.",
		Output: "File mode validates v1 then reports path, format_version, archive_bytes, and archive_sha256 in the normal human/JSON contract. --stdout fail-closes on the HTTP format header before writing only ZIP bytes; a transport failure after the first byte leaves an intentionally truncated stdout stream and diagnostics only on stderr.",
		Errors: "Invalid output flags use exit 2; unsafe or unavailable destinations use exit 6; existing destinations without --overwrite use OUTPUT_EXISTS; data/checksum failures use exit 7; cancellation and timeout use exit 9.",
		Safety: "File output is prepared and flushed beside the destination, then atomically published. Direct output inside --data-dir is rejected before creating a partial. --overwrite replaces only the exact regular non-symlink destination and never weakens archive validation.",
		Example: "moment backup export --data-dir ./moment-data --output ./moment-export.zip --json\n" +
			"moment --url http://127.0.0.1:8080 --token-file ./owner.token backup export --stdout > moment-export.zip",
		Capability: CapabilityBoth,
		RawStdout:  true,
		Flags: func(flags *pflag.FlagSet) {
			registerSingleStringFlag(flags, "output", "atomically publish the ZIP at this path")
			registerSingleBoolFlag(flags, "stdout", "write raw ZIP bytes to stdout")
			registerSingleBoolFlag(flags, "overwrite", "atomically replace an existing regular output file")
		},
		Run: func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
			return runBackupExport(ctx, invocation, input, info.Version)
		},
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:      []string{"backup", "inspect"},
		ArgsUsage: "<archive.zip>",
		Short:     "Inspect a portable archive into restartable staging",
		Long:      "Treat one Moment export as hostile input, validate it completely, rebuild the current schema and search index in a restartable staging generation, and issue one expiring commit token without changing active data.",
		Inputs:    "Exactly one regular non-symlink v1 ZIP path. Direct mode stages locally and binds the result to the current owner epoch. API mode streams application/zip to the authenticated server. --timeout bounds upload, validation, media checks, migrations, and index rebuild.",
		Output:    "The normal human/JSON contract reports format, counts, time range, media bytes, missing-alt warnings, replacement impact, the complete peak disk estimate, expiry, and the one-time staging token. Save the token for backup import confirmation.",
		Errors:    "Hostile or corrupt archives use stable integrity errors and exit 7; unavailable input or disk uses exit 6; an active inspection uses IMPORT_IN_PROGRESS; cancellation uses exit 9.",
		Safety:    "Inspection never changes CURRENT or active media. ZIP paths, links, duplicates, counts, sizes, ratios, checksums, records, references, and image bytes are bounded before staging members become authority candidates.",
		Example: "moment backup inspect ./moment-export.zip --data-dir ./moment-data --json\n" +
			"moment --url http://127.0.0.1:8080 --token-file ./owner.token backup inspect ./moment-export.zip --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Run:        runBackupInspect,
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:      []string{"backup", "import"},
		ArgsUsage: "[archive.zip]",
		Short:     "Plan or commit one portable archive replacement",
		Long:      "Validate one archive and replace the active generation in one coordinated operation, or consume an advanced staging token from backup inspect. Commit creates and verifies a pre-import backup, atomically switches CURRENT, proves readiness, and can automatically roll back on failure.",
		Inputs:    "Provide exactly one positional archive path or --staging-token. Archive mode first computes a zero-persistence plan; interactive confirmation names that archive hash and replacement scope before a separately bound restore. Direct mode requires the server to be stopped and holds Import→Serve continuously before its second validation and commit. --dry-run returns only the plan.",
		Output:    "Dry-run reports the token-free replacement/disk plan after removing all temporary staging. Interactive archive import presents the same hash, counts, replacement, disk, and warnings before confirmation; --yes still computes and binds that plan before returning durable commit status. Tokens are never echoed.",
		Errors:    "Replay, expiry, owner rotation, disk pressure, instance use, integrity, maintenance, and rollback failures use stable codes and exits. Resolve an ambiguous API result with 'moment backup status', never by replaying the token.",
		Safety:    "This command replaces active authority. --dry-run leaves no staging token, journal, backup, CURRENT, or maintenance change. Actual archive import binds confirmation to a zero-persistence plan, then revalidates the same file identity, size, and SHA-256 before commit. --no-input never confirms implicitly.",
		Example: "moment backup import ./moment-export.zip --data-dir ./moment-data --dry-run --json\n" +
			"moment --url http://127.0.0.1:8080 --token-file ./owner.token backup import ./moment-export.zip --yes --json",
		Args:       cobra.MaximumNArgs(1),
		Capability: CapabilityBoth,
		Policy: SafetyPolicy{
			Destructive: true, SupportsDryRun: true, DeferredConfirmation: true,
			Confirmation: "Replace the active Moment dataset using this archive or inspected staging token?",
		},
		Flags: func(flags *pflag.FlagSet) {
			registerSingleStringFlag(flags, "staging-token", "one-time token returned by backup inspect")
		},
		Run: func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
			return runBackupImport(ctx, invocation, input, info.Version)
		},
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:   []string{"backup", "status"},
		Short:  "Inspect the latest durable import operation",
		Long:   "Inspect the latest durable import operation by reading the authoritative journal after completion, restart, rollback, or an ambiguous network result without replaying a staging token.",
		Inputs: "No staging token or confirmation. Direct mode reads the selected local instance; API mode authenticates with MOMENT_TOKEN or --token-file.",
		Output: "Direct and API modes return the same safe phase, active/previous/new generations, verified backup identity, rollback outcome, stable error code, and timestamps.",
		Errors: "Missing or unsafe journal state, authentication, transport, and integrity failures use stable codes and exits. A needs_attention phase remains visible and never implies readiness.",
		Safety: "Read-only. The command never claims a token, enters maintenance, changes CURRENT, or exposes token, archive hash, or local paths.",
		Example: "moment backup status --data-dir ./moment-data --json\n" +
			"moment --url http://127.0.0.1:8080 --token-file ./owner.token backup status --json",
		Capability: CapabilityBoth,
		Run: func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
			return runBackupStatus(ctx, invocation, input, info.Version)
		},
	})
	registerBackupMerge(application, info)
}

func runBackupStatus(ctx context.Context, invocation Invocation, _ CommandInput, appVersion string) (Result, error) {
	var status archive.CommitStatus
	var meta map[string]any
	var err error
	switch invocation.Mode() {
	case ModeDirect:
		target, targetErr := invocation.DirectTransport()
		if targetErr != nil {
			return Result{}, targetErr
		}
		committer, createErr := archive.NewCommitter(archive.CommitOptions{DataDir: target.DataDirectory(), AppVersion: appVersion})
		if createErr != nil {
			return Result{}, backupImportCLIError(createErr)
		}
		status, err = committer.Status(ctx)
	case ModeAPI:
		status, meta, err = importStatusAPI(ctx, invocation)
	default:
		return Result{}, errors.New("unsupported backup status transport")
	}
	if err != nil {
		return Result{}, backupImportCLIError(err)
	}
	text := fmt.Sprintf("Import status: %s\nActive generation: %s\nPrevious generation: %s\nNew generation: %s",
		status.Phase, status.ActiveGeneration, status.PreviousGeneration, status.NewGeneration)
	return Result{Data: status, Text: text, Meta: meta}, nil
}

func runBackupImport(ctx context.Context, invocation Invocation, input CommandInput, appVersion string) (Result, error) {
	token, err := input.String("staging-token")
	if err != nil {
		return Result{}, err
	}
	hasToken := input.Changed("staging-token")
	arguments := input.Args()
	hasArchive := len(arguments) == 1
	if hasToken == hasArchive {
		return Result{}, invalidArgument("provide exactly one archive path or --staging-token", "use a local Moment export ZIP, or the unexpired token returned by backup inspect")
	}
	if hasToken && (token == "" || len(token) > 256 || strings.TrimSpace(token) != token) {
		return Result{}, invalidArgument("--staging-token must be non-empty and canonical", "use the unexpired token returned by backup inspect")
	}
	if invocation.Safety().DryRun && hasToken {
		return Result{}, invalidArgument("--dry-run requires an archive path", "provide the export ZIP instead of an already persistent staging token")
	}
	archivePath := ""
	if hasArchive {
		archivePath = arguments[0]
	}
	if invocation.Safety().DryRun {
		plan, meta, planErr := runBackupImportPlan(ctx, invocation, archivePath, appVersion)
		if planErr != nil {
			return Result{}, backupImportCLIError(planErr)
		}
		text := fmt.Sprintf("Import dry-run validated with no persistent staging\nEntries: %d\nComments: %d\nMedia: %d\nRequired additional bytes: %d\nAvailable bytes: %d",
			plan.Counts.Entries, plan.Counts.Comments, plan.Counts.Media, plan.Disk.RequiredAdditionalBytes, plan.Disk.AvailableBytes)
		return Result{Data: plan, Text: text, Meta: meta}, nil
	}
	var confirmedPlan archive.ImportPlan
	var confirmedIdentity importArchiveIdentity
	if hasArchive {
		initial, identityErr := inspectImportArchivePath(archivePath)
		if identityErr != nil {
			return Result{}, backupImportCLIError(identityErr)
		}
		confirmedPlan, _, identityErr = runBackupImportPlan(ctx, invocation, archivePath, appVersion)
		if identityErr != nil {
			return Result{}, backupImportCLIError(identityErr)
		}
		confirmedIdentity, identityErr = bindImportArchiveIdentity(ctx, archivePath, initial, confirmedPlan)
		if identityErr != nil {
			return Result{}, backupImportCLIError(identityErr)
		}
		if confirmErr := invocation.ConfirmScope(importScopeConfirmation(confirmedPlan)); confirmErr != nil {
			return Result{}, confirmErr
		}
		if identityErr := verifyImportArchiveIdentity(archivePath, confirmedIdentity); identityErr != nil {
			return Result{}, backupImportCLIError(identityErr)
		}
	} else if confirmErr := invocation.ConfirmScope("Replace the active Moment dataset using this inspected staging token?"); confirmErr != nil {
		return Result{}, confirmErr
	}
	var result archive.CommitResult
	var meta map[string]any
	switch invocation.Mode() {
	case ModeDirect:
		target, targetErr := invocation.DirectTransport()
		if targetErr != nil {
			return Result{}, targetErr
		}
		root, openErr := runtimefs.ResolveDataDir(target.DataDirectory())
		if openErr != nil {
			return Result{}, backupImportCLIError(openErr)
		}
		status, statusErr := auth.ReadStatus(root)
		if statusErr != nil || !status.Initialized || status.SessionEpoch == 0 {
			return Result{}, ownerAuthError(errors.Join(statusErr, auth.ErrNotInitialized))
		}
		committer, createErr := archive.NewCommitter(archive.CommitOptions{DataDir: root, AppVersion: appVersion})
		if createErr != nil {
			return Result{}, createErr
		}
		if hasArchive {
			result, err = committer.CommitFileOffline(ctx, archivePath, archive.InputBinding{
				FileInfo: confirmedIdentity.info, Size: confirmedIdentity.size, SHA256: confirmedPlan.ArchiveSHA256,
			}, status.SessionEpoch)
		} else {
			result, err = committer.Commit(ctx, token, status.SessionEpoch, nil)
		}
	case ModeAPI:
		if hasArchive {
			result, meta, err = restoreImportAPI(ctx, invocation, archivePath, confirmedPlan.ArchiveSHA256, confirmedIdentity)
		} else {
			result, meta, err = commitImportAPI(ctx, invocation, token)
		}
	default:
		return Result{}, errors.New("unsupported backup import transport")
	}
	if err != nil {
		return Result{}, backupImportCLIError(err)
	}
	status := result.Status
	text := fmt.Sprintf("Import committed\nPhase: %s\nActive generation: %s\nPrevious generation: %s\nVerified backup: %s",
		status.Phase, status.ActiveGeneration, status.PreviousGeneration, status.BackupName)
	return Result{Data: result, Text: text, Meta: meta}, nil
}

type importArchiveIdentity struct {
	info   os.FileInfo
	size   int64
	sha256 string
}

func inspectImportArchivePath(archivePath string) (os.FileInfo, error) {
	if strings.TrimSpace(archivePath) == "" || strings.ContainsRune(archivePath, 0) {
		return nil, invalidArgument("archive path is required", "provide one complete Moment export ZIP")
	}
	info, err := os.Lstat(archivePath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, domain.NewError(domain.CodeExportInvalid, "archive input must be a regular non-symlink file", nil)
	}
	return info, nil
}

func bindImportArchiveIdentity(ctx context.Context, archivePath string, initial os.FileInfo, plan archive.ImportPlan) (importArchiveIdentity, error) {
	file, err := os.Open(archivePath)
	if err != nil {
		return importArchiveIdentity{}, err
	}
	defer func() { _ = file.Close() }()
	opened, err := file.Stat()
	if err != nil || initial == nil || !opened.Mode().IsRegular() || !os.SameFile(initial, opened) {
		return importArchiveIdentity{}, errors.Join(domain.NewError(domain.CodeExportInvalid, "archive file identity changed after import planning", nil), err)
	}
	digest := sha256.New()
	copied, err := io.CopyBuffer(digest, &backupContextReader{ctx: ctx, reader: file}, make([]byte, 128<<10))
	if err != nil {
		return importArchiveIdentity{}, err
	}
	finished, err := file.Stat()
	actualSHA := hex.EncodeToString(digest.Sum(nil))
	if err != nil || !os.SameFile(opened, finished) || copied != opened.Size() || finished.Size() != opened.Size() {
		return importArchiveIdentity{}, errors.Join(domain.NewError(domain.CodeExportInvalid, "archive file changed while binding the import preview", nil), err)
	}
	if plan.ArchiveSHA256 != actualSHA || plan.Disk.ArchiveBytes != copied {
		return importArchiveIdentity{}, domain.NewError(domain.CodeExportChecksumMismatch, "archive bytes do not match the import preview", nil)
	}
	return importArchiveIdentity{info: finished, size: copied, sha256: actualSHA}, nil
}

func verifyImportArchiveIdentity(archivePath string, identity importArchiveIdentity) error {
	info, err := os.Lstat(archivePath)
	if err != nil {
		return err
	}
	if identity.info == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 ||
		!os.SameFile(identity.info, info) || info.Size() != identity.size || !validLowerHex(identity.sha256, sha256.Size) {
		return domain.NewError(domain.CodeExportChecksumMismatch, "archive identity changed after the confirmed import preview", nil)
	}
	return nil
}

func importScopeConfirmation(plan archive.ImportPlan) string {
	warnings := "none"
	if len(plan.Warnings) != 0 {
		warnings = strings.Join(plan.Warnings, "; ")
	}
	return fmt.Sprintf(
		"Import preview\nArchive SHA-256: %s\nIncoming: %d entries, %d comments, %d media (%d bytes)\nReplacing: %d entries, %d comments, %d media (%d bytes)\nDisk: %d additional bytes required, %d available\nWarnings: %s\nReplace the active Moment dataset with this exact archive?",
		plan.ArchiveSHA256,
		plan.Counts.Entries, plan.Counts.Comments, plan.Counts.Media, plan.MediaBytes,
		plan.Replacement.Counts.Entries, plan.Replacement.Counts.Comments, plan.Replacement.Counts.Media, plan.Replacement.DataBytes,
		plan.Disk.RequiredAdditionalBytes, plan.Disk.AvailableBytes, warnings,
	)
}

func runBackupImportPlan(ctx context.Context, invocation Invocation, archivePath, appVersion string) (archive.ImportPlan, map[string]any, error) {
	switch invocation.Mode() {
	case ModeDirect:
		target, err := invocation.DirectTransport()
		if err != nil {
			return archive.ImportPlan{}, nil, err
		}
		root, err := runtimefs.ResolveDataDir(target.DataDirectory())
		if err != nil {
			return archive.ImportPlan{}, nil, err
		}
		status, err := auth.ReadStatus(root)
		if err != nil || !status.Initialized || status.SessionEpoch == 0 {
			return archive.ImportPlan{}, nil, ownerAuthError(errors.Join(err, auth.ErrNotInitialized))
		}
		committer, err := archive.NewCommitter(archive.CommitOptions{DataDir: root, AppVersion: appVersion})
		if err != nil {
			return archive.ImportPlan{}, nil, err
		}
		plan, err := committer.PlanFileOffline(ctx, archivePath, status.SessionEpoch)
		return plan, nil, err
	case ModeAPI:
		return uploadImportPlan(ctx, invocation, archivePath)
	default:
		return archive.ImportPlan{}, nil, errors.New("unsupported backup import transport")
	}
}

func runBackupInspect(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
	archivePath := input.Args()[0]
	var result archive.Inspection
	var meta map[string]any
	var err error
	switch invocation.Mode() {
	case ModeDirect:
		target, targetErr := invocation.DirectTransport()
		if targetErr != nil {
			return Result{}, targetErr
		}
		runtimeState, openErr := runtimefs.OpenContext(ctx, target.DataDirectory())
		if openErr != nil {
			return Result{}, backupInspectCLIError(openErr)
		}
		status, statusErr := auth.ReadStatus(runtimeState.Root)
		if statusErr != nil {
			return Result{}, ownerAuthError(statusErr)
		}
		if !status.Initialized || status.SessionEpoch == 0 {
			return Result{}, ownerAuthError(auth.ErrNotInitialized)
		}
		inspector, createErr := archive.NewInspector(archive.InspectOptions{DataDir: runtimeState.Root})
		if createErr != nil {
			return Result{}, createErr
		}
		result, err = inspector.InspectFile(ctx, archivePath, status.SessionEpoch)
	case ModeAPI:
		result, meta, err = uploadImportInspection(ctx, invocation, archivePath)
	default:
		return Result{}, errors.New("unsupported backup inspect transport")
	}
	if err != nil {
		return Result{}, backupInspectCLIError(err)
	}
	text := fmt.Sprintf("Archive inspected into restartable staging\nFormat: v%d\nEntries: %d\nComments: %d\nMedia: %d\nRequired additional bytes: %d\nAvailable bytes: %d\nExpires: %s\nStaging token: %s",
		result.FormatVersion, result.Counts.Entries, result.Counts.Comments, result.Counts.Media,
		result.Disk.RequiredAdditionalBytes, result.Disk.AvailableBytes, result.ExpiresAt.Format(time.RFC3339), result.StagingToken)
	return Result{Data: result, Text: text, Meta: meta}, nil
}

func uploadImportInspection(ctx context.Context, invocation Invocation, archivePath string) (archive.Inspection, map[string]any, error) {
	before, err := os.Lstat(archivePath)
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return archive.Inspection{}, nil, invalidArgument("archive input must be a regular non-symlink file", "provide one complete Moment export ZIP")
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	defer func() { _ = file.Close() }()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return archive.Inspection{}, nil, errors.Join(invalidArgument("archive input changed while opening", "retry with a stable regular non-symlink ZIP"), err)
	}
	target, err := invocation.APITransport()
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	token, err := readAPIToken(target)
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	endpoint, err := url.JoinPath(target.BaseURL().String(), "/api/v1/owner/import/inspect")
	if err != nil {
		return archive.Inspection{}, nil, invalidArgument("the import inspection endpoint URL is invalid", "check --url and retry")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, file)
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	request.ContentLength = after.Size()
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/zip")
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	if len(body) > maxAPIResponse {
		return archive.Inspection{}, nil, invalidAPIResponse()
	}
	if !validAPIJSONContentType(response.Header.Get("Content-Type")) || validateUniqueJSONObjects(body) != nil {
		return archive.Inspection{}, nil, invalidAPIResponse()
	}
	if response.StatusCode != http.StatusCreated {
		return archive.Inspection{}, nil, decodeOwnerAPIFailure(response.StatusCode, body)
	}
	inspection, meta, err := decodeImportInspectionAPI(body, after.Size(), time.Now().UTC())
	if err != nil {
		return archive.Inspection{}, nil, invalidAPIResponse()
	}
	return inspection, meta, nil
}

func uploadImportPlan(ctx context.Context, invocation Invocation, archivePath string) (archive.ImportPlan, map[string]any, error) {
	body, archiveBytes, err := uploadImportArchive(ctx, invocation, archivePath, "/api/v1/owner/import/plan", http.StatusOK, "", importArchiveIdentity{})
	if err != nil {
		return archive.ImportPlan{}, nil, err
	}
	plan, meta, err := decodeImportPlanAPI(body, archiveBytes)
	if err != nil {
		return archive.ImportPlan{}, nil, invalidAPIResponse()
	}
	return plan, meta, nil
}

func restoreImportAPI(ctx context.Context, invocation Invocation, archivePath, expectedArchiveSHA string, identity importArchiveIdentity) (archive.CommitResult, map[string]any, error) {
	body, _, err := uploadImportArchive(ctx, invocation, archivePath, "/api/v1/owner/import/restore", http.StatusOK, expectedArchiveSHA, identity)
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	result, meta, err := decodeImportCommitAPI(body)
	if err != nil {
		return archive.CommitResult{}, nil, invalidAPIResponse()
	}
	return result, meta, nil
}

func uploadImportArchive(ctx context.Context, invocation Invocation, archivePath, endpointPath string, successStatus int, expectedArchiveSHA string, identity importArchiveIdentity) ([]byte, int64, error) {
	return uploadImportArchiveBounded(ctx, invocation, archivePath, endpointPath, successStatus, expectedArchiveSHA, identity, maxAPIResponse)
}

func uploadImportArchiveBounded(ctx context.Context, invocation Invocation, archivePath, endpointPath string, successStatus int, expectedArchiveSHA string, identity importArchiveIdentity, responseLimit int64) ([]byte, int64, error) {
	if responseLimit < 1 {
		return nil, 0, errors.New("import response limit must be positive")
	}
	before, err := os.Lstat(archivePath)
	if err != nil {
		return nil, 0, err
	}
	if !before.Mode().IsRegular() || before.Mode()&os.ModeSymlink != 0 {
		return nil, 0, invalidArgument("archive input must be a regular non-symlink file", "provide one complete Moment export ZIP")
	}
	file, err := os.Open(archivePath)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = file.Close() }()
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		return nil, 0, errors.Join(invalidArgument("archive input changed while opening", "retry with a stable regular non-symlink ZIP"), err)
	}
	if expectedArchiveSHA != "" {
		if expectedArchiveSHA != identity.sha256 || identity.info == nil || !os.SameFile(identity.info, after) || identity.size != after.Size() {
			return nil, 0, domain.NewError(domain.CodeExportChecksumMismatch, "archive identity changed after the confirmed import preview", nil)
		}
	}
	target, err := invocation.APITransport()
	if err != nil {
		return nil, 0, err
	}
	token, err := readAPIToken(target)
	if err != nil {
		return nil, 0, err
	}
	endpoint, err := url.JoinPath(target.BaseURL().String(), endpointPath)
	if err != nil {
		return nil, 0, invalidArgument("the import endpoint URL is invalid", "check --url and retry")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, file)
	if err != nil {
		return nil, 0, err
	}
	request.ContentLength = after.Size()
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Content-Type", "application/zip")
	request.Header.Set("Accept", "application/json")
	if expectedArchiveSHA != "" {
		request.Header.Set("X-Moment-Archive-SHA256", expectedArchiveSHA)
	}
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, responseLimit+1))
	if err != nil {
		return nil, 0, err
	}
	if int64(len(body)) > responseLimit {
		return nil, 0, invalidAPIResponse()
	}
	if !validAPIJSONContentType(response.Header.Get("Content-Type")) || validateUniqueJSONObjects(body) != nil {
		return nil, 0, invalidAPIResponse()
	}
	if response.StatusCode != successStatus {
		return nil, 0, decodeOwnerAPIFailure(response.StatusCode, body)
	}
	return body, after.Size(), nil
}

func commitImportAPI(ctx context.Context, invocation Invocation, stagingToken string) (archive.CommitResult, map[string]any, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	ownerToken, err := readAPIToken(target)
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	endpoint, err := url.JoinPath(target.BaseURL().String(), "/api/v1/owner/import/commit")
	if err != nil {
		return archive.CommitResult{}, nil, invalidArgument("the import commit endpoint URL is invalid", "check --url and retry")
	}
	body, err := json.Marshal(map[string]string{"staging_token": stagingToken})
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+ownerToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	if len(responseBody) > maxAPIResponse {
		return archive.CommitResult{}, nil, invalidAPIResponse()
	}
	if !validAPIJSONContentType(response.Header.Get("Content-Type")) || validateUniqueJSONObjects(responseBody) != nil {
		return archive.CommitResult{}, nil, invalidAPIResponse()
	}
	if response.StatusCode != http.StatusOK {
		return archive.CommitResult{}, nil, decodeOwnerAPIFailure(response.StatusCode, responseBody)
	}
	result, meta, err := decodeImportCommitAPI(responseBody)
	if err != nil {
		return archive.CommitResult{}, nil, invalidAPIResponse()
	}
	return result, meta, nil
}

func importStatusAPI(ctx context.Context, invocation Invocation) (archive.CommitStatus, map[string]any, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return archive.CommitStatus{}, nil, err
	}
	ownerToken, err := readAPIToken(target)
	if err != nil {
		return archive.CommitStatus{}, nil, err
	}
	endpoint, err := url.JoinPath(target.BaseURL().String(), "/api/v1/owner/import/status")
	if err != nil {
		return archive.CommitStatus{}, nil, invalidArgument("the import status endpoint URL is invalid", "check --url and retry")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return archive.CommitStatus{}, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+ownerToken)
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return archive.CommitStatus{}, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil {
		return archive.CommitStatus{}, nil, err
	}
	if len(responseBody) > maxAPIResponse {
		return archive.CommitStatus{}, nil, invalidAPIResponse()
	}
	if !validAPIJSONContentType(response.Header.Get("Content-Type")) || validateUniqueJSONObjects(responseBody) != nil {
		return archive.CommitStatus{}, nil, invalidAPIResponse()
	}
	if response.StatusCode != http.StatusOK {
		return archive.CommitStatus{}, nil, decodeOwnerAPIFailure(response.StatusCode, responseBody)
	}
	status, meta, err := decodeImportStatusAPI(responseBody)
	if err != nil {
		return archive.CommitStatus{}, nil, invalidAPIResponse()
	}
	return status, meta, nil
}

func decodeImportPlanAPI(body []byte, expectedArchiveBytes int64) (archive.ImportPlan, map[string]any, error) {
	if !utf8.Valid(body) {
		return archive.ImportPlan{}, nil, errors.New("import plan response is not valid UTF-8")
	}
	envelope, err := decodeExactJSONObject(body, "data", "meta")
	if err != nil {
		return archive.ImportPlan{}, nil, err
	}
	data, err := decodeExactJSONObject(envelope["data"],
		"dry_run", "format_version", "archive_sha256", "counts", "time_range", "media_bytes", "missing_alt",
		"disk", "replacement", "warnings",
	)
	if err != nil {
		return archive.ImportPlan{}, nil, err
	}
	if _, err := decodeExactJSONObject(data["counts"], "entries", "comments", "media"); err != nil {
		return archive.ImportPlan{}, nil, err
	}
	timeRange, err := decodeImportTimeRangeObject(data["time_range"])
	if err != nil {
		return archive.ImportPlan{}, nil, err
	}
	if _, err := decodeExactJSONObject(data["disk"],
		"archive_bytes", "expanded_bytes", "staging_bytes", "pre_import_backup_bytes", "rollback_reserve_bytes",
		"safety_margin_bytes", "required_additional_bytes", "estimated_peak_footprint_bytes", "available_bytes", "space_ok",
	); err != nil {
		return archive.ImportPlan{}, nil, err
	}
	replacement, err := decodeExactJSONObject(data["replacement"], "counts", "media_bytes", "data_bytes")
	if err != nil {
		return archive.ImportPlan{}, nil, err
	}
	if _, err := decodeExactJSONObject(replacement["counts"], "entries", "comments", "media"); err != nil {
		return archive.ImportPlan{}, nil, err
	}
	metadata, err := decodeExactJSONObject(envelope["meta"], "request_id")
	if err != nil {
		return archive.ImportPlan{}, nil, err
	}
	var plan archive.ImportPlan
	decoder := json.NewDecoder(bytes.NewReader(envelope["data"]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&plan); err != nil || decoder.Decode(&struct{}{}) != io.EOF || !plan.DryRun {
		return archive.ImportPlan{}, nil, errors.New("import plan data cannot be decoded exactly")
	}
	requestID, err := decodeImportRequestID(metadata["request_id"])
	if err != nil {
		return archive.ImportPlan{}, nil, err
	}
	preview := archive.Inspection{
		FormatVersion: plan.FormatVersion, ArchiveSHA256: plan.ArchiveSHA256, Counts: plan.Counts,
		TimeRange: plan.TimeRange, MediaBytes: plan.MediaBytes, MissingAlt: plan.MissingAlt,
		Disk: plan.Disk, Replacement: plan.Replacement, Warnings: plan.Warnings,
	}
	if err := validateImportPreviewAPI(preview, timeRange, expectedArchiveBytes); err != nil {
		return archive.ImportPlan{}, nil, err
	}
	return plan, map[string]any{"request_id": requestID}, nil
}

func decodeImportCommitAPI(body []byte) (archive.CommitResult, map[string]any, error) {
	if !utf8.Valid(body) {
		return archive.CommitResult{}, nil, errors.New("import commit response is not valid UTF-8")
	}
	envelope, err := decodeExactJSONObject(body, "data", "meta")
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	data, err := decodeExactJSONObject(envelope["data"], "status")
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	statusRaw, err := decodeExactJSONObject(data["status"],
		"operation_id", "phase", "active_generation", "previous_generation", "new_generation",
		"backup_name", "backup_verified", "rollback_attempted", "rollback_succeeded",
		"started_at", "updated_at", "completed_at",
	)
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	metadata, err := decodeExactJSONObject(envelope["meta"], "request_id")
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	var result archive.CommitResult
	decoder := json.NewDecoder(bytes.NewReader(envelope["data"]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return archive.CommitResult{}, nil, errors.New("import commit data cannot be decoded exactly")
	}
	requestID, err := decodeImportRequestID(metadata["request_id"])
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	status := result.Status
	if status.Phase != archive.CommitSucceeded || !validImportGenerationID(status.OperationID) ||
		status.OperationID != status.NewGeneration || status.ActiveGeneration != status.NewGeneration ||
		!validRuntimeGenerationID(status.PreviousGeneration) || status.PreviousGeneration == status.NewGeneration ||
		!status.BackupVerified || status.BackupName == "" || filepath.Base(status.BackupName) != status.BackupName ||
		status.RollbackAttempted || status.RollbackSucceeded || status.ErrorCode != "" || status.CompletedAt == nil ||
		status.StartedAt.IsZero() || status.UpdatedAt.Before(status.StartedAt) || !status.CompletedAt.Equal(status.UpdatedAt) {
		return archive.CommitResult{}, nil, errors.New("import commit status is not a successful canonical terminal state")
	}
	for _, field := range []string{"started_at", "updated_at", "completed_at"} {
		var encoded string
		if err := json.Unmarshal(statusRaw[field], &encoded); err != nil || !strings.HasSuffix(encoded, "Z") {
			return archive.CommitResult{}, nil, errors.New("import commit timestamp is not canonical UTC")
		}
		parsed, err := time.Parse(time.RFC3339Nano, encoded)
		if err != nil || parsed.Format(time.RFC3339Nano) != encoded {
			return archive.CommitResult{}, nil, errors.New("import commit timestamp is not canonical UTC")
		}
	}
	return result, map[string]any{"request_id": requestID}, nil
}

func decodeImportStatusAPI(body []byte) (archive.CommitStatus, map[string]any, error) {
	if !utf8.Valid(body) {
		return archive.CommitStatus{}, nil, errors.New("import status response is not valid UTF-8")
	}
	envelope, err := decodeExactJSONObject(body, "data", "meta")
	if err != nil {
		return archive.CommitStatus{}, nil, err
	}
	statusRaw, err := decodeJSONObjectWithOptional(envelope["data"],
		[]string{
			"operation_id", "phase", "active_generation", "previous_generation", "new_generation",
			"backup_verified", "rollback_attempted", "rollback_succeeded", "started_at", "updated_at",
		},
		[]string{"backup_name", "error_code", "completed_at"},
	)
	if err != nil {
		return archive.CommitStatus{}, nil, err
	}
	metadata, err := decodeExactJSONObject(envelope["meta"], "request_id")
	if err != nil {
		return archive.CommitStatus{}, nil, err
	}
	var status archive.CommitStatus
	decoder := json.NewDecoder(bytes.NewReader(envelope["data"]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&status); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return archive.CommitStatus{}, nil, errors.New("import status data cannot be decoded exactly")
	}
	requestID, err := decodeImportRequestID(metadata["request_id"])
	if err != nil {
		return archive.CommitStatus{}, nil, err
	}
	if err := validateImportStatusAPI(status, statusRaw); err != nil {
		return archive.CommitStatus{}, nil, err
	}
	return status, map[string]any{"request_id": requestID}, nil
}

func validateImportStatusAPI(status archive.CommitStatus, raw map[string]json.RawMessage) error {
	if !validImportGenerationID(status.OperationID) || status.OperationID != status.NewGeneration ||
		!validRuntimeGenerationID(status.PreviousGeneration) || !validRuntimeGenerationID(status.ActiveGeneration) ||
		status.PreviousGeneration == status.NewGeneration ||
		(status.ActiveGeneration != status.PreviousGeneration && status.ActiveGeneration != status.NewGeneration) {
		return errors.New("import status generation binding is invalid")
	}
	validPhase := false
	for _, phase := range []archive.CommitPhase{
		archive.CommitClaimed, archive.CommitBackingUp, archive.CommitFinalizing, archive.CommitSwitching,
		archive.CommitVerifying, archive.CommitRollingBack, archive.CommitSucceeded, archive.CommitFailed,
		archive.CommitRolledBack, archive.CommitNeedsAttention,
	} {
		if status.Phase == phase {
			validPhase = true
			break
		}
	}
	if !validPhase || status.StartedAt.IsZero() || status.UpdatedAt.Before(status.StartedAt) {
		return errors.New("import status phase or time ordering is invalid")
	}
	for field, expected := range map[string]time.Time{"started_at": status.StartedAt, "updated_at": status.UpdatedAt} {
		parsed, err := decodeCanonicalUTCTime(raw[field])
		if err != nil || !expected.Equal(parsed) {
			return errors.New("import status timestamp is not canonical UTC")
		}
	}
	completedRaw, hasCompleted := raw["completed_at"]
	if hasCompleted != (status.CompletedAt != nil) {
		return errors.New("import status completion presence is invalid")
	}
	if hasCompleted {
		completed, err := decodeCanonicalUTCTime(completedRaw)
		if err != nil || !status.CompletedAt.Equal(completed) || completed.Before(status.UpdatedAt) {
			return errors.New("import status completion timestamp is invalid")
		}
	}
	_, hasBackupName := raw["backup_name"]
	if hasBackupName != (status.BackupName != "") || (status.BackupName == "") != !status.BackupVerified ||
		(status.BackupName != "" && (filepath.Base(status.BackupName) != status.BackupName || !strings.HasSuffix(status.BackupName, ".zip"))) {
		return errors.New("import status backup identity is invalid")
	}
	_, hasErrorCode := raw["error_code"]
	if hasErrorCode != (status.ErrorCode != "") || !validImportErrorCode(status.ErrorCode) {
		return errors.New("import status error code is invalid")
	}
	if status.RollbackSucceeded && !status.RollbackAttempted {
		return errors.New("import status rollback outcome is invalid")
	}
	switch status.Phase {
	case archive.CommitClaimed, archive.CommitBackingUp:
		if status.BackupVerified || status.RollbackAttempted || status.CompletedAt != nil || status.ErrorCode != "" || status.ActiveGeneration != status.PreviousGeneration {
			return errors.New("early import status has impossible fields")
		}
	case archive.CommitFinalizing:
		if !status.BackupVerified || status.RollbackAttempted || status.CompletedAt != nil || status.ErrorCode != "" || status.ActiveGeneration != status.PreviousGeneration {
			return errors.New("finalizing import status has impossible fields")
		}
	case archive.CommitSwitching, archive.CommitVerifying:
		if !status.BackupVerified || status.RollbackAttempted || status.CompletedAt != nil || status.ErrorCode != "" ||
			(status.Phase == archive.CommitVerifying && status.ActiveGeneration != status.NewGeneration) {
			return errors.New("switching import status has impossible fields")
		}
	case archive.CommitRollingBack:
		if !status.BackupVerified || !status.RollbackAttempted || status.RollbackSucceeded || status.CompletedAt != nil || status.ErrorCode != "" {
			return errors.New("rolling-back import status has impossible fields")
		}
	case archive.CommitSucceeded:
		if !status.BackupVerified || status.RollbackAttempted || status.ErrorCode != "" || status.CompletedAt == nil ||
			status.ActiveGeneration != status.NewGeneration || !status.CompletedAt.Equal(status.UpdatedAt) {
			return errors.New("successful import status has impossible fields")
		}
	case archive.CommitFailed:
		if status.RollbackAttempted || status.ErrorCode == "" || status.CompletedAt == nil || status.ActiveGeneration != status.PreviousGeneration || !status.CompletedAt.Equal(status.UpdatedAt) {
			return errors.New("failed import status has impossible fields")
		}
	case archive.CommitRolledBack:
		if !status.BackupVerified || !status.RollbackAttempted || !status.RollbackSucceeded || status.ErrorCode == "" ||
			status.CompletedAt == nil || status.ActiveGeneration != status.PreviousGeneration || !status.CompletedAt.Equal(status.UpdatedAt) {
			return errors.New("rolled-back import status has impossible fields")
		}
	case archive.CommitNeedsAttention:
		if status.ErrorCode == "" || status.RollbackSucceeded {
			return errors.New("needs-attention import status has impossible fields")
		}
	}
	return nil
}

func validImportErrorCode(value string) bool {
	if value == "" {
		return true
	}
	if len(value) > 128 {
		return false
	}
	for _, character := range value {
		if (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' {
			return false
		}
	}
	return true
}

func validRuntimeGenerationID(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value || value == "." || value == ".." {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func decodeImportInspectionAPI(body []byte, expectedArchiveBytes int64, observedAt time.Time) (archive.Inspection, map[string]any, error) {
	if !utf8.Valid(body) {
		return archive.Inspection{}, nil, errors.New("import inspection response is not valid UTF-8")
	}
	envelope, err := decodeExactJSONObject(body, "data", "meta")
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	data, err := decodeExactJSONObject(envelope["data"],
		"format_version", "archive_sha256", "generation_id", "counts", "time_range", "media_bytes", "missing_alt",
		"disk", "replacement", "warnings", "expires_at", "staging_token",
	)
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	if _, err := decodeExactJSONObject(data["counts"], "entries", "comments", "media"); err != nil {
		return archive.Inspection{}, nil, err
	}
	timeRange, err := decodeImportTimeRangeObject(data["time_range"])
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	if _, err := decodeExactJSONObject(data["disk"],
		"archive_bytes", "expanded_bytes", "staging_bytes", "pre_import_backup_bytes", "rollback_reserve_bytes",
		"safety_margin_bytes", "required_additional_bytes", "estimated_peak_footprint_bytes", "available_bytes", "space_ok",
	); err != nil {
		return archive.Inspection{}, nil, err
	}
	replacement, err := decodeExactJSONObject(data["replacement"], "counts", "media_bytes", "data_bytes")
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	if _, err := decodeExactJSONObject(replacement["counts"], "entries", "comments", "media"); err != nil {
		return archive.Inspection{}, nil, err
	}
	metadata, err := decodeExactJSONObject(envelope["meta"], "request_id")
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	var inspection archive.Inspection
	decoder := json.NewDecoder(bytes.NewReader(envelope["data"]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&inspection); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return archive.Inspection{}, nil, errors.New("import inspection data cannot be decoded exactly")
	}
	requestID, err := decodeImportRequestID(metadata["request_id"])
	if err != nil {
		return archive.Inspection{}, nil, err
	}
	if err := validateImportInspectionAPI(inspection, data, timeRange, expectedArchiveBytes, observedAt); err != nil {
		return archive.Inspection{}, nil, err
	}
	return inspection, map[string]any{"request_id": requestID}, nil
}

func decodeExactJSONObject(raw []byte, expectedKeys ...string) (map[string]json.RawMessage, error) {
	return decodeJSONObjectWithOptional(raw, expectedKeys, nil)
}

func decodeJSONObjectWithOptional(raw []byte, requiredKeys, optionalKeys []string) (map[string]json.RawMessage, error) {
	allowedKeys := make([]string, 0, len(requiredKeys)+len(optionalKeys))
	allowedKeys = append(allowedKeys, requiredKeys...)
	allowedKeys = append(allowedKeys, optionalKeys...)
	expected := make(map[string]struct{}, len(allowedKeys))
	for _, key := range allowedKeys {
		if key == "" {
			return nil, errors.New("empty expected JSON key")
		}
		if _, duplicate := expected[key]; duplicate {
			return nil, errors.New("duplicate expected JSON key")
		}
		expected[key] = struct{}{}
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return nil, errors.New("JSON value is not an object")
	}
	fields := make(map[string]json.RawMessage, len(allowedKeys))
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok {
			return nil, errors.New("JSON object key is invalid")
		}
		if _, allowed := expected[key]; !allowed {
			return nil, errors.New("JSON object contains an unknown key")
		}
		if _, duplicate := fields[key]; duplicate {
			return nil, errors.New("JSON object contains a duplicate key")
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil || bytes.Equal(bytes.TrimSpace(value), []byte("null")) {
			return nil, errors.New("JSON object value is invalid")
		}
		fields[key] = value
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return nil, errors.New("JSON object is incomplete")
	}
	for _, key := range requiredKeys {
		if _, found := fields[key]; !found {
			return nil, errors.New("JSON object is incomplete")
		}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, errors.New("JSON object contains trailing data")
	}
	return fields, nil
}

func decodeImportTimeRangeObject(raw []byte) (map[string]json.RawMessage, error) {
	if empty, err := decodeExactJSONObject(raw); err == nil {
		return empty, nil
	}
	return decodeExactJSONObject(raw, "from", "to")
}

func decodeImportRequestID(raw json.RawMessage) (string, error) {
	var value string
	if err := json.Unmarshal(raw, &value); err != nil || len(value) < 5 || len(value) > 128 || !strings.HasPrefix(value, "req_") {
		return "", errors.New("import inspection request ID is invalid")
	}
	for _, character := range value[4:] {
		if (character < 'a' || character > 'z') && (character < 'A' || character > 'Z') && (character < '0' || character > '9') && character != '_' && character != '-' {
			return "", errors.New("import inspection request ID is invalid")
		}
	}
	return value, nil
}

func validateImportInspectionAPI(value archive.Inspection, raw map[string]json.RawMessage, timeRange map[string]json.RawMessage, expectedArchiveBytes int64, observedAt time.Time) error {
	if value.FormatVersion != archive.FormatVersion || !validLowerHex(value.ArchiveSHA256, sha256.Size) || !validImportGenerationID(value.GenerationID) {
		return errors.New("import inspection identity is invalid")
	}
	if err := validateImportPreviewAPI(value, timeRange, expectedArchiveBytes); err != nil {
		return err
	}
	expiresAt, err := decodeCanonicalUTCTime(raw["expires_at"])
	if err != nil || observedAt.IsZero() || !value.ExpiresAt.Equal(expiresAt) {
		return errors.New("import inspection expiry is invalid")
	}
	latestExpiry := observedAt.UTC().Add(importInspectionTokenTTL + importInspectionClockSkew)
	if !expiresAt.After(observedAt.UTC()) || expiresAt.After(latestExpiry) {
		return errors.New("import inspection expiry is outside the usable token window")
	}
	token, err := base64.RawURLEncoding.Strict().DecodeString(value.StagingToken)
	if err != nil || len(token) != sha256.Size || base64.RawURLEncoding.EncodeToString(token) != value.StagingToken {
		return errors.New("import inspection staging token is invalid")
	}
	return nil
}

func validateImportPreviewAPI(value archive.Inspection, timeRange map[string]json.RawMessage, expectedArchiveBytes int64) error {
	if value.FormatVersion != archive.FormatVersion || !validLowerHex(value.ArchiveSHA256, sha256.Size) {
		return errors.New("import preview identity is invalid")
	}
	if !validImportCounts(value.Counts, true) || !validImportCounts(value.Replacement.Counts, false) || value.MediaBytes < 0 || value.MissingAlt < 0 || value.Replacement.MediaBytes < 0 || value.Replacement.DataBytes < 0 || value.Replacement.MediaBytes > value.Replacement.DataBytes {
		return errors.New("import inspection counts are invalid")
	}
	if value.Counts.Entries == 0 {
		if value.Counts.Comments != 0 || value.MissingAlt != 0 || len(timeRange) != 0 || value.TimeRange.From != nil || value.TimeRange.To != nil {
			return errors.New("empty import inspection has a time range")
		}
	} else {
		if len(timeRange) != 2 || value.TimeRange.From == nil || value.TimeRange.To == nil {
			return errors.New("non-empty import inspection has no complete time range")
		}
		from, err := decodeCanonicalUTCTime(timeRange["from"])
		if err != nil {
			return err
		}
		to, err := decodeCanonicalUTCTime(timeRange["to"])
		if err != nil || from.After(to) || !value.TimeRange.From.Equal(from) || !value.TimeRange.To.Equal(to) {
			return errors.New("import inspection time range is invalid")
		}
	}
	if value.Warnings == nil || len(value.Warnings) > 1 {
		return errors.New("import inspection warnings are invalid")
	}
	if value.MissingAlt == 0 {
		if len(value.Warnings) != 0 {
			return errors.New("import inspection warnings do not match missing alt count")
		}
	} else if len(value.Warnings) != 1 || value.Warnings[0] != fmt.Sprintf("%d media usages have no image description", value.MissingAlt) {
		return errors.New("import inspection warnings do not match missing alt count")
	}
	return validateImportDiskEstimate(value, expectedArchiveBytes)
}

func validLowerHex(value string, decodedBytes int) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == decodedBytes && hex.EncodeToString(decoded) == value
}

func validImportGenerationID(value string) bool {
	const prefix = "import-"
	const timestampLength = len("20060102T150405000Z")
	if len(value) != len(prefix)+timestampLength+1+16 || !strings.HasPrefix(value, prefix) || value[len(prefix)+timestampLength] != '-' {
		return false
	}
	timestamp := value[len(prefix) : len(prefix)+timestampLength]
	parsed, err := time.Parse("20060102T150405000Z", timestamp)
	return err == nil && parsed.Location() == time.UTC && parsed.Format("20060102T150405000Z") == timestamp && validLowerHex(value[len(prefix)+timestampLength+1:], 8)
}

func validImportCounts(value archive.ImportCounts, enforcePortableLimits bool) bool {
	if value.Entries < 0 || value.Comments < 0 || value.Media < 0 {
		return false
	}
	return !enforcePortableLimits || value.Entries <= 100_000 && value.Comments <= 500_000 && value.Media <= 100_000
}

func decodeCanonicalUTCTime(raw json.RawMessage) (time.Time, error) {
	var text string
	if err := json.Unmarshal(raw, &text); err != nil || !strings.HasSuffix(text, "Z") {
		return time.Time{}, errors.New("timestamp is not a canonical UTC string")
	}
	value, err := time.Parse(time.RFC3339Nano, text)
	if err != nil || value.IsZero() || value.Location() != time.UTC || value.Format(time.RFC3339Nano) != text {
		return time.Time{}, errors.New("timestamp is not canonical UTC")
	}
	return value, nil
}

func validateImportDiskEstimate(value archive.Inspection, expectedArchiveBytes int64) error {
	disk := value.Disk
	if expectedArchiveBytes <= 0 || disk.ArchiveBytes != expectedArchiveBytes || disk.ExpandedBytes <= 0 || value.MediaBytes > disk.ExpandedBytes || !disk.SpaceOK {
		return errors.New("import inspection disk estimate has invalid base values")
	}
	jsonBytes := disk.ExpandedBytes - value.MediaBytes
	staging, err := checkedImportResponseAdd(value.MediaBytes, jsonBytes, jsonBytes)
	if err != nil || disk.StagingBytes != staging || disk.PreImportBackupBytes != value.Replacement.DataBytes || disk.RollbackReserveBytes != value.Replacement.DataBytes {
		return errors.New("import inspection staging estimate is invalid")
	}
	base, err := checkedImportResponseAdd(staging, value.Replacement.DataBytes, value.Replacement.DataBytes)
	if err != nil {
		return err
	}
	margin := base / 10
	if margin < int64(2<<30) {
		margin = int64(2 << 30)
	}
	required, err := checkedImportResponseAdd(base, margin)
	if err != nil || disk.SafetyMarginBytes != margin || disk.RequiredAdditionalBytes != required || disk.AvailableBytes < required {
		return errors.New("import inspection required disk estimate is invalid")
	}
	peak, err := checkedImportResponseAdd(disk.ArchiveBytes, value.Replacement.DataBytes, required)
	if err != nil || disk.EstimatedPeakFootprintBytes != peak {
		return errors.New("import inspection peak disk estimate is invalid")
	}
	return nil
}

func checkedImportResponseAdd(values ...int64) (int64, error) {
	var total int64
	for _, value := range values {
		if value < 0 || value > int64(^uint64(0)>>1)-total {
			return 0, errors.New("import inspection byte count overflows")
		}
		total += value
	}
	return total, nil
}

func backupInspectCLIError(err error) error {
	if err == nil {
		return nil
	}
	var commandErr *CommandError
	if errors.As(err, &commandErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		return canonicalPublicError(string(domainErr.Code), domainErr.Message, domainErr.Details, 0).WithCause(err)
	}
	if errors.Is(err, runtimefs.ErrIncompleteLayout) || errors.Is(err, runtimefs.ErrInvalidInstance) || errors.Is(err, runtimefs.ErrInvalidCurrent) {
		return NewCommandError("INSTANCE_NOT_READY", "the Moment data directory is not a complete readable instance", "initialize or repair the selected data directory before retrying import inspection", ExitIO).WithCause(err)
	}
	return err
}

func backupImportCLIError(err error) error {
	if err == nil {
		return nil
	}
	var commandErr *CommandError
	if errors.As(err, &commandErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		return canonicalPublicError(string(domainErr.Code), domainErr.Message, domainErr.Details, 0).WithCause(err)
	}
	if errors.Is(err, runtimefs.ErrIncompleteLayout) || errors.Is(err, runtimefs.ErrInvalidInstance) || errors.Is(err, runtimefs.ErrInvalidCurrent) {
		return NewCommandError("INSTANCE_NOT_READY", "the Moment data directory is not a complete ready instance", "repair the selected instance and run doctor before retrying import", ExitIO).WithCause(err)
	}
	return err
}

func runBackupExport(ctx context.Context, invocation Invocation, input CommandInput, appVersion string) (Result, error) {
	output, err := input.String("output")
	if err != nil {
		return Result{}, err
	}
	stdout, err := input.Bool("stdout")
	if err != nil {
		return Result{}, err
	}
	overwrite, err := input.Bool("overwrite")
	if err != nil {
		return Result{}, err
	}
	outputSelected := input.Changed("output")
	if outputSelected == stdout {
		return Result{}, invalidArgument("choose exactly one of --output or --stdout", "provide a destination path or explicitly select raw stdout")
	}
	if outputSelected && (strings.TrimSpace(output) == "" || strings.ContainsRune(output, 0)) {
		return Result{}, invalidArgument("--output must name a ZIP file", "provide a non-empty local destination path")
	}
	if stdout && invocation.Options().JSON {
		return Result{}, invalidArgument("--stdout cannot be combined with --json", "remove --json or use --output for a machine-readable result")
	}
	if stdout && overwrite {
		return Result{}, invalidArgument("--overwrite only applies to --output", "remove --overwrite when streaming to stdout")
	}
	if outputSelected && invocation.Mode() == ModeDirect {
		target, err := invocation.DirectTransport()
		if err != nil {
			return Result{}, err
		}
		if err := ensureBackupOutputOutsideDataDir(output, target.DataDirectory()); err != nil {
			return Result{}, err
		}
	}

	producer, err := backupProducer(ctx, invocation, appVersion)
	if err != nil {
		return Result{}, backupCLIError(err)
	}
	if stdout {
		return Result{Binary: func(destination io.Writer) error {
			_, err := producer(ctx, destination)
			return backupCLIError(err)
		}}, nil
	}
	summary, err := archive.WriteFile(ctx, output, overwrite, producer)
	if err != nil {
		return Result{}, backupCLIError(err)
	}
	return backupResult(summary), nil
}

func ensureBackupOutputOutsideDataDir(output, dataDir string) error {
	root, err := runtimefs.ResolveDataDir(dataDir)
	if err != nil {
		return backupOutputUnavailable(err)
	}
	realRoot, err := filepath.EvalSymlinks(root)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) {
			return nil
		}
		return backupOutputUnavailable(err)
	}
	rootInfo, err := os.Stat(realRoot)
	if err != nil {
		return backupOutputUnavailable(err)
	}
	outputPath, err := filepath.Abs(filepath.Clean(output))
	if err != nil {
		return backupOutputUnavailable(err)
	}
	realParent, err := filepath.EvalSymlinks(filepath.Dir(outputPath))
	if err != nil {
		return backupOutputUnavailable(err)
	}
	for candidate := realParent; ; candidate = filepath.Dir(candidate) {
		info, err := os.Stat(candidate)
		if err != nil {
			return backupOutputUnavailable(err)
		}
		if os.SameFile(rootInfo, info) {
			return NewCommandError("OUTPUT_UNAVAILABLE", "direct export output must be outside the selected Moment data directory", "choose an output path outside --data-dir and retry", ExitIO)
		}
		parent := filepath.Dir(candidate)
		if parent == candidate {
			break
		}
	}
	return nil
}

func backupOutputUnavailable(cause error) error {
	return NewCommandError("OUTPUT_UNAVAILABLE", "the archive output path cannot be resolved safely", "choose an existing writable directory outside --data-dir and retry", ExitIO).WithCause(cause)
}

func backupProducer(_ context.Context, invocation Invocation, appVersion string) (func(context.Context, io.Writer) (archive.Summary, error), error) {
	switch invocation.Mode() {
	case ModeDirect:
		target, err := invocation.DirectTransport()
		if err != nil {
			return nil, err
		}
		exporter, err := archive.NewExporter(archive.Options{DataDir: target.DataDirectory(), AppVersion: appVersion})
		if err != nil {
			return nil, err
		}
		return exporter.Export, nil
	case ModeAPI:
		return func(operationContext context.Context, destination io.Writer) (archive.Summary, error) {
			return downloadExport(operationContext, invocation, destination)
		}, nil
	default:
		return nil, errors.New("unsupported backup export transport")
	}
}

func downloadExport(ctx context.Context, invocation Invocation, destination io.Writer) (archive.Summary, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return archive.Summary{}, err
	}
	token, err := readAPIToken(target)
	if err != nil {
		return archive.Summary{}, err
	}
	endpoint, err := url.JoinPath(target.BaseURL().String(), "/api/v1/owner/export")
	if err != nil {
		return archive.Summary{}, invalidArgument("the export endpoint URL is invalid", "check --url and retry")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, http.NoBody)
	if err != nil {
		return archive.Summary{}, err
	}
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("Accept", "application/zip")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return archive.Summary{}, err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode >= 200 && response.StatusCode < 300 && response.StatusCode != http.StatusOK {
		return archive.Summary{}, invalidAPIResponse()
	}
	if response.StatusCode != http.StatusOK {
		body, readErr := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
		if readErr != nil {
			return archive.Summary{}, readErr
		}
		if len(body) > maxAPIResponse {
			return archive.Summary{}, invalidAPIResponse()
		}
		if !validAPIJSONContentType(response.Header.Get("Content-Type")) || validateUniqueJSONObjects(body) != nil {
			return archive.Summary{}, invalidAPIResponse()
		}
		return archive.Summary{}, decodeOwnerAPIFailure(response.StatusCode, body)
	}
	mediaType, _, parseErr := mime.ParseMediaType(response.Header.Get("Content-Type"))
	if parseErr != nil || mediaType != "application/zip" {
		return archive.Summary{}, invalidAPIResponse()
	}
	if response.Header.Get("X-Archive-Format") != archive.FormatHeaderValue {
		return archive.Summary{}, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the server did not confirm Moment export format v1", "upgrade the server or inspect the reverse proxy before retrying", ExitUnavailable)
	}
	digest := sha256.New()
	counted := &backupDigestWriter{writer: io.MultiWriter(&backupDestinationWriter{writer: destination}, digest)}
	copied, err := io.CopyBuffer(counted, &backupContextReader{ctx: ctx, reader: response.Body}, make([]byte, 128<<10))
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return archive.Summary{}, contextErr
		}
		var destinationErr *backupDestinationError
		if errors.As(err, &destinationErr) {
			return archive.Summary{}, fmt.Errorf("%w: %w", archive.ErrDestinationUnavailable, destinationErr.Unwrap())
		}
		return archive.Summary{}, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the archive download ended unexpectedly", "retry the export and inspect the server or reverse proxy", ExitUnavailable).WithCause(err)
	}
	if response.ContentLength >= 0 && copied != response.ContentLength {
		return archive.Summary{}, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the archive download ended before its declared length", "retry the export and inspect the server or reverse proxy", ExitUnavailable)
	}
	if copied == 0 {
		return archive.Summary{}, NewCommandError("DEPENDENCY_INVALID_RESPONSE", "the archive download was empty", "retry the export and inspect the server or reverse proxy", ExitUnavailable)
	}
	return archive.Summary{FormatVersion: archive.FormatVersion, ArchiveBytes: counted.size, ArchiveSHA256: hex.EncodeToString(digest.Sum(nil))}, nil
}

func backupResult(summary archive.Summary) Result {
	return Result{
		Data: summary,
		Text: fmt.Sprintf("Exported Moment archive to %s\nFormat: v%d\nBytes: %d\nSHA-256: %s", summary.Path, summary.FormatVersion, summary.ArchiveBytes, summary.ArchiveSHA256),
	}
}

func backupCLIError(err error) error {
	if err == nil {
		return nil
	}
	var commandErr *CommandError
	if errors.As(err, &commandErr) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	if errors.Is(err, fs.ErrExist) {
		return NewCommandError("OUTPUT_EXISTS", "the archive output path already exists", "choose another --output path or explicitly add --overwrite", ExitConflict).WithCause(err)
	}
	if errors.Is(err, runtimefs.ErrIncompleteLayout) || errors.Is(err, runtimefs.ErrInvalidInstance) || errors.Is(err, runtimefs.ErrInvalidCurrent) || errors.Is(err, runtimefs.ErrEnvironmentMismatch) {
		return NewCommandError("INSTANCE_NOT_READY", "the Moment data directory is not a complete readable instance", "initialize or repair the selected data directory before retrying export", ExitIO).WithCause(err)
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		return canonicalPublicError(string(domainErr.Code), domainErr.Message, domainErr.Details, 0).WithCause(err)
	}
	if errors.Is(err, archive.ErrDestinationUnavailable) {
		return NewCommandError("OUTPUT_UNAVAILABLE", "the archive output path cannot be published safely", "choose a writable file path with an existing directory, then retry", ExitIO).WithCause(err)
	}
	return err
}

type backupContextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (reader *backupContextReader) Read(data []byte) (int, error) {
	if err := reader.ctx.Err(); err != nil {
		return 0, err
	}
	return reader.reader.Read(data)
}

type backupDigestWriter struct {
	writer io.Writer
	size   int64
}

type backupDestinationWriter struct{ writer io.Writer }

func (writer *backupDestinationWriter) Write(data []byte) (int, error) {
	written, err := writer.writer.Write(data)
	if err == nil && written != len(data) {
		err = io.ErrShortWrite
	}
	if err != nil {
		return written, &backupDestinationError{cause: err}
	}
	return written, nil
}

type backupDestinationError struct{ cause error }

func (err *backupDestinationError) Error() string { return "archive destination write failed" }
func (err *backupDestinationError) Unwrap() error { return err.cause }

func (writer *backupDigestWriter) Write(data []byte) (int, error) {
	written, err := writer.writer.Write(data)
	writer.size += int64(written)
	return written, err
}

type singleStringFlag struct {
	value string
	set   bool
}

func (flag *singleStringFlag) Set(value string) error {
	if flag.set {
		return errors.New("flag may only be provided once")
	}
	flag.value, flag.set = value, true
	return nil
}
func (flag *singleStringFlag) String() string { return flag.value }
func (*singleStringFlag) Type() string        { return "string" }

type singleBoolFlag struct {
	value bool
	set   bool
}

func (flag *singleBoolFlag) Set(value string) error {
	if flag.set {
		return errors.New("flag may only be provided once")
	}
	parsed, err := strconv.ParseBool(value)
	if err != nil {
		return err
	}
	flag.value, flag.set = parsed, true
	return nil
}
func (flag *singleBoolFlag) String() string { return strconv.FormatBool(flag.value) }
func (*singleBoolFlag) Type() string        { return "bool" }
func (*singleBoolFlag) IsBoolFlag() bool    { return true }

func registerSingleStringFlag(flags *pflag.FlagSet, name, usage string) {
	flags.Var(&singleStringFlag{}, name, usage)
}

func registerSingleBoolFlag(flags *pflag.FlagSet, name, usage string) {
	flags.Var(&singleBoolFlag{}, name, usage)
	flags.Lookup(name).NoOptDefVal = "true"
}
