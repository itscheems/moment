package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

// PostRepository adapts SQLite transactions to the post application's ports.
type PostRepository struct {
	transactions Transactor
	locks        *runtimefs.Locker
	maintenance  *runtimefs.Lease
}

var _ app.PostTransactions = (*PostRepository)(nil)

func newPostRepository(transactions Transactor) (*PostRepository, error) {
	if transactions == nil {
		return nil, errors.New("SQLite transactions are required")
	}
	return &PostRepository{transactions: transactions}, nil
}

// NewPostRepository constructs the production SQLite post adapter and
// coordinates every ordinary write in maintenance-shared -> media-shared order.
func NewPostRepository(transactions Transactor, locks *runtimefs.Locker) (*PostRepository, error) {
	if transactions == nil {
		return nil, errors.New("SQLite transactions are required")
	}
	if locks == nil {
		return nil, errors.New("runtime locks are required")
	}
	return &PostRepository{transactions: transactions, locks: locks}, nil
}

// NewPostRepositoryWithinMaintenance reuses a caller-held ordinary-write
// maintenance lease and acquires only media.lock shared before the transaction.
func NewPostRepositoryWithinMaintenance(transactions Transactor, maintenance *runtimefs.Lease, locks *runtimefs.Locker) (*PostRepository, error) {
	if transactions == nil {
		return nil, errors.New("SQLite transactions are required")
	}
	if !maintenance.Holds(runtimefs.MaintenanceLock, runtimefs.Shared) {
		return nil, errors.New("shared maintenance lease is required")
	}
	if locks == nil {
		return nil, errors.New("runtime locks are required")
	}
	return &PostRepository{transactions: transactions, locks: locks, maintenance: maintenance}, nil
}

// Read supplies a transaction-bound store backed by the query-only pool.
func (repository *PostRepository) Read(ctx context.Context, operation func(app.PostStore) error) error {
	if operation == nil {
		return errors.New("post read operation is required")
	}
	return repository.transactions.ReadTx(ctx, func(queries Queries) error {
		return operation(newPostStore(queries))
	})
}

// Write supplies a transaction-bound store backed by the serialized writer.
func (repository *PostRepository) Write(ctx context.Context, operation func(app.PostStore) error) error {
	if operation == nil {
		return errors.New("post write operation is required")
	}
	if repository.locks == nil {
		return repository.write(ctx, operation)
	}
	if repository.maintenance != nil {
		if !repository.maintenance.Holds(runtimefs.MaintenanceLock, runtimefs.Shared) {
			return errors.New("post maintenance lease is no longer held")
		}
		mediaLease, err := repository.locks.Acquire(ctx, runtimefs.MediaLock, runtimefs.Shared)
		if err != nil {
			return fmt.Errorf("coordinate post media references: %w", err)
		}
		defer func() { _ = mediaLease.Release() }()
		return repository.write(ctx, operation)
	}
	leases, err := repository.locks.AcquireSet(ctx,
		runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Shared},
		runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Shared},
	)
	if err != nil {
		return fmt.Errorf("coordinate post write: %w", err)
	}
	defer func() { _ = leases.Release() }()
	return repository.write(ctx, operation)
}

func (repository *PostRepository) write(ctx context.Context, operation func(app.PostStore) error) error {
	return repository.transactions.WriteTx(ctx, func(queries Queries) error {
		return operation(newPostStore(queries))
	})
}

type postStore struct {
	queries Queries
	mediaReferenceStore
}

func newPostStore(queries Queries) postStore {
	return postStore{queries: queries, mediaReferenceStore: newMediaReferenceStore(queries)}
}

var _ app.PostStore = postStore{}

func (store postStore) InsertEntry(ctx context.Context, entry domain.Entry, contentText string) error {
	_, err := store.queries.ExecContext(ctx, `
INSERT INTO entries (id, kind, content_md, content_text, created_at_ms, updated_at_ms, published_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?)`,
		entry.ID.String(), string(entry.Kind), entry.ContentMD, contentText,
		domain.TimeToMillis(entry.CreatedAt), domain.TimeToMillis(entry.UpdatedAt), domain.TimeToMillis(entry.PublishedAt),
	)
	if err != nil {
		return fmt.Errorf("insert entry: %w", err)
	}
	return nil
}

func (store postStore) EntryByID(ctx context.Context, id domain.ID) (domain.Entry, bool, error) {
	entry, err := scanEntry(store.queries.QueryRowContext(ctx, `
SELECT id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms
FROM entries
WHERE id = ? AND kind = ?`, id.String(), string(domain.KindMoment)))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Entry{}, false, nil
	}
	if err != nil {
		return domain.Entry{}, false, fmt.Errorf("query entry: %w", err)
	}
	return entry, true, nil
}

func (store postStore) ListEntries(ctx context.Context, query app.TimelineQuery, limit int) ([]domain.Entry, error) {
	statement := `
SELECT id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms
FROM entries
WHERE kind = ?`
	arguments := []any{string(domain.KindMoment)}
	if query.FromMS != nil {
		statement += "\n  AND published_at_ms >= ?"
		arguments = append(arguments, *query.FromMS)
	}
	if query.ToMS != nil {
		statement += "\n  AND published_at_ms < ?"
		arguments = append(arguments, *query.ToMS)
	}
	if query.Position != nil {
		statement += "\n  AND (published_at_ms < ? OR (published_at_ms = ? AND id < ?))"
		arguments = append(arguments, query.Position.PublishedAtMS, query.Position.PublishedAtMS, query.Position.ID.String())
	}
	statement += "\nORDER BY published_at_ms DESC, id DESC\nLIMIT ?"
	arguments = append(arguments, limit)
	rows, err := store.queries.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query timeline entries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	entries := make([]domain.Entry, 0, limit)
	for rows.Next() {
		entry, scanErr := scanEntry(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan timeline entry: %w", scanErr)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate timeline entries: %w", err)
	}
	return entries, nil
}

func (store postStore) UpdateEntry(ctx context.Context, entry domain.Entry, contentText string) (bool, error) {
	result, err := store.queries.ExecContext(ctx, `
UPDATE entries
SET content_md = ?, content_text = ?, updated_at_ms = ?
WHERE id = ? AND kind = ?`, entry.ContentMD, contentText, domain.TimeToMillis(entry.UpdatedAt), entry.ID.String(), string(domain.KindMoment))
	if err != nil {
		return false, fmt.Errorf("update entry: %w", err)
	}
	return exactlyOneChanged(result, "update entry")
}

func (store postStore) DeleteEntry(ctx context.Context, id domain.ID) (bool, error) {
	result, err := store.queries.ExecContext(ctx, `DELETE FROM entries WHERE id = ? AND kind = ?`, id.String(), string(domain.KindMoment))
	if err != nil {
		return false, fmt.Errorf("delete entry: %w", err)
	}
	return exactlyOneChanged(result, "delete entry")
}

func (store postStore) IdempotencyRecord(ctx context.Context, scope, key string) (app.IdempotencyRecord, bool, error) {
	var (
		record     app.IdempotencyRecord
		resourceID string
		createdAt  int64
	)
	err := store.queries.QueryRowContext(ctx, `
SELECT scope, key, request_hash, response_resource_id, created_at_ms
FROM idempotency_keys
WHERE scope = ? AND key = ?`, scope, key).Scan(
		&record.Scope, &record.Key, &record.RequestHash, &resourceID, &createdAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return app.IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return app.IdempotencyRecord{}, false, fmt.Errorf("query idempotency record: %w", err)
	}
	id, err := domain.ParseID(resourceID)
	if err != nil {
		return app.IdempotencyRecord{}, false, domain.NewError(domain.CodeDataIntegrityFailed, "stored idempotency record has an invalid resource ID", nil)
	}
	record.ResponseResourceID = id
	record.CreatedAt = domain.TimeFromMillis(createdAt)
	return record, true, nil
}

func (store postStore) InsertIdempotencyRecord(ctx context.Context, record app.IdempotencyRecord) error {
	_, err := store.queries.ExecContext(ctx, `
INSERT INTO idempotency_keys (scope, key, request_hash, response_resource_id, created_at_ms)
VALUES (?, ?, ?, ?, ?)`, record.Scope, record.Key, record.RequestHash, record.ResponseResourceID.String(), domain.TimeToMillis(record.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert idempotency record: %w", err)
	}
	return nil
}

type entryScanner interface {
	Scan(...any) error
}

func scanEntry(scanner entryScanner) (domain.Entry, error) {
	var (
		idString                          string
		kind                              string
		content                           string
		createdAt, updatedAt, publishedAt int64
	)
	if err := scanner.Scan(&idString, &kind, &content, &createdAt, &updatedAt, &publishedAt); err != nil {
		return domain.Entry{}, err
	}
	id, err := domain.ParseID(idString)
	if err != nil {
		return domain.Entry{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored entry has an invalid ID", nil)
	}
	entry := domain.Entry{
		ID:          id,
		Kind:        domain.EntryKind(kind),
		ContentMD:   content,
		CreatedAt:   domain.TimeFromMillis(createdAt),
		UpdatedAt:   domain.TimeFromMillis(updatedAt),
		PublishedAt: domain.TimeFromMillis(publishedAt),
	}
	if err := entry.Validate(); err != nil {
		return domain.Entry{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored entry violates the domain contract", map[string]any{"post_id": id.String()})
	}
	return entry, nil
}

func exactlyOneChanged(result sql.Result, operation string) (bool, error) {
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("%s rows affected: %w", operation, err)
	}
	if changed > 1 {
		return false, fmt.Errorf("%s changed %d entries", operation, changed)
	}
	return changed == 1, nil
}
