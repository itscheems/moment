package doctor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/auth"
	mediastore "github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

type doctorFixture struct {
	root    string
	current runtimefs.Generation
}

const doctorWALHelper = "MOMENT_DOCTOR_WAL_HELPER"

func newDoctorFixture(t *testing.T) doctorFixture {
	t.Helper()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{
		DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment,
	})
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Ready(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{}); err != nil {
		t.Fatal(err)
	}
	return doctorFixture{root: runtimeState.Root, current: runtimeState.Current}
}

func TestRegistryJSONV1AndHealthyReadOnlyModes(t *testing.T) {
	fixture := newDoctorFixture(t)
	service, err := New(Options{DataDir: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	before := generationSnapshot(t, fixture.current.Root)
	for _, mode := range []Mode{ModeSummary, ModeDeep} {
		report, err := service.Run(context.Background(), mode)
		if err != nil {
			t.Fatalf("%s doctor: %v", mode, err)
		}
		if err := ValidateReport(report, mode); err != nil {
			t.Fatalf("%s report: %v", mode, err)
		}
		if report.Status != OverallHealthy || report.Failure() != FailureNone || report.Counts.Fail != 0 || report.Counts.Warning != 0 {
			t.Fatalf("%s report = %#v", mode, report)
		}
		if mode == ModeDeep && report.Counts.Skipped != 0 {
			t.Fatalf("deep skipped checks = %d", report.Counts.Skipped)
		}
	}
	after := generationSnapshot(t, fixture.current.Root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("doctor mutated authority\nbefore=%#v\nafter=%#v", before, after)
	}

	ids := Registry()
	if len(ids) != 21 {
		t.Fatalf("registry length = %d", len(ids))
	}
	seen := make(map[string]bool, len(ids))
	for _, id := range ids {
		if seen[id] {
			t.Fatalf("duplicate check id %q", id)
		}
		seen[id] = true
	}
}

func TestDeepDoctorReadsUncleanWALWithoutMutatingGeneration(t *testing.T) {
	if os.Getenv(doctorWALHelper) == "1" {
		runDoctorWALHelper()
		return
	}
	fixture := newDoctorFixture(t)
	command := exec.Command(os.Args[0], "-test.run=^TestDeepDoctorReadsUncleanWALWithoutMutatingGeneration$")
	command.Env = append(os.Environ(), doctorWALHelper+"=1", "MOMENT_DOCTOR_WAL_ROOT="+fixture.root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unclean WAL helper: %v\n%s", err, output)
	}
	walInfo, err := os.Stat(fixture.current.DatabasePath + "-wal")
	if err != nil || walInfo.Size() == 0 {
		t.Fatalf("helper did not leave durable WAL: %#v %v", walInfo, err)
	}
	before := generationSnapshot(t, fixture.current.Root)
	tempBefore, _ := filepath.Glob(filepath.Join(os.TempDir(), "moment-doctor-sqlite-*"))
	service, err := New(Options{DataDir: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Run(context.Background(), ModeDeep)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReport(report, ModeDeep); err != nil {
		t.Fatal(err)
	}
	fts := findCheck(t, report, CheckFTSConsistency)
	if fts.Status != StatusPass || fts.Evidence["total"] != 1 {
		t.Fatalf("doctor missed WAL-only committed entry: %#v", fts)
	}
	after := generationSnapshot(t, fixture.current.Root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("active generation changed while reading WAL\nbefore=%#v\nafter=%#v", before, after)
	}
	tempAfter, _ := filepath.Glob(filepath.Join(os.TempDir(), "moment-doctor-sqlite-*"))
	if !reflect.DeepEqual(tempBefore, tempAfter) {
		t.Fatalf("private diagnostic snapshot was not cleaned: before=%v after=%v", tempBefore, tempAfter)
	}
}

func TestDeepDoctorSupportsInitializedNeverServedLegacyLocks(t *testing.T) {
	fixture := newDoctorFixture(t)
	for _, name := range []runtimefs.LockName{runtimefs.ServeLock, runtimefs.MaintenanceLock, runtimefs.MediaLock} {
		if err := os.Remove(filepath.Join(fixture.root, "locks", string(name)+".lock")); err != nil {
			t.Fatal(err)
		}
	}
	service, err := New(Options{DataDir: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Run(context.Background(), ModeDeep)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateReport(report, ModeDeep); err != nil {
		t.Fatal(err)
	}
	if report.Status != OverallHealthy || findCheck(t, report, CheckInstanceLocks).Status != StatusPass {
		t.Fatalf("legacy never-served report = %#v", report)
	}
	for _, name := range []runtimefs.LockName{runtimefs.ServeLock, runtimefs.MaintenanceLock, runtimefs.MediaLock} {
		if _, err := os.Lstat(filepath.Join(fixture.root, "locks", string(name)+".lock")); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("doctor created legacy-missing %s lock: %v", name, err)
		}
	}
}

func TestDoctorDistinguishesLiveStaleAndContendedLocks(t *testing.T) {
	fixture := newDoctorFixture(t)
	locker, err := runtimefs.OpenExistingLocker(fixture.root)
	if err != nil {
		t.Fatal(err)
	}

	staleService, err := New(Options{DataDir: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	stale, err := staleService.Run(context.Background(), ModeSummary)
	if err != nil {
		t.Fatal(err)
	}
	if state := findCheck(t, stale, CheckInstanceLocks).Evidence["serve_state"]; state != "available" {
		t.Fatalf("persistent unlocked filename state = %#v", state)
	}

	serve, err := locker.Acquire(context.Background(), runtimefs.ServeLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	live, err := staleService.Run(context.Background(), ModeSummary)
	if err != nil {
		_ = serve.Release()
		t.Fatal(err)
	}
	if state := findCheck(t, live, CheckInstanceLocks).Evidence["serve_state"]; state != "held" {
		_ = serve.Release()
		t.Fatalf("live serve state = %#v", state)
	}
	if err := serve.Release(); err != nil {
		t.Fatal(err)
	}

	importLease, err := locker.Acquire(context.Background(), runtimefs.ImportLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	contendedService, err := New(Options{DataDir: fixture.root, SnapshotWait: 25 * time.Millisecond})
	if err != nil {
		_ = importLease.Release()
		t.Fatal(err)
	}
	contended, err := contendedService.Run(context.Background(), ModeDeep)
	if err != nil {
		_ = importLease.Release()
		t.Fatal(err)
	}
	if err := importLease.Release(); err != nil {
		t.Fatal(err)
	}
	if contended.Failure() != FailureUnavailable || findCheck(t, contended, CheckInstanceLocks).Status != StatusFail ||
		findCheck(t, contended, CheckDatabaseOpen).Status != StatusSkipped {
		t.Fatalf("contended report = %#v", contended)
	}
}

func TestDoctorDatabaseSnapshotBoundFailsClosedAndCleansTemp(t *testing.T) {
	fixture := newDoctorFixture(t)
	tempBefore, _ := filepath.Glob(filepath.Join(os.TempDir(), "moment-doctor-sqlite-*"))
	service, err := New(Options{DataDir: fixture.root, MaxDatabaseBytes: 1})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Run(context.Background(), ModeDeep)
	if err != nil {
		t.Fatal(err)
	}
	check := findCheck(t, report, CheckDatabaseOpen)
	if report.Failure() != FailureUnavailable || check.Status != StatusFail || check.Evidence["bounded"] != true {
		t.Fatalf("bounded report = %#v", report)
	}
	tempAfter, _ := filepath.Glob(filepath.Join(os.TempDir(), "moment-doctor-sqlite-*"))
	if !reflect.DeepEqual(tempBefore, tempAfter) {
		t.Fatalf("bounded copy leaked temp: before=%v after=%v", tempBefore, tempAfter)
	}
}

func TestDoctorRejectsTemporaryDirectoryInsideAuthorityWithoutMutation(t *testing.T) {
	fixture := newDoctorFixture(t)
	authorityTemp := filepath.Join(fixture.root, "tmp")
	t.Setenv("TMPDIR", authorityTemp)
	before, err := os.ReadDir(authorityTemp)
	if err != nil {
		t.Fatal(err)
	}
	service, err := New(Options{DataDir: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Run(context.Background(), ModeDeep)
	if err != nil {
		t.Fatal(err)
	}
	if findCheck(t, report, CheckDatabaseOpen).Status != StatusFail || report.Failure() != FailureIntegrity {
		t.Fatalf("unsafe temp report = %#v", report)
	}
	after, err := os.ReadDir(authorityTemp)
	if err != nil {
		t.Fatal(err)
	}
	if len(before) != 0 || len(after) != 0 {
		t.Fatalf("unsafe temp base was mutated: before=%v after=%v", before, after)
	}
}

func runDoctorWALHelper() {
	ctx := context.Background()
	runtimeState, err := runtimefs.Open(os.Getenv("MOMENT_DOCTOR_WAL_ROOT"))
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	database, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	err = database.WriteTx(ctx, func(queries sqlitestore.Queries) error {
		_, err := queries.ExecContext(ctx, `INSERT INTO entries
(id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FC0', 'moment', 'wal only committed content', 1, 1, 1, 'wal only committed content')`)
		return err
	})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func TestDeterministicCorruptionChecks(t *testing.T) {
	tests := []struct {
		name    string
		corrupt func(*testing.T, doctorFixture)
		checkID string
		status  Status
		overall OverallStatus
		failure FailureKind
	}{
		{
			name: "bad CURRENT", checkID: CheckCurrentGeneration, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				writeFixtureFile(t, filepath.Join(fixture.root, "CURRENT"), []byte("../escape\n"), 0o600)
			},
		},
		{
			name: "missing CURRENT", checkID: CheckCurrentGeneration, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				if err := os.Remove(filepath.Join(fixture.root, "CURRENT")); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "CURRENT.next remnant", checkID: CheckCurrentNextState, status: StatusWarning, overall: OverallWarning, failure: FailureNone,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				writeFixtureFile(t, filepath.Join(fixture.root, "CURRENT.next"), []byte(fixture.current.ID+"\n"), 0o600)
			},
		},
		{
			name: "migration drift", checkID: CheckSchemaVersion, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				mutateDatabase(t, fixture.current.DatabasePath, `UPDATE schema_migrations SET checksum = ? WHERE version = 1`, strings.Repeat("b", 64))
			},
		},
		{
			name: "foreign key violation", checkID: CheckForeignKeys, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				mutateDatabase(t, fixture.current.DatabasePath, `PRAGMA foreign_keys = OFF;
INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAV', '01ARZ3NDEKTSV4RRFFQ69G5FAY', 0, NULL)`)
			},
		},
		{
			name: "FTS source drift", checkID: CheckFTSConsistency, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				mutateDatabase(t, fixture.current.DatabasePath, `INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAV', 'moment', 'CANARY_PRIVATE_POST_BODY', 1, 1, 1, 'wrong derived text')`)
			},
		},
		{
			name: "missing referenced original", checkID: CheckMissingMedia, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				sha := strings.Repeat("a", 64)
				mutateDatabase(t, fixture.current.DatabasePath, `INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAV', 'moment', 'private body', 1, 1, 1, 'private body');
INSERT INTO media (id, sha256, storage_path, original_name, mime_type, byte_size, width, height, created_at_ms, unreferenced_since_ms)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAY', '`+sha+`', 'media/original/aa/`+sha+`.png', 'CANARY_SECRET_NAME.png', 'image/png', 1, 1, 1, 1, NULL);
INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAV', '01ARZ3NDEKTSV4RRFFQ69G5FAY', 0, NULL)`)
			},
		},
		{
			name: "media reference clock mismatch", checkID: CheckMediaReferenceConsistency, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				seedMediaReferenceMismatch(t, fixture)
			},
		},
		{
			name: "SQLite byte corruption", checkID: CheckDatabaseOpen, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				writeFixtureFile(t, fixture.current.DatabasePath, []byte("not a sqlite database CANARY_DATABASE_BYTES"), 0o600)
			},
		},
		{
			name: "malformed import staging", checkID: CheckStaleImportStaging, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				if err := os.Mkdir(filepath.Join(fixture.root, "staging", "import", "CANARY_BAD_STAGE"), 0o700); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "incomplete ready staging", checkID: CheckStaleImportStaging, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				instance, err := runtimefs.ReadInstance(fixture.root)
				if err != nil {
					t.Fatal(err)
				}
				stageID := "import-incomplete"
				stageRoot := filepath.Join(fixture.root, "staging", "import", stageID)
				if err := os.Mkdir(stageRoot, 0o700); err != nil {
					t.Fatal(err)
				}
				metadata := fmt.Sprintf(`{"token_hash":"%s","expires_at_ms":%d,"archive_sha256":"%s","instance_id":"%s","session_epoch":1,"generation_id":"%s","state":"ready"}`+"\n",
					strings.Repeat("a", 64), time.Now().Add(time.Hour).UnixMilli(), strings.Repeat("b", 64), instance.ID, stageID)
				writeFixtureFile(t, filepath.Join(stageRoot, "staging.json"), []byte(metadata), 0o600)
			},
		},
		{
			name: "rollback journal remnant", checkID: CheckImportRecoveryState, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				writeFixtureFile(t, filepath.Join(fixture.root, "staging", "import-status", "CANARY_OPERATION.json"), []byte(`{"token_hash":"CANARY_STAGING_TOKEN_HASH"}`), 0o600)
			},
		},
		{
			name: "orphan original blob", checkID: CheckOrphanMedia, status: StatusWarning, overall: OverallWarning, failure: FailureNone,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				path := filepath.Join(fixture.current.OriginalsDir, "aa", "orphan.png")
				if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
					t.Fatal(err)
				}
				writeFixtureFile(t, path, []byte("CANARY_ORPHAN_BYTES"), 0o600)
			},
		},
		{
			name: "secret permissions", checkID: CheckSecretPermissions, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				if err := os.Chmod(filepath.Join(fixture.root, "secrets.json"), 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "database permissions", checkID: CheckCurrentGeneration, status: StatusFail, overall: OverallUnhealthy, failure: FailureIO,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				if runtime.GOOS == "windows" {
					t.Skip("POSIX permission bits are not authoritative on Windows")
				}
				t.Cleanup(func() { _ = os.Chmod(fixture.current.DatabasePath, 0o600) })
				if err := os.Chmod(fixture.current.DatabasePath, 0o200); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "media directory permissions", checkID: CheckMediaDirectory, status: StatusFail, overall: OverallUnhealthy, failure: FailureIO,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				if runtime.GOOS == "windows" {
					t.Skip("POSIX permission bits are not authoritative on Windows")
				}
				t.Cleanup(func() { _ = os.Chmod(fixture.current.OriginalsDir, 0o700) })
				if err := os.Chmod(fixture.current.OriginalsDir, 0o500); err != nil {
					t.Fatal(err)
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDoctorFixture(t)
			test.corrupt(t, fixture)
			service, err := New(Options{DataDir: fixture.root})
			if err != nil {
				t.Fatal(err)
			}
			report, err := service.Run(context.Background(), ModeDeep)
			if err != nil {
				t.Fatal(err)
			}
			if err := ValidateReport(report, ModeDeep); err != nil {
				t.Fatal(err)
			}
			check := findCheck(t, report, test.checkID)
			if check.Status != test.status || report.Status != test.overall || report.Failure() != test.failure {
				t.Fatalf("check=%#v overall=%s failure=%s", check, report.Status, report.Failure())
			}
			encoded := fmt.Sprintf("%#v", report)
			for _, secret := range []string{"CANARY_PRIVATE_POST_BODY", "CANARY_SECRET_NAME", fixture.root} {
				if strings.Contains(encoded, secret) {
					t.Fatalf("report leaked %q: %s", secret, encoded)
				}
			}
		})
	}
}

func TestDeepDoctorClassifiesCorruptPreviewAsDerivedWarning(t *testing.T) {
	fixture := newDoctorFixture(t)
	runtimeState, err := runtimefs.Open(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	store, err := mediastore.NewStore(runtimeState, database, mediastore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	pngBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		t.Fatal(err)
	}
	value, preview, err := store.Ingest(context.Background(), "CANARY_ORIGINAL_NAME.png", bytes.NewReader(pngBytes))
	if err != nil {
		t.Fatal(err)
	}
	if err := database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		if _, err := queries.ExecContext(context.Background(), `INSERT INTO entries
(id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FC0', 'moment', 'preview fixture', 1, 1, 1, 'preview fixture')`); err != nil {
			return err
		}
		if _, err := queries.ExecContext(context.Background(), `INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FC0', ?, 0, NULL)`, value.ID.String()); err != nil {
			return err
		}
		_, err := queries.ExecContext(context.Background(), `UPDATE media SET unreferenced_since_ms = NULL WHERE id = ?`, value.ID.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	writeFixtureFile(t, preview.Path, []byte("CANARY_CORRUPT_PREVIEW_BYTES"), 0o600)
	service, err := New(Options{DataDir: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Run(context.Background(), ModeDeep)
	if err != nil {
		t.Fatal(err)
	}
	previewCheck := findCheck(t, report, CheckPreviewCache)
	if previewCheck.Status != StatusWarning || report.Status != OverallWarning || report.Failure() != FailureNone ||
		findCheck(t, report, CheckMediaChecksum).Status != StatusPass {
		t.Fatalf("preview report = %#v", report)
	}
	if strings.Contains(fmt.Sprintf("%#v", report), "CANARY_") {
		t.Fatalf("preview report leaked content: %#v", report)
	}
}

func TestCancellationRecognizesWrappedAndJoinedErrors(t *testing.T) {
	for _, err := range []error{
		fmt.Errorf("wrapped: %w", context.Canceled),
		errors.Join(errors.New("secondary"), fmt.Errorf("wrapped: %w", context.DeadlineExceeded)),
	} {
		if !cancelled(err) {
			t.Fatalf("wrapped cancellation not recognized: %v", err)
		}
	}
	fixture := newDoctorFixture(t)
	service, err := New(Options{DataDir: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	report, err := service.Run(ctx, ModeDeep)
	if !errors.Is(err, context.Canceled) || report.Status != OverallCancelled || report.Failure() != FailureCancelled {
		t.Fatalf("cancelled report=%#v err=%v", report, err)
	}
	if validateErr := ValidateReport(report, ModeDeep); validateErr != nil {
		t.Fatal(validateErr)
	}
}

func TestFailurePriorityDoesNotDependOnTraversalOrder(t *testing.T) {
	checks := []Check{
		failed(CheckDataDir, "io", "repair", nil, failureIO),
		failed(CheckSchemaVersion, "integrity", "repair", nil, failureIntegrity),
		failed(CheckInstanceLocks, "unavailable", "repair", nil, failureUnavailable),
	}
	first := Report{Checks: append([]Check(nil), checks...)}
	second := Report{Checks: []Check{checks[2], checks[0], checks[1]}}
	if first.Failure() != FailureIntegrity || second.Failure() != FailureIntegrity {
		t.Fatalf("priority changed by traversal: %s/%s", first.Failure(), second.Failure())
	}
}

func findCheck(t *testing.T, report Report, id string) Check {
	t.Helper()
	for _, check := range report.Checks {
		if check.ID == id {
			return check
		}
	}
	t.Fatalf("missing check %s", id)
	return Check{}
}

func mutateDatabase(t *testing.T, path, statement string, arguments ...any) {
	t.Helper()
	database, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.ExecContext(context.Background(), statement, arguments...); err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
}

func seedMediaReferenceMismatch(t *testing.T, fixture doctorFixture) {
	t.Helper()
	runtimeState, err := runtimefs.Open(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	pngBytes, err := base64.StdEncoding.DecodeString("iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII=")
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	store, err := mediastore.NewStore(runtimeState, database, mediastore.Options{})
	if err != nil {
		_ = database.Close()
		t.Fatal(err)
	}
	value, _, err := store.Ingest(context.Background(), "CANARY_REFERENCE_NAME.png", bytes.NewReader(pngBytes))
	if err == nil {
		err = database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
			_, writeErr := queries.ExecContext(context.Background(), `UPDATE media SET unreferenced_since_ms = NULL WHERE id = ?`, value.ID.String())
			return writeErr
		})
	}
	closeErr := database.Close()
	if err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
}

func writeFixtureFile(t *testing.T, path string, contents []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatal(err)
	}
}

type generationFingerprint struct {
	Mode     fs.FileMode
	Size     int64
	Modified int64
	Digest   [sha256.Size]byte
}

func generationSnapshot(t *testing.T, root string) map[string]generationFingerprint {
	t.Helper()
	result := make(map[string]generationFingerprint)
	err := filepath.Walk(root, func(path string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		fingerprint := generationFingerprint{Mode: info.Mode(), Size: info.Size(), Modified: info.ModTime().UnixNano()}
		if info.Mode().IsRegular() {
			contents, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			fingerprint.Digest = sha256.Sum256(contents)
		}
		result[filepath.ToSlash(relative)] = fingerprint
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return result
}
