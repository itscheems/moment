package archive

import (
	"archive/zip"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestInspectBuildsRestartableCurrentSchemaWithoutTouchingCurrent(t *testing.T) {
	fixture := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, fixture)
	currentBefore, err := os.ReadFile(filepath.Join(fixture.runtime.Root, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 11, 1, 2, 3, 0, time.UTC)
	inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
	result, err := inspector.InspectFile(context.Background(), archivePath, 7)
	if err != nil {
		t.Fatal(err)
	}
	if result.FormatVersion != 1 || result.Counts != (ImportCounts{Entries: 1, Comments: 2, Media: 2}) || result.MissingAlt != 1 || result.StagingToken == "" {
		t.Fatalf("inspection = %#v", result)
	}
	if !result.Disk.SpaceOK || result.Disk.RequiredAdditionalBytes <= 0 || result.Disk.SafetyMarginBytes < minimumSafetyMarginBytes {
		t.Fatalf("disk = %#v", result.Disk)
	}
	currentAfter, err := os.ReadFile(filepath.Join(fixture.runtime.Root, "CURRENT"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(currentBefore, currentAfter) {
		t.Fatalf("CURRENT changed: before=%q after=%q", currentBefore, currentAfter)
	}
	stageRoot := filepath.Join(fixture.runtime.Root, "staging", "import", result.GenerationID)
	metadataBytes, err := os.ReadFile(filepath.Join(stageRoot, "staging.json"))
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(metadataBytes, []byte(result.StagingToken)) {
		t.Fatal("staging metadata contains plaintext token")
	}
	stagedGeneration := stagingGeneration(stageRoot, result.GenerationID)
	stagedRuntime := &runtimefs.Runtime{Root: fixture.runtime.Root, Instance: fixture.runtime.Instance, Current: stagedGeneration, Locks: fixture.runtime.Locks}
	stagedDatabase, err := sqlitestore.Open(context.Background(), stagedRuntime, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	stagedSnapshot, err := stagedDatabase.BeginArchiveSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var stagedEntries []sqlitestore.ArchiveEntry
	if err := stagedSnapshot.Entries(context.Background(), func(value sqlitestore.ArchiveEntry) error {
		stagedEntries = append(stagedEntries, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(stagedEntries) != 1 || len(stagedEntries[0].Media) != 3 || stagedEntries[0].Media[0].AltText != nil ||
		stagedEntries[0].Media[1].AltText == nil || *stagedEntries[0].Media[1].AltText != "" ||
		stagedEntries[0].Media[2].AltText == nil || *stagedEntries[0].Media[2].AltText != "街景" {
		t.Fatalf("staged entries = %#v", stagedEntries)
	}
	if err := stagedSnapshot.Close(); err != nil {
		t.Fatal(err)
	}
	if err := stagedDatabase.ReadTx(context.Background(), func(queries sqlitestore.Queries) error {
		var normalized string
		var matches int
		if err := queries.QueryRowContext(context.Background(), `SELECT content_text FROM entries WHERE id = ?`, entryID).Scan(&normalized); err != nil {
			return err
		}
		if err := queries.QueryRowContext(context.Background(), `SELECT count(*) FROM entry_fts WHERE entry_fts MATCH ?`, "Unicode").Scan(&matches); err != nil {
			return err
		}
		if normalized != "unicode 世界" || matches != 1 {
			return errors.New("staged normalized text or FTS query result is invalid")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := stagedDatabase.Close(); err != nil {
		t.Fatal(err)
	}

	restarted := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
	resumed, err := restarted.Resume(context.Background(), result.StagingToken, 7)
	if err != nil {
		t.Fatal(err)
	}
	if resumed.GenerationID != result.GenerationID || resumed.StagingToken != "" || resumed.Counts != result.Counts {
		t.Fatalf("resumed = %#v", resumed)
	}
	type claimResult struct {
		claim StagingClaim
		err   error
	}
	start := make(chan struct{})
	claims := make(chan claimResult, 2)
	for range 2 {
		go func() {
			<-start
			claim, err := restarted.Claim(context.Background(), result.StagingToken, 7)
			claims <- claimResult{claim: claim, err: err}
		}()
	}
	close(start)
	first, second := <-claims, <-claims
	var claim StagingClaim
	switch {
	case first.err == nil && domain.HasCode(second.err, domain.CodeImportStagingInvalid):
		claim = first.claim
	case second.err == nil && domain.HasCode(first.err, domain.CodeImportStagingInvalid):
		claim = second.claim
	default:
		t.Fatalf("concurrent claims = %#v, %#v", first, second)
	}
	if claim.GenerationID != result.GenerationID || claim.Generation.Root != filepath.Join(stageRoot, "generation") {
		t.Fatalf("claim = %#v", claim)
	}
	if _, err := restarted.Claim(context.Background(), result.StagingToken, 7); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
		t.Fatalf("replay error = %v", err)
	}
}

func TestInspectDiscardsReadyStageFromRevokedOwnerEpoch(t *testing.T) {
	fixture := newExportFixture(t, false)
	archivePath := exportFixtureFile(t, fixture)
	now := time.Date(2026, 8, 11, 1, 20, 0, 0, time.UTC)
	inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
	inspector.random = bytes.NewReader(append(bytes.Repeat([]byte{7}, 40), bytes.Repeat([]byte{8}, 40)...))
	first, err := inspector.InspectFile(context.Background(), archivePath, 1)
	if err != nil {
		t.Fatal(err)
	}
	second, err := inspector.InspectFile(context.Background(), archivePath, 2)
	if err != nil {
		t.Fatal(err)
	}
	if first.GenerationID == second.GenerationID || second.StagingToken == "" {
		t.Fatalf("revoked stage was reused: first=%#v second=%#v", first, second)
	}
	if _, err := os.Stat(filepath.Join(fixture.runtime.Root, "staging", "import", first.GenerationID)); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("revoked staging directory still exists: %v", err)
	}
}

func TestInspectOfflineSnapshotLeavesActiveGenerationByteForByteUntouched(t *testing.T) {
	source := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, source)
	target, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	targetDatabase, err := sqlitestore.Open(context.Background(), target, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := targetDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	for _, suffix := range []string{"-journal", "-shm", "-wal"} {
		if _, err := os.Lstat(target.Current.DatabasePath + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("offline target has sidecar %s before inspect: %v", suffix, err)
		}
	}
	before := fingerprintGeneration(t, target.Current.Root)
	now := time.Date(2026, 8, 11, 1, 30, 0, 0, time.UTC)
	if _, err := testInspector(t, target.Root, nil, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 1); err != nil {
		t.Fatal(err)
	}
	after := fingerprintGeneration(t, target.Current.Root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("active generation changed during offline inspect:\nbefore=%#v\nafter=%#v", before, after)
	}
	for _, suffix := range []string{"-journal", "-shm", "-wal"} {
		if _, err := os.Lstat(target.Current.DatabasePath + suffix); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("offline inspect created sidecar %s: %v", suffix, err)
		}
	}
}

func TestInspectOfflineSnapshotIncludesUncleanWALWithoutTouchingActive(t *testing.T) {
	if os.Getenv("MOMENT_IMPORT_WAL_HELPER") == "1" {
		runUncleanImportWALWriter()
		return
	}
	source := newExportFixture(t, true)
	archivePath := exportFixtureFile(t, source)
	target, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	targetDatabase, err := sqlitestore.Open(context.Background(), target, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := targetDatabase.Close(); err != nil {
		t.Fatal(err)
	}
	command := exec.Command(os.Args[0], "-test.run=^TestInspectOfflineSnapshotIncludesUncleanWALWithoutTouchingActive$")
	command.Env = append(os.Environ(), "MOMENT_IMPORT_WAL_HELPER=1", "MOMENT_IMPORT_WAL_DATA_DIR="+target.Root)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unclean WAL writer failed: %v\n%s", err, output)
	}
	walInfo, err := os.Stat(target.Current.DatabasePath + "-wal")
	if err != nil || walInfo.Size() == 0 {
		t.Fatalf("unclean writer did not leave a non-empty WAL: %v", err)
	}
	before := fingerprintGeneration(t, target.Current.Root)
	now := time.Date(2026, 8, 11, 1, 31, 0, 0, time.UTC)
	result, err := testInspector(t, target.Root, nil, &now, math.MaxUint64).InspectFile(context.Background(), archivePath, 1)
	if err != nil {
		t.Fatal(err)
	}
	if result.Replacement.Counts.Entries != 1 {
		t.Fatalf("replacement entries = %d, want committed WAL entry", result.Replacement.Counts.Entries)
	}
	after := fingerprintGeneration(t, target.Current.Root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("active generation changed while reading unclean WAL:\nbefore=%#v\nafter=%#v", before, after)
	}
}

func runUncleanImportWALWriter() {
	ctx := context.Background()
	runtimeState, err := runtimefs.Open(os.Getenv("MOMENT_IMPORT_WAL_DATA_DIR"))
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
		_, err := queries.ExecContext(ctx, `INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms) VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FC0', 'moment', 'committed only in WAL', 1, 1, 1)`)
		return err
	})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

type generationFingerprint struct {
	Mode      os.FileMode
	Size      int64
	Modified  int64
	Digest    [sha256.Size]byte
	IsRegular bool
}

func fingerprintGeneration(t *testing.T, root string) map[string]generationFingerprint {
	t.Helper()
	result := make(map[string]generationFingerprint)
	err := filepath.Walk(root, func(name string, info os.FileInfo, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, name)
		if err != nil {
			return err
		}
		fingerprint := generationFingerprint{Mode: info.Mode(), Size: info.Size(), Modified: info.ModTime().UnixNano(), IsRegular: info.Mode().IsRegular()}
		if fingerprint.IsRegular {
			contents, err := os.ReadFile(name)
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

func TestInspectRejectsMismatchedExpiredAndCorruptRestartState(t *testing.T) {
	t.Run("owner epoch mismatch", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
		inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
		result, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), 3)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.Resume(context.Background(), result.StagingToken, 4); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
			t.Fatalf("mismatch error = %v", err)
		}
	})

	t.Run("expired", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
		inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
		result, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), 3)
		if err != nil {
			t.Fatal(err)
		}
		now = now.Add(defaultInspectionTTL)
		if _, err := inspector.Resume(context.Background(), result.StagingToken, 3); !domain.HasCode(err, domain.CodeImportStagingExpired) {
			t.Fatalf("expiry error = %v", err)
		}
	})

	t.Run("corrupt metadata", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 2, 0, 0, 0, time.UTC)
		inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
		result, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), 3)
		if err != nil {
			t.Fatal(err)
		}
		metadataPath := filepath.Join(fixture.runtime.Root, "staging", "import", result.GenerationID, "staging.json")
		if err := os.WriteFile(metadataPath, []byte("{broken\n"), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.Resume(context.Background(), result.StagingToken, 3); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
			t.Fatalf("corrupt error = %v", err)
		}
	})
}

func TestInspectRejectsHostileArchivesBeforeReadyToken(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*testing.T, map[string][]byte, *[]string)
		code   domain.Code
	}{
		{name: "traversal", mutate: func(_ *testing.T, members map[string][]byte, order *[]string) {
			members["../escape"] = []byte("bad")
			*order = append(*order, "../escape")
		}, code: domain.CodeExportInvalid},
		{name: "absolute", mutate: func(_ *testing.T, members map[string][]byte, order *[]string) {
			members["/absolute"] = []byte("bad")
			*order = append(*order, "/absolute")
		}, code: domain.CodeExportInvalid},
		{name: "windows volume", mutate: func(_ *testing.T, members map[string][]byte, order *[]string) {
			members["C:/escape"] = []byte("bad")
			*order = append(*order, "C:/escape")
		}, code: domain.CodeExportInvalid},
		{name: "backslash traversal", mutate: func(_ *testing.T, members map[string][]byte, order *[]string) {
			members[`..\escape`] = []byte("bad")
			*order = append(*order, `..\escape`)
		}, code: domain.CodeExportInvalid},
		{name: "unknown version", mutate: func(t *testing.T, members map[string][]byte, _ *[]string) {
			var value map[string]any
			if err := json.Unmarshal(members["manifest.json"], &value); err != nil {
				t.Fatal(err)
			}
			value["format_version"] = float64(99)
			members["manifest.json"] = append(mustMarshalTest(t, value), '\n')
			rebuildManifestExpanded(t, members)
			rebuildChecksums(members)
		}, code: domain.CodeExportVersionUnsupported},
		{name: "bad checksum", mutate: func(_ *testing.T, members map[string][]byte, _ *[]string) {
			members["checksums.sha256"] = append([]byte(nil), members["checksums.sha256"]...)
			if members["checksums.sha256"][0] == '0' {
				members["checksums.sha256"][0] = '1'
			} else {
				members["checksums.sha256"][0] = '0'
			}
		}, code: domain.CodeExportChecksumMismatch},
		{name: "malformed jsonl", mutate: func(t *testing.T, members map[string][]byte, _ *[]string) {
			members["data/entries.jsonl"] = []byte("{broken}\n")
			rebuildManifestExpanded(t, members)
			rebuildChecksums(members)
		}, code: domain.CodeExportInvalid},
		{name: "invalid media bytes", mutate: func(t *testing.T, members map[string][]byte, _ *[]string) {
			for name := range members {
				if strings.HasPrefix(name, "media/") {
					members[name] = []byte("not an image")
					break
				}
			}
			rebuildManifestExpanded(t, members)
			rebuildChecksums(members)
		}, code: domain.CodeExportInvalid},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := newExportFixture(t, true)
			valid := exportFixtureBytes(t, fixture)
			members, order := readArchive(t, valid)
			test.mutate(t, members, &order)
			name := filepath.Join(t.TempDir(), "hostile.zip")
			writeArchive(t, name, members, order)
			now := time.Date(2026, 8, 11, 3, 0, 0, 0, time.UTC)
			inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
			if _, err := inspector.InspectFile(context.Background(), name, 1); !domain.HasCode(err, test.code) {
				t.Fatalf("error = %v, want %s", err, test.code)
			}
			assertNoReadyStaging(t, fixture.runtime.Root)
		})
	}
}

func TestInspectRejectsDuplicateSymlinkRatioQuotaDiskAndCancellation(t *testing.T) {
	t.Run("duplicate", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		members, order := readArchive(t, exportFixtureBytes(t, fixture))
		name := filepath.Join(t.TempDir(), "duplicate.zip")
		writeArchiveHeaders(t, name, append(order, "README.txt"), members, nil)
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		if _, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), name, 1); !domain.HasCode(err, domain.CodeExportInvalid) {
			t.Fatalf("duplicate error = %v", err)
		}
	})

	t.Run("symlink", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		members, order := readArchive(t, exportFixtureBytes(t, fixture))
		name := filepath.Join(t.TempDir(), "symlink.zip")
		writeArchiveHeaders(t, name, order, members, map[string]os.FileMode{"README.txt": os.ModeSymlink | 0o777})
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		if _, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(context.Background(), name, 1); !domain.HasCode(err, domain.CodeExportInvalid) {
			t.Fatalf("symlink error = %v", err)
		}
	})

	t.Run("compression ratio", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		members, order := readArchive(t, exportFixtureBytes(t, fixture))
		members["README.txt"] = bytes.Repeat([]byte{'A'}, 1<<20)
		name := filepath.Join(t.TempDir(), "ratio.zip")
		writeArchive(t, name, members, order)
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		inspector, err := NewInspector(InspectOptions{DataDir: fixture.runtime.Root, ActiveDatabase: fixture.database, Clock: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{9}, 128)), AvailableBytes: func(string) (uint64, error) { return math.MaxUint64, nil }, Limits: InspectLimits{MaxCompression: 2}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectFile(context.Background(), name, 1); !domain.HasCode(err, domain.CodeExportInvalid) {
			t.Fatalf("ratio error = %v", err)
		}
	})

	t.Run("manifest quota", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		members, order := readArchive(t, exportFixtureBytes(t, fixture))
		setManifestCount(t, members, "entries", 2)
		rebuildManifestExpanded(t, members)
		rebuildChecksums(members)
		name := filepath.Join(t.TempDir(), "quota.zip")
		writeArchive(t, name, members, order)
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		inspector, err := NewInspector(InspectOptions{DataDir: fixture.runtime.Root, ActiveDatabase: fixture.database, Clock: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{8}, 128)), AvailableBytes: func(string) (uint64, error) { return math.MaxUint64, nil }, Limits: InspectLimits{MaxEntries: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectFile(context.Background(), name, 1); !domain.HasCode(err, domain.CodeExportInvalid) {
			t.Fatalf("quota error = %v", err)
		}
	})

	t.Run("compressed size quota", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		inspector, err := NewInspector(InspectOptions{DataDir: fixture.runtime.Root, ActiveDatabase: fixture.database, Clock: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{8}, 128)), AvailableBytes: func(string) (uint64, error) { return math.MaxUint64, nil }, Limits: InspectLimits{MaxArchiveBytes: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), 1); !domain.HasCode(err, domain.CodeExportInvalid) {
			t.Fatalf("compressed size error = %v", err)
		}
	})

	t.Run("expanded size quota", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		inspector, err := NewInspector(InspectOptions{DataDir: fixture.runtime.Root, ActiveDatabase: fixture.database, Clock: func() time.Time { return now }, Random: bytes.NewReader(bytes.Repeat([]byte{8}, 128)), AvailableBytes: func(string) (uint64, error) { return math.MaxUint64, nil }, Limits: InspectLimits{MaxExpandedBytes: 1}})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), 1); !domain.HasCode(err, domain.CodeExportInvalid) {
			t.Fatalf("expanded size error = %v", err)
		}
	})

	t.Run("disk", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, 0)
		_, err := inspector.InspectFile(context.Background(), exportFixtureFile(t, fixture), 1)
		if !domain.HasCode(err, domain.CodeImportDiskSpaceInsufficient) {
			t.Fatalf("disk error = %v", err)
		}
		assertNoReadyStaging(t, fixture.runtime.Root)
	})

	t.Run("cancel", func(t *testing.T) {
		fixture := newExportFixture(t, true)
		ctx, cancel := context.WithCancel(context.Background())
		reader := &cancelAfterReader{reader: bytes.NewReader(exportFixtureBytes(t, fixture)), cancel: cancel}
		now := time.Date(2026, 8, 11, 4, 0, 0, 0, time.UTC)
		_, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).Inspect(ctx, reader, 1)
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error = %v", err)
		}
		assertNoReadyStaging(t, fixture.runtime.Root)
	})
}

func testInspector(t *testing.T, dataDir string, database *sqlitestore.Database, now *time.Time, available uint64) *Inspector {
	t.Helper()
	inspector, err := NewInspector(InspectOptions{
		DataDir: dataDir, ActiveDatabase: database, Clock: func() time.Time { return *now },
		Random:         bytes.NewReader(bytes.Repeat([]byte{7}, 256)),
		AvailableBytes: func(string) (uint64, error) { return available, nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	return inspector
}

func exportFixtureBytes(t *testing.T, fixture exportFixture) []byte {
	t.Helper()
	var output bytes.Buffer
	if _, err := fixture.exporter.Export(context.Background(), &output); err != nil {
		t.Fatal(err)
	}
	return output.Bytes()
}

func exportFixtureFile(t *testing.T, fixture exportFixture) string {
	t.Helper()
	name := filepath.Join(t.TempDir(), "portable.zip")
	if err := os.WriteFile(name, exportFixtureBytes(t, fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	return name
}

func assertNoReadyStaging(t *testing.T, root string) {
	t.Helper()
	entries, err := os.ReadDir(filepath.Join(root, "staging", "import"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		metadata, err := readStagingMetadata(filepath.Join(root, "staging", "import", entry.Name()))
		if err == nil && metadata.State == stagingReady {
			t.Fatalf("unexpected ready staging %s", entry.Name())
		}
	}
}

func writeArchiveHeaders(t *testing.T, name string, order []string, members map[string][]byte, modes map[string]os.FileMode) {
	t.Helper()
	file, err := os.Create(name)
	if err != nil {
		t.Fatal(err)
	}
	writer := zip.NewWriter(file)
	for _, memberName := range order {
		header := &zip.FileHeader{Name: memberName, Method: zip.Deflate}
		if mode, exists := modes[memberName]; exists {
			header.SetMode(mode)
		} else {
			header.SetMode(0o644)
		}
		member, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := member.Write(members[memberName]); err != nil {
			t.Fatal(err)
		}
	}
	if err := errors.Join(writer.Close(), file.Close()); err != nil {
		t.Fatal(err)
	}
}

func TestFinishClaimRejectsLeaseFromAnotherInstance(t *testing.T) {
	first, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	second, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	inspector, err := NewInspector(InspectOptions{DataDir: second.Root})
	if err != nil {
		t.Fatal(err)
	}
	held, err := first.Locks.AcquireSet(context.Background(), runtimefs.LockRequest{Name: runtimefs.ImportLock, Mode: runtimefs.Exclusive})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = held.Release() }()
	id := "import-20260811T000000000Z-0011223344556677"
	claim := StagingClaim{GenerationID: id, StageRoot: filepath.Join(second.Root, "staging", "import", id)}
	if err := inspector.FinishClaim(claim, false, held); !errors.Is(err, runtimefs.ErrInsufficientLocks) {
		t.Fatalf("cross-instance claim finalization error=%v", err)
	}
}

type cancelAfterReader struct {
	reader io.Reader
	cancel context.CancelFunc
	done   bool
}

func (reader *cancelAfterReader) Read(data []byte) (int, error) {
	read, err := reader.reader.Read(data)
	if !reader.done {
		reader.done = true
		reader.cancel()
	}
	return read, err
}
