package sqlite

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMigrationChecksumCanonicalizesLineEndings(t *testing.T) {
	lf := "CREATE TABLE example (id INTEGER);\n-- next statement\nSELECT 1;\n"
	wantSQL, wantChecksum := canonicalMigration([]byte(lf))

	for name, contents := range map[string]string{
		"LF":      lf,
		"CRLF":    "CREATE TABLE example (id INTEGER);\r\n-- next statement\r\nSELECT 1;\r\n",
		"lone CR": "CREATE TABLE example (id INTEGER);\r-- next statement\rSELECT 1;\r",
		"mixed":   "CREATE TABLE example (id INTEGER);\r\n-- next statement\rSELECT 1;\n",
	} {
		t.Run(name, func(t *testing.T) {
			actualSQL, actualChecksum := canonicalMigration([]byte(contents))
			if actualSQL != wantSQL {
				t.Fatalf("canonical SQL = %q, want %q", actualSQL, wantSQL)
			}
			if actualChecksum != wantChecksum {
				t.Fatalf("checksum = %q, want %q", actualChecksum, wantChecksum)
			}
		})
	}
}

// Migrations run with foreign keys disabled so a referenced table can be
// rebuilt without DROP TABLE quietly cascading its children away. The pragma
// lives on a pooled connection, so the risk this pins is that the connection
// goes back to the pool still carrying it and silently disables enforcement
// for ordinary writes.
func TestMigrationsLeaveForeignKeyEnforcementOn(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})

	for name, pool := range map[string]*sql.DB{"write": db.write, "read": db.read} {
		// Every connection in the pool, not just the first one handed out.
		for attempt := range 4 {
			var enforced int
			if err := pool.QueryRowContext(context.Background(), `PRAGMA foreign_keys`).Scan(&enforced); err != nil {
				t.Fatalf("%s pool attempt %d: %v", name, attempt, err)
			}
			if enforced != 1 {
				t.Fatalf("%s pool attempt %d has foreign keys disabled", name, attempt)
			}
		}
	}

	assertWriteRejected(t, db, `
INSERT INTO entry_media (entry_id, media_id, position, alt_text)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAV', '01ARZ3NDEKTSV4RRFFQ69G5FAY', 0, NULL)`)
}

// The check that stands in for the enforcement migrations run without.
func TestMigrationsRefuseToLandForeignKeyViolations(t *testing.T) {
	t.Parallel()
	pool := openRawMigrationPool(t)

	orphaning := []migration{{version: 1, name: "orphan", sql: `
CREATE TABLE parent (id TEXT PRIMARY KEY) STRICT;
CREATE TABLE child (parent_id TEXT NOT NULL REFERENCES parent(id)) STRICT;
INSERT INTO child (parent_id) VALUES ('missing');`, checksum: strings.Repeat("a", 64)}}

	err := applyMigrations(context.Background(), pool, orphaning, time.Unix(1, 0))
	if err == nil {
		t.Fatal("expected a migration that leaves an orphan behind to fail")
	}
	if !strings.Contains(err.Error(), "foreign key violations") {
		t.Fatalf("error should name the violation, got %v", err)
	}

	// Rolled back: the ledger must not claim the migration landed.
	var tables int
	if err := pool.QueryRowContext(context.Background(),
		`SELECT count(*) FROM sqlite_master WHERE name IN ('parent', 'child', 'schema_migrations')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("expected the whole transaction to roll back, found %d tables", tables)
	}

	var enforced int
	if err := pool.QueryRowContext(context.Background(), `PRAGMA foreign_keys`).Scan(&enforced); err != nil {
		t.Fatal(err)
	}
	if enforced != 1 {
		t.Fatal("a failed migration must still restore foreign key enforcement")
	}
}

// A migration that rebuilds a referenced table keeps every child row, which is
// exactly what DROP TABLE would destroy if enforcement were left on.
func TestMigrationsCanRebuildAReferencedTableWithoutLosingChildren(t *testing.T) {
	t.Parallel()
	pool := openRawMigrationPool(t)

	seed := migration{version: 1, name: "seed", sql: `
CREATE TABLE parent (id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind = 'a')) STRICT;
CREATE TABLE child (parent_id TEXT NOT NULL REFERENCES parent(id) ON DELETE CASCADE, note TEXT NOT NULL) STRICT;
INSERT INTO parent (id, kind) VALUES ('p', 'a');
INSERT INTO child (parent_id, note) VALUES ('p', 'keep me');`, checksum: strings.Repeat("b", 64)}
	rebuild := migration{version: 2, name: "rebuild", sql: `
CREATE TABLE parent_next (id TEXT PRIMARY KEY, kind TEXT NOT NULL CHECK (kind IN ('a', 'b'))) STRICT;
INSERT INTO parent_next SELECT * FROM parent;
DROP TABLE parent;
ALTER TABLE parent_next RENAME TO parent;`, checksum: strings.Repeat("c", 64)}

	if err := applyMigrations(context.Background(), pool, []migration{seed}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(context.Background(), pool, []migration{seed, rebuild}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}

	var note string
	if err := pool.QueryRowContext(context.Background(), `SELECT note FROM child`).Scan(&note); err != nil {
		t.Fatalf("the child row did not survive the rebuild: %v", err)
	}
	if note != "keep me" {
		t.Fatalf("note = %q", note)
	}

	var reference string
	if err := pool.QueryRowContext(context.Background(),
		`SELECT sql FROM sqlite_master WHERE name = 'child'`).Scan(&reference); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reference, "parent_next") {
		t.Fatalf("the child still points at the superseded table: %s", reference)
	}
	if _, err := pool.ExecContext(context.Background(),
		`INSERT INTO parent (id, kind) VALUES ('q', 'b')`); err != nil {
		t.Fatalf("the relaxed check did not take effect: %v", err)
	}
}

func openRawMigrationPool(t *testing.T) *sql.DB {
	t.Helper()
	pool, err := sql.Open(moderncDriverName,
		pinnedSQLiteDSN(filepath.Join(t.TempDir(), "migration.db"), time.Second, false))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = pool.Close() })
	return pool
}
