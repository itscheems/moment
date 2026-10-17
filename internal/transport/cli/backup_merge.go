package cli

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/buildinfo"
	"github.com/moment/moment/internal/runtimefs"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
)

const (
	mergeSessionVersion = 1
	mergeSessionLimit   = int64(64 << 20)
	mergeAPIResponseMax = int64(64 << 20)
)

type mergeSessionDocument struct {
	Version      int                    `json:"version"`
	StagingToken string                 `json:"staging_token"`
	Selection    archive.MergeSelection `json:"selection"`
}

func registerBackupMerge(application *Application, info buildinfo.Info) {
	application.MustRegisterLeaf(LeafSpec{
		Path:      []string{"backup", "merge-plan"},
		ArgsUsage: "<archive.zip>",
		Short:     "Plan a conflict-aware portable backup merge",
		Long:      "Validate one portable archive, compare every portable authority record with the current instance, and create a restartable explicit selection without changing active data.",
		Inputs:    "One regular non-symlink v1 ZIP and required --selection-output <json>. The private selection file contains the expiring staging credential and editable default actions; keep its mode at 0600.",
		Output:    "Human output reports deterministic counts. JSON also includes bounded local and incoming post, comment, location, profile, avatar, cover, and pinned-post review data. The private selection file is written atomically for editing before backup merge.",
		Errors:    "hostile input, unsafe output paths, active imports, disk pressure, cancellation, and direct-mode server use fail with stable errors.",
		Safety:    "Read-only for active authority. Planning creates isolated expiring staging but never creates a pre-merge backup or switches CURRENT.",
		Example: "moment --as writer backup merge-plan ./moment-export.zip --data-dir ./moment-data --selection-output ./merge-selection.json --json\n" +
			"moment --url http://127.0.0.1:8080 --token-file ./account.token backup merge-plan ./moment-export.zip --selection-output ./merge-selection.json --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Flags: func(flags *pflag.FlagSet) {
			registerSingleStringFlag(flags, "selection-output", "atomically write the private editable merge selection JSON")
		},
		Run: func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
			return runBackupMergePlan(ctx, invocation, input, info.Version)
		},
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:      []string{"backup", "merge"},
		ArgsUsage: "<merge-selection.json>",
		Short:     "Commit a reviewed portable backup merge",
		Long:      "Consume one private selection produced by backup merge-plan, rebuild the merged authority graph, create and verify a pre-merge backup, atomically switch CURRENT, prove readiness, and automatically roll back on failure.",
		Inputs:    "One exact 0600 selection JSON. Every post, comment, profile field, and dependency decision must remain bound to the staged archive and base authority.",
		Output:    "Dry-run reports action counts without consuming the staging credential. Commit reports the same durable import status as replacement restore; resolve ambiguous API outcomes with backup status.",
		Errors:    "Stale, incomplete, swapped, expired, replayed, unsafe-permission, maintenance, and rollback failures use stable errors and never silently fall back to replacement.",
		Safety:    "Destructive. --yes bypasses only the confirmation prompt. It never bypasses exact selection, current-authority digest, current account credential, archive hash, backup, readiness, or rollback checks.",
		Example: "moment --as writer backup merge ./merge-selection.json --data-dir ./moment-data --dry-run --json\n" +
			"moment --url http://127.0.0.1:8080 --token-file ./account.token backup merge ./merge-selection.json --yes --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Policy: SafetyPolicy{
			Destructive: true, SupportsDryRun: true, DeferredConfirmation: true,
			Confirmation: "Commit this exact reviewed backup merge?",
		},
		Run: func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
			return runBackupMerge(ctx, invocation, input, info.Version)
		},
	})
	application.MustRegisterLeaf(LeafSpec{
		Path:      []string{"backup", "merge-discard"},
		ArgsUsage: "<merge-selection.json>",
		Short:     "Discard an uncommitted portable backup merge plan",
		Long:      "Consume the private staging credential only to remove its unclaimed merge staging generation, allowing a different backup inspection to start without waiting for expiry.",
		Inputs:    "One exact 0600 selection JSON produced by backup merge-plan. The current account credential and staging credential must still match; committing or terminal plans cannot be cancelled.",
		Output:    "Reports the discarded plan ID. Active authority, CURRENT, backups, and durable import status are unchanged.",
		Errors:    "Expired, claimed, replayed, mismatched, unsafe-permission, active-server direct mode, and cancellation failures use stable errors.",
		Safety:    "Removes only isolated uncommitted staging bound to the supplied credential. Direct mode requires --as naming an active administrator and the server to be stopped; API mode asks the live account service to discard it.",
		Example: "moment --as writer backup merge-discard ./merge-selection.json --data-dir ./moment-data --json\n" +
			"moment --url http://127.0.0.1:8080 --token-file ./account.token backup merge-discard ./merge-selection.json --json",
		Args:       cobra.ExactArgs(1),
		Capability: CapabilityBoth,
		Run: func(ctx context.Context, invocation Invocation, input CommandInput) (Result, error) {
			return runBackupMergeDiscard(ctx, invocation, input, info.Version)
		},
	})
}

func runBackupMergePlan(ctx context.Context, invocation Invocation, input CommandInput, appVersion string) (Result, error) {
	output, err := input.String("selection-output")
	if err != nil {
		return Result{}, err
	}
	if !input.Changed("selection-output") || strings.TrimSpace(output) == "" || strings.ContainsRune(output, 0) {
		return Result{}, invalidArgument("--selection-output is required", "provide a private JSON path to review and commit the merge")
	}
	archivePath := input.Args()[0]
	var inspection archive.MergeInspection
	var meta map[string]any
	switch invocation.Mode() {
	case ModeDirect:
		target, err := invocation.DirectTransport()
		if err != nil {
			return Result{}, err
		}
		root, err := runtimefs.ResolveDataDir(target.DataDirectory())
		if err != nil {
			return Result{}, backupImportCLIError(err)
		}
		status, err := auth.ReadStatus(root)
		if err != nil || !status.Initialized || status.SessionEpoch == 0 {
			return Result{}, authenticationError(errors.Join(err, auth.ErrNotInitialized))
		}
		committer, err := archive.NewCommitter(archive.CommitOptions{DataDir: root, AppVersion: appVersion})
		if err != nil {
			return Result{}, backupImportCLIError(err)
		}
		inspection, err = committer.InspectMergeFileOffline(ctx, archivePath, directArchiveSubject(ctx))
		if err != nil {
			return Result{}, backupImportCLIError(err)
		}
	case ModeAPI:
		inspection, meta, err = uploadMergeInspection(ctx, invocation, archivePath)
		if err != nil {
			return Result{}, backupImportCLIError(err)
		}
	default:
		return Result{}, errors.New("unsupported backup merge-plan transport")
	}
	document := mergeSessionDocument{Version: mergeSessionVersion, StagingToken: inspection.StagingToken, Selection: inspection.Plan.DefaultSelection}
	if err := writePrivateMergeSession(output, document); err != nil {
		return Result{}, backupImportCLIError(err)
	}
	inspection.StagingToken = ""
	text := fmt.Sprintf("Merge plan created\nUnchanged: %d\nIncoming only: %d\nLocal only: %d\nConflicts: %d\nPrivate selection: %s\nExpires: %s",
		inspection.Plan.Summary.Unchanged, inspection.Plan.Summary.IncomingOnly, inspection.Plan.Summary.LocalOnly, inspection.Plan.Summary.Conflict,
		output, inspection.ExpiresAt.Format(time.RFC3339))
	return Result{Data: inspection, Text: text, Meta: meta}, nil
}

func runBackupMerge(ctx context.Context, invocation Invocation, input CommandInput, appVersion string) (Result, error) {
	document, err := readPrivateMergeSession(input.Args()[0])
	if err != nil {
		return Result{}, backupImportCLIError(err)
	}
	counts := mergeActionCounts(document.Selection)
	if invocation.Safety().DryRun {
		return Result{Data: map[string]any{"dry_run": true, "plan_id": document.Selection.PlanID, "actions": counts}, Text: mergeActionText("Merge dry-run", counts)}, nil
	}
	if err := invocation.ConfirmScope(mergeActionText("Merge preview", counts) + "\nCommit this exact reviewed backup merge?"); err != nil {
		return Result{}, err
	}
	var result archive.CommitResult
	var meta map[string]any
	switch invocation.Mode() {
	case ModeDirect:
		target, err := invocation.DirectTransport()
		if err != nil {
			return Result{}, err
		}
		root, err := runtimefs.ResolveDataDir(target.DataDirectory())
		if err != nil {
			return Result{}, backupImportCLIError(err)
		}
		status, err := auth.ReadStatus(root)
		if err != nil || !status.Initialized || status.SessionEpoch == 0 {
			return Result{}, authenticationError(errors.Join(err, auth.ErrNotInitialized))
		}
		committer, err := archive.NewCommitter(archive.CommitOptions{DataDir: root, AppVersion: appVersion})
		if err != nil {
			return Result{}, backupImportCLIError(err)
		}
		result, err = committer.CommitMerge(ctx, document.StagingToken, directArchiveSubject(ctx), document.Selection, nil)
		if err != nil {
			return Result{}, backupImportCLIError(err)
		}
	case ModeAPI:
		result, meta, err = commitMergeAPI(ctx, invocation, document)
		if err != nil {
			return Result{}, backupImportCLIError(err)
		}
	default:
		return Result{}, errors.New("unsupported backup merge transport")
	}
	text := fmt.Sprintf("Merge committed\nPhase: %s\nActive generation: %s\nVerified backup: %s", result.Status.Phase, result.Status.ActiveGeneration, result.Status.BackupName)
	return Result{Data: result, Text: text, Meta: meta}, nil
}

func runBackupMergeDiscard(ctx context.Context, invocation Invocation, input CommandInput, appVersion string) (Result, error) {
	document, err := readPrivateMergeSession(input.Args()[0])
	if err != nil {
		return Result{}, backupImportCLIError(err)
	}
	var meta map[string]any
	switch invocation.Mode() {
	case ModeDirect:
		target, err := invocation.DirectTransport()
		if err != nil {
			return Result{}, err
		}
		root, err := runtimefs.ResolveDataDir(target.DataDirectory())
		if err != nil {
			return Result{}, backupImportCLIError(err)
		}
		status, err := auth.ReadStatus(root)
		if err != nil || !status.Initialized || status.SessionEpoch == 0 {
			return Result{}, authenticationError(errors.Join(err, auth.ErrNotInitialized))
		}
		committer, err := archive.NewCommitter(archive.CommitOptions{DataDir: root, AppVersion: appVersion})
		if err != nil {
			return Result{}, backupImportCLIError(err)
		}
		if err := committer.DiscardMergeFileOffline(ctx, document.StagingToken, directArchiveSubject(ctx)); err != nil {
			return Result{}, backupImportCLIError(err)
		}
	case ModeAPI:
		meta, err = discardMergeAPI(ctx, invocation, document.StagingToken)
		if err != nil {
			return Result{}, backupImportCLIError(err)
		}
	default:
		return Result{}, errors.New("unsupported backup merge-discard transport")
	}
	data := map[string]any{"discarded": true, "plan_id": document.Selection.PlanID}
	return Result{Data: data, Text: "Merge plan discarded\nPlan: " + document.Selection.PlanID, Meta: meta}, nil
}

func mergeActionCounts(selection archive.MergeSelection) map[string]int {
	result := map[string]int{"add": 0, "skip": 0, "keep_local": 0, "use_incoming": 0, "keep_both": 0, "profile_keep_local": 0, "profile_use_incoming": 0}
	for _, item := range selection.Items {
		result[string(item.Action)]++
	}
	for _, item := range selection.SiteFields {
		result["profile_"+string(item.Action)]++
	}
	return result
}

func mergeActionText(title string, counts map[string]int) string {
	return fmt.Sprintf("%s\nAdd: %d\nSkip: %d\nKeep local: %d\nUse incoming: %d\nKeep both: %d\nProfile keep local: %d\nProfile use incoming: %d",
		title, counts["add"], counts["skip"], counts["keep_local"], counts["use_incoming"], counts["keep_both"], counts["profile_keep_local"], counts["profile_use_incoming"])
}

func uploadMergeInspection(ctx context.Context, invocation Invocation, archivePath string) (archive.MergeInspection, map[string]any, error) {
	body, _, err := uploadImportArchiveBounded(ctx, invocation, archivePath, "/api/v1/backups/import/merge/plan", http.StatusCreated, "", importArchiveIdentity{}, mergeAPIResponseMax)
	if err != nil {
		return archive.MergeInspection{}, nil, err
	}
	return decodeMergeInspectionAPI(body, time.Now().UTC())
}

func decodeMergeInspectionAPI(body []byte, observedAt time.Time) (archive.MergeInspection, map[string]any, error) {
	envelope, err := decodeExactJSONObject(body, "data", "meta")
	if err != nil {
		return archive.MergeInspection{}, nil, err
	}
	if _, err := decodeExactJSONObjectAllowNull(envelope["data"], []string{"merge_blocked_reason"}, "plan", "review", "incoming_counts", "incoming_media_bytes", "disk", "replacement", "merge_allowed", "merge_blocked_reason", "warnings", "expires_at", "staging_token"); err != nil {
		return archive.MergeInspection{}, nil, err
	}
	metadata, err := decodeExactJSONObject(envelope["meta"], "request_id")
	if err != nil {
		return archive.MergeInspection{}, nil, err
	}
	var value archive.MergeInspection
	decoder := json.NewDecoder(bytes.NewReader(envelope["data"]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&value); err != nil || decoder.Decode(&struct{}{}) != io.EOF || archive.ValidateMergePlan(value.Plan) != nil || archive.ValidateMergeReview(value.Plan, value.Review) != nil {
		return archive.MergeInspection{}, nil, errors.New("merge inspection data is invalid")
	}
	token, err := base64.RawURLEncoding.Strict().DecodeString(value.StagingToken)
	if err != nil || len(token) != 32 || !value.ExpiresAt.After(observedAt.Add(-importInspectionClockSkew)) || value.ExpiresAt.After(observedAt.Add(importInspectionTokenTTL+importInspectionClockSkew)) || !value.ExpiresAt.Equal(value.Plan.ExpiresAt) {
		return archive.MergeInspection{}, nil, errors.New("merge inspection token or expiry is invalid")
	}
	if value.Counts.Entries < 0 || value.Counts.Comments < 0 || value.Counts.Media < 0 || value.MediaBytes < 0 || value.Warnings == nil || !value.MergeAllowed || value.MergeBlockedReason != nil {
		return archive.MergeInspection{}, nil, errors.New("merge inspection counts are invalid")
	}
	requestID, err := decodeImportRequestID(metadata["request_id"])
	if err != nil {
		return archive.MergeInspection{}, nil, err
	}
	return value, map[string]any{"request_id": requestID}, nil
}

func commitMergeAPI(ctx context.Context, invocation Invocation, document mergeSessionDocument) (archive.CommitResult, map[string]any, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	accountToken, err := readAPIToken(target)
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	endpoint, err := url.JoinPath(target.BaseURL().String(), "/api/v1/backups/import/merge/commit")
	if err != nil {
		return archive.CommitResult{}, nil, invalidArgument("the import merge endpoint URL is invalid", "check --url and retry")
	}
	body, err := json.Marshal(map[string]any{"staging_token": document.StagingToken, "selection": document.Selection})
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	request.Header.Set("Authorization", "Bearer "+accountToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return archive.CommitResult{}, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil || len(responseBody) > maxAPIResponse || !validAPIJSONContentType(response.Header.Get("Content-Type")) || validateUniqueJSONObjects(responseBody) != nil {
		return archive.CommitResult{}, nil, errors.Join(invalidAPIResponse(), err)
	}
	if response.StatusCode != http.StatusOK {
		return archive.CommitResult{}, nil, decodeAPIFailure(response.StatusCode, responseBody)
	}
	result, meta, err := decodeImportCommitAPI(responseBody)
	if err != nil {
		return archive.CommitResult{}, nil, invalidAPIResponse()
	}
	return result, meta, nil
}

func discardMergeAPI(ctx context.Context, invocation Invocation, stagingToken string) (map[string]any, error) {
	target, err := invocation.APITransport()
	if err != nil {
		return nil, err
	}
	accountToken, err := readAPIToken(target)
	if err != nil {
		return nil, err
	}
	endpoint, err := url.JoinPath(target.BaseURL().String(), "/api/v1/backups/import/merge/discard")
	if err != nil {
		return nil, invalidArgument("the import merge discard endpoint URL is invalid", "check --url and retry")
	}
	body, err := json.Marshal(map[string]string{"staging_token": stagingToken})
	if err != nil {
		return nil, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	request.Header.Set("Authorization", "Bearer "+accountToken)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return nil, err
	}
	defer func() { _ = response.Body.Close() }()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxAPIResponse+1))
	if err != nil || len(responseBody) > maxAPIResponse || !validAPIJSONContentType(response.Header.Get("Content-Type")) || validateUniqueJSONObjects(responseBody) != nil {
		return nil, errors.Join(invalidAPIResponse(), err)
	}
	if response.StatusCode != http.StatusOK {
		return nil, decodeAPIFailure(response.StatusCode, responseBody)
	}
	envelope, err := decodeExactJSONObject(responseBody, "data", "meta")
	if err != nil {
		return nil, invalidAPIResponse()
	}
	data, err := decodeExactJSONObject(envelope["data"], "discarded")
	if err != nil {
		return nil, invalidAPIResponse()
	}
	var discarded bool
	if err := json.Unmarshal(data["discarded"], &discarded); err != nil || !discarded {
		return nil, invalidAPIResponse()
	}
	metadata, err := decodeExactJSONObject(envelope["meta"], "request_id")
	if err != nil {
		return nil, invalidAPIResponse()
	}
	requestID, err := decodeImportRequestID(metadata["request_id"])
	if err != nil {
		return nil, invalidAPIResponse()
	}
	return map[string]any{"request_id": requestID}, nil
}

func writePrivateMergeSession(path string, document mergeSessionDocument) (resultErr error) {
	data, err := json.MarshalIndent(document, "", "  ")
	if err != nil || int64(len(data)) > mergeSessionLimit {
		return errors.Join(errors.New("merge selection file exceeds its bound"), err)
	}
	data = append(data, '\n')
	clean, err := filepath.Abs(filepath.Clean(path))
	if err != nil || filepath.Base(clean) == "." || filepath.Base(clean) == ".." {
		return errors.Join(errors.New("merge selection output path is invalid"), err)
	}
	parent := filepath.Dir(clean)
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.Join(errors.New("merge selection output directory is unsafe"), err)
	}
	if _, err := os.Lstat(clean); err == nil {
		return errors.New("merge selection output already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	temporary, err := os.CreateTemp(parent, ".merge-selection-*")
	if err != nil {
		return err
	}
	temporaryPath := temporary.Name()
	published := false
	defer func() {
		if !published {
			_ = os.Remove(temporaryPath)
		}
	}()
	if err := temporary.Chmod(0o600); err != nil {
		_ = temporary.Close()
		return err
	}
	if _, err := temporary.Write(data); err != nil {
		_ = temporary.Close()
		return err
	}
	if err := errors.Join(temporary.Sync(), temporary.Close()); err != nil {
		return err
	}
	if err := os.Rename(temporaryPath, clean); err != nil {
		return err
	}
	published = true
	return runtimefs.SyncDirectory(parent)
}

func readPrivateMergeSession(path string) (mergeSessionDocument, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Mode().Perm()&0o077 != 0 || info.Size() < 2 || info.Size() > mergeSessionLimit {
		return mergeSessionDocument{}, errors.Join(errors.New("merge selection must be a private 0600 regular file"), err)
	}
	data, err := os.ReadFile(path)
	if err != nil || int64(len(data)) > mergeSessionLimit || data[len(data)-1] != '\n' || validateUniqueJSONObjects(data) != nil {
		return mergeSessionDocument{}, errors.Join(errors.New("merge selection file bytes are invalid"), err)
	}
	var document mergeSessionDocument
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || decoder.Decode(&struct{}{}) != io.EOF || document.Version != mergeSessionVersion || document.StagingToken == "" || len(document.StagingToken) > 256 {
		return mergeSessionDocument{}, errors.New("merge selection file schema is invalid")
	}
	return document, nil
}
