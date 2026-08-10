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

func applyMigrations(ctx context.Context, db *sql.DB, migrations []migration, now time.Time) error {
	tx, err := db.BeginTx(ctx, nil)
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

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit migrations: %w", err)
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
