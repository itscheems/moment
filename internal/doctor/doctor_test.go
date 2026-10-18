package doctor

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/archive"
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
	repo, err := sqlitestore.NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(repo, app.WithAccountIDGenerator(func() (string, error) { return "0123456789abcdef0123456789abcdef", nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.InitializeSiteAccount(context.Background(), "writer"); err != nil {
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
	if len(ids) != 23 {
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

func TestSummaryReportIsTransportIndependent(t *testing.T) {
	fixture := newDoctorFixture(t)
	runtimeState, err := runtimefs.Open(fixture.root)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	serve, err := runtimeState.Locks.Acquire(context.Background(), runtimefs.ServeLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = serve.Release() }()
	direct, err := New(Options{DataDir: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	api, err := New(Options{DataDir: fixture.root, ActiveDatabase: database})
	if err != nil {
		t.Fatal(err)
	}
	directReport, err := direct.Run(context.Background(), ModeSummary)
	if err != nil {
		t.Fatal(err)
	}
	apiReport, err := api.Run(context.Background(), ModeSummary)
	if err != nil {
		t.Fatal(err)
	}
	normalizeVolatileSummaryEvidence(&directReport)
	normalizeVolatileSummaryEvidence(&apiReport)
	if !reflect.DeepEqual(directReport, apiReport) {
		t.Fatalf("summary transport drift\ndirect=%#v\napi=%#v", directReport, apiReport)
	}
}

func normalizeVolatileSummaryEvidence(report *Report) {
	for index := range report.Checks {
		if report.Checks[index].ID == CheckDiskSpace {
			report.Checks[index].Evidence["available_bytes"] = uint64(0)
		}
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
	diagnosticTemp := t.TempDir()
	t.Setenv("TMPDIR", diagnosticTemp)
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
	assertDirectoryEmpty(t, diagnosticTemp)
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
	diagnosticTemp := t.TempDir()
	t.Setenv("TMPDIR", diagnosticTemp)
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
	assertDirectoryEmpty(t, diagnosticTemp)
}

func TestDeepDoctorTreatsRealEarlyImportFailuresAsTerminalWarnings(t *testing.T) {
	tests := []struct {
		name   string
		source io.Reader
	}{
		{name: "copy failure leaves empty hashes", source: doctorFailReader{}},
		{name: "preflight failure leaves archive hash only", source: strings.NewReader("not a portable zip")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newDoctorFixture(t)
			inspector, err := archive.NewInspector(archive.InspectOptions{DataDir: fixture.root})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := inspector.Inspect(context.Background(), test.source, archiveTestAuthorization(1)); err == nil {
				t.Fatal("early import inspection unexpectedly succeeded")
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
			staging := findCheck(t, report, CheckStaleImportStaging)
			if report.Status != OverallWarning || report.Failure() != FailureNone || staging.Status != StatusWarning ||
				staging.Evidence["failed"] != 1 || staging.Evidence["invalid"] != 0 {
				t.Fatalf("failed staging report = %#v", report)
			}
		})
	}
}

type doctorFailReader struct{}

func (doctorFailReader) Read([]byte) (int, error) {
	return 0, errors.New("injected import copy failure")
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
(id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text, author_user_id)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FC0', 'moment', 'wal only committed content', 1, 1, 1, 'wal only committed content', '0123456789abcdef0123456789abcdef')`)
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
			name: "oversized CURRENT", checkID: CheckCurrentGeneration, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				writeFixtureFile(t, filepath.Join(fixture.root, "CURRENT"), []byte(strings.Repeat("x", 1<<20)), 0o600)
			},
		},
		{
			name: "oversized instance metadata", checkID: CheckCurrentGeneration, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				writeFixtureFile(t, filepath.Join(fixture.root, "instance.json"), []byte(strings.Repeat("x", 1<<20)), 0o600)
			},
		},
		{
			name: "CURRENT symlink to secret", checkID: CheckCurrentGeneration, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				path := filepath.Join(fixture.root, "CURRENT")
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(filepath.Join(fixture.root, "secrets.json"), path); err != nil {
					t.Skipf("symlinks unavailable: %v", err)
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
			// The projection is a copy of what the body says. A copy that
			// drifts is invisible until a picture is deleted while still
			// shown, or kept forever after being removed.
			name: "article projection drifted from its body", checkID: CheckArticleIntegrity, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				mutateDatabase(t, fixture.current.DatabasePath, `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text, author_user_id)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FD7', 'article', NULL, 1, 1, 1, '标题', '0123456789abcdef0123456789abcdef');
INSERT INTO entry_article (entry_id, title, body_json, body_schema_version, cover_media_id, excerpt)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FD7', '标题',
  '{"schema_version":1,"type":"doc","content":[{"type":"image","attrs":{"media_id":"01ARZ3NDEKTSV4RRFFQ69G5FAY"}}]}',
  1, NULL, '')`)
			},
		},
		{
			name: "article body that no longer parses", checkID: CheckArticleIntegrity, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				mutateDatabase(t, fixture.current.DatabasePath, `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text, author_user_id)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FD8', 'article', NULL, 1, 1, 1, '标题', '0123456789abcdef0123456789abcdef');
INSERT INTO entry_article (entry_id, title, body_json, body_schema_version, cover_media_id, excerpt)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FD8', '标题', '{"schema_version":1,"type":"doc","content":[{"type":"callout"}]}', 1, NULL, '')`)
			},
		},
		{
			name: "dangling pinned post", checkID: CheckSiteConfiguration, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				// Corrupt the row deliberately, then restore the normal guard so
				// the diagnostic must find the bad reference rather than a missing trigger.
				db, err := sql.Open("sqlite", fixture.current.DatabasePath)
				if err != nil {
					t.Fatal(err)
				}
				var guard string
				err = db.QueryRow(`SELECT sql FROM sqlite_master WHERE name='site_pin_update_guard'`).Scan(&guard)
				if closeErr := db.Close(); err != nil || closeErr != nil {
					t.Fatal(errors.Join(err, closeErr))
				}
				mutateDatabase(t, fixture.current.DatabasePath, `PRAGMA foreign_keys=OFF; DROP TRIGGER site_pin_update_guard;
UPDATE site_settings SET pinned_entry_id='01ARZ3NDEKTSV4RRFFQ69G5FAV' WHERE id=1;`+guard)
			},
		},
		{
			name: "FTS source drift", checkID: CheckFTSConsistency, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				mutateDatabase(t, fixture.current.DatabasePath, `INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text, author_user_id)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAV', 'moment', 'CANARY_PRIVATE_POST_BODY', 1, 1, 1, 'wrong derived text', '0123456789abcdef0123456789abcdef')`)
			},
		},
		{
			name: "missing comment media original", checkID: CheckMissingMedia, status: StatusFail, overall: OverallUnhealthy, failure: FailureIntegrity,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				sha := strings.Repeat("a", 64)
				mutateDatabase(t, fixture.current.DatabasePath, `INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text, author_user_id)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAV', 'moment', 'private body', 1, 1, 1, 'private body', '0123456789abcdef0123456789abcdef');
INSERT INTO media (id, sha256, storage_path, original_name, mime_type, byte_size, width, height, created_at_ms, unreferenced_since_ms)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAY', '`+sha+`', 'media/original/aa/`+sha+`.png', 'CANARY_SECRET_NAME.png', 'image/png', 1, 1, 1, 1, NULL);
INSERT INTO comments (id, entry_id, parent_id, author_name, content_md, created_at_ms)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FB0', '01ARZ3NDEKTSV4RRFFQ69G5FAV', NULL, 'Guest', 'private reply', 1);
INSERT INTO comment_media (comment_id, media_id, position, alt_text)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FB0', '01ARZ3NDEKTSV4RRFFQ69G5FAY', 0, NULL)`)
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
				if err := os.Chmod(fixture.current.DatabasePath, 0o400); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "broad instance metadata permissions", checkID: CheckCurrentGeneration, status: StatusFail, overall: OverallUnhealthy, failure: FailureIO,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				if runtime.GOOS == "windows" {
					t.Skip("POSIX permission bits are not authoritative on Windows")
				}
				path := filepath.Join(fixture.root, "instance.json")
				t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
				if err := os.Chmod(path, 0o644); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "broad CURRENT permissions", checkID: CheckCurrentGeneration, status: StatusFail, overall: OverallUnhealthy, failure: FailureIO,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				if runtime.GOOS == "windows" {
					t.Skip("POSIX permission bits are not authoritative on Windows")
				}
				path := filepath.Join(fixture.root, "CURRENT")
				t.Cleanup(func() { _ = os.Chmod(path, 0o600) })
				if err := os.Chmod(path, 0o666); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "generation lacks owner write", checkID: CheckCurrentGeneration, status: StatusFail, overall: OverallUnhealthy, failure: FailureIO,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				if runtime.GOOS == "windows" {
					t.Skip("POSIX permission bits are not authoritative on Windows")
				}
				t.Cleanup(func() { _ = os.Chmod(fixture.current.Root, 0o700) })
				if err := os.Chmod(fixture.current.Root, 0o500); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "generations parent broad permissions", checkID: CheckCurrentGeneration, status: StatusFail, overall: OverallUnhealthy, failure: FailureIO,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				if runtime.GOOS == "windows" {
					t.Skip("POSIX permission bits are not authoritative on Windows")
				}
				path := filepath.Join(fixture.root, "generations")
				t.Cleanup(func() { _ = os.Chmod(path, 0o700) })
				if err := os.Chmod(path, 0o755); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "data directory traverse permissions", checkID: CheckDataDir, status: StatusFail, overall: OverallUnhealthy, failure: FailureIO,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				if runtime.GOOS == "windows" {
					t.Skip("POSIX permission bits are not authoritative on Windows")
				}
				t.Cleanup(func() { _ = os.Chmod(fixture.root, 0o700) })
				if err := os.Chmod(fixture.root, 0o600); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "broad lock permissions", checkID: CheckInstanceLocks, status: StatusFail, overall: OverallUnhealthy, failure: FailureIO,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				if runtime.GOOS == "windows" {
					t.Skip("POSIX permission bits are not authoritative on Windows")
				}
				lockRoot := filepath.Join(fixture.root, "locks")
				lockFile := filepath.Join(lockRoot, "import.lock")
				t.Cleanup(func() {
					_ = os.Chmod(lockRoot, 0o700)
					_ = os.Chmod(lockFile, 0o600)
				})
				if err := os.Chmod(lockRoot, 0o755); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(lockFile, 0o666); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "unreadable original permissions", checkID: CheckMediaChecksum, status: StatusFail, overall: OverallUnhealthy, failure: FailureIO,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				if runtime.GOOS == "windows" {
					t.Skip("POSIX permission bits are not authoritative on Windows")
				}
				original := seedReferencedMedia(t, fixture)
				t.Cleanup(func() { _ = os.Chmod(original, 0o600) })
				if err := os.Chmod(original, 0); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "untraversable original shard", checkID: CheckMediaChecksum, status: StatusFail, overall: OverallUnhealthy, failure: FailureIO,
			corrupt: func(t *testing.T, fixture doctorFixture) {
				if runtime.GOOS == "windows" {
					t.Skip("POSIX permission bits are not authoritative on Windows")
				}
				shard := filepath.Dir(seedReferencedMedia(t, fixture))
				t.Cleanup(func() { _ = os.Chmod(shard, 0o700) })
				if err := os.Chmod(shard, 0o600); err != nil {
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
			if test.checkID == CheckStaleImportStaging && findCheck(t, report, CheckImportRecoveryState).Status != StatusPass {
				t.Fatalf("staging-only corruption contaminated recovery: %#v", report)
			}
			if test.checkID == CheckImportRecoveryState && findCheck(t, report, CheckStaleImportStaging).Status != StatusPass {
				t.Fatalf("recovery-only corruption contaminated staging: %#v", report)
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
(id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text, author_user_id)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FC0', 'moment', 'preview fixture', 1, 1, 1, 'preview fixture', '0123456789abcdef0123456789abcdef')`); err != nil {
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

func TestDeepDoctorTreatsBoundedTerminalRecoveryHistoryAsWarning(t *testing.T) {
	fixture := newDoctorFixture(t)
	statusRoot := filepath.Join(fixture.root, "staging", "import-status")
	now := time.Date(2026, 8, 11, 12, 0, 0, 0, time.UTC)
	latestID := ""
	for index := 0; index < defaultMaxStagingItems+1; index++ {
		id := "import-20260811T120000000Z-" + fmt.Sprintf("%016x", index+1)
		completed := now.Add(time.Duration(index) * time.Second)
		status := archive.CommitStatus{
			OperationID: id, Phase: archive.CommitFailed, ActiveGeneration: fixture.current.ID,
			PreviousGeneration: fixture.current.ID, NewGeneration: id, ErrorCode: "DIAGNOSTIC_FIXTURE",
			StartedAt: completed, UpdatedAt: completed, CompletedAt: &completed,
		}
		data, err := json.Marshal(struct {
			Version int `json:"version"`
			archive.CommitStatus
		}{Version: 1, CommitStatus: status})
		if err != nil {
			t.Fatal(err)
		}
		writeFixtureFile(t, filepath.Join(statusRoot, id+".json"), append(data, '\n'), 0o600)
		latestID = id
	}
	writeFixtureFile(t, filepath.Join(statusRoot, "LATEST"), []byte(latestID+"\n"), 0o600)
	service, err := New(Options{DataDir: fixture.root})
	if err != nil {
		t.Fatal(err)
	}
	report, err := service.Run(context.Background(), ModeDeep)
	if err != nil {
		t.Fatal(err)
	}
	check := findCheck(t, report, CheckImportRecoveryState)
	if check.Status != StatusWarning || report.Status != OverallWarning || report.Failure() != FailureNone || check.Evidence["bounded"] != true {
		t.Fatalf("recovery=%#v report=%#v", check, report)
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

func seedReferencedMedia(t *testing.T, fixture doctorFixture) string {
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
	value, _, err := store.Ingest(context.Background(), "CANARY_PERMISSION_NAME.png", bytes.NewReader(pngBytes))
	if err == nil {
		err = database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
			if _, writeErr := queries.ExecContext(context.Background(), `INSERT INTO entries
(id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text, author_user_id)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FB0', 'moment', 'permission fixture', 1, 1, 1, 'permission fixture', '0123456789abcdef0123456789abcdef')`); writeErr != nil {
				return writeErr
			}
			if _, writeErr := queries.ExecContext(context.Background(), `INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FB0', ?, 0, NULL)`, value.ID.String()); writeErr != nil {
				return writeErr
			}
			_, writeErr := queries.ExecContext(context.Background(), `UPDATE media SET unreferenced_since_ms = NULL WHERE id = ?`, value.ID.String())
			return writeErr
		})
	}
	closeErr := database.Close()
	if err != nil || closeErr != nil {
		t.Fatal(errors.Join(err, closeErr))
	}
	return filepath.Join(fixture.current.Root, filepath.FromSlash(value.StoragePath))
}

func writeFixtureFile(t *testing.T, path string, contents []byte, mode fs.FileMode) {
	t.Helper()
	if err := os.WriteFile(path, contents, mode); err != nil {
		t.Fatal(err)
	}
}

func assertDirectoryEmpty(t *testing.T, path string) {
	t.Helper()
	entries, err := os.ReadDir(path)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("private diagnostic temporary directory was not cleaned: %v", entries)
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

func archiveTestAuthorization(n int) archive.Authorization {
	return archive.Authorization{AccountID: "0123456789abcdef0123456789abcdef", CredentialID: fmt.Sprintf("session:%032x", n)}
}
