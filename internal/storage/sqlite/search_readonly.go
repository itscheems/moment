package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/moment/moment/internal/runtimefs"
)

// ErrSearchSchemaUpgradeRequired means a dry-run cannot inspect search state
// without first allowing the normal writable startup path to migrate it.
var ErrSearchSchemaUpgradeRequired = errors.New("search schema upgrade is required")

// SearchReadOnlyDatabase is a file-read-only transaction source used by
// search rebuild dry-runs. It never applies migrations or bootstrap writes.
type SearchReadOnlyDatabase struct{ pool *sql.DB }

var _ Transactor = (*SearchReadOnlyDatabase)(nil)

// OpenSearchReadOnly opens the selected database with SQLite mode=ro and
// requires the exact current migration set before returning.
func OpenSearchReadOnly(ctx context.Context, runtimeState *runtimefs.Runtime, options Options) (*SearchReadOnlyDatabase, error) {
	if runtimeState == nil || runtimeState.Current.DatabasePath == "" {
		return nil, errors.New("runtime current generation is required")
	}
	busyTimeout := options.BusyTimeout
	if busyTimeout == 0 {
		busyTimeout = defaultBusyTimeout
	}
	if busyTimeout < time.Millisecond || busyTimeout > maxBusyTimeout {
		return nil, fmt.Errorf("busy timeout must be between 1ms and %s", maxBusyTimeout)
	}
	absolutePath, err := filepath.Abs(runtimeState.Current.DatabasePath)
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite path: %w", err)
	}
	pool, err := openPool(sqliteStrictReadOnlyDSN(absolutePath, busyTimeout), 1)
	if err != nil {
		return nil, fmt.Errorf("open SQLite read-only pool: %w", err)
	}
	closeOnError := func(err error) (*SearchReadOnlyDatabase, error) {
		_ = pool.Close()
		return nil, err
	}
	if err := pool.PingContext(ctx); err != nil {
		return closeOnError(fmt.Errorf("open SQLite read-only database: %w", err))
	}
	migrations, err := embeddedMigrations()
	if err != nil {
		return closeOnError(err)
	}
	applied, err := loadAppliedMigrations(ctx, pool)
	if err != nil {
		return closeOnError(err)
	}
	if err := validateAppliedMigrations(applied, migrations); err != nil {
		return closeOnError(err)
	}
	if len(applied) != len(migrations) {
		return closeOnError(fmt.Errorf("%w: database has version %d but dry-run requires %d", ErrSearchSchemaUpgradeRequired, len(applied), len(migrations)))
	}
	if err := verifySchemaCatalog(ctx, pool); err != nil {
		return closeOnError(err)
	}
	return &SearchReadOnlyDatabase{pool: pool}, nil
}

func sqliteStrictReadOnlyDSN(databasePath string, busyTimeout time.Duration) string {
	urlPath := filepath.ToSlash(databasePath)
	if runtime.GOOS == "windows" && !strings.HasPrefix(urlPath, "/") {
		urlPath = "/" + urlPath
	}
	values := url.Values{}
	values.Set("mode", "ro")
	values.Set("_busy_timeout", strconv.FormatInt(busyTimeout.Milliseconds(), 10))
	values.Set("_dqs", "0")
	values.Set("_foreign_keys", "on")
	values.Set("_query_only", "on")
	return (&url.URL{Scheme: "file", Path: urlPath, RawQuery: values.Encode()}).String()
}

func sqlitePinnedImmutableDSN(databasePath string, busyTimeout time.Duration) string {
	urlPath := normalizedFileURLPath(databasePath, runtime.GOOS)
	values := url.Values{}
	values.Set("mode", "ro")
	values.Set("immutable", "1")
	values.Set("_busy_timeout", strconv.FormatInt(busyTimeout.Milliseconds(), 10))
	values.Set("_dqs", "0")
	values.Set("_foreign_keys", "on")
	values.Set("_query_only", "on")
	return (&url.URL{Scheme: "file", Path: urlPath, RawQuery: values.Encode()}).String()
}

// ReadTx runs one consistent read-only SQLite transaction.
func (database *SearchReadOnlyDatabase) ReadTx(ctx context.Context, operation func(Queries) error) error {
	if database == nil || database.pool == nil {
		return ErrClosed
	}
	if operation == nil {
		return errors.New("transaction operation is required")
	}
	tx, err := database.pool.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("begin SQLite read-only transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := operation(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite read-only transaction: %w", err)
	}
	return nil
}

// WriteTx always rejects mutation through the dry-run database.
func (*SearchReadOnlyDatabase) WriteTx(context.Context, func(Queries) error) error {
	return errors.New("search dry-run database is read-only")
}

// Close releases the read-only pool.
func (database *SearchReadOnlyDatabase) Close() error {
	if database == nil || database.pool == nil {
		return nil
	}
	return database.pool.Close()
}
