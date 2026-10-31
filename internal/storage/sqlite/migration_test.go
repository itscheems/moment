package sqlite

import (
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
			if err := pool.QueryRowContext(testAccountContext(), `PRAGMA foreign_keys`).Scan(&enforced); err != nil {
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

	err := applyMigrations(testAccountContext(), pool, orphaning, time.Unix(1, 0))
	if err == nil {
		t.Fatal("expected a migration that leaves an orphan behind to fail")
	}
	if !strings.Contains(err.Error(), "foreign key violations") {
		t.Fatalf("error should name the violation, got %v", err)
	}

	// Rolled back: the ledger must not claim the migration landed.
	var tables int
	if err := pool.QueryRowContext(testAccountContext(),
		`SELECT count(*) FROM sqlite_master WHERE name IN ('parent', 'child', 'schema_migrations')`).Scan(&tables); err != nil {
		t.Fatal(err)
	}
	if tables != 0 {
		t.Fatalf("expected the whole transaction to roll back, found %d tables", tables)
	}

	var enforced int
	if err := pool.QueryRowContext(testAccountContext(), `PRAGMA foreign_keys`).Scan(&enforced); err != nil {
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

	if err := applyMigrations(testAccountContext(), pool, []migration{seed}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(testAccountContext(), pool, []migration{seed, rebuild}, time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}

	var note string
	if err := pool.QueryRowContext(testAccountContext(), `SELECT note FROM child`).Scan(&note); err != nil {
		t.Fatalf("the child row did not survive the rebuild: %v", err)
	}
	if note != "keep me" {
		t.Fatalf("note = %q", note)
	}

	var reference string
	if err := pool.QueryRowContext(testAccountContext(),
		`SELECT sql FROM sqlite_master WHERE name = 'child'`).Scan(&reference); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(reference, "parent_next") {
		t.Fatalf("the child still points at the superseded table: %s", reference)
	}
	if _, err := pool.ExecContext(testAccountContext(),
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

func TestSessionProvenanceUpgradePreservesAccountsAndCredentials(t *testing.T) {
	pool := openRawMigrationPool(t)
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	const before = 3
	if err := applyMigrations(testAccountContext(), pool, migrations[:before], time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	_, err = pool.ExecContext(testAccountContext(), `
INSERT INTO users(id,username,display_name,role,status,created_at_ms,updated_at_ms) VALUES ('11111111111111111111111111111111','upgrade_writer','Upgrade Writer','admin','active',1,1);
INSERT INTO api_tokens(id,user_id,label,token_hash,created_at_ms) VALUES ('22222222222222222222222222222222','11111111111111111111111111111111','existing',zeroblob(32),1);
INSERT INTO account_passwords(user_id,password_hash,updated_at_ms) VALUES ('11111111111111111111111111111111',x'010203',1);
INSERT INTO sessions(id,user_id,issued_at_ms,expires_at_ms,last_seen_at_ms,surface) VALUES ('33333333333333333333333333333333','11111111111111111111111111111111',1,9999999999999,1,'web');`)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(testAccountContext(), pool, migrations, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	var sessions, tokens, passwords, users int
	for query, destination := range map[string]*int{
		`SELECT count(*) FROM sessions WHERE revoked_at_ms IS NOT NULL AND source_token_id IS NULL`:       &sessions,
		`SELECT count(*) FROM api_tokens WHERE revoked_at_ms IS NULL AND token_hash=zeroblob(32)`:         &tokens,
		`SELECT count(*) FROM account_passwords WHERE password_hash=x'010203'`:                            &passwords,
		`SELECT count(*) FROM users WHERE username='upgrade_writer' AND role='admin' AND status='active'`: &users,
	} {
		if err := pool.QueryRowContext(testAccountContext(), query).Scan(destination); err != nil {
			t.Fatal(err)
		}
	}
	if sessions != 1 || tokens != 1 || passwords != 1 || users != 1 {
		t.Fatalf("migration changed authoritative data: sessions=%d tokens=%d passwords=%d users=%d", sessions, tokens, passwords, users)
	}
	if err := applyMigrations(testAccountContext(), pool, migrations, time.Unix(3, 0)); err != nil {
		t.Fatalf("upgrade not idempotent: %v", err)
	}
}

func TestInteractionHistoryUpgradePreservesLiveRelationships(t *testing.T) {
	pool := openRawMigrationPool(t)
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(testAccountContext(), pool, migrations[:8], time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	_, err = pool.Exec(`
 INSERT INTO users(id,username,display_name,role,status,created_at_ms,updated_at_ms) VALUES ('11111111111111111111111111111111','cedar','Cedar','admin','active',1,1);
 INSERT INTO entries(id,kind,content_md,author_user_id,created_at_ms,updated_at_ms,published_at_ms) VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAV','moment','message','11111111111111111111111111111111',1,1,1);
 INSERT INTO comments(id,entry_id,author_name,content_md,created_at_ms) VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAW','01ARZ3NDEKTSV4RRFFQ69G5FAV','Cedar','reply',1);
 INSERT INTO post_likes VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAV','11111111111111111111111111111111',1);
 INSERT INTO comment_likes VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAW','11111111111111111111111111111111',1);
 INSERT INTO entry_mentions VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAV','cedar','11111111111111111111111111111111',1);
 INSERT INTO comment_mentions VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAW','cedar','11111111111111111111111111111111',1);
 INSERT INTO comment_reply_recipients VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAW','11111111111111111111111111111111');
 INSERT INTO notifications(id,recipient_id,actor_name,event_type,subject_kind,subject_id,post_id,post_kind,reasons,occurred_at_ms) VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAX','11111111111111111111111111111111','Maple','post.liked','post','01ARZ3NDEKTSV4RRFFQ69G5FAV','01ARZ3NDEKTSV4RRFFQ69G5FAV','moment',8,1);
 INSERT INTO notification_seen VALUES ('11111111111111111111111111111111','all',1);`)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(testAccountContext(), pool, migrations, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"post_likes", "comment_likes", "entry_mentions", "comment_mentions", "comment_reply_recipients", "notifications", "notification_seen"} {
		var count int
		if err := pool.QueryRow(`SELECT count(*) FROM ` + table).Scan(&count); err != nil || count != 1 {
			t.Fatalf("%s rows = %d, %v", table, count, err)
		}
	}
	var historical, unavailable, sequence int
	if err := pool.QueryRow(`SELECT historical,force_unavailable,sequence FROM notifications`).Scan(&historical, &unavailable, &sequence); err != nil || historical != 0 || unavailable != 0 || sequence != 1 {
		t.Fatalf("live history changed: %d %d %d %v", historical, unavailable, sequence, err)
	}
	if _, err := pool.Exec(`INSERT INTO post_likes(post_id,user_id,created_at_ms) VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FAV','11111111111111111111111111111111',2)`); err == nil {
		t.Fatal("upgrade removed live like uniqueness")
	}
}
