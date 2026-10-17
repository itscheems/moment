package archive

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestDiagnosePersistentStateClassifiesBoundedRedactedStaging(t *testing.T) {
	fixture := newExportFixture(t, false)
	runtimeState := fixture.runtime
	archiveBytes := exportFixtureBytes(t, fixture)
	if err := fixture.database.WriteTx(context.Background(), func(q sqlitestore.Queries) error {
		_, err := q.ExecContext(context.Background(), `DELETE FROM sessions`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := fixture.database.Close(); err != nil {
		t.Fatal(err)
	}
	databaseBytes, err := os.ReadFile(runtimeState.Current.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
	tests := []struct {
		name     string
		state    string
		expires  time.Time
		complete bool
	}{
		{name: "import-ready", state: stagingReady, expires: now.Add(time.Hour), complete: true},
		{name: "import-expired", state: stagingReady, expires: now.Add(-time.Second), complete: true},
		{name: "import-committing", state: stagingCommitting, expires: now.Add(time.Hour), complete: false},
		{name: "import-used", state: stagingUsed, expires: now.Add(time.Hour)},
		{name: "import-failed", state: stagingFailed, expires: now.Add(time.Hour)},
	}
	for _, test := range tests {
		stageRoot := filepath.Join(runtimeState.Root, "staging", "import", test.name)
		if err := os.Mkdir(stageRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		if test.complete {
			for path, data := range map[string][]byte{"archive.zip": archiveBytes, filepath.Join("generation", "moment.db"): databaseBytes} {
				full := filepath.Join(stageRoot, path)
				if err := os.MkdirAll(filepath.Dir(full), 0o700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(full, data, 0o600); err != nil {
					t.Fatal(err)
				}
			}
			if err := os.MkdirAll(filepath.Join(stageRoot, "generation", "media", "original"), 0o700); err != nil {
				t.Fatal(err)
			}
		}
		if err := writeStagingMetadata(stageRoot, stagingMetadata{
			TokenHash: strings.Repeat("a", 64), ArchiveSHA: shaHex(archiveBytes), InstanceID: runtimeState.Instance.ID,
			Authorization: testAuthorization(1), GenerationID: test.name, ExpiresAtMS: test.expires.UnixMilli(), State: test.state,
		}); err != nil {
			t.Fatal(err)
		}
	}
	invalidRoot := filepath.Join(runtimeState.Root, "staging", "import", "import-invalid")
	if err := os.Mkdir(invalidRoot, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := writeStagingMetadata(invalidRoot, stagingMetadata{
		TokenHash: "CANARY_TOKEN_HASH", ArchiveSHA: strings.Repeat("b", 64), InstanceID: runtimeState.Instance.ID,
		Authorization: testAuthorization(1), GenerationID: "different-generation", ExpiresAtMS: now.Add(time.Hour).UnixMilli(), State: stagingReady,
	}); err != nil {
		t.Fatal(err)
	}

	staging, recovery, stagingErr, recoveryErr := DiagnosePersistentState(context.Background(), runtimeState.Root, runtimeState.Instance.ID, runtimeState.Current.ID, now, 32)
	if stagingErr != nil || recoveryErr != nil {
		t.Fatal(errors.Join(stagingErr, recoveryErr))
	}
	if staging.Total != 6 || staging.Ready != 1 || staging.Expired != 1 || staging.Committing != 1 || staging.Used != 1 || staging.Failed != 1 || staging.Invalid != 1 || staging.Incomplete != 1 || staging.Bounded {
		t.Fatalf("staging = %#v", staging)
	}
	if recovery.Journals != 0 || recovery.Invalid != 0 {
		t.Fatalf("recovery = %#v", recovery)
	}
	encoded, err := json.Marshal(struct {
		Staging  StagingDiagnostic
		Recovery RecoveryDiagnostic
	}{staging, recovery})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(encoded), "CANARY") || strings.Contains(string(encoded), strings.Repeat("a", 64)) || strings.Contains(string(encoded), runtimeState.Root) {
		t.Fatalf("diagnostic leaked private staging identity: %s", encoded)
	}

	bounded, _, stagingErr, recoveryErr := DiagnosePersistentState(context.Background(), runtimeState.Root, runtimeState.Instance.ID, runtimeState.Current.ID, now, 2)
	if stagingErr != nil || recoveryErr != nil || !bounded.Bounded || bounded.Total != 2 {
		t.Fatalf("bounded = %#v, %v", bounded, errors.Join(stagingErr, recoveryErr))
	}
}

func TestDiagnoseStagingRejectsTruncatedOrHashMismatchedPayloadsReadOnly(t *testing.T) {
	tests := []struct {
		name   string
		full   bool
		mutate func(*testing.T, string)
	}{
		{name: "zero archive", mutate: func(t *testing.T, stageRoot string) {
			if err := os.Truncate(filepath.Join(stageRoot, "archive.zip"), 0); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "truncated archive", mutate: func(t *testing.T, stageRoot string) {
			path := filepath.Join(stageRoot, "archive.zip")
			info, err := os.Stat(path)
			if err != nil {
				t.Fatal(err)
			}
			if err := os.Truncate(path, info.Size()/2); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "archive hash mismatch", mutate: func(t *testing.T, stageRoot string) {
			metadata, err := readStagingMetadata(stageRoot)
			if err != nil {
				t.Fatal(err)
			}
			metadata.ArchiveSHA = strings.Repeat("c", 64)
			if err := writeStagingMetadata(stageRoot, metadata); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "truncated database", mutate: func(t *testing.T, stageRoot string) {
			if err := os.Truncate(filepath.Join(stageRoot, "generation", "moment.db"), 100); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "valid SQLite row tamper", full: true, mutate: func(t *testing.T, stageRoot string) {
			if err := mutateStagedDatabase(filepath.Join(stageRoot, "generation", "moment.db"), `UPDATE entries SET content_md = 'tampered body'`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "valid SQLite site tamper", full: true, mutate: func(t *testing.T, stageRoot string) {
			if err := mutateStagedDatabase(filepath.Join(stageRoot, "generation", "moment.db"), `UPDATE site_settings SET title = 'tampered title' WHERE id = 1`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "valid SQLite media tamper", full: true, mutate: func(t *testing.T, stageRoot string) {
			if err := mutateStagedDatabase(filepath.Join(stageRoot, "generation", "moment.db"), `UPDATE media SET original_name = 'tampered.png'`); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "valid SQLite FTS tamper", full: true, mutate: func(t *testing.T, stageRoot string) {
			if err := mutateStagedDatabase(filepath.Join(stageRoot, "generation", "moment.db"), `INSERT INTO entry_fts (entry_id, content_text) SELECT entry_id, content_text FROM entry_fts LIMIT 1`); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExportFixture(t, test.full)
			now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
			inspection, err := testInspector(t, fixture.runtime.Root, nil, &now, math.MaxUint64).Inspect(context.Background(), bytes.NewReader(exportFixtureBytes(t, fixture)), testAuthorization(1))
			if err != nil {
				t.Fatal(err)
			}
			stageRoot := filepath.Join(fixture.runtime.Root, "staging", "import", inspection.GenerationID)
			test.mutate(t, stageRoot)
			before := fingerprintGeneration(t, stageRoot)

			staging, _, stagingErr, recoveryErr := DiagnosePersistentState(context.Background(), fixture.runtime.Root, fixture.runtime.Instance.ID, fixture.runtime.Current.ID, now, 32)
			if stagingErr != nil || recoveryErr != nil || staging.Total != 1 || staging.Ready != 1 || staging.Incomplete != 1 || staging.Invalid != 0 {
				t.Fatalf("staging = %#v, err=%v", staging, errors.Join(stagingErr, recoveryErr))
			}
			if after := fingerprintGeneration(t, stageRoot); !reflect.DeepEqual(before, after) {
				t.Fatalf("diagnostic mutated staging authority\nbefore=%#v\nafter=%#v", before, after)
			}
		})
	}
}

func TestDiagnoseRecoveryUsesDurableAuthorityAcrossHistory(t *testing.T) {
	t.Run("two real sequential successful imports", func(t *testing.T) {
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
		now := time.Date(2026, 8, 11, 9, 0, 0, 0, time.UTC)
		for range 2 {
			inspection, err := testInspector(t, fixture.runtime.Root, nil, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, Authorization{AccountID: fixtureAccountID, CredentialID: "local"})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64).Commit(context.Background(), inspection.StagingToken, Authorization{AccountID: fixtureAccountID, CredentialID: "local"}, nil); err != nil {
				t.Fatal(err)
			}
			now = now.Add(time.Minute)
		}
		current, err := runtimefs.Open(fixture.runtime.Root)
		if err != nil {
			t.Fatal(err)
		}
		_, recovery, stagingErr, recoveryErr := DiagnosePersistentState(context.Background(), fixture.runtime.Root, current.Instance.ID, current.Current.ID, now, 32)
		if stagingErr != nil || recoveryErr != nil || recovery.Journals != 2 || recovery.CurrentMismatch != 0 || recovery.Invalid != 0 || recovery.LatestPhase != CommitSucceeded {
			t.Fatalf("sequential recovery = %#v, %v", recovery, errors.Join(stagingErr, recoveryErr))
		}
	})

	t.Run("historical failed journal does not contradict newer success", func(t *testing.T) {
		runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
		failedID := "import-20260811T100000000Z-0011223344556677"
		failed := terminalDiagnosticStatus(failedID, runtimeState.Current.ID, failedID, CommitFailed, now)
		if err := writeCommitStatus(filepath.Join(runtimeState.Root, "staging", "import", failedID), failed); err != nil {
			t.Fatal(err)
		}
		succeededID := "import-20260811T100100000Z-8899aabbccddeeff"
		succeeded := terminalDiagnosticStatus(succeededID, runtimeState.Current.ID, succeededID, CommitSucceeded, now.Add(time.Minute))
		if err := writeCommitStatus(filepath.Join(runtimeState.Root, "staging", "import", succeededID), succeeded); err != nil {
			t.Fatal(err)
		}
		_, recovery, stagingErr, recoveryErr := DiagnosePersistentState(context.Background(), runtimeState.Root, runtimeState.Instance.ID, succeededID, now, 32)
		if stagingErr != nil || recoveryErr != nil || recovery.Journals != 2 || recovery.CurrentMismatch != 0 || recovery.LatestPhase != CommitSucceeded {
			t.Fatalf("terminal history recovery = %#v, %v", recovery, errors.Join(stagingErr, recoveryErr))
		}
	})

	t.Run("active operation wins across clock rollback", func(t *testing.T) {
		runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 8, 11, 11, 0, 0, 0, time.UTC)
		activeID := "import-20260811T110000000Z-0011223344556677"
		activeRoot := filepath.Join(runtimeState.Root, "staging", "import", activeID)
		if err := os.Mkdir(activeRoot, 0o700); err != nil {
			t.Fatal(err)
		}
		if err := writeStagingMetadata(activeRoot, stagingMetadata{
			TokenHash: strings.Repeat("a", 64), ArchiveSHA: strings.Repeat("b", 64), InstanceID: runtimeState.Instance.ID,
			Authorization: Authorization{AccountID: fixtureAccountID, CredentialID: "local"}, GenerationID: activeID, ExpiresAtMS: now.Add(time.Hour).UnixMilli(), State: stagingCommitting,
		}); err != nil {
			t.Fatal(err)
		}
		active := CommitStatus{
			OperationID: activeID, Phase: CommitClaimed, ActiveGeneration: runtimeState.Current.ID,
			PreviousGeneration: runtimeState.Current.ID, NewGeneration: activeID, StartedAt: now, UpdatedAt: now,
		}
		if err := writeCommitStatus(activeRoot, active); err != nil {
			t.Fatal(err)
		}
		futureID := "import-20260811T120000000Z-8899aabbccddeeff"
		future := terminalDiagnosticStatus(futureID, runtimeState.Current.ID, futureID, CommitFailed, now.Add(time.Hour))
		if err := writeCommitStatus(filepath.Join(runtimeState.Root, "staging", "import", futureID), future); err != nil {
			t.Fatal(err)
		}
		_, recovery, stagingErr, recoveryErr := DiagnosePersistentState(context.Background(), runtimeState.Root, runtimeState.Instance.ID, runtimeState.Current.ID, now, 32)
		if stagingErr != nil || recoveryErr != nil || recovery.Active != 1 || recovery.CurrentMismatch != 0 || recovery.LatestPhase != CommitClaimed {
			t.Fatalf("active recovery = %#v, %v", recovery, errors.Join(stagingErr, recoveryErr))
		}
	})

	t.Run("terminal history beyond display limit remains valid", func(t *testing.T) {
		runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
		for index := 0; index < 33; index++ {
			id := "import-20260811T120000000Z-" + fmt.Sprintf("%016x", index+1)
			status := terminalDiagnosticStatus(id, runtimeState.Current.ID, runtimeState.Current.ID, CommitFailed, now.Add(time.Duration(index)*time.Second))
			if err := writeCommitStatus(filepath.Join(runtimeState.Root, "staging", "import", id), status); err != nil {
				t.Fatal(err)
			}
		}
		_, recovery, stagingErr, recoveryErr := DiagnosePersistentState(context.Background(), runtimeState.Root, runtimeState.Instance.ID, runtimeState.Current.ID, now, 32)
		if stagingErr != nil || recoveryErr != nil || recovery.Journals != 32 || !recovery.Bounded || recovery.CurrentMismatch != 0 || recovery.Invalid != 0 || recovery.LatestPhase != CommitFailed {
			t.Fatalf("bounded terminal recovery = %#v, %v", recovery, errors.Join(stagingErr, recoveryErr))
		}
	})
}

func TestDiagnoseRecoveryRejectsUnsafeLatestAndTemporaryState(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, string, string)
	}{
		{name: "corrupt latest", mutate: func(t *testing.T, root, _ string) {
			if err := os.WriteFile(filepath.Join(root, "staging", "import-status", commitLatestFile), []byte("not valid\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "dangling latest", mutate: func(t *testing.T, root, _ string) {
			if err := os.WriteFile(filepath.Join(root, "staging", "import-status", commitLatestFile), []byte("import-20260811T130100000Z-deadbeefdeadbeef\n"), 0o600); err != nil {
				t.Fatal(err)
			}
		}},
		{name: "latest symlink", mutate: func(t *testing.T, root, id string) {
			latest := filepath.Join(root, "staging", "import-status", commitLatestFile)
			if err := os.Remove(latest); err != nil {
				t.Fatal(err)
			}
			if err := os.Symlink(id+".json", latest); err != nil {
				t.Skipf("symlink unavailable: %v", err)
			}
		}},
		{name: "unsafe latest temporary", mutate: func(t *testing.T, root, _ string) {
			if err := os.Mkdir(filepath.Join(root, "staging", "import-status", ".LATEST-123"), 0o700); err != nil {
				t.Fatal(err)
			}
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			now := time.Date(2026, 8, 11, 13, 0, 0, 0, time.UTC)
			id := "import-20260811T130000000Z-0011223344556677"
			status := terminalDiagnosticStatus(id, runtimeState.Current.ID, id, CommitFailed, now)
			if err := writeCommitStatus(filepath.Join(runtimeState.Root, "staging", "import", id), status); err != nil {
				t.Fatal(err)
			}
			test.mutate(t, runtimeState.Root, id)
			_, recovery, stagingErr, recoveryErr := DiagnosePersistentState(context.Background(), runtimeState.Root, runtimeState.Instance.ID, runtimeState.Current.ID, now, 32)
			if stagingErr != nil || recoveryErr == nil || recovery.Invalid != 1 {
				t.Fatalf("unsafe recovery = %#v, staging=%v recovery=%v", recovery, stagingErr, recoveryErr)
			}
		})
	}
}

func terminalDiagnosticStatus(id, previous, active string, phase CommitPhase, at time.Time) CommitStatus {
	completed := at
	return CommitStatus{
		OperationID: id, Phase: phase, ActiveGeneration: active, PreviousGeneration: previous, NewGeneration: id,
		ErrorCode: "DIAGNOSTIC_FIXTURE", StartedAt: at, UpdatedAt: at, CompletedAt: &completed,
	}
}
