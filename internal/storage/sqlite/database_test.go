package sqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/runtimefs"
)

const (
	entryOne    = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	entryTwo    = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	entryThree  = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
	mediaOne    = "01ARZ3NDEKTSV4RRFFQ69G5FAY"
	mediaTwo    = "01ARZ3NDEKTSV4RRFFQ69G5FAZ"
	commentRoot = "01ARZ3NDEKTSV4RRFFQ69G5FB0"
	commentLeaf = "01ARZ3NDEKTSV4RRFFQ69G5FB1"
	commentPeer = "01ARZ3NDEKTSV4RRFFQ69G5FB2"
	commentElse = "01ARZ3NDEKTSV4RRFFQ69G5FB3"
	commentBad  = "01ARZ3NDEKTSV4RRFFQ69G5FB4"
)

func TestOpenMigratesCurrentGenerationAndProbesFTS(t *testing.T) {
	t.Parallel()
	ctx := testAccountContext()
	db, runtimeState := openTestDatabase(t, Options{})

	if db.Path() != runtimeState.Current.DatabasePath {
		t.Fatalf("opened %q instead of current generation %q", db.Path(), runtimeState.Current.DatabasePath)
	}
	if _, err := os.Stat(db.Path()); err != nil {
		t.Fatalf("current generation database was not created: %v", err)
	}
	if err := db.Ready(ctx); err != nil {
		t.Fatalf("database is not ready: %v", err)
	}
	if err := db.ProbeCapabilities(ctx); err != nil {
		t.Fatalf("FTS5 trigram capability probe failed: %v", err)
	}

	expected := []string{
		"comment_location", "comment_media", "comment_pins", "comments", "entries", "entry_fts", "entry_link_card", "entry_location", "entry_media",
		"idempotency_keys", "identities", "media", "schema_migrations", "sessions", "site_settings", "users",
	}
	rows, err := db.read.QueryContext(ctx, `
SELECT name
FROM sqlite_schema
WHERE name IN ('comment_location', 'comment_media', 'comment_pins', 'comments', 'entries', 'entry_fts', 'entry_link_card', 'entry_location', 'entry_media', 'identities', 'idempotency_keys', 'media', 'schema_migrations', 'sessions', 'site_settings', 'users')
ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var actual []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		actual = append(actual, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Fatalf("schema tables = %v, want %v", actual, expected)
	}

	for index, migration := range db.migrations {
		var checksum string
		if err := db.read.QueryRowContext(ctx, `SELECT checksum FROM schema_migrations WHERE version = ?`, index+1).Scan(&checksum); err != nil {
			t.Fatal(err)
		}
		if checksum != migration.checksum {
			t.Fatalf("migration %d checksum = %q, want %q", index+1, checksum, migration.checksum)
		}
	}

	var matches int
	if err := db.WriteTx(ctx, func(queries Queries) error {
		if _, err := queries.ExecContext(ctx, `INSERT INTO entry_fts (entry_id, content_text) VALUES (?, ?)`, entryOne, "remembering a quiet shoreline"); err != nil {
			return err
		}
		return queries.QueryRowContext(ctx, `SELECT count(*) FROM entry_fts WHERE content_text MATCH 'quiet'`).Scan(&matches)
	}); err != nil {
		t.Fatalf("migrated trigram table is not operational: %v", err)
	}
	if matches != 1 {
		t.Fatalf("migrated trigram table returned %d matches, want 1", matches)
	}
}

func TestEveryConnectionHasRequiredPragmasAndWritePoolIsBounded(t *testing.T) {
	t.Parallel()
	ctx := testAccountContext()
	timeout := 1375 * time.Millisecond
	db, _ := openTestDatabase(t, Options{BusyTimeout: timeout, ReadPoolSize: 3})

	if got := db.write.Stats().MaxOpenConnections; got != writePoolSize {
		t.Fatalf("write pool max = %d, want %d", got, writePoolSize)
	}
	if got := db.read.Stats().MaxOpenConnections; got != 3 {
		t.Fatalf("read pool max = %d, want 3", got)
	}
	assertPoolPragmas(ctx, t, db.write, 1, timeout, false)
	assertPoolPragmas(ctx, t, db.read, 3, timeout, true)

	err := db.ReadTx(ctx, func(queries Queries) error {
		_, err := queries.ExecContext(ctx, `CREATE TABLE forbidden_read_write (id INTEGER)`)
		if err == nil {
			t.Fatal("read connection accepted a write")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("read transaction cleanup failed: %v", err)
	}
}

func TestWALAndMigrationsSurviveReopen(t *testing.T) {
	t.Parallel()
	ctx := testAccountContext()
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "first")

	var appliedAt int64
	if err := db.read.QueryRowContext(ctx, `SELECT applied_at_ms FROM schema_migrations WHERE version = 1`).Scan(&appliedAt); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	restartedRuntime, err := runtimefs.Open(runtimeState.Root)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, restartedRuntime, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })

	state, err := readPragmaState(ctx, reopened.read)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.EqualFold(state.journalMode, "wal") {
		t.Fatalf("journal mode after reopen = %q", state.journalMode)
	}
	var content string
	var reappliedAt int64
	if err := reopened.read.QueryRowContext(ctx, `SELECT content_md FROM entries WHERE id = ?`, entryOne).Scan(&content); err != nil {
		t.Fatal(err)
	}
	if err := reopened.read.QueryRowContext(ctx, `SELECT applied_at_ms FROM schema_migrations WHERE version = 1`).Scan(&reappliedAt); err != nil {
		t.Fatal(err)
	}
	if content != "first" || reappliedAt != appliedAt {
		t.Fatalf("reopen changed authority data: content=%q applied_at=%d/%d", content, appliedAt, reappliedAt)
	}
}

func TestWALRecoversAfterUncleanWriterExit(t *testing.T) {
	if os.Getenv("MOMENT_SQLITE_WAL_HELPER") == "1" {
		runUncleanWALWriter()
		return
	}
	t.Parallel()
	ctx := testAccountContext()
	db, runtimeState := openTestDatabase(t, Options{})
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestWALRecoversAfterUncleanWriterExit$")
	command.Env = append(os.Environ(),
		"MOMENT_SQLITE_WAL_HELPER=1",
		"MOMENT_SQLITE_WAL_DATA_DIR="+runtimeState.Root,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("unclean WAL writer failed: %v\n%s", err, output)
	}
	walInfo, err := os.Stat(runtimeState.Current.DatabasePath + "-wal")
	if err != nil {
		t.Fatalf("unclean writer did not leave a WAL for recovery: %v", err)
	}
	if walInfo.Size() == 0 {
		t.Fatal("unclean writer left an empty WAL")
	}

	restartedRuntime, err := runtimefs.Open(runtimeState.Root)
	if err != nil {
		t.Fatal(err)
	}
	recovered, err := Open(ctx, restartedRuntime, Options{})
	if err != nil {
		t.Fatalf("recover WAL on reopen: %v", err)
	}
	t.Cleanup(func() { _ = recovered.Close() })
	assertCount(t, recovered.read, `SELECT count(*) FROM entries WHERE id = ?`, 1, entryThree)
}

func runUncleanWALWriter() {
	ctx := testAccountContext()
	runtimeState, err := runtimefs.Open(os.Getenv("MOMENT_SQLITE_WAL_DATA_DIR"))
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	db, err := Open(ctx, runtimeState, Options{})
	if err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if _, err := db.write.ExecContext(ctx, `PRAGMA wal_autocheckpoint = 0`); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	if err := db.WriteTx(ctx, func(queries Queries) error {
		_, err := queries.ExecContext(ctx, entryInsertSQL, entryThree, "moment", "from WAL", int64(1), int64(1), int64(1))
		return err
	}); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}
	os.Exit(0)
}

func TestTransactionsCommitRollbackAndRequireReadiness(t *testing.T) {
	t.Parallel()
	ctx := testAccountContext()
	db, runtimeState := openTestDatabase(t, Options{})
	injected := errors.New("rollback requested")
	err := db.WriteTx(ctx, func(queries Queries) error {
		if _, err := queries.ExecContext(ctx, entryInsertSQL, entryOne, "moment", "rolled back", int64(1), int64(1), int64(1)); err != nil {
			return err
		}
		return injected
	})
	if !errors.Is(err, injected) {
		t.Fatalf("write transaction error = %v", err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM entries WHERE id = ?`, 0, entryOne)

	insertEntry(t, db, entryOne, "committed")
	assertCount(t, db.read, `SELECT count(*) FROM entries WHERE id = ?`, 1, entryOne)

	unready, err := connect(runtimeState.Current.DatabasePath, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = unready.Close() })
	err = unready.WriteTx(ctx, func(Queries) error { return nil })
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("uninitialized database transaction error = %v", err)
	}
}

func TestMigrationChecksumDriftAndNewerSchemaFailReadiness(t *testing.T) {
	t.Run("checksum drift", func(t *testing.T) {
		ctx := testAccountContext()
		db, runtimeState := openTestDatabase(t, Options{})
		if _, err := db.write.ExecContext(ctx, `UPDATE schema_migrations SET checksum = ? WHERE version = 1`, strings.Repeat("0", 64)); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		restarted, err := runtimefs.Open(runtimeState.Root)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Open(ctx, restarted, Options{})
		if !errors.Is(err, ErrNotReady) || !errors.Is(err, ErrMigrationDrift) {
			t.Fatalf("checksum drift error = %v", err)
		}
	})

	t.Run("schema too new", func(t *testing.T) {
		ctx := testAccountContext()
		db, runtimeState := openTestDatabase(t, Options{})
		if _, err := db.write.ExecContext(ctx, `
INSERT INTO schema_migrations (version, name, applied_at_ms, checksum)
VALUES (999, 'future', 1, ?)`, strings.Repeat("f", 64)); err != nil {
			t.Fatal(err)
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		restarted, err := runtimefs.Open(runtimeState.Root)
		if err != nil {
			t.Fatal(err)
		}
		_, err = Open(ctx, restarted, Options{})
		if !errors.Is(err, ErrNotReady) || !errors.Is(err, ErrSchemaTooNew) {
			t.Fatalf("future schema error = %v", err)
		}
	})
}

func TestSchemaCatalogTamperingFailsReadiness(t *testing.T) {
	for _, testCase := range []struct {
		name      string
		statement string
	}{
		{name: "authority table", statement: `DROP TABLE entries`},
		{name: "FTS virtual table", statement: `DROP TABLE entry_fts`},
		{name: "comment insert trigger", statement: `DROP TRIGGER comments_parent_insert_guard`},
		{name: "comment update trigger", statement: `DROP TRIGGER comments_parent_update_guard`},
		{name: "media path insert trigger", statement: `DROP TRIGGER media_storage_path_insert_guard`},
		{name: "media path update trigger", statement: `DROP TRIGGER media_storage_path_update_guard`},
		{name: "required index", statement: `DROP INDEX entries_timeline_idx`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := testAccountContext()
			db, runtimeState := openTestDatabase(t, Options{})
			if _, err := db.write.ExecContext(ctx, testCase.statement); err != nil {
				t.Fatal(err)
			}
			if err := db.Ready(ctx); !errors.Is(err, ErrNotReady) || !errors.Is(err, ErrSchemaIncomplete) {
				t.Fatalf("live tampered schema readiness error = %v", err)
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			restarted, err := runtimefs.Open(runtimeState.Root)
			if err != nil {
				t.Fatal(err)
			}
			_, err = Open(ctx, restarted, Options{})
			if !errors.Is(err, ErrNotReady) || !errors.Is(err, ErrSchemaIncomplete) {
				t.Fatalf("tampered schema was reported ready: %v", err)
			}
		})
	}
}

func TestFailedMigrationBatchRollsBackAndLeavesDatabaseNotReady(t *testing.T) {
	t.Parallel()
	ctx := testAccountContext()
	db, _ := openTestDatabase(t, Options{})
	sqlText := `
CREATE TABLE migration_must_rollback (id INTEGER);
THIS IS NOT VALID SQL;`
	digest := sha256.Sum256([]byte(sqlText))
	db.migrations = append(db.migrations, migration{
		version:  len(db.migrations) + 1,
		name:     "injected_failure",
		sql:      sqlText,
		checksum: hex.EncodeToString(digest[:]),
	})

	err := db.bootstrap(ctx)
	if !errors.Is(err, ErrNotReady) {
		t.Fatalf("failed migration bootstrap error = %v", err)
	}
	if err := db.Ready(ctx); !errors.Is(err, ErrNotReady) {
		t.Fatalf("failed migration readiness error = %v", err)
	}
	assertCount(t, db.write, `SELECT count(*) FROM sqlite_schema WHERE name = 'migration_must_rollback'`, 0)
	assertCount(t, db.write, `SELECT count(*) FROM schema_migrations`, len(db.migrations)-1)
}

func TestSchemaEnforcesAuthorityConstraints(t *testing.T) {
	t.Parallel()
	ctx := testAccountContext()
	db, _ := openTestDatabase(t, Options{})

	insertEntry(t, db, entryOne, "one")
	insertEntry(t, db, entryTwo, "two")
	mustWrite(t, db, `
INSERT INTO media (id, sha256, storage_path, original_name, mime_type, byte_size, width, height, created_at_ms, unreferenced_since_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`, mediaOne, strings.Repeat("a", 64), "media/original/aa/"+strings.Repeat("a", 64)+".jpg", "a.jpg", "image/jpeg", 100, 10, 10, 1)
	mustWrite(t, db, `
INSERT INTO media (id, sha256, storage_path, original_name, mime_type, byte_size, width, height, created_at_ms, unreferenced_since_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`, mediaTwo, strings.Repeat("b", 64), "media/original/bb/"+strings.Repeat("b", 64)+".png", "b.png", "image/png", 200, 20, 20, 1)

	mustWrite(t, db, `INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 0, NULL)`, entryOne, mediaOne)
	mustWrite(t, db, `INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 1, '')`, entryOne, mediaOne)
	mustWrite(t, db, `INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 2, 'shoreline')`, entryOne, mediaOne)

	rows, err := db.read.QueryContext(ctx, `SELECT alt_text FROM entry_media WHERE entry_id = ? ORDER BY position`, entryOne)
	if err != nil {
		t.Fatal(err)
	}
	var alts []sql.NullString
	for rows.Next() {
		var alt sql.NullString
		if err := rows.Scan(&alt); err != nil {
			t.Fatal(err)
		}
		alts = append(alts, alt)
	}
	if err := rows.Close(); err != nil {
		t.Fatal(err)
	}
	if len(alts) != 3 || alts[0].Valid || !alts[1].Valid || alts[1].String != "" || alts[2].String != "shoreline" {
		t.Fatalf("alt tri-state was not preserved: %+v", alts)
	}

	assertWriteRejected(t, db, `INSERT INTO entries VALUES (?, 'future', '', 1, 1, 1)`, entryThree)
	assertWriteRejected(t, db, `INSERT INTO entries VALUES ('not-a-ulid', 'moment', '', 1, 1, 1)`)
	assertWriteRejected(t, db, `INSERT INTO entries VALUES ('81ARZ3NDEKTSV4RRFFQ69G5FAV', 'moment', '', 1, 1, 1)`)
	assertWriteRejected(t, db, `INSERT INTO entry_media VALUES (?, ?, 3, ?)`, entryOne, mediaOne, " \t\n ")
	assertWriteRejected(t, db, `INSERT INTO entry_media VALUES (?, ?, 3, ?)`, entryOne, mediaOne, "\u3000\u00a0")
	assertWriteRejected(t, db, `INSERT INTO entry_media VALUES (?, ?, 3, ?)`, entryOne, mediaOne, strings.Repeat("界", 1001))
	assertWriteRejected(t, db, `INSERT INTO entry_media VALUES (?, ?, 3, ?)`, entryOne, mediaOne, []byte("not text"))
	// An article body places its own pictures, so the column now stops at 50
	// and the nine a moment may carry is a rule the domain holds
	// (domain.MaxEntryMediaUsages), not this check.
	assertWriteRejected(t, db, `INSERT INTO entry_media VALUES (?, ?, 50, NULL)`, entryOne, mediaOne)
	assertWriteRejected(t, db, `INSERT INTO entry_media VALUES (?, ?, 3, NULL)`, entryOne, entryThree)
	assertWriteRejected(t, db, `
INSERT INTO media VALUES (?, ?, ?, 'bad.bmp', 'image/bmp', 1, 1, 1, 1, NULL)`, entryThree, strings.Repeat("c", 64), "media/original/cc/c.bmp")
	assertWriteRejected(t, db, `
INSERT INTO media VALUES (?, ?, '../escape.jpg', 'escape.jpg', 'image/jpeg', 1, 1, 1, 1, NULL)`, entryThree, strings.Repeat("c", 64))
	for _, invalidPath := range []string{
		".",
		"other/file.jpg",
		"media/original/cc/" + strings.Repeat("c", 64) + ":evil.jpg",
		"media/original/dd/" + strings.Repeat("c", 64) + ".jpg",
		"media/original/cc/" + strings.Repeat("c", 64) + ".png",
	} {
		assertWriteRejected(t, db, `
INSERT INTO media VALUES (?, ?, ?, 'invalid.jpg', 'image/jpeg', 1, 1, 1, 1, NULL)`, entryThree, strings.Repeat("c", 64), invalidPath)
	}

	mustWrite(t, db, `INSERT INTO comments (id, entry_id, parent_id, author_name, content_md, created_at_ms) VALUES (?, ?, NULL, 'Owner', 'root', 1)`, commentRoot, entryOne)
	mustWrite(t, db, `INSERT INTO comments (id, entry_id, parent_id, author_name, content_md, created_at_ms) VALUES (?, ?, ?, 'Guest', 'reply', 2)`, commentLeaf, entryOne, commentRoot)
	mustWrite(t, db, `INSERT INTO comments (id, entry_id, parent_id, author_name, content_md, created_at_ms) VALUES (?, ?, NULL, 'Owner', 'peer', 3)`, commentPeer, entryOne)
	mustWrite(t, db, `INSERT INTO comments (id, entry_id, parent_id, author_name, content_md, created_at_ms) VALUES (?, ?, NULL, 'Else', 'other entry', 4)`, commentElse, entryTwo)
	mustWrite(t, db, `INSERT INTO comment_media VALUES (?, ?, 0, NULL)`, commentRoot, mediaOne)
	mustWrite(t, db, `INSERT INTO comment_media VALUES (?, ?, 1, '')`, commentRoot, mediaOne)
	mustWrite(t, db, `INSERT INTO comment_media VALUES (?, ?, 2, 'shoreline')`, commentRoot, mediaOne)
	assertWriteRejected(t, db, `INSERT INTO comment_media VALUES (?, ?, 3, ?)`, commentRoot, mediaOne, " \t\n ")
	assertWriteRejected(t, db, `INSERT INTO comment_media VALUES (?, ?, 3, ?)`, commentRoot, mediaOne, strings.Repeat("界", 1001))
	assertWriteRejected(t, db, `INSERT INTO comment_media VALUES (?, ?, 4, NULL)`, commentRoot, mediaOne)
	assertWriteRejected(t, db, `INSERT INTO comment_media VALUES (?, ?, 3, NULL)`, commentBad, mediaOne)
	assertWriteRejected(t, db, `INSERT INTO comment_media VALUES (?, ?, 3, NULL)`, commentRoot, entryThree)
	assertWriteRejected(t, db, `INSERT INTO comments (id, entry_id, parent_id, author_name, content_md, created_at_ms) VALUES (?, ?, ?, 'Deep', 'no', 5)`, commentBad, entryOne, commentLeaf)
	assertWriteRejected(t, db, `INSERT INTO comments (id, entry_id, parent_id, author_name, content_md, created_at_ms) VALUES (?, ?, ?, 'Cross', 'no', 5)`, commentBad, entryOne, commentElse)
	assertWriteRejected(t, db, `UPDATE comments SET parent_id = ? WHERE id = ?`, commentPeer, commentRoot)
	assertWriteRejected(t, db, `UPDATE comments SET entry_id = ? WHERE id = ?`, entryTwo, commentRoot)
	assertWriteRejected(t, db, `UPDATE comments SET parent_id = id WHERE id = ?`, commentPeer)
	assertWriteRejected(t, db, `INSERT INTO comments (id, entry_id, parent_id, author_name, author_user_id, content_md, created_at_ms) VALUES (?, ?, NULL, 'Unknown account', 'ffffffffffffffffffffffffffffffff', 'no', 5)`, commentBad, entryOne)

	mustWrite(t, db, `DELETE FROM comments WHERE id = ?`, commentRoot)
	assertCount(t, db.read, `SELECT count(*) FROM comments WHERE id IN (?, ?)`, 0, commentRoot, commentLeaf)
	assertCount(t, db.read, `SELECT count(*) FROM comment_media WHERE comment_id IN (?, ?)`, 0, commentRoot, commentLeaf)
	mustWrite(t, db, `DELETE FROM entries WHERE id = ?`, entryTwo)
	assertCount(t, db.read, `SELECT count(*) FROM comments WHERE id = ?`, 0, commentElse)

	mustWrite(t, db, `
UPDATE site_settings
SET title = 'Moment', timezone = 'Asia/Shanghai', public_base_url = NULL, comments_enabled = 1
WHERE id = 1`)
	// The avatar reference that keeps media pinned lives on the account row now.
	mustWrite(t, db, `
UPDATE users SET avatar_media_id = ?
WHERE id = (SELECT profile_user_id FROM site_settings WHERE id = 1)`, mediaTwo)
	var publicBaseURL sql.NullString
	if err := db.read.QueryRowContext(ctx, `SELECT public_base_url FROM site_settings WHERE id = 1`).Scan(&publicBaseURL); err != nil {
		t.Fatal(err)
	}
	if publicBaseURL.Valid {
		t.Fatalf("unset public base URL was not preserved as NULL: %+v", publicBaseURL)
	}
	mustWrite(t, db, `UPDATE site_settings SET public_base_url = 'https://example.test' WHERE id = 1`)
	if err := db.read.QueryRowContext(ctx, `SELECT public_base_url FROM site_settings WHERE id = 1`).Scan(&publicBaseURL); err != nil {
		t.Fatal(err)
	}
	if !publicBaseURL.Valid || publicBaseURL.String != "https://example.test" {
		t.Fatalf("configured public base URL was not preserved: %+v", publicBaseURL)
	}
	mustWrite(t, db, `UPDATE site_settings SET public_base_url = NULL WHERE id = 1`)
	assertWriteRejected(t, db, `
INSERT INTO site_settings (id, title, timezone, public_base_url, comments_enabled, profile_user_id)
VALUES (2, 'Other', 'UTC', 'https://other.test', 1, (SELECT profile_user_id FROM site_settings WHERE id = 1))`)
	assertWriteRejected(t, db, `UPDATE site_settings SET public_base_url = '' WHERE id = 1`)
	assertWriteRejected(t, db, `UPDATE site_settings SET public_base_url = '   ' WHERE id = 1`)
	assertCount(t, db.read, `SELECT count(*) FROM site_settings`, 1)
	assertWriteRejected(t, db, `UPDATE site_settings SET comments_enabled = 2 WHERE id = 1`)
	assertWriteRejected(t, db, `DELETE FROM media WHERE id = ?`, mediaTwo)

	mustWrite(t, db, `
INSERT INTO idempotency_keys (scope, key, request_hash, response_resource_id, created_at_ms)
VALUES ('posts.create', 'request-1', ?, ?, 1)`, strings.Repeat("d", 64), entryOne)
	assertWriteRejected(t, db, `
INSERT INTO idempotency_keys (scope, key, request_hash, response_resource_id, created_at_ms)
VALUES ('posts.create', 'request-1', ?, ?, 2)`, strings.Repeat("e", 64), entryOne)

	mustWrite(t, db, `DELETE FROM entries WHERE id = ?`, entryOne)
	assertCount(t, db.read, `SELECT count(*) FROM entry_media WHERE entry_id = ?`, 0, entryOne)
	assertCount(t, db.read, `SELECT count(*) FROM media WHERE id = ?`, 1, mediaOne)
}

func TestOptionsAreBounded(t *testing.T) {
	runtimeState, err := runtimefs.Initialize(testAccountContext(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	for _, options := range []Options{
		{BusyTimeout: time.Microsecond},
		{BusyTimeout: maxBusyTimeout + time.Millisecond},
		{ReadPoolSize: -1},
		{ReadPoolSize: maxReadPoolSize + 1},
	} {
		if _, err := Open(testAccountContext(), runtimeState, options); err == nil {
			t.Fatalf("invalid options were accepted: %+v", options)
		}
	}
}

const entryInsertSQL = `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, author_user_id)
VALUES (?, ?, ?, ?, ?, ?, '0123456789abcdef0123456789abcdef')`

func openTestDatabase(t *testing.T, options Options) (*Database, *runtimefs.Runtime) {
	t.Helper()
	runtimeState, err := runtimefs.Initialize(testAccountContext(), runtimefs.Config{
		DataDir:     t.TempDir(),
		Environment: runtimefs.EnvironmentDevelopment,
	})
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(testAccountContext(), runtimeState, options)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	repository, err := NewAccountRepository(db, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(repository, app.WithAccountIDGenerator(func() (string, error) { return testAccountID, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.InitializeSiteAccount(testAccountContext(), "writer"); err != nil {
		t.Fatal(err)
	}
	return db, runtimeState
}

func insertEntry(t *testing.T, db *Database, id, content string) {
	t.Helper()
	mustWrite(t, db, entryInsertSQL, id, "moment", content, int64(1), int64(1), int64(1))
}

func mustWrite(t *testing.T, db *Database, statement string, arguments ...any) {
	t.Helper()
	err := db.WriteTx(testAccountContext(), func(queries Queries) error {
		_, err := queries.ExecContext(testAccountContext(), statement, arguments...)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
}

func assertWriteRejected(t *testing.T, db *Database, statement string, arguments ...any) {
	t.Helper()
	err := db.WriteTx(testAccountContext(), func(queries Queries) error {
		_, err := queries.ExecContext(testAccountContext(), statement, arguments...)
		return err
	})
	if err == nil {
		t.Fatalf("invalid direct SQL was accepted: %s", strings.TrimSpace(statement))
	}
}

type countQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func assertCount(t *testing.T, queryer countQueryer, statement string, expected int, arguments ...any) {
	t.Helper()
	var actual int
	if err := queryer.QueryRowContext(testAccountContext(), statement, arguments...).Scan(&actual); err != nil {
		t.Fatal(err)
	}
	if actual != expected {
		t.Fatalf("count = %d, want %d for %s", actual, expected, strings.TrimSpace(statement))
	}
}

func assertPoolPragmas(ctx context.Context, t *testing.T, pool *sql.DB, count int, timeout time.Duration, readOnly bool) {
	t.Helper()
	connections := make([]*sql.Conn, 0, count)
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	for range count {
		connection, err := pool.Conn(ctx)
		if err != nil {
			t.Fatal(err)
		}
		connections = append(connections, connection)
	}
	for _, connection := range connections {
		state, err := readPragmaState(ctx, connection)
		if err != nil {
			t.Fatal(err)
		}
		queryOnly := 0
		if readOnly {
			queryOnly = 1
		}
		if state.foreignKeys != 1 || state.journalMode != "wal" || state.busyTimeout != timeout.Milliseconds() || state.queryOnly != queryOnly {
			t.Fatalf("invalid connection pragmas: %+v", state)
		}
	}
}

func TestDatabasePathUsesRuntimeGeneration(t *testing.T) {
	t.Parallel()
	dataDir := filepath.Join(t.TempDir(), "data with ? and #")
	runtimeState, err := runtimefs.Initialize(testAccountContext(), runtimefs.Config{DataDir: dataDir})
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(testAccountContext(), runtimeState, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	want := filepath.Join(runtimeState.Current.Root, "moment.db")
	if db.Path() != want {
		t.Fatalf("database path = %q, want %q", db.Path(), want)
	}
}
