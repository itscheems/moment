package archive

import (
	"context"
	"errors"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestCommitCreatesVerifiedBackupSwitchesAndRejectsReplay(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	oldID := fixture.runtime.Current.ID
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fixture.runtime.Root, "secrets.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.InitializeOffline(context.Background(), fixture.runtime, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
	inspector := testInspector(t, fixture.runtime.Root, nil, &now, math.MaxUint64)
	inspection, err := inspector.InspectFile(context.Background(), archivePath, 1)
	if err != nil {
		t.Fatal(err)
	}
	committer, err := NewCommitter(CommitOptions{
		DataDir: fixture.runtime.Root, AppVersion: "0.1.0", Clock: func() time.Time { return now },
		AvailableBytes: func(string) (uint64, error) { return math.MaxUint64, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := committer.Commit(context.Background(), inspection.StagingToken, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status.Phase != CommitSucceeded || result.Status.ActiveGeneration != inspection.GenerationID || !result.Status.BackupVerified {
		t.Fatalf("commit result = %#v", result)
	}
	runtimeState, err := runtimefs.Open(fixture.runtime.Root)
	if err != nil {
		t.Fatal(err)
	}
	if runtimeState.Current.ID != inspection.GenerationID {
		t.Fatalf("CURRENT=%s want %s", runtimeState.Current.ID, inspection.GenerationID)
	}
	if _, err := os.Stat(filepath.Join(runtimeState.Root, "generations", oldID)); err != nil {
		t.Fatalf("old generation was not retained: %v", err)
	}
	if err := ValidateFile(filepath.Join(runtimeState.Root, "backups", result.Status.BackupName)); err != nil {
		t.Fatalf("pre-import backup is invalid: %v", err)
	}
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 1, nil); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
		t.Fatalf("replay error = %v", err)
	}
	status, err := committer.Status(context.Background())
	if err != nil || status.Phase != CommitSucceeded || status.OperationID != inspection.GenerationID {
		t.Fatalf("status=%#v err=%v", status, err)
	}
}

func TestCommitReadinessFailureRollsBackTrueCurrent(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	oldID := fixture.runtime.Current.ID
	now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	host := &rejectNewGenerationHost{database: fixture.database, rejectID: inspection.GenerationID, epoch: 9}
	committer, err := NewCommitter(CommitOptions{
		DataDir: fixture.runtime.Root, AppVersion: "0.1.0", Clock: func() time.Time { return now },
		AvailableBytes: func(string) (uint64, error) { return math.MaxUint64, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := committer.Commit(context.Background(), inspection.StagingToken, 9, host)
	if err == nil || result.Status.Phase != CommitRolledBack || !result.Status.RollbackSucceeded || result.Status.ActiveGeneration != oldID {
		t.Fatalf("rollback result=%#v err=%v", result, err)
	}
	runtimeState, openErr := runtimefs.Open(fixture.runtime.Root)
	if openErr != nil || runtimeState.Current.ID != oldID {
		t.Fatalf("rollback CURRENT=%#v err=%v", runtimeState.Current, openErr)
	}
	if _, replayErr := committer.Commit(context.Background(), inspection.StagingToken, 9, host); !domain.HasCode(replayErr, domain.CodeImportStagingInvalid) {
		t.Fatalf("rolled-back token replay error = %v", replayErr)
	}
}

func TestRollbackReopensTruePreviousGenerationAfterSwitchBarrierError(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	oldID := fixture.runtime.Current.ID
	now := time.Date(2026, 8, 11, 4, 15, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	host := &rejectNewGenerationHost{database: fixture.database, rejectID: inspection.GenerationID, epoch: 9}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	committer.switchCurrent = func(ctx context.Context, runtimeState *runtimefs.Runtime, id string, held *runtimefs.LeaseSet) error {
		err := runtimeState.SwitchCurrent(ctx, id, held)
		if err == nil && id == oldID {
			return errors.New("injected rollback durability barrier failure")
		}
		return err
	}
	result, err := committer.Commit(context.Background(), inspection.StagingToken, 9, host)
	if err == nil || result.Status.Phase != CommitNeedsAttention || result.Status.ActiveGeneration != oldID || result.Status.RollbackSucceeded {
		t.Fatalf("rollback barrier result=%#v err=%v", result, err)
	}
	current, resolveErr := runtimefs.Open(fixture.runtime.Root)
	if resolveErr != nil || current.Current.ID != oldID {
		t.Fatalf("rollback barrier CURRENT=%#v err=%v", current.Current, resolveErr)
	}
	if host.database == nil {
		t.Fatal("rollback barrier error left the true previous generation closed")
	}
	if readyErr := host.database.Ready(context.Background()); readyErr != nil {
		t.Fatalf("reactivated previous database is not ready: %v", readyErr)
	}
	if host.endCalls != 0 {
		t.Fatalf("needs-attention rollback reopened admission %d times", host.endCalls)
	}
}

func TestCommitReloadsOwnerEpochAfterMaintenanceWait(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 4, 30, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	host := &rejectNewGenerationHost{database: fixture.database, epoch: 10}
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 9, host); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("rotated owner epoch error=%v", err)
	}
	current, err := runtimefs.Open(fixture.runtime.Root)
	if err != nil || current.Current.ID != fixture.runtime.Current.ID {
		t.Fatalf("owner rotation changed CURRENT: %#v %v", current.Current, err)
	}
	backups, err := os.ReadDir(filepath.Join(fixture.runtime.Root, "backups"))
	if err != nil || len(backups) != 0 {
		t.Fatalf("owner rotation created backup: %#v %v", backups, err)
	}
}

func TestDirectCommitChecksServeOwnershipBeforeClaimingToken(t *testing.T) {
	fixture := newExportFixture(t, false)
	archivePath := exportFixtureFile(t, fixture)
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.InitializeOffline(context.Background(), fixture.runtime, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 4, 35, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, nil, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 1)
	if err != nil {
		t.Fatal(err)
	}
	serve, err := fixture.runtime.Locks.Acquire(context.Background(), runtimefs.ServeLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 1, nil); !domain.HasCode(err, domain.CodeInstanceInUse) {
		t.Fatalf("server-in-use error=%v", err)
	}
	stageRoot := filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID)
	metadata, err := readStagingMetadata(stageRoot)
	if err != nil || metadata.State != stagingReady {
		t.Fatalf("blocked commit consumed staging metadata=%#v err=%v", metadata, err)
	}
	if entries, err := os.ReadDir(filepath.Join(fixture.runtime.Root, "staging", "import-status")); err != nil || len(entries) != 0 {
		t.Fatalf("blocked commit wrote journals=%#v err=%v", entries, err)
	}
	if err := serve.Release(); err != nil {
		t.Fatal(err)
	}
	result, err := committer.Commit(context.Background(), inspection.StagingToken, 1, nil)
	if err != nil || result.Status.Phase != CommitSucceeded {
		t.Fatalf("retry after server stop result=%#v err=%v", result, err)
	}
}

func TestCommitRevalidatesEveryStagedOriginalAfterAdmissionWait(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 4, 45, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	stageRoot := filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID)
	host := &rejectNewGenerationHost{database: fixture.database, epoch: 9, begin: func() error {
		var original string
		err := filepath.WalkDir(filepath.Join(stageRoot, "generation", "media", "original"), func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				return walkErr
			}
			if entry.Type().IsRegular() && original == "" {
				original = path
			}
			return nil
		})
		if err != nil || original == "" {
			return errors.Join(errors.New("no staged original"), err)
		}
		return os.WriteFile(original, []byte("tampered"), 0o600)
	}}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 9, host); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
		t.Fatalf("staged original tamper error=%v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(fixture.runtime.Root, "backups")); len(entries) != 0 {
		t.Fatalf("tampered staging created %d backups", len(entries))
	}
}

func TestCommitFinalDiskKeepsSeparateBackupAndRollbackReserve(t *testing.T) {
	fixture := newExportFixture(t, true)
	dataBytes, err := regularTreeBytes(context.Background(), fixture.runtime.Current.Root)
	if err != nil {
		t.Fatal(err)
	}
	margin := dataBytes / 10
	if margin < minimumSafetyMarginBytes {
		margin = minimumSafetyMarginBytes
	}
	required, err := checkedAdd(dataBytes, dataBytes, margin)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 5, 0, 0, 0, time.UTC)
	committer := testCommitter(t, fixture.runtime.Root, &now, uint64(required))
	if err := committer.checkFinalDisk(context.Background(), fixture.runtime, fixture.database); err != nil {
		t.Fatalf("exact final disk boundary rejected: %v", err)
	}
	committer.availableBytes = func(string) (uint64, error) { return uint64(required - 1), nil }
	if err := committer.checkFinalDisk(context.Background(), fixture.runtime, fixture.database); !domain.HasCode(err, domain.CodeImportDiskSpaceInsufficient) {
		t.Fatalf("below final disk boundary error=%v", err)
	}
}

func TestRecoverReconcilesUsedMetadataBeforeSucceededJournal(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 5, 30, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	runtimeState, err := runtimefs.Open(fixture.runtime.Root)
	if err != nil {
		t.Fatal(err)
	}
	held, err := runtimeState.Locks.AcquireSet(context.Background(),
		runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive},
		runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Exclusive},
		runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Exclusive},
	)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := committer.inspector.ClaimWithinLocks(context.Background(), runtimeState, inspection.StagingToken, 9, held)
	if err != nil {
		t.Fatal(err)
	}
	status := CommitStatus{
		OperationID: claim.GenerationID, Phase: CommitVerifying, ActiveGeneration: claim.GenerationID,
		PreviousGeneration: runtimeState.Current.ID, NewGeneration: claim.GenerationID, StartedAt: now, UpdatedAt: now,
		BackupName: "recovery-test.zip", BackupVerified: true,
	}
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeState.Root, "backups", status.BackupName), archiveBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := runtimeState.FinalizeStagedGeneration(context.Background(), claim.Generation, held); err != nil {
		t.Fatal(err)
	}
	if err := runtimeState.SwitchCurrent(context.Background(), claim.GenerationID, held); err != nil {
		t.Fatal(err)
	}
	if err := committer.writeStatus(claim.StageRoot, status); err != nil {
		t.Fatal(err)
	}
	if err := committer.inspector.FinishClaim(claim, true, held); err != nil {
		t.Fatal(err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	recovery, err := runtimeState.Locks.AcquireSet(context.Background(),
		runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive},
		runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Exclusive},
		runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Exclusive},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = recovery.Release() }()
	if err := committer.RecoverWithinLocks(context.Background(), runtimeState, recovery); err != nil {
		t.Fatal(err)
	}
	recovered, err := committer.Status(context.Background())
	if err != nil || recovered.Phase != CommitSucceeded {
		t.Fatalf("recovered status=%#v err=%v", recovered, err)
	}
}

func TestRecoverRollsBackCorruptSwitchedGeneration(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 5, 40, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	runtimeState, err := runtimefs.Open(fixture.runtime.Root)
	if err != nil {
		t.Fatal(err)
	}
	held, err := runtimeState.Locks.AcquireSet(context.Background(),
		runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive},
		runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Exclusive},
		runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Exclusive},
	)
	if err != nil {
		t.Fatal(err)
	}
	claim, err := committer.inspector.ClaimWithinLocks(context.Background(), runtimeState, inspection.StagingToken, 9, held)
	if err != nil {
		t.Fatal(err)
	}
	backupName := "corrupt-recovery-backup.zip"
	archiveBytes, err := os.ReadFile(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeState.Root, "backups", backupName), archiveBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	status := CommitStatus{
		OperationID: claim.GenerationID, Phase: CommitSwitching, ActiveGeneration: runtimeState.Current.ID,
		PreviousGeneration: runtimeState.Current.ID, NewGeneration: claim.GenerationID,
		BackupName: backupName, BackupVerified: true, StartedAt: now, UpdatedAt: now,
	}
	if _, err := runtimeState.FinalizeStagedGeneration(context.Background(), claim.Generation, held); err != nil {
		t.Fatal(err)
	}
	if err := runtimeState.SwitchCurrent(context.Background(), claim.GenerationID, held); err != nil {
		t.Fatal(err)
	}
	if err := committer.writeStatus(claim.StageRoot, status); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Remove(runtimeState.Current.DatabasePath + suffix); err != nil && !errors.Is(err, os.ErrNotExist) {
			t.Fatal(err)
		}
	}
	if err := os.Truncate(runtimeState.Current.DatabasePath, 1); err != nil {
		t.Fatal(err)
	}
	if err := committer.RecoverWithinLocks(context.Background(), runtimeState, held); err != nil {
		t.Fatal(err)
	}
	current, err := runtimeState.ResolveCurrent()
	if err != nil || current.ID != status.PreviousGeneration {
		t.Fatalf("rollback CURRENT=%#v err=%v", current, err)
	}
	recovered, err := committer.Status(context.Background())
	if err != nil || recovered.Phase != CommitRolledBack || !recovered.RollbackSucceeded {
		t.Fatalf("rollback status=%#v err=%v", recovered, err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverReconcilesTerminalFailedJournalWithCommittingMetadata(t *testing.T) {
	fixture := newExportFixture(t, false)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 5, 45, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	runtimeState, err := runtimefs.Open(fixture.runtime.Root)
	if err != nil {
		t.Fatal(err)
	}
	held, err := runtimeState.Locks.AcquireSet(context.Background(), runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := committer.inspector.ClaimWithinLocks(context.Background(), runtimeState, inspection.StagingToken, 9, held)
	if err != nil {
		t.Fatal(err)
	}
	completed := now
	status := CommitStatus{
		OperationID: claim.GenerationID, Phase: CommitFailed, ActiveGeneration: runtimeState.Current.ID,
		PreviousGeneration: runtimeState.Current.ID, NewGeneration: claim.GenerationID,
		ErrorCode: string(domain.CodeDataIntegrityFailed), StartedAt: now, UpdatedAt: now, CompletedAt: &completed,
	}
	if err := committer.writeStatus(claim.StageRoot, status); err != nil {
		t.Fatal(err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	recovery, err := runtimeState.Locks.AcquireSet(context.Background(),
		runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive},
		runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Exclusive},
		runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Exclusive},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = recovery.Release() }()
	if err := committer.RecoverWithinLocks(context.Background(), runtimeState, recovery); err != nil {
		t.Fatal(err)
	}
	metadata, err := readStagingMetadata(claim.StageRoot)
	if err != nil || metadata.State != stagingFailed {
		t.Fatalf("reconciled metadata=%#v err=%v", metadata, err)
	}
}

func TestTerminalStatusSurvivesStagingCleanup(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(fixture.runtime.Root, "secrets.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.InitializeOffline(context.Background(), fixture.runtime, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 6, 0, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, nil, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 1)
	if err != nil {
		t.Fatal(err)
	}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 1, nil); err != nil {
		t.Fatal(err)
	}
	if err := os.RemoveAll(filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID)); err != nil {
		t.Fatal(err)
	}
	status, err := committer.Status(context.Background())
	if err != nil || status.Phase != CommitSucceeded {
		t.Fatalf("retained status=%#v err=%v", status, err)
	}
}

func TestCommitStatusRejectsSymlinkEntry(t *testing.T) {
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 6, 30, 0, 0, time.UTC)
	committer := testCommitter(t, runtimeState.Root, &now, math.MaxUint64)
	statusRoot := filepath.Join(runtimeState.Root, "staging", "import-status")
	if err := os.Symlink(filepath.Join(runtimeState.Root, "CURRENT"), filepath.Join(statusRoot, "import-unsafe.json")); err != nil {
		t.Fatal(err)
	}
	if _, err := committer.Status(context.Background()); err == nil {
		t.Fatal("symlinked import status entry was accepted")
	}
}

func TestCommitStatusIgnoresAbandonedAtomicTemporaryAfterCrash(t *testing.T) {
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 6, 45, 0, 0, time.UTC)
	committer := testCommitter(t, runtimeState.Root, &now, math.MaxUint64)
	id := "import-20260811T064500000Z-0011223344556677"
	stageRoot := filepath.Join(runtimeState.Root, "staging", "import", id)
	if err := os.Mkdir(stageRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeStagingMetadata(stageRoot, stagingMetadata{
		TokenHash: strings.Repeat("0", 64), ExpiresAtMS: now.Add(time.Hour).UnixMilli(), ArchiveSHA: strings.Repeat("1", 64),
		InstanceID: runtimeState.Instance.ID, SessionEpoch: 1, GenerationID: id, State: stagingCommitting,
	}); err != nil {
		t.Fatal(err)
	}
	status := CommitStatus{
		OperationID: id, Phase: CommitClaimed, ActiveGeneration: runtimeState.Current.ID,
		PreviousGeneration: runtimeState.Current.ID, NewGeneration: id, StartedAt: now, UpdatedAt: now,
	}
	if err := committer.writeStatus(stageRoot, status); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runtimeState.Root, "staging", "import-status", "."+id+".json-1234"), []byte("partial"), 0o600); err != nil {
		t.Fatal(err)
	}
	actual, err := committer.Status(context.Background())
	if err != nil || actual.OperationID != id {
		t.Fatalf("status with crash temporary=%#v err=%v", actual, err)
	}
}

func TestCommitStatusPrefersInterruptedOperationAcrossClockRollback(t *testing.T) {
	fixture := newExportFixture(t, false)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 7, 0, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	held, err := fixture.runtime.Locks.AcquireSet(context.Background(), runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive})
	if err != nil {
		t.Fatal(err)
	}
	claim, err := committer.inspector.ClaimWithinLocks(context.Background(), fixture.runtime, inspection.StagingToken, 9, held)
	if err != nil {
		t.Fatal(err)
	}
	if err := held.Release(); err != nil {
		t.Fatal(err)
	}
	active := CommitStatus{
		OperationID: claim.GenerationID, Phase: CommitClaimed, ActiveGeneration: fixture.runtime.Current.ID,
		PreviousGeneration: fixture.runtime.Current.ID, NewGeneration: claim.GenerationID, StartedAt: now, UpdatedAt: now,
	}
	if err := committer.writeStatus(claim.StageRoot, active); err != nil {
		t.Fatal(err)
	}
	future := now.Add(time.Hour)
	oldID := "import-20260811T080000000Z-0011223344556677"
	completed := future
	terminal := CommitStatus{
		OperationID: oldID, Phase: CommitSucceeded, ActiveGeneration: fixture.runtime.Current.ID,
		PreviousGeneration: fixture.runtime.Current.ID, NewGeneration: oldID,
		StartedAt: future, UpdatedAt: future, CompletedAt: &completed,
	}
	if err := committer.writeStatus(filepath.Join(fixture.runtime.Root, "staging", "import", oldID), terminal); err != nil {
		t.Fatal(err)
	}
	actual, err := committer.Status(context.Background())
	if err != nil || actual.OperationID != claim.GenerationID {
		t.Fatalf("clock rollback selected status=%#v err=%v", actual, err)
	}
}

func TestCommitKeepsMaintenanceClosedWhenOldBundleCannotReactivate(t *testing.T) {
	fixture := newExportFixture(t, false)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	host := &rejectNewGenerationHost{
		database: fixture.database, rejectID: fixture.runtime.Current.ID, epoch: 9,
		closeErr: errors.New("injected close failure"),
	}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 9, host); err == nil {
		t.Fatal("commit unexpectedly survived close and old-reactivation failures")
	}
	if host.endCalls != 0 {
		t.Fatalf("maintenance reopened %d times without a ready bundle", host.endCalls)
	}
	status, err := committer.Status(context.Background())
	if err != nil || status.Phase != CommitNeedsAttention {
		t.Fatalf("failure status=%#v err=%v", status, err)
	}
}

func TestCommitPersistsTerminalFailureBeforeReopeningAdmission(t *testing.T) {
	fixture := newExportFixture(t, false)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 8, 30, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	var endErr error
	host := &rejectNewGenerationHost{database: fixture.database, epoch: 10}
	host.end = func() {
		status, statusErr := committer.Status(context.Background())
		metadata, metadataErr := readStagingMetadata(filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID))
		if statusErr != nil || metadataErr != nil || status.Phase != CommitFailed || metadata.State != stagingFailed {
			endErr = errors.Join(errors.New("admission reopened before failure became durable"), statusErr, metadataErr)
		}
	}
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 9, host); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("owner rotation error=%v", err)
	}
	if host.endCalls != 1 || endErr != nil {
		t.Fatalf("maintenance end calls=%d err=%v", host.endCalls, endErr)
	}
}

func testCommitter(t *testing.T, root string, now *time.Time, available uint64) *Committer {
	t.Helper()
	committer, err := NewCommitter(CommitOptions{
		DataDir: root, AppVersion: "0.1.0", Clock: func() time.Time { return *now },
		AvailableBytes: func(string) (uint64, error) { return available, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return committer
}

type rejectNewGenerationHost struct {
	database *sqlitestore.Database
	rejectID string
	epoch    uint64
	begin    func() error
	closeErr error
	endCalls int
	end      func()
}

func (host *rejectNewGenerationHost) BeginMaintenance(context.Context) error {
	if host.begin != nil {
		return host.begin()
	}
	return nil
}
func (host *rejectNewGenerationHost) CurrentSessionEpoch() (uint64, error) {
	return host.epoch, nil
}
func (host *rejectNewGenerationHost) ActiveDatabase() *sqlitestore.Database { return host.database }
func (host *rejectNewGenerationHost) CloseActive() error {
	if host.database == nil {
		return nil
	}
	err := host.database.Close()
	host.database = nil
	return errors.Join(err, host.closeErr)
}
func (host *rejectNewGenerationHost) Activate(ctx context.Context, runtimeState *runtimefs.Runtime) error {
	if runtimeState.Current.ID == host.rejectID {
		return errors.New("injected new-generation readiness failure")
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		return err
	}
	if err := database.Ready(ctx); err != nil {
		_ = database.Close()
		return err
	}
	host.database = database
	return nil
}
func (host *rejectNewGenerationHost) EndMaintenance() {
	host.endCalls++
	if host.end != nil {
		host.end()
	}
}
