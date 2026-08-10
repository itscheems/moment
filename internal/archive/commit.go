package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const (
	commitJournalVersion = 1
	commitJournalLimit   = int64(32 << 10)
	rollbackTimeout      = 2 * time.Minute
	directServeLockWait  = 150 * time.Millisecond
)

// CommitPhase is a stable import status consumed by CLI, Web, and doctor.
type CommitPhase string

const (
	// CommitClaimed means the one-time staging token has been claimed.
	CommitClaimed CommitPhase = "claimed"
	// CommitBackingUp means the verified pre-import backup is being created.
	CommitBackingUp CommitPhase = "backing_up"
	// CommitFinalizing means the staged generation is being durably published.
	CommitFinalizing CommitPhase = "finalizing"
	// CommitSwitching means CURRENT is being atomically changed.
	CommitSwitching CommitPhase = "switching"
	// CommitVerifying means the new live generation is undergoing readiness checks.
	CommitVerifying CommitPhase = "verifying"
	// CommitRollingBack means recovery of the previous generation is in progress.
	CommitRollingBack CommitPhase = "rolling_back"
	// CommitSucceeded is the successful terminal state.
	CommitSucceeded CommitPhase = "succeeded"
	// CommitFailed is a terminal failure before CURRENT changed.
	CommitFailed CommitPhase = "failed"
	// CommitRolledBack is a terminal failure with verified automatic recovery.
	CommitRolledBack CommitPhase = "rolled_back"
	// CommitNeedsAttention is a fail-closed state requiring operator recovery.
	CommitNeedsAttention CommitPhase = "needs_attention"
)

// CommitStatus deliberately excludes the staging token, token hash, archive
// hash, and absolute local paths.
type CommitStatus struct {
	OperationID        string      `json:"operation_id"`
	Phase              CommitPhase `json:"phase"`
	ActiveGeneration   string      `json:"active_generation"`
	PreviousGeneration string      `json:"previous_generation"`
	NewGeneration      string      `json:"new_generation"`
	BackupName         string      `json:"backup_name,omitempty"`
	BackupVerified     bool        `json:"backup_verified"`
	RollbackAttempted  bool        `json:"rollback_attempted"`
	RollbackSucceeded  bool        `json:"rollback_succeeded"`
	ErrorCode          string      `json:"error_code,omitempty"`
	StartedAt          time.Time   `json:"started_at"`
	UpdatedAt          time.Time   `json:"updated_at"`
	CompletedAt        *time.Time  `json:"completed_at,omitempty"`
}

// CommitResult is identical across direct and API transports.
type CommitResult struct {
	Status CommitStatus `json:"status"`
}

// CommitHost owns live HTTP admission and the currently published service
// bundle. BeginMaintenance must stop new ordinary requests and drain admitted
// work before it returns.
type CommitHost interface {
	BeginMaintenance(context.Context) error
	CurrentSessionEpoch() (uint64, error)
	ActiveDatabase() *sqlitestore.Database
	CloseActive() error
	Activate(context.Context, *runtimefs.Runtime) error
	EndMaintenance()
}

// CommitOptions supplies deterministic seams without weakening production
// validation.
type CommitOptions struct {
	DataDir        string
	AppVersion     string
	Clock          func() time.Time
	AvailableBytes func(string) (uint64, error)
}

// Committer promotes inspected staging generations through one durable state
// machine.
type Committer struct {
	dataDir        string
	clock          func() time.Time
	availableBytes func(string) (uint64, error)
	inspector      *Inspector
	exporter       *Exporter
	switchCurrent  func(context.Context, *runtimefs.Runtime, string, *runtimefs.LeaseSet) error
}

// NewCommitter constructs the import commit use case.
func NewCommitter(options CommitOptions) (*Committer, error) {
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.AvailableBytes == nil {
		options.AvailableBytes = runtimefs.AvailableBytes
	}
	inspector, err := NewInspector(InspectOptions{DataDir: options.DataDir, Clock: options.Clock, AvailableBytes: options.AvailableBytes})
	if err != nil {
		return nil, err
	}
	exporter, err := NewExporter(Options{DataDir: options.DataDir, AppVersion: options.AppVersion, Clock: options.Clock})
	if err != nil {
		return nil, err
	}
	return &Committer{
		dataDir: options.DataDir, clock: options.Clock, availableBytes: options.AvailableBytes,
		inspector: inspector, exporter: exporter,
		switchCurrent: func(ctx context.Context, runtimeState *runtimefs.Runtime, id string, held *runtimefs.LeaseSet) error {
			return runtimeState.SwitchCurrent(ctx, id, held)
		},
	}, nil
}

// Commit claims and consumes exactly one staged token. API callers supply a
// host; direct callers pass nil and therefore also acquire serve.lock.
func (committer *Committer) Commit(ctx context.Context, token string, sessionEpoch uint64, host CommitHost) (result CommitResult, resultErr error) {
	if committer == nil || sessionEpoch == 0 {
		return CommitResult{}, domain.NewError(domain.CodeInvalidArgument, "import commit requires a staging token and owner epoch", nil)
	}
	runtimeState, err := runtimefs.OpenForCoordination(committer.dataDir)
	if err != nil {
		return CommitResult{}, err
	}
	held, err := runtimeState.Locks.AcquireSet(ctx, runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive})
	if err != nil {
		return CommitResult{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, held.Release()) }()
	if host == nil {
		lockContext, cancelLock := context.WithTimeout(ctx, directServeLockWait)
		lockErr := runtimeState.Locks.ExtendSet(lockContext, held, runtimefs.LockRequest{Name: runtimefs.ServeLock, Mode: runtimefs.Exclusive})
		cancelLock()
		if lockErr != nil {
			if err := ctx.Err(); err != nil {
				return CommitResult{}, err
			}
			if errors.Is(lockErr, context.DeadlineExceeded) {
				return CommitResult{}, domain.NewError(domain.CodeInstanceInUse, "Moment is serving this instance", nil)
			}
			return CommitResult{}, errors.Join(domain.NewError(domain.CodeInstanceInUse, "Moment is serving this instance", nil), lockErr)
		}
	}
	if err := runtimeState.RefreshCurrent(held); err != nil {
		return CommitResult{}, err
	}
	current := runtimeState.Current
	claim, err := committer.inspector.ClaimWithinLocks(ctx, runtimeState, token, sessionEpoch, held)
	if err != nil {
		return CommitResult{}, err
	}
	now := committer.now()
	status := CommitStatus{
		OperationID: claim.GenerationID, Phase: CommitClaimed, ActiveGeneration: current.ID,
		PreviousGeneration: current.ID, NewGeneration: claim.GenerationID, StartedAt: now, UpdatedAt: now,
	}
	if err := committer.writeStatus(claim.StageRoot, status); err != nil {
		_ = committer.inspector.FinishClaim(claim, false, held)
		return CommitResult{}, err
	}
	terminal := false
	bundleReady := false
	defer func() {
		if !terminal && resultErr != nil {
			actual, resolveErr := runtimeState.ResolveCurrent()
			if resolveErr == nil {
				status.ActiveGeneration = actual.ID
			}
			completed := committer.nextStatusTime(status)
			status.Phase, status.ErrorCode, status.UpdatedAt, status.CompletedAt = CommitFailed, errorCode(resultErr), completed, &completed
			if resolveErr != nil || (host != nil && !bundleReady) ||
				(status.ActiveGeneration != status.PreviousGeneration && status.ActiveGeneration != status.NewGeneration) {
				status.Phase = CommitNeedsAttention
			}
			_ = committer.writeStatus(claim.StageRoot, status)
			_ = committer.inspector.FinishClaim(claim, false, held)
		}
		// Keep admission closed until the terminal journal and unreplayable
		// staging state have both been persisted.
		if host != nil && bundleReady {
			host.EndMaintenance()
		}
	}()

	if host != nil {
		if err := host.BeginMaintenance(ctx); err != nil {
			return CommitResult{}, err
		}
		bundleReady = true
	}
	if err := runtimeState.Locks.ExtendSet(ctx, held,
		runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Exclusive},
		runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Exclusive},
	); err != nil {
		return CommitResult{}, err
	}
	// Claim occurred before waits; reject expiration or owner rotation again
	// before backup or active-data closure.
	currentEpoch := uint64(0)
	if host != nil {
		currentEpoch, err = host.CurrentSessionEpoch()
	} else {
		var ownerStatus auth.Status
		ownerStatus, err = auth.ReadStatus(runtimeState.Root)
		currentEpoch = ownerStatus.SessionEpoch
	}
	if err != nil || currentEpoch != sessionEpoch {
		return CommitResult{}, errors.Join(domain.NewError(domain.CodeUnauthorized, "owner authority changed while import commit waited", nil), err)
	}
	if err := committer.revalidateClaim(ctx, runtimeState, claim, currentEpoch); err != nil {
		return CommitResult{}, err
	}

	activeDatabase := (*sqlitestore.Database)(nil)
	closeDirect := false
	if host != nil {
		activeDatabase = host.ActiveDatabase()
	} else {
		activeDatabase, err = sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
		closeDirect = err == nil
	}
	if err != nil || activeDatabase == nil {
		if host != nil {
			bundleReady = false
		}
		return CommitResult{}, errors.Join(errors.New("open active database for import commit"), err)
	}
	defer func() {
		if closeDirect {
			resultErr = errors.Join(resultErr, activeDatabase.Close())
		}
	}()
	if err := committer.checkFinalDisk(ctx, runtimeState, activeDatabase); err != nil {
		return CommitResult{}, err
	}

	status.Phase = CommitBackingUp
	status.UpdatedAt = committer.nextStatusTime(status)
	if err := committer.writeStatus(claim.StageRoot, status); err != nil {
		return CommitResult{}, err
	}
	backupName := fmt.Sprintf("pre-import-%s-%s.zip", committer.now().Format("20060102T150405000Z"), current.ID)
	backupPath := filepath.Join(runtimeState.Root, "backups", backupName)
	if _, err := WriteFile(ctx, backupPath, false, func(writeCtx context.Context, destination io.Writer) (Summary, error) {
		return committer.exporter.ExportWithinLocks(writeCtx, destination, runtimeState, activeDatabase, held)
	}); err != nil {
		return CommitResult{}, err
	}
	if err := ValidateFileContext(ctx, backupPath); err != nil {
		return CommitResult{}, err
	}
	status.BackupName, status.BackupVerified = backupName, true
	status.Phase, status.UpdatedAt = CommitFinalizing, committer.nextStatusTime(status)
	if err := committer.writeStatus(claim.StageRoot, status); err != nil {
		return CommitResult{}, err
	}

	if closeDirect {
		if err := activeDatabase.Close(); err != nil {
			return CommitResult{}, err
		}
		closeDirect = false
	} else {
		bundleReady = false
		if err := host.CloseActive(); err != nil {
			reactivateErr := committer.reactivateAfterFailure(ctx, runtimeState, current.ID, host)
			bundleReady = reactivateErr == nil
			return CommitResult{}, errors.Join(err, reactivateErr)
		}
	}
	bundleReady = host == nil
	finalized, err := runtimeState.FinalizeStagedGeneration(ctx, claim.Generation, held)
	if err != nil {
		reactivateErr := committer.reactivateAfterFailure(ctx, runtimeState, current.ID, host)
		bundleReady = reactivateErr == nil
		return CommitResult{}, errors.Join(err, reactivateErr)
	}
	status.Phase, status.UpdatedAt = CommitSwitching, committer.nextStatusTime(status)
	if err := committer.writeStatus(claim.StageRoot, status); err != nil {
		reactivateErr := committer.reactivateAfterFailure(ctx, runtimeState, current.ID, host)
		bundleReady = reactivateErr == nil
		return CommitResult{}, errors.Join(err, reactivateErr)
	}
	if err := committer.switchCurrent(ctx, runtimeState, finalized.ID, held); err != nil {
		actual, resolveErr := runtimeState.ResolveCurrent()
		if resolveErr == nil && actual.ID == finalized.ID {
			rolled, rollbackErr := committer.rollback(ctx, runtimeState, held, claim, status, host, err)
			bundleReady = rolled.Status.RollbackSucceeded
			terminal = true
			return rolled, rollbackErr
		}
		reactivateErr := committer.reactivateAfterFailure(ctx, runtimeState, current.ID, host)
		bundleReady = reactivateErr == nil
		return CommitResult{}, errors.Join(err, resolveErr, reactivateErr)
	}
	status.Phase, status.ActiveGeneration, status.UpdatedAt = CommitVerifying, finalized.ID, committer.nextStatusTime(status)
	if err := committer.writeStatus(claim.StageRoot, status); err != nil {
		rolled, rollbackErr := committer.rollback(ctx, runtimeState, held, claim, status, host, err)
		bundleReady = rolled.Status.RollbackSucceeded
		terminal = true
		return rolled, rollbackErr
	}
	if err := committer.reactivate(ctx, runtimeState, finalized.ID, host); err != nil {
		rolled, rollbackErr := committer.rollback(ctx, runtimeState, held, claim, status, host, err)
		bundleReady = rolled.Status.RollbackSucceeded
		terminal = true
		return rolled, rollbackErr
	}
	bundleReady = true
	if err := committer.inspector.FinishClaim(claim, true, held); err != nil {
		rolled, rollbackErr := committer.rollback(ctx, runtimeState, held, claim, status, host, err)
		bundleReady = rolled.Status.RollbackSucceeded
		terminal = true
		return rolled, rollbackErr
	}
	completed := committer.nextStatusTime(status)
	status.Phase, status.ActiveGeneration, status.UpdatedAt, status.CompletedAt = CommitSucceeded, finalized.ID, completed, &completed
	if err := committer.writeStatus(claim.StageRoot, status); err != nil {
		rolled, rollbackErr := committer.rollback(ctx, runtimeState, held, claim, status, host, err)
		bundleReady = rolled.Status.RollbackSucceeded
		terminal = true
		return rolled, rollbackErr
	}
	terminal = true
	return CommitResult{Status: status}, nil
}

func (committer *Committer) now() time.Time {
	return committer.clock().UTC().Truncate(time.Millisecond)
}

func (committer *Committer) nextStatusTime(status CommitStatus) time.Time {
	now := committer.now()
	if now.Before(status.UpdatedAt) {
		return status.UpdatedAt
	}
	return now
}

func (committer *Committer) revalidateClaim(ctx context.Context, runtimeState *runtimefs.Runtime, claim StagingClaim, sessionEpoch uint64) error {
	if sessionEpoch != claim.SessionEpoch || !committer.now().Before(claim.ExpiresAt) {
		return domain.NewError(domain.CodeImportStagingExpired, "import staging token expired or owner authority changed while waiting", nil)
	}
	state, err := readStagingMetadata(claim.StageRoot)
	if err != nil || state.State != stagingCommitting || state.GenerationID != claim.GenerationID || state.SessionEpoch != sessionEpoch || state.ArchiveSHA != claim.ArchiveSHA {
		return importStagingInvalid("claimed staging metadata changed", err)
	}
	hash, err := fileSHA256(ctx, claim.ArchivePath, committer.inspector.limits.MaxArchiveBytes)
	if err != nil || hash != claim.ArchiveSHA {
		return importStagingInvalid("claimed archive changed", err)
	}
	document, err := committer.inspector.preflight(ctx, claim.ArchivePath)
	if err != nil || document.Counts.Entries != claim.Counts.Entries || document.Counts.Comments != claim.Counts.Comments || document.Counts.Media != claim.Counts.Media || document.Limits.ExpandedBytes != claim.ExpandedBytes {
		return importStagingInvalid("claimed archive contract changed", err)
	}
	if err := validateStagingGeneration(ctx, runtimeState, claim.Generation, claim.ArchivePath, document); err != nil {
		return importStagingInvalid("claimed staging generation changed", err)
	}
	return nil
}

func (committer *Committer) checkFinalDisk(ctx context.Context, runtimeState *runtimefs.Runtime, database *sqlitestore.Database) (resultErr error) {
	snapshot, err := database.BeginArchiveSnapshot(ctx)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, snapshot.Close()) }()
	if err := snapshot.ValidateIntegrity(ctx); err != nil {
		return err
	}
	dataBytes, err := regularTreeBytes(ctx, runtimeState.Current.Root)
	if err != nil {
		return err
	}
	available, err := committer.availableBytes(runtimeState.Root)
	if err != nil {
		return err
	}
	availableBytes := int64(math.MaxInt64)
	if available <= math.MaxInt64 {
		availableBytes = int64(available)
	}
	margin := dataBytes / 10
	if margin < minimumSafetyMarginBytes {
		margin = minimumSafetyMarginBytes
	}
	// Staging and the uploaded archive already occupy disk. The remaining peak
	// contract still reserves one full current-data copy for the verified
	// portable backup and a distinct full copy for rollback headroom.
	required, err := checkedAdd(dataBytes, dataBytes, margin)
	if err != nil {
		return err
	}
	if availableBytes < required {
		return domain.NewError(domain.CodeImportDiskSpaceInsufficient, "insufficient disk space for verified pre-import backup and rollback reserve", map[string]any{
			"available_bytes": availableBytes, "required_additional_bytes": required,
		})
	}
	return nil
}

func (committer *Committer) reactivate(ctx context.Context, runtimeState *runtimefs.Runtime, id string, host CommitHost) error {
	current, err := runtimeState.ResolveCurrent()
	if err != nil || current.ID != id {
		return errors.Join(errors.New("CURRENT does not select the generation being activated"), err)
	}
	runtimeState.Current = current
	if host != nil {
		return host.Activate(ctx, runtimeState)
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return err
	}
	defer func() { _ = database.Close() }()
	return database.Ready(ctx)
}

func (committer *Committer) reactivateAfterFailure(ctx context.Context, runtimeState *runtimefs.Runtime, id string, host CommitHost) error {
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	return committer.reactivate(recovery, runtimeState, id, host)
}

func (committer *Committer) rollback(ctx context.Context, runtimeState *runtimefs.Runtime, held *runtimefs.LeaseSet, claim StagingClaim, status CommitStatus, host CommitHost, primary error) (CommitResult, error) {
	recovery, cancel := context.WithTimeout(context.WithoutCancel(ctx), rollbackTimeout)
	defer cancel()
	status.Phase, status.RollbackAttempted, status.UpdatedAt = CommitRollingBack, true, committer.nextStatusTime(status)
	journalErr := committer.writeStatus(claim.StageRoot, status)
	closeErr := error(nil)
	if host != nil {
		closeErr = host.CloseActive()
	}
	switchErr := committer.switchCurrent(recovery, runtimeState, status.PreviousGeneration, held)
	actual, resolveErr := runtimeState.ResolveCurrent()
	if resolveErr == nil {
		status.ActiveGeneration = actual.ID
	}
	activateErr := error(nil)
	if resolveErr == nil && actual.ID == status.PreviousGeneration {
		activateErr = committer.reactivate(recovery, runtimeState, status.PreviousGeneration, host)
	}
	rollbackErr := errors.Join(journalErr, closeErr, switchErr, resolveErr, activateErr)
	completed := committer.nextStatusTime(status)
	status.UpdatedAt, status.CompletedAt = completed, &completed
	if rollbackErr == nil {
		status.Phase, status.RollbackSucceeded = CommitRolledBack, true
	} else {
		status.Phase = CommitNeedsAttention
	}
	status.ErrorCode = errorCode(primary)
	finishErr := committer.inspector.FinishClaim(claim, false, held)
	writeErr := committer.writeStatus(claim.StageRoot, status)
	return CommitResult{Status: status}, errors.Join(primary, rollbackErr, finishErr, writeErr)
}

// RecoverWithinLocks resolves an interrupted commit from durable journal plus
// the sole CURRENT authority. It never promotes CURRENT.next and never makes a
// committing token ready again. Server startup calls this before constructing
// the live service bundle.
func (committer *Committer) RecoverWithinLocks(ctx context.Context, runtimeState *runtimefs.Runtime, held *runtimefs.LeaseSet) error {
	if committer == nil || runtimeState == nil || held == nil ||
		!held.BelongsTo(runtimeState.Root) ||
		!held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.MaintenanceLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.MediaLock, runtimefs.Exclusive) {
		return runtimefs.ErrInsufficientLocks
	}
	status, stageRoot, err := committer.latestStatusWithRoot(runtimeState.Root)
	if err != nil {
		if domain.HasCode(err, domain.CodeImportStagingInvalid) {
			return nil
		}
		return err
	}
	claim := StagingClaim{GenerationID: status.NewGeneration, StageRoot: stageRoot}
	switch status.Phase {
	case CommitSucceeded, CommitFailed, CommitRolledBack:
		if _, err := os.Lstat(stageRoot); errors.Is(err, os.ErrNotExist) {
			return nil
		} else if err != nil {
			return err
		}
		return committer.inspector.FinishClaim(claim, status.Phase == CommitSucceeded, held)
	case CommitNeedsAttention:
		return domain.NewError(domain.CodeDataIntegrityFailed, "an interrupted import requires operator attention", map[string]any{"active_generation": status.ActiveGeneration})
	}
	current, err := runtimeState.ResolveCurrent()
	if err != nil {
		status.Phase, status.ErrorCode, status.UpdatedAt = CommitNeedsAttention, string(domain.CodeDataIntegrityFailed), committer.nextStatusTime(status)
		_ = committer.writeStatus(stageRoot, status)
		return err
	}
	status.ActiveGeneration = current.ID
	completed := committer.nextStatusTime(status)
	if current.ID == status.PreviousGeneration {
		if err := committer.reactivate(ctx, runtimeState, status.PreviousGeneration, nil); err != nil {
			status.Phase, status.ErrorCode, status.UpdatedAt = CommitNeedsAttention, string(domain.CodeDataIntegrityFailed), completed
			_ = committer.writeStatus(stageRoot, status)
			return err
		}
		status.Phase, status.ErrorCode, status.UpdatedAt, status.CompletedAt = CommitFailed, string(domain.CodeDataIntegrityFailed), completed, &completed
		if status.RollbackAttempted {
			status.Phase, status.RollbackSucceeded = CommitRolledBack, true
		}
		if err := committer.inspector.FinishClaim(claim, false, held); err != nil {
			return err
		}
		return committer.writeStatus(stageRoot, status)
	}
	if current.ID != status.NewGeneration {
		status.Phase, status.ErrorCode, status.UpdatedAt = CommitNeedsAttention, string(domain.CodeDataIntegrityFailed), completed
		_ = committer.writeStatus(stageRoot, status)
		return domain.NewError(domain.CodeDataIntegrityFailed, "CURRENT does not match either side of an interrupted import", map[string]any{"active_generation": current.ID})
	}
	runtimeState.Current = current
	verificationErr := committer.validateRecoveredGeneration(ctx, runtimeState, stageRoot, status)
	if verificationErr == nil {
		verificationErr = committer.reactivate(ctx, runtimeState, current.ID, nil)
	}
	if verificationErr == nil {
		if finishErr := committer.inspector.FinishClaim(claim, true, held); finishErr != nil {
			return finishErr
		}
		status.Phase, status.UpdatedAt, status.CompletedAt = CommitSucceeded, completed, &completed
		return committer.writeStatus(stageRoot, status)
	}
	status.Phase, status.RollbackAttempted, status.UpdatedAt = CommitRollingBack, true, committer.nextStatusTime(status)
	if err := committer.writeStatus(stageRoot, status); err != nil {
		return err
	}
	switchErr := committer.switchCurrent(ctx, runtimeState, status.PreviousGeneration, held)
	actual, resolveErr := runtimeState.ResolveCurrent()
	if resolveErr != nil || actual.ID != status.PreviousGeneration {
		status.Phase, status.ErrorCode, status.UpdatedAt = CommitNeedsAttention, string(domain.CodeDataIntegrityFailed), committer.nextStatusTime(status)
		if resolveErr == nil {
			status.ActiveGeneration = actual.ID
		}
		_ = committer.writeStatus(stageRoot, status)
		return errors.Join(switchErr, resolveErr, errors.New("rollback CURRENT did not select the previous generation"))
	}
	status.ActiveGeneration = actual.ID
	activateErr := committer.reactivate(ctx, runtimeState, status.PreviousGeneration, nil)
	if switchErr != nil || activateErr != nil {
		status.Phase, status.ActiveGeneration, status.ErrorCode, status.UpdatedAt = CommitNeedsAttention, status.PreviousGeneration, string(domain.CodeDataIntegrityFailed), committer.nextStatusTime(status)
		_ = committer.writeStatus(stageRoot, status)
		return errors.Join(switchErr, activateErr)
	}
	status.Phase, status.ActiveGeneration, status.RollbackSucceeded = CommitRolledBack, status.PreviousGeneration, true
	completed = committer.nextStatusTime(status)
	status.ErrorCode, status.UpdatedAt, status.CompletedAt = string(domain.CodeDataIntegrityFailed), completed, &completed
	if err := committer.inspector.FinishClaim(claim, false, held); err != nil {
		return err
	}
	return committer.writeStatus(stageRoot, status)
}

func (committer *Committer) validateRecoveredGeneration(ctx context.Context, runtimeState *runtimefs.Runtime, stageRoot string, status CommitStatus) error {
	if !status.BackupVerified || status.BackupName == "" {
		return errors.New("interrupted import has no verified pre-import backup")
	}
	if err := ValidateFileContext(ctx, filepath.Join(runtimeState.Root, "backups", status.BackupName)); err != nil {
		return fmt.Errorf("revalidate pre-import backup: %w", err)
	}
	metadata, err := readStagingMetadata(stageRoot)
	if err != nil || metadata.GenerationID != status.NewGeneration || (metadata.State != stagingCommitting && metadata.State != stagingUsed) {
		return errors.Join(errors.New("interrupted import staging metadata is invalid"), err)
	}
	archivePath := filepath.Join(stageRoot, "archive.zip")
	hash, err := fileSHA256(ctx, archivePath, committer.inspector.limits.MaxArchiveBytes)
	if err != nil || hash != metadata.ArchiveSHA {
		return errors.Join(errors.New("interrupted import archive changed"), err)
	}
	document, err := committer.inspector.preflight(ctx, archivePath)
	if err != nil {
		return err
	}
	if err := ValidateFileContext(ctx, archivePath); err != nil {
		return err
	}
	return validateStagingGeneration(ctx, runtimeState, runtimeState.Current, archivePath, document)
}

// Status returns the newest durable import operation without requiring or
// disclosing the original token.
func (committer *Committer) Status(ctx context.Context) (CommitStatus, error) {
	if err := ctx.Err(); err != nil {
		return CommitStatus{}, err
	}
	root, err := runtimefs.ResolveDataDir(committer.dataDir)
	if err != nil {
		return CommitStatus{}, err
	}
	return committer.latestStatus(root)
}

func (committer *Committer) latestStatus(root string) (CommitStatus, error) {
	status, _, err := committer.latestStatusWithRoot(root)
	return status, err
}

func (committer *Committer) latestStatusWithRoot(root string) (CommitStatus, string, error) {
	statusRoot := filepath.Join(root, "staging", "import-status")
	rootInfo, err := os.Lstat(statusRoot)
	if err != nil || !rootInfo.IsDir() || rootInfo.Mode()&os.ModeSymlink != 0 {
		return CommitStatus{}, "", errors.Join(errors.New("import status directory is unsafe"), err)
	}
	entries, err := os.ReadDir(statusRoot)
	if err != nil {
		return CommitStatus{}, "", err
	}
	type locatedStatus struct {
		status        CommitStatus
		root          string
		metadataState string
	}
	statuses := make([]locatedStatus, 0, len(entries))
	for _, entry := range entries {
		name := entry.Name()
		path := filepath.Join(statusRoot, name)
		info, infoErr := os.Lstat(path)
		if isCommitStatusTemporary(name) {
			if infoErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
				return CommitStatus{}, "", errors.Join(errors.New("import status temporary entry is unsafe"), infoErr)
			}
			// WritePrivateFileAtomic may leave this same-directory prepared file
			// after SIGKILL. It is never authority and must not block recovery or
			// concurrent status reads.
			continue
		}
		if infoErr != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || filepath.Ext(name) != ".json" {
			return CommitStatus{}, "", errors.Join(errors.New("import status directory contains an unsafe entry"), infoErr)
		}
		status, readErr := readCommitStatus(path)
		if readErr != nil || strings.TrimSuffix(name, ".json") != status.OperationID {
			return CommitStatus{}, "", errors.Join(errors.New("import status identity is invalid"), readErr)
		}
		stageRoot := filepath.Join(root, "staging", "import", status.OperationID)
		if stageInfo, stageErr := os.Lstat(stageRoot); stageErr == nil {
			if !stageInfo.IsDir() || stageInfo.Mode()&os.ModeSymlink != 0 {
				return CommitStatus{}, "", errors.New("import staging status target is unsafe")
			}
			metadata, metadataErr := readStagingMetadata(stageRoot)
			if metadataErr != nil || metadata.GenerationID != status.OperationID {
				return CommitStatus{}, "", errors.Join(errors.New("import staging status metadata is invalid"), metadataErr)
			}
			statuses = append(statuses, locatedStatus{status: status, root: stageRoot, metadataState: metadata.State})
		} else if !errors.Is(stageErr, os.ErrNotExist) || !terminalCommitPhase(status.Phase) {
			return CommitStatus{}, "", errors.Join(errors.New("active import status has no safe staging directory"), stageErr)
		} else {
			statuses = append(statuses, locatedStatus{status: status, root: stageRoot})
		}
	}
	if len(statuses) == 0 {
		return CommitStatus{}, "", domain.NewError(domain.CodeImportStagingInvalid, "no import commit status exists", nil)
	}
	// An interrupted operation is authority even if the system clock moved
	// backwards and an older terminal journal therefore has a later wall-clock
	// timestamp. A terminal journal with committing metadata is the crash gap
	// between journal and staging reconciliation and must also win.
	active := make([]locatedStatus, 0, 1)
	for _, located := range statuses {
		if located.metadataState == stagingCommitting || !terminalCommitPhase(located.status.Phase) {
			active = append(active, located)
		}
	}
	if len(active) > 1 {
		return CommitStatus{}, "", errors.New("multiple active import commit journals exist")
	}
	if len(active) == 1 {
		return active[0].status, active[0].root, nil
	}
	sort.Slice(statuses, func(i, j int) bool { return statuses[i].status.UpdatedAt.After(statuses[j].status.UpdatedAt) })
	return statuses[0].status, statuses[0].root, nil
}

func terminalCommitPhase(phase CommitPhase) bool {
	return phase == CommitSucceeded || phase == CommitFailed || phase == CommitRolledBack
}

func isCommitStatusTemporary(name string) bool {
	if !strings.HasPrefix(name, ".") {
		return false
	}
	marker := strings.LastIndex(name, ".json-")
	if marker <= 1 || marker+len(".json-") >= len(name) {
		return false
	}
	if !validCommitGenerationID(name[1:marker], true) {
		return false
	}
	for _, character := range name[marker+len(".json-"):] {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func (committer *Committer) writeStatus(stageRoot string, status CommitStatus) error {
	if err := validateCommitStatus(status); err != nil {
		return err
	}
	document := struct {
		Version int `json:"version"`
		CommitStatus
	}{Version: commitJournalVersion, CommitStatus: status}
	data, err := json.Marshal(document)
	if err != nil {
		return err
	}
	statusRoot, err := commitStatusRoot(stageRoot)
	if err != nil {
		return err
	}
	return runtimefs.WritePrivateFileAtomic(statusRoot, status.OperationID+".json", append(data, '\n'))
}

func readCommitStatus(path string) (CommitStatus, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return CommitStatus{}, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() < 2 || info.Size() > commitJournalLimit {
		return CommitStatus{}, errors.New("import commit journal is unsafe")
	}
	data, err := os.ReadFile(path)
	if err != nil || int64(len(data)) > commitJournalLimit || data[len(data)-1] != '\n' {
		return CommitStatus{}, errors.Join(errors.New("import commit journal bytes are invalid"), err)
	}
	if err := validateJSONObjectTokens(data[:len(data)-1]); err != nil {
		return CommitStatus{}, err
	}
	var document struct {
		Version int `json:"version"`
		CommitStatus
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&document); err != nil || decoder.Decode(&struct{}{}) != io.EOF || document.Version != commitJournalVersion {
		return CommitStatus{}, errors.New("import commit journal JSON is invalid")
	}
	return document.CommitStatus, validateCommitStatus(document.CommitStatus)
}

func commitStatusRoot(stageRoot string) (string, error) {
	clean := filepath.Clean(stageRoot)
	if filepath.Base(clean) == "." || filepath.Base(clean) == ".." || !strings.HasPrefix(filepath.Base(clean), "import-") {
		return "", errors.New("import staging root is invalid")
	}
	importRoot := filepath.Dir(clean)
	if filepath.Base(importRoot) != "import" || filepath.Base(filepath.Dir(importRoot)) != "staging" {
		return "", errors.New("import staging root is outside the runtime layout")
	}
	statusRoot := filepath.Join(filepath.Dir(importRoot), "import-status")
	info, err := os.Lstat(statusRoot)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", errors.Join(errors.New("import status directory is unsafe"), err)
	}
	return statusRoot, nil
}

func validateCommitStatus(status CommitStatus) error {
	if !validCommitGenerationID(status.OperationID, true) || status.OperationID != status.NewGeneration ||
		!validCommitGenerationID(status.PreviousGeneration, false) || !validCommitGenerationID(status.ActiveGeneration, false) ||
		status.StartedAt.IsZero() || status.UpdatedAt.Before(status.StartedAt) {
		return errors.New("import commit journal binding is invalid")
	}
	switch status.Phase {
	case CommitClaimed, CommitBackingUp, CommitFinalizing, CommitSwitching, CommitVerifying, CommitRollingBack, CommitSucceeded, CommitFailed, CommitRolledBack, CommitNeedsAttention:
	default:
		return errors.New("import commit journal phase is invalid")
	}
	if (status.BackupName == "") != !status.BackupVerified || (status.BackupName != "" && filepath.Base(status.BackupName) != status.BackupName) {
		return errors.New("import commit backup identity is invalid")
	}
	if terminalCommitPhase(status.Phase) && status.CompletedAt == nil {
		return errors.New("terminal import commit journal has no completion time")
	}
	return nil
}

func validCommitGenerationID(value string, requireImport bool) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value || value == "." || value == ".." || (requireImport && !strings.HasPrefix(value, "import-")) {
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

func errorCode(err error) string {
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		return string(domainErr.Code)
	}
	return string(domain.CodeDataIntegrityFailed)
}
