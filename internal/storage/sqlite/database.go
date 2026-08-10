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
	"sync"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/runtimefs"

	// Register the pinned CGo-free database/sql driver.
	_ "modernc.org/sqlite"
)

const (
	defaultBusyTimeout   = 5 * time.Second
	maxBusyTimeout       = 60 * time.Second
	defaultReadPoolSize  = 4
	maxReadPoolSize      = 32
	writePoolSize        = 1
	moderncDriverName    = "sqlite"
	ftsCapabilityTable   = "temp.__moment_fts5_trigram_probe"
	ftsCapabilityContent = "alpha-bravo-charlie"
)

// Stable storage lifecycle failures.
var (
	ErrNotReady         = errors.New("SQLite schema is not ready")
	ErrClosed           = errors.New("SQLite database is closed")
	ErrMigrationDrift   = errors.New("SQLite migration checksum drift")
	ErrSchemaTooNew     = errors.New("SQLite schema is newer than this binary")
	ErrSchemaIncomplete = errors.New("SQLite schema catalog is incomplete")
	ErrFTSUnavailable   = errors.New("SQLite FTS5 trigram capability is unavailable")
)

// Options bounds connection behavior. Zero values select safe defaults.
type Options struct {
	BusyTimeout  time.Duration
	ReadPoolSize int
}

// Database owns independent read and write pools for one active runtime
// generation. The write pool is deliberately fixed to one connection.
type Database struct {
	path         string
	read         *sql.DB
	write        *sql.DB
	busyTimeout  time.Duration
	readPoolSize int
	journalMode  string
	migrations   []migration

	stateMu        sync.RWMutex
	ready          bool
	readinessCause error
}

// Open resolves the database exclusively through runtimefs' current
// generation, applies embedded migrations, verifies every configured
// connection, and probes FTS5 trigram before reporting success.
func Open(ctx context.Context, runtimeState *runtimefs.Runtime, options Options) (*Database, error) {
	if runtimeState == nil {
		return nil, errors.New("runtime is required")
	}
	databasePath := runtimeState.Current.DatabasePath
	if databasePath == "" {
		return nil, errors.New("runtime current generation has no database path")
	}
	db, err := connect(databasePath, options)
	if err != nil {
		return nil, err
	}
	if err := db.bootstrap(ctx); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// OpenDevelopmentPinned opens a pre-pinned database descriptor path with an
// in-memory rollback journal, avoiding WAL sidecars that would require another
// pathname resolution. The caller keeps the original file/directory handle
// alive for the complete database lifetime.
func OpenDevelopmentPinned(ctx context.Context, runtimeState *runtimefs.Runtime, held *runtimefs.LeaseSet, authority *runtimefs.DevelopmentAuthority, options Options) (*Database, error) {
	if authority == nil || authority.Validate(runtimeState, held) != nil {
		return nil, runtimefs.ErrInsufficientLocks
	}
	descriptorPath := authority.DatabasePath()
	busyTimeout := options.BusyTimeout
	if busyTimeout == 0 {
		busyTimeout = defaultBusyTimeout
	}
	readPoolSize := options.ReadPoolSize
	if readPoolSize == 0 {
		readPoolSize = defaultReadPoolSize
	}
	if busyTimeout < time.Millisecond || busyTimeout > maxBusyTimeout || readPoolSize < 1 || readPoolSize > maxReadPoolSize {
		return nil, errors.New("pinned development database options are outside bounds")
	}
	migrations, err := embeddedMigrations()
	if err != nil {
		return nil, err
	}
	writePool, err := openPool(pinnedSQLiteDSN(descriptorPath, busyTimeout, false), writePoolSize)
	if err != nil {
		return nil, err
	}
	readPool, err := openPool(pinnedSQLiteDSN(descriptorPath, busyTimeout, true), readPoolSize)
	if err != nil {
		_ = writePool.Close()
		return nil, err
	}
	database := &Database{
		path: descriptorPath, read: readPool, write: writePool, busyTimeout: busyTimeout,
		readPoolSize: readPoolSize, journalMode: "memory", migrations: migrations, readinessCause: ErrNotReady,
	}
	if err := database.bootstrap(ctx); err != nil {
		_ = database.Close()
		return nil, err
	}
	return database, nil
}

func pinnedSQLiteDSN(databasePath string, busyTimeout time.Duration, readOnly bool) string {
	values := url.Values{}
	values.Set("_busy_timeout", strconv.FormatInt(busyTimeout.Milliseconds(), 10))
	values.Set("_dqs", "0")
	values.Set("_foreign_keys", "on")
	values.Set("_journal_mode", "memory")
	values.Set("_synchronous", "full")
	if readOnly {
		values.Set("_query_only", "on")
	} else {
		values.Set("_txlock", "immediate")
	}
	databasePath = normalizedFileURLPath(databasePath, runtime.GOOS)
	return (&url.URL{Scheme: "file", Path: databasePath, RawQuery: values.Encode()}).String()
}

func connect(databasePath string, options Options) (*Database, error) {
	absolutePath, err := filepath.Abs(databasePath)
	if err != nil {
		return nil, fmt.Errorf("resolve SQLite path: %w", err)
	}
	busyTimeout := options.BusyTimeout
	if busyTimeout == 0 {
		busyTimeout = defaultBusyTimeout
	}
	if busyTimeout < time.Millisecond || busyTimeout > maxBusyTimeout {
		return nil, fmt.Errorf("busy timeout must be between 1ms and %s", maxBusyTimeout)
	}
	readPoolSize := options.ReadPoolSize
	if readPoolSize == 0 {
		readPoolSize = defaultReadPoolSize
	}
	if readPoolSize < 1 || readPoolSize > maxReadPoolSize {
		return nil, fmt.Errorf("read pool size must be between 1 and %d", maxReadPoolSize)
	}
	migrations, err := embeddedMigrations()
	if err != nil {
		return nil, err
	}

	writePool, err := openPool(sqliteDSN(absolutePath, busyTimeout, false), writePoolSize)
	if err != nil {
		return nil, fmt.Errorf("open SQLite write pool: %w", err)
	}
	readPool, err := openPool(sqliteDSN(absolutePath, busyTimeout, true), readPoolSize)
	if err != nil {
		_ = writePool.Close()
		return nil, fmt.Errorf("open SQLite read pool: %w", err)
	}
	return &Database{
		path:           absolutePath,
		read:           readPool,
		write:          writePool,
		busyTimeout:    busyTimeout,
		readPoolSize:   readPoolSize,
		migrations:     migrations,
		readinessCause: ErrNotReady,
		journalMode:    "wal",
	}, nil
}

func openPool(dsn string, size int) (*sql.DB, error) {
	pool, err := sql.Open(moderncDriverName, dsn)
	if err != nil {
		return nil, err
	}
	pool.SetMaxOpenConns(size)
	pool.SetMaxIdleConns(size)
	return pool, nil
}

func sqliteDSN(databasePath string, busyTimeout time.Duration, readOnly bool) string {
	urlPath := normalizedFileURLPath(databasePath, runtime.GOOS)
	values := url.Values{}
	values.Set("_busy_timeout", strconv.FormatInt(busyTimeout.Milliseconds(), 10))
	values.Set("_dqs", "0")
	values.Set("_foreign_keys", "on")
	values.Set("_journal_mode", "wal")
	if readOnly {
		values.Set("_query_only", "on")
	} else {
		values.Set("_txlock", "immediate")
	}
	return (&url.URL{Scheme: "file", Path: urlPath, RawQuery: values.Encode()}).String()
}

func normalizedFileURLPath(databasePath, operatingSystem string) string {
	urlPath := filepath.ToSlash(databasePath)
	if operatingSystem == "windows" {
		urlPath = strings.ReplaceAll(urlPath, `\`, "/")
	}
	if operatingSystem == "windows" && !strings.HasPrefix(urlPath, "/") {
		urlPath = "/" + urlPath
	}
	return urlPath
}

func (db *Database) bootstrap(ctx context.Context) error {
	db.setReadiness(false, ErrNotReady)
	expectedJournal := db.journalMode
	if expectedJournal == "" {
		expectedJournal = "wal"
	}
	if err := verifyPoolConnections(ctx, db.write, writePoolSize, db.busyTimeout, false, expectedJournal); err != nil {
		return db.failReadiness(fmt.Errorf("verify SQLite write connections: %w", err))
	}
	if err := applyMigrations(ctx, db.write, db.migrations, time.Now()); err != nil {
		return db.failReadiness(err)
	}
	if err := initializeSiteSettings(ctx, db.write, app.HostTimezone()); err != nil {
		return db.failReadiness(err)
	}
	if err := bootstrapSearchDerived(ctx, db.write); err != nil {
		return db.failReadiness(err)
	}
	if err := verifySchemaCatalog(ctx, db.write); err != nil {
		return db.failReadiness(err)
	}
	if err := db.probeCapabilities(ctx); err != nil {
		return db.failReadiness(err)
	}
	if err := verifyPoolConnections(ctx, db.read, db.readPoolSize, db.busyTimeout, true, expectedJournal); err != nil {
		return db.failReadiness(fmt.Errorf("verify SQLite read connections: %w", err))
	}
	db.setReadiness(true, nil)
	return nil
}

// Path returns the current generation database path selected at open time.
func (db *Database) Path() string { return db.path }

// Ready verifies the startup state and the exact applied migration prefix.
func (db *Database) Ready(ctx context.Context) error {
	if err := db.requireReady(); err != nil {
		return err
	}
	if err := db.read.PingContext(ctx); err != nil {
		return fmt.Errorf("%w: ping read pool: %w", ErrNotReady, err)
	}
	if err := db.write.PingContext(ctx); err != nil {
		return fmt.Errorf("%w: ping write pool: %w", ErrNotReady, err)
	}
	applied, err := loadAppliedMigrations(ctx, db.read)
	if err != nil {
		return fmt.Errorf("%w: %w", ErrNotReady, err)
	}
	if err := validateAppliedMigrations(applied, db.migrations); err != nil {
		return fmt.Errorf("%w: %w", ErrNotReady, err)
	}
	if len(applied) != len(db.migrations) {
		return fmt.Errorf("%w: applied migration count %d, expected %d", ErrNotReady, len(applied), len(db.migrations))
	}
	if err := verifySchemaCatalog(ctx, db.read); err != nil {
		return fmt.Errorf("%w: %w", ErrNotReady, err)
	}
	return nil
}

// ProbeCapabilities re-runs the actual FTS5 trigram create/insert/query probe.
func (db *Database) ProbeCapabilities(ctx context.Context) error {
	if err := db.requireReady(); err != nil {
		return err
	}
	return db.probeCapabilities(ctx)
}

func (db *Database) probeCapabilities(ctx context.Context) error {
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("%w: begin probe: %w", ErrFTSUnavailable, err)
	}
	defer func() { _ = tx.Rollback() }()
	if _, err := tx.ExecContext(ctx, `CREATE VIRTUAL TABLE `+ftsCapabilityTable+` USING fts5(content, tokenize='trigram')`); err != nil {
		return fmt.Errorf("%w: create probe table: %w", ErrFTSUnavailable, err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO `+ftsCapabilityTable+` (content) VALUES (?)`, ftsCapabilityContent); err != nil {
		return fmt.Errorf("%w: populate probe table: %w", ErrFTSUnavailable, err)
	}
	var matches int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM `+ftsCapabilityTable+` WHERE content MATCH 'bravo'`).Scan(&matches); err != nil {
		return fmt.Errorf("%w: query probe table: %w", ErrFTSUnavailable, err)
	}
	if matches != 1 {
		return fmt.Errorf("%w: trigram query returned %d matches", ErrFTSUnavailable, matches)
	}
	return nil
}

func (db *Database) failReadiness(err error) error {
	db.setReadiness(false, err)
	return fmt.Errorf("%w: %w", ErrNotReady, err)
}

func (db *Database) setReadiness(ready bool, cause error) {
	db.stateMu.Lock()
	db.ready = ready
	db.readinessCause = cause
	db.stateMu.Unlock()
}

func (db *Database) requireReady() error {
	db.stateMu.RLock()
	defer db.stateMu.RUnlock()
	if db.ready {
		return nil
	}
	if db.readinessCause == nil {
		return ErrNotReady
	}
	return fmt.Errorf("%w: %w", ErrNotReady, db.readinessCause)
}

// Close releases both pools and makes future transaction attempts fail.
func (db *Database) Close() error {
	if db == nil {
		return nil
	}
	db.setReadiness(false, ErrClosed)
	return errors.Join(db.read.Close(), db.write.Close())
}

type pragmaState struct {
	foreignKeys int
	journalMode string
	busyTimeout int64
	queryOnly   int
}

func verifyPoolConnections(ctx context.Context, pool *sql.DB, count int, busyTimeout time.Duration, readOnly bool, expectedJournal string) error {
	connections := make([]*sql.Conn, 0, count)
	defer func() {
		for _, connection := range connections {
			_ = connection.Close()
		}
	}()
	for range count {
		connection, err := pool.Conn(ctx)
		if err != nil {
			return err
		}
		connections = append(connections, connection)
	}
	for index, connection := range connections {
		state, err := readPragmaState(ctx, connection)
		if err != nil {
			return fmt.Errorf("connection %d: %w", index, err)
		}
		expectedQueryOnly := 0
		if readOnly {
			expectedQueryOnly = 1
		}
		if state.foreignKeys != 1 || !strings.EqualFold(state.journalMode, expectedJournal) ||
			state.busyTimeout != busyTimeout.Milliseconds() || state.queryOnly != expectedQueryOnly {
			return fmt.Errorf("connection %d has invalid pragmas: %+v", index, state)
		}
	}
	return nil
}

type pragmaQueryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func readPragmaState(ctx context.Context, queryer pragmaQueryer) (pragmaState, error) {
	var state pragmaState
	if err := queryer.QueryRowContext(ctx, `PRAGMA foreign_keys`).Scan(&state.foreignKeys); err != nil {
		return state, err
	}
	if err := queryer.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&state.journalMode); err != nil {
		return state, err
	}
	if err := queryer.QueryRowContext(ctx, `PRAGMA busy_timeout`).Scan(&state.busyTimeout); err != nil {
		return state, err
	}
	if err := queryer.QueryRowContext(ctx, `PRAGMA query_only`).Scan(&state.queryOnly); err != nil {
		return state, err
	}
	return state, nil
}
