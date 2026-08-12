package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

// CommentRepository adapts the active SQLite generation to comment use cases.
// Ordinary writes hold maintenance.lock shared so a generation replacement
// cannot race comment authority.
type CommentRepository struct {
	transactions Transactor
	locks        *runtimefs.Locker
	maintenance  *runtimefs.Lease
	development  *runtimefs.LeaseSet
	root         string
}

// NewDevelopmentCommentRepository is the explicit fixture boundary for one
// fully locked development generation build.
func NewDevelopmentCommentRepository(transactions Transactor, root string, held *runtimefs.LeaseSet) (*CommentRepository, error) {
	if transactions == nil || held == nil || !held.BelongsTo(root) ||
		!held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.ServeLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.MaintenanceLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.MediaLock, runtimefs.Exclusive) {
		return nil, runtimefs.ErrInsufficientLocks
	}
	return &CommentRepository{transactions: transactions, development: held, root: root}, nil
}

var _ app.CommentTransactions = (*CommentRepository)(nil)

// NewCommentRepository constructs the production comment adapter.
func NewCommentRepository(transactions Transactor, locks *runtimefs.Locker) (*CommentRepository, error) {
	if transactions == nil {
		return nil, errors.New("SQLite transactions are required")
	}
	if locks == nil {
		return nil, errors.New("runtime locks are required")
	}
	return &CommentRepository{transactions: transactions, locks: locks}, nil
}

// NewCommentRepositoryWithinMaintenance reuses a caller-held authoritative
// ordinary-write lease.
func NewCommentRepositoryWithinMaintenance(transactions Transactor, maintenance *runtimefs.Lease, locks *runtimefs.Locker) (*CommentRepository, error) {
	if transactions == nil {
		return nil, errors.New("SQLite transactions are required")
	}
	if !maintenance.Holds(runtimefs.MaintenanceLock, runtimefs.Shared) {
		return nil, errors.New("shared maintenance lease is required")
	}
	if locks == nil {
		return nil, errors.New("runtime locks are required")
	}
	return &CommentRepository{transactions: transactions, maintenance: maintenance, locks: locks}, nil
}

func (repository *CommentRepository) Read(ctx context.Context, operation func(app.CommentStore) error) error {
	if operation == nil {
		return errors.New("comment read operation is required")
	}
	return repository.transactions.ReadTx(ctx, func(queries Queries) error { return operation(newCommentStore(queries)) })
}

func (repository *CommentRepository) Write(ctx context.Context, operation func(app.CommentStore) error) error {
	if operation == nil {
		return errors.New("comment write operation is required")
	}
	if repository.development != nil {
		if !repository.development.BelongsTo(repository.root) ||
			!repository.development.Holds(runtimefs.MaintenanceLock, runtimefs.Exclusive) ||
			!repository.development.Holds(runtimefs.MediaLock, runtimefs.Exclusive) {
			return runtimefs.ErrInsufficientLocks
		}
		return repository.write(ctx, operation)
	}
	if repository.maintenance != nil {
		if !repository.maintenance.Holds(runtimefs.MaintenanceLock, runtimefs.Shared) {
			return errors.New("comment maintenance lease is no longer held")
		}
		mediaLease, err := repository.locks.Acquire(ctx, runtimefs.MediaLock, runtimefs.Shared)
		if err != nil {
			return fmt.Errorf("coordinate comment media references: %w", err)
		}
		defer func() { _ = mediaLease.Release() }()
		return repository.write(ctx, operation)
	}
	leases, err := repository.locks.AcquireSet(ctx,
		runtimefs.LockRequest{Name: runtimefs.MaintenanceLock, Mode: runtimefs.Shared},
		runtimefs.LockRequest{Name: runtimefs.MediaLock, Mode: runtimefs.Shared},
	)
	if err != nil {
		return fmt.Errorf("coordinate comment write: %w", err)
	}
	defer func() { _ = leases.Release() }()
	return repository.write(ctx, operation)
}

func (repository *CommentRepository) write(ctx context.Context, operation func(app.CommentStore) error) error {
	return repository.transactions.WriteTx(ctx, func(queries Queries) error { return operation(newCommentStore(queries)) })
}

type commentStore struct {
	queries Queries
	mediaReferenceStore
}

func newCommentStore(queries Queries) commentStore {
	return commentStore{queries: queries, mediaReferenceStore: newMediaReferenceStore(queries)}
}

var _ app.CommentStore = commentStore{}

func (store commentStore) EntryExists(ctx context.Context, id domain.ID) (bool, error) {
	var exists int
	err := store.queries.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM entries WHERE id = ? AND kind = ?)`, id.String(), string(domain.KindMoment)).Scan(&exists)
	if err != nil {
		return false, fmt.Errorf("check comment post: %w", err)
	}
	return exists == 1, nil
}

func (store commentStore) CommentsEnabled(ctx context.Context) (bool, error) {
	var enabled int
	if err := store.queries.QueryRowContext(ctx, `SELECT comments_enabled FROM site_settings WHERE id = 1`).Scan(&enabled); err != nil {
		return false, fmt.Errorf("read comment setting: %w", err)
	}
	return enabled == 1, nil
}

func (store commentStore) CommentByID(ctx context.Context, id domain.ID) (domain.Comment, bool, error) {
	record, err := scanComment(store.queries.QueryRowContext(ctx, `
SELECT id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms
FROM comments
WHERE id = ?`, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Comment{}, false, nil
	}
	if err != nil {
		return domain.Comment{}, false, fmt.Errorf("query comment: %w", err)
	}
	return record, true, nil
}

func (store commentStore) ListComments(ctx context.Context, entryID domain.ID) ([]domain.Comment, error) {
	rows, err := store.queries.QueryContext(ctx, `
SELECT child.id, child.entry_id, child.parent_id, child.author_kind, child.author_name, child.content_md, child.created_at_ms
FROM comments AS child
LEFT JOIN comments AS root ON root.id = child.parent_id
WHERE child.entry_id = ?
ORDER BY
    COALESCE(root.created_at_ms, child.created_at_ms) ASC,
    COALESCE(root.id, child.id) ASC,
    CASE WHEN child.parent_id IS NULL THEN 0 ELSE 1 END ASC,
    child.created_at_ms ASC,
    child.id ASC`, entryID.String())
	if err != nil {
		return nil, fmt.Errorf("query comment thread: %w", err)
	}
	defer func() { _ = rows.Close() }()
	records := make([]domain.Comment, 0)
	for rows.Next() {
		record, scanErr := scanComment(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan comment thread: %w", scanErr)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate comment thread: %w", err)
	}
	return records, nil
}

func (store commentStore) CommentMediaUsages(ctx context.Context, commentID domain.ID) ([]domain.EntryMediaUsage, error) {
	rows, err := store.queries.QueryContext(ctx, `
SELECT media_id, position, alt_text
FROM comment_media
WHERE comment_id = ?
ORDER BY position`, commentID.String())
	if err != nil {
		return nil, fmt.Errorf("query comment media usages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	usages := make([]domain.EntryMediaUsage, 0, domain.MaxCommentMediaUsages)
	for rows.Next() {
		var rawID string
		var position int
		var alt sql.NullString
		if err := rows.Scan(&rawID, &position, &alt); err != nil {
			return nil, fmt.Errorf("scan comment media usage: %w", err)
		}
		id, err := domain.ParseID(rawID)
		if err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment media ID is invalid", map[string]any{"comment_id": commentID.String()})
		}
		usage := domain.EntryMediaUsage{MediaID: id, Position: position}
		if alt.Valid {
			value := alt.String
			usage.AltText = &value
		}
		usages = append(usages, usage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate comment media usages: %w", err)
	}
	if err := domain.ValidateCommentMediaUsages(usages); err != nil {
		return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment media usages violate the ordered contract", map[string]any{"comment_id": commentID.String()})
	}
	return usages, nil
}

func (store commentStore) ReplaceCommentMediaUsages(ctx context.Context, commentID domain.ID, usages []domain.EntryMediaUsage) error {
	if err := domain.ValidateCommentMediaUsages(usages); err != nil {
		return err
	}
	var aggregateBytes int64
	for _, usage := range usages {
		var byteSize int64
		if err := store.queries.QueryRowContext(ctx, `SELECT byte_size FROM media WHERE id = ?`, usage.MediaID.String()).Scan(&byteSize); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.NewError(domain.CodeMediaNotFound, fmt.Sprintf("media %s does not exist", usage.MediaID), map[string]any{"media_id": usage.MediaID.String()})
			}
			return fmt.Errorf("validate comment media usage: %w", err)
		}
		if byteSize > domain.MaxCommentMediaBytes-aggregateBytes {
			return domain.NewError(domain.CodeMediaTooLarge, "comment media exceed the 40 MiB aggregate budget", map[string]any{"field": "media", "max_bytes": domain.MaxCommentMediaBytes})
		}
		aggregateBytes += byteSize
	}
	if _, err := store.queries.ExecContext(ctx, `DELETE FROM comment_media WHERE comment_id = ?`, commentID.String()); err != nil {
		return fmt.Errorf("clear comment media usages: %w", err)
	}
	for _, usage := range usages {
		var alt any
		if usage.AltText != nil {
			alt = *usage.AltText
		}
		if _, err := store.queries.ExecContext(ctx, `
INSERT INTO comment_media (comment_id, media_id, position, alt_text)
VALUES (?, ?, ?, ?)`, commentID.String(), usage.MediaID.String(), usage.Position, alt); err != nil {
			return fmt.Errorf("insert comment media usage: %w", err)
		}
	}
	return nil
}

func (store commentStore) CommentCascadeMediaIDs(ctx context.Context, commentID domain.ID) ([]domain.ID, error) {
	rows, err := store.queries.QueryContext(ctx, `
SELECT DISTINCT usage.media_id
FROM comment_media AS usage
JOIN comments AS comment ON comment.id = usage.comment_id
WHERE comment.id = ? OR comment.parent_id = ?
ORDER BY usage.media_id`, commentID.String(), commentID.String())
	if err != nil {
		return nil, fmt.Errorf("query comment cascade media: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []domain.ID
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, fmt.Errorf("scan comment cascade media: %w", err)
		}
		id, err := domain.ParseID(raw)
		if err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment media ID is invalid", nil)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate comment cascade media: %w", err)
	}
	return ids, nil
}

func (store commentStore) InsertComment(ctx context.Context, comment domain.Comment) error {
	_, err := store.queries.ExecContext(ctx, `
INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?)`, comment.ID.String(), comment.EntryID.String(), nullableCommentID(comment.ParentID), string(comment.AuthorKind), comment.AuthorName, comment.ContentMD, domain.TimeToMillis(comment.CreatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "comment parent must be a root in the same entry") {
			return domain.NewError(domain.CodeCommentDepthExceeded, "replies must target a top-level comment on the same post", nil)
		}
		return fmt.Errorf("insert comment: %w", err)
	}
	return nil
}

func (store commentStore) CountCommentReplies(ctx context.Context, id domain.ID) (int, error) {
	var count int
	if err := store.queries.QueryRowContext(ctx, `SELECT count(*) FROM comments WHERE parent_id = ?`, id.String()).Scan(&count); err != nil {
		return 0, fmt.Errorf("count comment replies: %w", err)
	}
	return count, nil
}

func (store commentStore) DeleteComment(ctx context.Context, id domain.ID) (bool, error) {
	result, err := store.queries.ExecContext(ctx, `DELETE FROM comments WHERE id = ?`, id.String())
	if err != nil {
		return false, fmt.Errorf("delete comment: %w", err)
	}
	return exactlyOneChanged(result, "delete comment")
}

func (store commentStore) IdempotencyRecord(ctx context.Context, scope, key string) (app.IdempotencyRecord, bool, error) {
	var record app.IdempotencyRecord
	var resourceID string
	var createdAt int64
	err := store.queries.QueryRowContext(ctx, `
SELECT scope, key, request_hash, response_resource_id, created_at_ms
FROM idempotency_keys
WHERE scope = ? AND key = ?`, scope, key).Scan(&record.Scope, &record.Key, &record.RequestHash, &resourceID, &createdAt)
	if errors.Is(err, sql.ErrNoRows) {
		return app.IdempotencyRecord{}, false, nil
	}
	if err != nil {
		return app.IdempotencyRecord{}, false, fmt.Errorf("query comment idempotency record: %w", err)
	}
	id, err := domain.ParseID(resourceID)
	if err != nil {
		return app.IdempotencyRecord{}, false, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment idempotency record has an invalid resource ID", nil)
	}
	record.ResponseResourceID = id
	record.CreatedAt = domain.TimeFromMillis(createdAt)
	return record, true, nil
}

func (store commentStore) InsertIdempotencyRecord(ctx context.Context, record app.IdempotencyRecord) error {
	_, err := store.queries.ExecContext(ctx, `
INSERT INTO idempotency_keys (scope, key, request_hash, response_resource_id, created_at_ms)
VALUES (?, ?, ?, ?, ?)`, record.Scope, record.Key, record.RequestHash, record.ResponseResourceID.String(), domain.TimeToMillis(record.CreatedAt))
	if err != nil {
		return fmt.Errorf("insert comment idempotency record: %w", err)
	}
	return nil
}

type commentScanner interface{ Scan(...any) error }

func scanComment(scanner commentScanner) (domain.Comment, error) {
	var idValue, entryValue, authorKind, authorName, content string
	var parent sql.NullString
	var createdAt int64
	if err := scanner.Scan(&idValue, &entryValue, &parent, &authorKind, &authorName, &content, &createdAt); err != nil {
		return domain.Comment{}, err
	}
	id, err := domain.ParseID(idValue)
	if err != nil {
		return domain.Comment{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment has an invalid ID", nil)
	}
	entryID, err := domain.ParseID(entryValue)
	if err != nil {
		return domain.Comment{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment has an invalid post ID", map[string]any{"comment_id": id.String()})
	}
	var parentID *domain.ID
	if parent.Valid {
		parsed, parseErr := domain.ParseID(parent.String)
		if parseErr != nil {
			return domain.Comment{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment has an invalid parent ID", map[string]any{"comment_id": id.String()})
		}
		parentID = &parsed
	}
	record := domain.Comment{ID: id, EntryID: entryID, ParentID: parentID, AuthorKind: domain.CommentAuthorKind(authorKind), AuthorName: authorName, ContentMD: content, CreatedAt: domain.TimeFromMillis(createdAt)}
	if err := record.Validate(); err != nil {
		return domain.Comment{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment violates the comment contract", map[string]any{"comment_id": id.String()})
	}
	return record, nil
}

func nullableCommentID(id *domain.ID) any {
	if id == nil {
		return nil
	}
	return id.String()
}
