package archive

import (
	"context"
	"database/sql"
	"errors"
	"io/fs"
	"math"
	"net/url"
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

func TestCommitArchiveRejectsExpectedHashMismatchBeforeClaimOrMaintenance(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	oldID := fixture.runtime.Current.ID
	now := time.Date(2026, 8, 11, 3, 10, 0, 0, time.UTC)
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	maintenanceCalls := 0
	host := &rejectNewGenerationHost{database: fixture.database, epoch: 9, begin: func() error {
		maintenanceCalls++
		return nil
	}}
	file, err := os.Open(archivePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = file.Close() }()
	if _, err := committer.CommitArchive(context.Background(), file, strings.Repeat("0", 64), 9, host); !domain.HasCode(err, domain.CodeExportChecksumMismatch) {
		t.Fatalf("expected archive hash mismatch error=%v", err)
	}
	if maintenanceCalls != 0 {
		t.Fatalf("hash mismatch entered maintenance %d times", maintenanceCalls)
	}
	runtimeState, err := runtimefs.Open(fixture.runtime.Root)
	if err != nil || runtimeState.Current.ID != oldID {
		t.Fatalf("hash mismatch CURRENT=%#v err=%v", runtimeState.Current, err)
	}
	backups, err := os.ReadDir(filepath.Join(fixture.runtime.Root, "backups"))
	if err != nil || len(backups) != 0 {
		t.Fatalf("hash mismatch backups=%#v err=%v", backups, err)
	}
}

func TestGenerationRetentionKeepsBeforeAndSweepsAtTwentyFourHours(t *testing.T) {
	fixture := newExportFixture(t, false)
	archivePath := exportFixtureFile(t, fixture)
	previous := fixture.runtime.Current.ID
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.InitializeOffline(context.Background(), fixture.runtime, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 3, 20, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, nil, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 1)
	if err != nil {
		t.Fatal(err)
	}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 1, nil); err != nil {
		t.Fatal(err)
	}
	previousRoot := filepath.Join(fixture.runtime.Root, "generations", previous)
	now = now.Add(generationRetention - time.Millisecond)
	if err := committer.SweepRetainedGenerations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(previousRoot); err != nil {
		t.Fatalf("generation was removed before retention elapsed: %v", err)
	}
	now = now.Add(time.Millisecond)
	if err := committer.SweepRetainedGenerations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(previousRoot); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("generation remained at retention boundary: %v", err)
	}
}

func TestGenerationRetentionFailsClosedForUnsafeEntryAndNeedsAttention(t *testing.T) {
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 12, 4, 0, 0, 0, time.UTC)
	committer := testCommitter(t, runtimeState.Root, &now, math.MaxUint64)
	id := "import-20260811T040000000Z-0011223344556677"
	candidate := filepath.Join(runtimeState.Root, "generations", id)
	if err := os.Mkdir(candidate, 0o700); err != nil {
		t.Fatal(err)
	}
	status := CommitStatus{
		OperationID: id, Phase: CommitNeedsAttention, ActiveGeneration: runtimeState.Current.ID,
		PreviousGeneration: runtimeState.Current.ID, NewGeneration: id,
		ErrorCode: string(domain.CodeDataIntegrityFailed), StartedAt: now.Add(-48 * time.Hour), UpdatedAt: now.Add(-48 * time.Hour),
	}
	if err := committer.writeStatus(filepath.Join(runtimeState.Root, "staging", "import", id), status); err != nil {
		t.Fatal(err)
	}
	if err := committer.SweepRetainedGenerations(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(candidate); err != nil {
		t.Fatalf("needs_attention generation was removed: %v", err)
	}
	unsafe := filepath.Join(runtimeState.Root, "generations", "unsafe-link")
	if err := os.Symlink(runtimeState.Current.Root, unsafe); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := committer.SweepRetainedGenerations(context.Background()); err == nil {
		t.Fatal("unsafe generation entry did not fail closed")
	}
	if _, err := os.Stat(candidate); err != nil {
		t.Fatalf("fail-closed sweep removed protected generation: %v", err)
	}
}

func TestGenerationRetentionKeepsCandidateWhenBackupRevalidationFails(t *testing.T) {
	fixture := newExportFixture(t, false)
	archivePath := exportFixtureFile(t, fixture)
	previous := fixture.runtime.Current.ID
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.InitializeOffline(context.Background(), fixture.runtime, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 3, 40, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, nil, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 1)
	if err != nil {
		t.Fatal(err)
	}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	result, err := committer.Commit(context.Background(), inspection.StagingToken, 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	backupPath := filepath.Join(fixture.runtime.Root, "backups", result.Status.BackupName)
	if err := os.WriteFile(backupPath, []byte("corrupt"), 0o600); err != nil {
		t.Fatal(err)
	}
	now = now.Add(generationRetention)
	if err := committer.SweepRetainedGenerations(context.Background()); err == nil {
		t.Fatal("corrupt retained backup did not fail closed")
	}
	if _, err := os.Stat(filepath.Join(fixture.runtime.Root, "generations", previous)); err != nil {
		t.Fatalf("candidate was removed despite corrupt backup: %v", err)
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

func TestSucceededJournalFailureRollsBackWithoutUsedMarkerConflict(t *testing.T) {
	fixture := newExportFixture(t, false)
	archivePath := exportFixtureFile(t, fixture)
	previous := fixture.runtime.Current.ID
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.InitializeOffline(context.Background(), fixture.runtime, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 4, 10, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, nil, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 1)
	if err != nil {
		t.Fatal(err)
	}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	injected := false
	committer.statusWriter = func(stageRoot string, status CommitStatus) error {
		if status.Phase == CommitSucceeded && !injected {
			injected = true
			return errors.New("injected succeeded journal failure")
		}
		return writeCommitStatus(stageRoot, status)
	}
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 1, nil); err == nil {
		t.Fatal("succeeded journal failure unexpectedly returned success")
	}
	status, err := committer.Status(context.Background())
	if err != nil || status.Phase != CommitRolledBack || status.ActiveGeneration != previous {
		t.Fatalf("journal failure status=%#v err=%v", status, err)
	}
	metadata, err := readStagingMetadata(filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID))
	if err != nil || metadata.State != stagingFailed {
		t.Fatalf("journal failure metadata=%#v err=%v", metadata, err)
	}
	runtimeState, err := runtimefs.OpenForCoordination(fixture.runtime.Root)
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
	defer func() { _ = held.Release() }()
	if err := runtimeState.RefreshCurrent(held); err != nil {
		t.Fatal(err)
	}
	if err := committer.RecoverWithinLocks(context.Background(), runtimeState, held); err != nil {
		t.Fatalf("restart reconciliation error=%v", err)
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

func TestMaintenanceDrainCancellationRemainsRecoverablePreSwitchFailure(t *testing.T) {
	fixture := newExportFixture(t, false)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 4, 32, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	host := &rejectNewGenerationHost{database: fixture.database, epoch: 9, begin: func() error { return context.Canceled }}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 9, host); !errors.Is(err, context.Canceled) {
		t.Fatalf("drain cancellation error=%v", err)
	}
	if host.endCalls != 0 || host.database == nil {
		t.Fatalf("drain cancellation changed live admission/bundle: end=%d database=%v", host.endCalls, host.database)
	}
	status, err := committer.Status(context.Background())
	if err != nil || status.Phase != CommitFailed || status.ActiveGeneration != fixture.runtime.Current.ID || status.ErrorCode != "CANCELLED" {
		t.Fatalf("drain cancellation status=%#v err=%v", status, err)
	}
	metadata, err := readStagingMetadata(filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID))
	if err != nil || metadata.State != stagingFailed {
		t.Fatalf("drain cancellation metadata=%#v err=%v", metadata, err)
	}
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
	defer func() { _ = held.Release() }()
	if err := committer.RecoverWithinLocks(context.Background(), runtimeState, held); err != nil {
		t.Fatalf("restart recovery rejected pre-switch cancellation: %v", err)
	}
}

func TestMaintenanceDrainTimeoutPersistsStableTimeoutCode(t *testing.T) {
	fixture := newExportFixture(t, false)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 4, 34, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	host := &rejectNewGenerationHost{database: fixture.database, epoch: 9, begin: func() error { return context.DeadlineExceeded }}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 9, host); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("drain timeout error=%v", err)
	}
	status, err := committer.Status(context.Background())
	if err != nil || status.Phase != CommitFailed || status.ErrorCode != "TIMEOUT" || status.ActiveGeneration != fixture.runtime.Current.ID {
		t.Fatalf("drain timeout status=%#v err=%v", status, err)
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

func TestCommitRejectsUnreferencedStagedMediaAfterInspection(t *testing.T) {
	fixture := newExportFixture(t, false)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 4, 47, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	stageDatabase := filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID, "generation", "moment.db")
	host := &rejectNewGenerationHost{database: fixture.database, epoch: 9, begin: func() error {
		return mutateStagedDatabase(stageDatabase, `
INSERT INTO media (
    id, sha256, storage_path, original_name, mime_type,
    byte_size, width, height, created_at_ms, unreferenced_since_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			"01ARZ3NDEKTSV4RRFFQ69G5FAV", strings.Repeat("a", 64),
			"media/original/aa/"+strings.Repeat("a", 64)+".png", "extra.png", "image/png",
			1, 1, 1, now.UnixMilli(), now.UnixMilli())
	}}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 9, host); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
		t.Fatalf("unreferenced staged media error=%v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(fixture.runtime.Root, "backups")); len(entries) != 0 {
		t.Fatalf("invalid staged media created %d backups", len(entries))
	}
}

func TestCommitRecomputesDerivedEntryContentAfterInspection(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 4, 49, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	stageDatabase := filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID, "generation", "moment.db")
	host := &rejectNewGenerationHost{database: fixture.database, epoch: 9, begin: func() error {
		if err := mutateStagedDatabase(stageDatabase, `UPDATE entries SET content_text = 'synchronized tamper'`); err != nil {
			return err
		}
		return mutateStagedDatabase(stageDatabase, `UPDATE entry_fts SET content_text = 'synchronized tamper'`)
	}}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 9, host); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
		t.Fatalf("synchronized derived-content tamper error=%v", err)
	}
	if entries, _ := os.ReadDir(filepath.Join(fixture.runtime.Root, "backups")); len(entries) != 0 {
		t.Fatalf("invalid derived content created %d backups", len(entries))
	}
}

func TestCommitRejectsDuplicateStagedFTSIdentity(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 4, 50, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	stageDatabase := filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID, "generation", "moment.db")
	host := &rejectNewGenerationHost{database: fixture.database, epoch: 9, begin: func() error {
		return mutateStagedDatabase(stageDatabase, `
INSERT INTO entry_fts (entry_id, content_text)
SELECT entry_id, content_text FROM entry_fts LIMIT 1`)
	}}
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	if _, err := committer.Commit(context.Background(), inspection.StagingToken, 9, host); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
		t.Fatalf("duplicate staged FTS identity error=%v", err)
	}
}

func TestCommitRejectsExtraStagedGenerationPaths(t *testing.T) {
	for _, relative := range []string{"secret.txt", "media/other/nested.txt", "media/original/ff/orphan.png"} {
		t.Run(strings.ReplaceAll(relative, "/", "_"), func(t *testing.T) {
			fixture := newExportFixture(t, true)
			archivePath := exportFixtureFile(t, fixture)
			now := time.Date(2026, 8, 11, 4, 51, 0, 0, time.UTC)
			inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
			if err != nil {
				t.Fatal(err)
			}
			generationRoot := filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID, "generation")
			host := &rejectNewGenerationHost{database: fixture.database, epoch: 9, begin: func() error {
				target := filepath.Join(generationRoot, filepath.FromSlash(relative))
				if err := os.MkdirAll(filepath.Dir(target), 0o700); err != nil {
					return err
				}
				return os.WriteFile(target, []byte("orphan"), 0o600)
			}}
			committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
			if _, err := committer.Commit(context.Background(), inspection.StagingToken, 9, host); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
				t.Fatalf("extra staged path %q error=%v", relative, err)
			}
		})
	}
}

func mutateStagedDatabase(path, statement string, arguments ...any) error {
	dsn := (&url.URL{Scheme: "file", Path: filepath.ToSlash(path), RawQuery: "_busy_timeout=5000&_foreign_keys=on"}).String()
	database, err := sql.Open("sqlite", dsn)
	if err != nil {
		return err
	}
	if _, err = database.Exec(statement, arguments...); err != nil {
		_ = database.Close()
		return err
	}
	return database.Close()
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

func TestCommitStatusUsesDurableLatestOperationAcrossTerminalClockRollback(t *testing.T) {
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 8, 0, 0, 0, time.UTC)
	committer := testCommitter(t, runtimeState.Root, &now, math.MaxUint64)
	firstID := "import-20260811T080000000Z-0011223344556677"
	firstCompleted := now
	first := CommitStatus{
		OperationID: firstID, Phase: CommitFailed, ActiveGeneration: runtimeState.Current.ID,
		PreviousGeneration: runtimeState.Current.ID, NewGeneration: firstID,
		ErrorCode: string(domain.CodeDataIntegrityFailed), StartedAt: now, UpdatedAt: now, CompletedAt: &firstCompleted,
	}
	if err := committer.writeStatus(filepath.Join(runtimeState.Root, "staging", "import", firstID), first); err != nil {
		t.Fatal(err)
	}
	rolledBackClock := now.Add(-time.Hour)
	secondID := "import-20260811T070000000Z-8899aabbccddeeff"
	secondCompleted := rolledBackClock
	second := CommitStatus{
		OperationID: secondID, Phase: CommitFailed, ActiveGeneration: runtimeState.Current.ID,
		PreviousGeneration: runtimeState.Current.ID, NewGeneration: secondID,
		ErrorCode: string(domain.CodeDataIntegrityFailed), StartedAt: rolledBackClock, UpdatedAt: rolledBackClock, CompletedAt: &secondCompleted,
	}
	if err := committer.writeStatus(filepath.Join(runtimeState.Root, "staging", "import", secondID), second); err != nil {
		t.Fatal(err)
	}
	actual, err := committer.Status(context.Background())
	if err != nil || actual.OperationID != secondID {
		t.Fatalf("latest terminal across clock rollback=%#v err=%v", actual, err)
	}
}

func TestCommitStatusUsesCurrentAcrossPostSwitchJournalGap(t *testing.T) {
	fixture := newExportFixture(t, false)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 7, 30, 0, 0, time.UTC)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 9)
	if err != nil {
		t.Fatal(err)
	}
	previous := fixture.runtime.Current.ID
	stageRoot := filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID)
	committer := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64)
	status := CommitStatus{
		OperationID: inspection.GenerationID, Phase: CommitSwitching, ActiveGeneration: previous,
		PreviousGeneration: previous, NewGeneration: inspection.GenerationID, StartedAt: now, UpdatedAt: now,
	}
	if err := committer.writeStatus(stageRoot, status); err != nil {
		t.Fatal(err)
	}
	held, err := fixture.runtime.Locks.AcquireSet(context.Background(),
		runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive},
		runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Exclusive},
		runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Exclusive},
	)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()
	finalized, err := fixture.runtime.FinalizeStagedGeneration(context.Background(), stagingGeneration(stageRoot, inspection.GenerationID), held)
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.runtime.SwitchCurrent(context.Background(), finalized.ID, held); err != nil {
		t.Fatal(err)
	}
	actual, err := committer.Status(context.Background())
	if err != nil || actual.Phase != CommitSwitching || actual.ActiveGeneration != finalized.ID {
		t.Fatalf("post-switch status=%#v err=%v", actual, err)
	}
}

func TestCommitStatusRejectsTerminalPhaseCurrentContradiction(t *testing.T) {
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 7, 45, 0, 0, time.UTC)
	committer := testCommitter(t, runtimeState.Root, &now, math.MaxUint64)
	id := "import-20260811T074500000Z-0011223344556677"
	completed := now
	status := CommitStatus{
		OperationID: id, Phase: CommitSucceeded, ActiveGeneration: id,
		PreviousGeneration: runtimeState.Current.ID, NewGeneration: id,
		BackupName: "pre-import.zip", BackupVerified: true,
		StartedAt: now, UpdatedAt: now, CompletedAt: &completed,
	}
	if err := committer.writeStatus(filepath.Join(runtimeState.Root, "staging", "import", id), status); err != nil {
		t.Fatal(err)
	}
	if _, err := committer.Status(context.Background()); !domain.HasCode(err, domain.CodeDataIntegrityFailed) {
		t.Fatalf("terminal/current contradiction error=%v", err)
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
