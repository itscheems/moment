package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// Queries is the storage-facing command/query surface supplied to repository
// callbacks without exposing connection-pool ownership.
type Queries interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// Transactor is the transaction boundary consumed by feature repositories.
type Transactor interface {
	ReadTx(context.Context, func(Queries) error) error
	WriteTx(context.Context, func(Queries) error) error
}

var _ Transactor = (*Database)(nil)

// ReadTx runs a consistent, query-only transaction.
func (db *Database) ReadTx(ctx context.Context, operation func(Queries) error) error {
	return db.withTransaction(ctx, db.read, &sql.TxOptions{ReadOnly: true}, operation)
}

// WriteTx serializes writers through the single-connection write pool.
func (db *Database) WriteTx(ctx context.Context, operation func(Queries) error) error {
	return db.withTransaction(ctx, db.write, nil, operation)
}

func (db *Database) withTransaction(ctx context.Context, pool *sql.DB, options *sql.TxOptions, operation func(Queries) error) error {
	if operation == nil {
		return errors.New("transaction operation is required")
	}
	if err := db.requireReady(); err != nil {
		return err
	}
	tx, err := pool.BeginTx(ctx, options)
	if err != nil {
		return fmt.Errorf("begin SQLite transaction: %w", err)
	}
	defer func() { _ = tx.Rollback() }()
	if err := operation(tx); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit SQLite transaction: %w", err)
	}
	return nil
}
