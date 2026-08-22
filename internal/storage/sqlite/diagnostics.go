package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/moment/moment/internal/runtimefs"
)

const diagnosticRowLimit = 100

// DiagnosticDatabase is a strict file-read-only SQLite handle. Unlike Open it
// never applies migrations, initializes settings, repairs search data, or
// changes journal configuration.
type DiagnosticDatabase struct {
	pool    *sql.DB
	closeFn func() error
}

var _ Transactor = (*DiagnosticDatabase)(nil)

// DiagnosticSchema reports the exact migration/catalog relationship without
// exposing migration checksums.
type DiagnosticSchema struct {
	AppliedVersion  int  `json:"applied_version"`
	ExpectedVersion int  `json:"expected_version"`
	Exact           bool `json:"exact"`
}

// DiagnosticIntegrity is bounded even for a badly damaged database.
type DiagnosticIntegrity struct {
	OK       bool `json:"ok"`
	Problems int  `json:"problems"`
	Bounded  bool `json:"bounded"`
}

// DiagnosticForeignKeys reports counts only; table names and row contents do
// not cross the diagnostic boundary.
type DiagnosticForeignKeys struct {
	Violations int  `json:"violations"`
	Bounded    bool `json:"bounded"`
}

// DiagnosticFTS describes the installed FTS5/trigram schema capability.
type DiagnosticFTS struct {
	FTS5    bool `json:"fts5"`
	Trigram bool `json:"trigram"`
}

// DiagnosticMedia is authoritative metadata needed by the doctor. User-facing
// filenames are deliberately excluded.
type DiagnosticMedia struct {
	ID                  string
	SHA256              string
	StoragePath         string
	OriginalName        string
	MIMEType            string
	ByteSize            int64
	Width               uint32
	Height              uint32
	CreatedAtMS         int64
	UnreferencedSinceMS *int64
}

// OpenDiagnostic opens an existing database in SQLite mode=ro/query_only. It
// intentionally performs no readiness bootstrap and therefore remains useful
// when the schema itself is damaged.
func OpenDiagnostic(ctx context.Context, databasePath string, busyTimeout time.Duration) (*DiagnosticDatabase, error) {
	if busyTimeout == 0 {
		busyTimeout = defaultBusyTimeout
	}
	if busyTimeout < time.Millisecond || busyTimeout > maxBusyTimeout {
		return nil, fmt.Errorf("busy timeout must be between 1ms and %s", maxBusyTimeout)
	}
	absolute, err := filepathAbs(databasePath)
	if err != nil {
		return nil, err
	}
	pool, err := openPool(sqliteStrictReadOnlyDSN(absolute, busyTimeout), 1)
	if err != nil {
		return nil, fmt.Errorf("open SQLite diagnostic pool: %w", err)
	}
	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("open SQLite read-only database: %w", err)
	}
	return &DiagnosticDatabase{pool: pool, closeFn: pool.Close}, nil
}

// OpenDevelopmentDiagnosticPinned opens a complete, closed development
// database through the descriptor path proven by DevelopmentAuthority. The
// immutable hint prevents SQLite from deriving WAL/journal sidecar names from
// /proc/self/fd or /dev/fd while keeping the view strictly read-only.
func OpenDevelopmentDiagnosticPinned(ctx context.Context, runtimeState *runtimefs.Runtime, held *runtimefs.LeaseSet, authority *runtimefs.DevelopmentAuthority) (*DiagnosticDatabase, error) {
	if authority == nil || authority.Validate(runtimeState, held) != nil {
		return nil, runtimefs.ErrInsufficientLocks
	}
	pool, err := openPool(sqlitePinnedImmutableDSN(authority.DatabasePath(), defaultBusyTimeout), 1)
	if err != nil {
		return nil, fmt.Errorf("open pinned SQLite diagnostic pool: %w", err)
	}
	if err := pool.PingContext(ctx); err != nil {
		_ = pool.Close()
		return nil, fmt.Errorf("open pinned SQLite read-only database: %w", err)
	}
	return &DiagnosticDatabase{pool: pool, closeFn: pool.Close}, nil
}

// NewDiagnosticView borrows the already-open server read pool. It neither
// opens SQLite again nor owns pool closure, so HTTP summary cannot create or
// modify sidecars beside the active database.
func NewDiagnosticView(database *Database) (*DiagnosticDatabase, error) {
	if database == nil || database.read == nil {
		return nil, errors.New("active SQLite database is required")
	}
	if err := database.requireReady(); err != nil {
		return nil, err
	}
	return &DiagnosticDatabase{pool: database.read, closeFn: func() error { return nil }}, nil
}

func filepathAbs(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", fmt.Errorf("resolve SQLite path: %w", err)
	}
	return absolute, nil
}

// Close releases the read-only pool.
func (database *DiagnosticDatabase) Close() error {
	if database == nil || database.pool == nil {
		return nil
	}
	if database.closeFn == nil {
		return nil
	}
	return database.closeFn()
}

// ReadTx supplies one consistent query-only transaction.
func (database *DiagnosticDatabase) ReadTx(ctx context.Context, operation func(Queries) error) error {
	if database == nil || database.pool == nil {
		return ErrClosed
	}
	if operation == nil {
		return errors.New("diagnostic read operation is required")
	}
	tx, err := database.pool.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin SQLite diagnostic transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := operation(tx); err != nil {
		return err
	}
	return tx.Commit()
}

// WriteTx rejects every attempted mutation.
func (*DiagnosticDatabase) WriteTx(context.Context, func(Queries) error) error {
	return errors.New("diagnostic database is read-only")
}

// Schema validates migration identity and the required catalog independently.
func (database *DiagnosticDatabase) Schema(ctx context.Context) (DiagnosticSchema, error) {
	migrations, err := embeddedMigrations()
	if err != nil {
		return DiagnosticSchema{}, err
	}
	result := DiagnosticSchema{ExpectedVersion: len(migrations)}
	applied, err := loadAppliedMigrations(ctx, database.pool)
	if err != nil {
		return result, err
	}
	if len(applied) > 0 {
		result.AppliedVersion = applied[len(applied)-1].version
	}
	if err := validateAppliedMigrations(applied, migrations); err != nil {
		return result, err
	}
	if len(applied) != len(migrations) {
		return result, fmt.Errorf("%w: applied migration count %d, expected %d", ErrNotReady, len(applied), len(migrations))
	}
	if err := verifySchemaCatalog(ctx, database.pool); err != nil {
		return result, err
	}
	result.Exact = true
	return result, nil
}

// Integrity runs SQLite's bounded integrity check.
func (database *DiagnosticDatabase) Integrity(ctx context.Context) (DiagnosticIntegrity, error) {
	rows, err := database.pool.QueryContext(ctx, `PRAGMA integrity_check(100)`)
	if err != nil {
		return DiagnosticIntegrity{}, err
	}
	defer func() { _ = rows.Close() }()
	result := DiagnosticIntegrity{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		var message string
		if err := rows.Scan(&message); err != nil {
			return result, err
		}
		if message != "ok" {
			result.Problems++
		}
		if result.Problems >= diagnosticRowLimit {
			result.Bounded = true
			break
		}
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	result.OK = result.Problems == 0
	return result, nil
}

// ForeignKeys scans at most one row beyond the public evidence bound.
func (database *DiagnosticDatabase) ForeignKeys(ctx context.Context) (DiagnosticForeignKeys, error) {
	rows, err := database.pool.QueryContext(ctx, `SELECT * FROM pragma_foreign_key_check LIMIT 101`)
	if err != nil {
		return DiagnosticForeignKeys{}, err
	}
	defer func() { _ = rows.Close() }()
	result := DiagnosticForeignKeys{}
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return result, err
		}
		var table, parent string
		var rowID sql.NullInt64
		var key int64
		if err := rows.Scan(&table, &rowID, &parent, &key); err != nil {
			return result, err
		}
		result.Violations++
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	if result.Violations > diagnosticRowLimit {
		result.Violations = diagnosticRowLimit
		result.Bounded = true
	}
	return result, nil
}

// FTSCapability proves that FTS5 is registered and the authoritative virtual
// table declares the trigram tokenizer without creating a temp table.
func (database *DiagnosticDatabase) FTSCapability(ctx context.Context) (DiagnosticFTS, error) {
	var modules int
	if err := database.pool.QueryRowContext(ctx, `SELECT count(*) FROM pragma_module_list WHERE name = 'fts5'`).Scan(&modules); err != nil {
		return DiagnosticFTS{}, err
	}
	result := DiagnosticFTS{FTS5: modules == 1}
	var statement string
	if err := database.pool.QueryRowContext(ctx, `SELECT sql FROM sqlite_schema WHERE name = 'entry_fts' AND type = 'table'`).Scan(&statement); err != nil {
		return result, err
	}
	normalized := strings.ToLower(strings.Join(strings.Fields(statement), " "))
	result.Trigram = strings.Contains(normalized, "tokenize = 'trigram'") || strings.Contains(normalized, "tokenize='trigram'")
	if !result.FTS5 || !result.Trigram {
		return result, ErrFTSUnavailable
	}
	return result, nil
}

// MediaRecords returns a bounded authority catalog for filesystem checks.
func (database *DiagnosticDatabase) MediaRecords(ctx context.Context, limit int) ([]DiagnosticMedia, bool, error) {
	if limit < 1 {
		return nil, false, errors.New("media diagnostic limit must be positive")
	}
	rows, err := database.pool.QueryContext(ctx, `
SELECT id, sha256, storage_path, original_name, mime_type, byte_size, width, height,
       created_at_ms, unreferenced_since_ms
FROM media
ORDER BY id
LIMIT ?`, limit+1)
	if err != nil {
		return nil, false, err
	}
	defer func() { _ = rows.Close() }()
	values := make([]DiagnosticMedia, 0, min(limit, 128))
	bounded := false
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return values, bounded, err
		}
		var value DiagnosticMedia
		var width, height int64
		var unreferenced sql.NullInt64
		if err := rows.Scan(&value.ID, &value.SHA256, &value.StoragePath, &value.OriginalName, &value.MIMEType, &value.ByteSize,
			&width, &height, &value.CreatedAtMS, &unreferenced); err != nil {
			return values, bounded, err
		}
		if len(values) == limit {
			bounded = true
			break
		}
		if width > 0 && width <= int64(^uint32(0)) {
			value.Width = uint32(width)
		}
		if height > 0 && height <= int64(^uint32(0)) {
			value.Height = uint32(height)
		}
		if unreferenced.Valid {
			clock := unreferenced.Int64
			value.UnreferencedSinceMS = &clock
		}
		values = append(values, value)
	}
	return values, bounded, rows.Err()
}

// DiagnosticAccounts summarises the shape of the account graph.
//
// Every field is a count of something that should be impossible, except the
// first two, which are counts of something that should be exactly one. A
// healthy site reads 1, 1, 0, 0, 0, 0.
type DiagnosticAccounts struct {
	Owners            int
	OwnerResolves     int
	OrphanIdentities  int
	OrphanSessions    int
	OrphanCommentRefs int
	ReservedUsernames []string
}

// Accounts reports account integrity for diagnostics.
func (database *DiagnosticDatabase) Accounts(ctx context.Context, reserved func(string) bool) (DiagnosticAccounts, error) {
	var value DiagnosticAccounts
	err := database.ReadTx(ctx, func(queries Queries) error {
		scalars := []struct {
			query  string
			target *int
		}{
			{`SELECT count(*) FROM users WHERE role = 'owner' AND status = 'active'`, &value.Owners},
			{`SELECT count(*) FROM site_settings AS site JOIN users AS account ON account.id = site.owner_id WHERE site.id = 1`, &value.OwnerResolves},
			{`SELECT count(*) FROM identities WHERE user_id NOT IN (SELECT id FROM users)`, &value.OrphanIdentities},
			{`SELECT count(*) FROM sessions WHERE user_id NOT IN (SELECT id FROM users)`, &value.OrphanSessions},
			// author_user_id deliberately carries no foreign key, so this is
			// the only thing that would notice it pointing at nothing.
			{`SELECT count(*) FROM comments WHERE author_user_id IS NOT NULL AND author_user_id NOT IN (SELECT id FROM users)`, &value.OrphanCommentRefs},
		}
		for _, scalar := range scalars {
			if err := queries.QueryRowContext(ctx, scalar.query).Scan(scalar.target); err != nil {
				return fmt.Errorf("read account diagnostics: %w", err)
			}
		}
		if reserved == nil {
			return nil
		}
		rows, err := queries.QueryContext(ctx, `SELECT username FROM users ORDER BY username`)
		if err != nil {
			return fmt.Errorf("read account usernames: %w", err)
		}
		defer func() { _ = rows.Close() }()
		for rows.Next() {
			var username string
			if err := rows.Scan(&username); err != nil {
				return fmt.Errorf("scan account username: %w", err)
			}
			if reserved(username) {
				value.ReservedUsernames = append(value.ReservedUsernames, username)
			}
		}
		return rows.Err()
	})
	return value, err
}
