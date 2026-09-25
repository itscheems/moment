package sqlite

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"embed"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

const migrationLedgerDDL = `
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY CHECK (version > 0),
    name TEXT NOT NULL UNIQUE CHECK (name <> ''),
    applied_at_ms INTEGER NOT NULL CHECK (applied_at_ms > 0),
    checksum TEXT NOT NULL CHECK (
        length(checksum) = 64
        AND checksum NOT GLOB '*[^0-9a-f]*'
    )
) STRICT`

//go:embed migrations/*.sql
var migrationFiles embed.FS

type migration struct {
	version  int
	name     string
	sql      string
	checksum string
}

type appliedMigration struct {
	version  int
	name     string
	checksum string
}

func embeddedMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrationFiles, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read embedded migrations: %w", err)
	}

	migrations := make([]migration, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || path.Ext(entry.Name()) != ".sql" {
			continue
		}
		stem := strings.TrimSuffix(entry.Name(), ".sql")
		parts := strings.SplitN(stem, "_", 2)
		if len(parts) != 2 || parts[1] == "" {
			return nil, fmt.Errorf("invalid embedded migration filename %q", entry.Name())
		}
		version, err := strconv.Atoi(parts[0])
		if err != nil || version < 1 {
			return nil, fmt.Errorf("invalid embedded migration version in %q", entry.Name())
		}
		contents, err := migrationFiles.ReadFile(path.Join("migrations", entry.Name()))
		if err != nil {
			return nil, fmt.Errorf("read embedded migration %q: %w", entry.Name(), err)
		}
		canonicalSQL, checksum := canonicalMigration(contents)
		migrations = append(migrations, migration{
			version:  version,
			name:     parts[1],
			sql:      canonicalSQL,
			checksum: checksum,
		})
	}

	sort.Slice(migrations, func(i, j int) bool { return migrations[i].version < migrations[j].version })
	if len(migrations) == 0 {
		return nil, errors.New("no embedded SQLite migrations")
	}
	for index, item := range migrations {
		expected := index + 1
		if item.version != expected {
			return nil, fmt.Errorf("embedded migrations must be contiguous: got version %d, want %d", item.version, expected)
		}
	}
	return migrations, nil
}

func canonicalMigration(contents []byte) (string, string) {
	canonical := bytes.ReplaceAll(contents, []byte("\r\n"), []byte("\n"))
	canonical = bytes.ReplaceAll(canonical, []byte("\r"), []byte("\n"))
	digest := sha256.Sum256(canonical)
	return string(canonical), hex.EncodeToString(digest[:])
}

// applyMigrations runs every unapplied migration in one transaction, with
// foreign key enforcement switched off for the duration.
//
// Disabling enforcement is step one of SQLite's documented procedure for
// changing a table that other tables reference, and it is not a convenience.
// With enforcement on, DROP TABLE performs an implicit DELETE FROM that carries
// out ON DELETE actions, so dropping a superseded copy of a referenced table
// silently empties every child that cascades from it -- committed, with no
// error. Enforcement also makes ALTER TABLE ... RENAME rewrite the REFERENCES
// clause of every table pointing at the renamed one, which drags otherwise
// untouched tables into the rebuild and multiplies the chances of losing one.
//
// What replaces enforcement is foreign_key_check, run inside the transaction
// before the commit whenever a migration actually ran. A migration that leaves
// an orphan behind therefore fails and rolls back instead of landing.
//
// The pragma cannot be toggled inside a transaction, so it is set on one
// dedicated connection taken out of the pool. Restoring it is mandatory before
// that connection goes back: a pooled connection stuck with enforcement off
// would quietly disable it for ordinary writes. A failed restore fails the
// whole open, so the process does not continue with one.
func applyMigrations(ctx context.Context, db *sql.DB, migrations []migration, now time.Time) error {
	conn, err := db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("open migration connection: %w", err)
	}
	defer func() { _ = conn.Close() }()

	if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys=OFF`); err != nil {
		return fmt.Errorf("disable foreign keys for migration: %w", err)
	}
	runErr := runMigrationTransaction(ctx, conn, migrations, now)
	return errors.Join(runErr, restoreForeignKeys(ctx, conn))
}

func restoreForeignKeys(ctx context.Context, conn *sql.Conn) error {
	// A cancelled context must not leave the connection with enforcement off.
	restore := context.WithoutCancel(ctx)
	if _, err := conn.ExecContext(restore, `PRAGMA foreign_keys=ON`); err != nil {
		return fmt.Errorf("restore foreign keys after migration: %w", err)
	}
	var enforced int
	if err := conn.QueryRowContext(restore, `PRAGMA foreign_keys`).Scan(&enforced); err != nil {
		return fmt.Errorf("read back foreign key enforcement: %w", err)
	}
	if enforced != 1 {
		return errors.New("foreign key enforcement stayed off after migration")
	}
	return nil
}

func runMigrationTransaction(ctx context.Context, conn *sql.Conn, migrations []migration, now time.Time) error {
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin migration transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()

	if _, err := tx.ExecContext(ctx, migrationLedgerDDL); err != nil {
		return fmt.Errorf("create migration ledger: %w", err)
	}
	applied, err := loadAppliedMigrations(ctx, tx)
	if err != nil {
		return err
	}
	if err := validateAppliedMigrations(applied, migrations); err != nil {
		return err
	}

	appliedCount := len(applied)
	for _, item := range migrations[appliedCount:] {
		if _, err := tx.ExecContext(ctx, item.sql); err != nil {
			return fmt.Errorf("apply migration %04d_%s: %w", item.version, item.name, err)
		}
		if _, err := tx.ExecContext(ctx, `
INSERT INTO schema_migrations (version, name, applied_at_ms, checksum)
VALUES (?, ?, ?, ?)`, item.version, item.name, now.UTC().UnixMilli(), item.checksum); err != nil {
			return fmt.Errorf("record migration %04d_%s: %w", item.version, item.name, err)
		}
	}

	if len(migrations) > appliedCount {
		if err := assertNoForeignKeyViolations(ctx, tx); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
	}
	return nil
}

// assertNoForeignKeyViolations is the gate that stands in for the enforcement
// migrations run without. It names the first offending row rather than only
// reporting that something is wrong, because a migration that produced orphans
// usually produced them in one place.
func assertNoForeignKeyViolations(ctx context.Context, tx *sql.Tx) error {
	rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check foreign keys after migration: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var (
		violations int
		first      string
	)
	for rows.Next() {
		var (
			table  sql.NullString
			rowID  sql.NullInt64
			parent sql.NullString
			fkID   sql.NullInt64
		)
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			return fmt.Errorf("scan foreign key violation: %w", err)
		}
		violations++
		if violations == 1 {
			first = fmt.Sprintf("%s row %d references missing %s", table.String, rowID.Int64, parent.String)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read foreign key violations: %w", err)
	}
	if violations > 0 {
		return fmt.Errorf("migrations left %d foreign key violations behind, first: %s", violations, first)
	}
	return nil
}

type rowQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func loadAppliedMigrations(ctx context.Context, queryer rowQueryer) ([]appliedMigration, error) {
	rows, err := queryer.QueryContext(ctx, `
SELECT version, name, checksum
FROM schema_migrations
ORDER BY version`)
	if err != nil {
		return nil, fmt.Errorf("read migration ledger: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var applied []appliedMigration
	for rows.Next() {
		var item appliedMigration
		if err := rows.Scan(&item.version, &item.name, &item.checksum); err != nil {
			return nil, fmt.Errorf("scan migration ledger: %w", err)
		}
		applied = append(applied, item)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate migration ledger: %w", err)
	}
	return applied, nil
}

func validateAppliedMigrations(applied []appliedMigration, migrations []migration) error {
	if len(applied) > 0 && applied[len(applied)-1].version > len(migrations) {
		return fmt.Errorf("%w: database has version %d but binary supports %d", ErrSchemaTooNew, applied[len(applied)-1].version, len(migrations))
	}
	if len(applied) > len(migrations) {
		return fmt.Errorf("%w: database has %d migration records but binary supports %d", ErrMigrationDrift, len(applied), len(migrations))
	}
	for index, item := range applied {
		expectedVersion := index + 1
		if item.version != expectedVersion {
			return fmt.Errorf("%w: applied versions are not a contiguous prefix at %d", ErrMigrationDrift, item.version)
		}
		expected := migrations[index]
		if item.name != expected.name || item.checksum != expected.checksum {
			return fmt.Errorf("%w: migration %d metadata does not match this binary", ErrMigrationDrift, item.version)
		}
	}
	return nil
}
