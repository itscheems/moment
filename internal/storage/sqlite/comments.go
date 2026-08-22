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

// commentColumns is shared by every comment read so a column added to the
// table cannot reach one query and miss another.
const commentColumns = `comments.id, comments.entry_id, comments.parent_id, comments.author_kind, comments.author_name,
       comments.author_user_id, comments.reply_to_comment_id, comments.content_md, comments.created_at_ms,
       location.name, location.display_name, location.country_code,
       location.country, location.region, location.city, location.district,
       location.category, location.provider, location.provider_place_id,
       location.latitude_e7, location.longitude_e7, location.coordinate_system`

// Comments read oldest first, so the seek predicate walks forward with `>`.
// The timeline walks backward with `<`; mixing the two directions in one
// codebase is the easiest thing here to get wrong, and getting it wrong
// returns a plausible wrong page rather than an error.
const commentSeekPredicate = `(comments.created_at_ms > ? OR (comments.created_at_ms = ? AND comments.id > ?))`

const commentKeysetOrder = `ORDER BY comments.created_at_ms ASC, comments.id ASC LIMIT ?`

func commentSeekArguments(after *app.CommentPosition) (string, []any) {
	if after == nil {
		return "", nil
	}
	return " AND " + commentSeekPredicate, []any{after.CreatedAtMS, after.CreatedAtMS, after.ID.String()}
}

// ListCommentRoots returns one keyset page of top-level comments.
func (store commentStore) ListCommentRoots(ctx context.Context, entryID domain.ID, after *app.CommentPosition, limit int) ([]domain.Comment, error) {
	predicate, arguments := commentSeekArguments(after)
	query := `SELECT ` + commentColumns + `
FROM comments
LEFT JOIN comment_location AS location ON location.comment_id = comments.id
WHERE comments.entry_id = ? AND comments.parent_id IS NULL` + predicate + `
` + commentKeysetOrder
	return store.queryCommentPage(ctx, query, append(append([]any{entryID.String()}, arguments...), limit)...)
}

// ListCommentReplies returns one keyset page of replies under a single root.
func (store commentStore) ListCommentReplies(ctx context.Context, rootID domain.ID, after *app.CommentPosition, limit int) ([]domain.Comment, error) {
	predicate, arguments := commentSeekArguments(after)
	query := `SELECT ` + commentColumns + `
FROM comments
LEFT JOIN comment_location AS location ON location.comment_id = comments.id
WHERE comments.parent_id = ?` + predicate + `
` + commentKeysetOrder
	return store.queryCommentPage(ctx, query, append(append([]any{rootID.String()}, arguments...), limit)...)
}

func (store commentStore) queryCommentPage(ctx context.Context, query string, arguments ...any) ([]domain.Comment, error) {
	rows, err := store.queries.QueryContext(ctx, query, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query comment page: %w", err)
	}
	defer func() { _ = rows.Close() }()
	records := make([]domain.Comment, 0)
	for rows.Next() {
		record, scanErr := scanComment(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan comment page: %w", scanErr)
		}
		records = append(records, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate comment page: %w", err)
	}
	return records, nil
}

// ReplyCounts totals the replies under each root in one query.
//
// The client used to derive this by counting a fully loaded thread. Once only
// part of a thread is loaded that arithmetic is no longer available to it, and
// asking per root would put a query behind every row on the page.
func (store commentStore) ReplyCounts(ctx context.Context, rootIDs []domain.ID) (map[domain.ID]int, error) {
	counts := make(map[domain.ID]int, len(rootIDs))
	if len(rootIDs) == 0 {
		return counts, nil
	}
	placeholders := make([]string, len(rootIDs))
	arguments := make([]any, len(rootIDs))
	for index, id := range rootIDs {
		placeholders[index] = "?"
		arguments[index] = id.String()
		counts[id] = 0
	}
	rows, err := store.queries.QueryContext(ctx, `
SELECT parent_id, count(*) FROM comments
WHERE parent_id IN (`+strings.Join(placeholders, ", ")+`)
GROUP BY parent_id`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query reply counts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var rawID string
		var total int
		if err := rows.Scan(&rawID, &total); err != nil {
			return nil, fmt.Errorf("scan reply counts: %w", err)
		}
		id, parseErr := domain.ParseID(rawID)
		if parseErr != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored reply has an invalid parent ID", nil)
		}
		counts[id] = total
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate reply counts: %w", err)
	}
	return counts, nil
}

// CommentsByID loads a set of comments in one query.
//
// Reply targets are resolved through this: a reply names the comment it
// answers, and the answer has to be rendered even when that comment is not on
// the page the reader is looking at. Missing IDs are simply absent from the
// result.
func (store commentStore) CommentsByID(ctx context.Context, ids []domain.ID) (map[domain.ID]domain.Comment, error) {
	records := make(map[domain.ID]domain.Comment, len(ids))
	if len(ids) == 0 {
		return records, nil
	}
	placeholders := make([]string, len(ids))
	arguments := make([]any, len(ids))
	for index, id := range ids {
		placeholders[index] = "?"
		arguments[index] = id.String()
	}
	rows, err := store.queries.QueryContext(ctx, `
SELECT comments.id, comments.entry_id, comments.parent_id, comments.author_kind, comments.author_name,
       comments.author_user_id, comments.reply_to_comment_id, comments.content_md, comments.created_at_ms,
       location.name, location.display_name, location.country_code,
       location.country, location.region, location.city, location.district,
       location.category, location.provider, location.provider_place_id,
       location.latitude_e7, location.longitude_e7, location.coordinate_system
FROM comments
LEFT JOIN comment_location AS location ON location.comment_id = comments.id
WHERE comments.id IN (`+strings.Join(placeholders, ", ")+`)`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query comments by id: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		record, scanErr := scanComment(rows)
		if scanErr != nil {
			return nil, fmt.Errorf("scan comments by id: %w", scanErr)
		}
		records[record.ID] = record
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate comments by id: %w", err)
	}
	return records, nil
}

// SiteOwnerID reads the account that authors every owner comment.
func (store commentStore) SiteOwnerID(ctx context.Context) (string, error) {
	var ownerID string
	if err := store.queries.QueryRowContext(ctx, `SELECT owner_id FROM site_settings WHERE id = 1`).Scan(&ownerID); err != nil {
		return "", fmt.Errorf("read site owner: %w", err)
	}
	return ownerID, nil
}

// CommentAuthorIdentities resolves account IDs to the profile a comment reader
// needs.
//
// Moment has one account, so the only row that can match lives in the site
// singleton. The query is written as a set lookup rather than a single fetch
// because that is what the eventual user table needs, and because a caller
// that already batches its IDs does not have to change when it arrives.
func (store commentStore) CommentAuthorIdentities(ctx context.Context, ids []string) (map[string]app.CommentAuthorIdentity, error) {
	identities := make(map[string]app.CommentAuthorIdentity, len(ids))
	if len(ids) == 0 {
		return identities, nil
	}
	var ownerID, username, displayName string
	var avatar sql.NullString
	err := store.queries.QueryRowContext(ctx, `
SELECT owner_id, username, display_name, avatar_media_id FROM site_settings WHERE id = 1`).
		Scan(&ownerID, &username, &displayName, &avatar)
	if err != nil {
		return nil, fmt.Errorf("read comment author identity: %w", err)
	}
	for _, id := range ids {
		if id != ownerID {
			continue
		}
		identity := app.CommentAuthorIdentity{UserID: ownerID, Username: username, DisplayName: displayName}
		if avatar.Valid {
			value := avatar.String
			identity.AvatarMediaID = &value
		}
		identities[id] = identity
	}
	return identities, nil
}

func (store commentStore) CommentByID(ctx context.Context, id domain.ID) (domain.Comment, bool, error) {
	record, err := scanComment(store.queries.QueryRowContext(ctx, `
SELECT id, entry_id, parent_id, author_kind, author_name, author_user_id, reply_to_comment_id, content_md, created_at_ms
     , location.name, location.display_name, location.country_code,
       location.country, location.region, location.city, location.district,
       location.category, location.provider, location.provider_place_id,
       location.latitude_e7, location.longitude_e7, location.coordinate_system
FROM comments
LEFT JOIN comment_location AS location ON location.comment_id = comments.id
WHERE comments.id = ?`, id.String()))
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
SELECT child.id, child.entry_id, child.parent_id, child.author_kind, child.author_name, child.author_user_id, child.reply_to_comment_id, child.content_md, child.created_at_ms,
       location.name, location.display_name, location.country_code,
       location.country, location.region, location.city, location.district,
       location.category, location.provider, location.provider_place_id,
       location.latitude_e7, location.longitude_e7, location.coordinate_system
FROM comments AS child
LEFT JOIN comments AS root ON root.id = child.parent_id
LEFT JOIN comment_location AS location ON location.comment_id = child.id
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

func (store commentStore) PinnedCommentID(ctx context.Context, entryID domain.ID) (*domain.ID, error) {
	var raw string
	err := store.queries.QueryRowContext(ctx, `SELECT comment_id FROM comment_pins WHERE entry_id = ?`, entryID.String()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query pinned comment: %w", err)
	}
	id, err := domain.ParseID(raw)
	if err != nil {
		return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored pinned comment ID is invalid", map[string]any{"entry_id": entryID.String()})
	}
	return &id, nil
}

// CommentMediaUsagesByComment loads the attachments for a whole page at once.
//
// The caller used to ask per comment inside a loop. That was invisible while a
// thread always arrived whole and would have become a fixed per-page cost the
// moment threads started paginating.
func (store commentStore) CommentMediaUsagesByComment(ctx context.Context, commentIDs []domain.ID) (map[domain.ID][]app.MediaUsageProjection, error) {
	usagesByComment := make(map[domain.ID][]app.MediaUsageProjection, len(commentIDs))
	if len(commentIDs) == 0 {
		return usagesByComment, nil
	}
	placeholders := make([]string, len(commentIDs))
	arguments := make([]any, len(commentIDs))
	for index, id := range commentIDs {
		placeholders[index] = "?"
		arguments[index] = id.String()
		usagesByComment[id] = []app.MediaUsageProjection{}
	}
	rows, err := store.queries.QueryContext(ctx, `
SELECT usage.comment_id, usage.media_id, usage.position, usage.alt_text, media.mime_type, media.width, media.height
FROM comment_media AS usage
LEFT JOIN media ON media.id = usage.media_id
WHERE usage.comment_id IN (`+strings.Join(placeholders, ", ")+`)
ORDER BY usage.comment_id, usage.position`, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query comment media usages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var rawCommentID, rawMediaID string
		var position int
		var alt, mimeType sql.NullString
		var width, height sql.NullInt64
		if err := rows.Scan(&rawCommentID, &rawMediaID, &position, &alt, &mimeType, &width, &height); err != nil {
			return nil, fmt.Errorf("scan comment media usage: %w", err)
		}
		commentID, err := domain.ParseID(rawCommentID)
		if err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment media comment ID is invalid", nil)
		}
		mediaID, err := domain.ParseID(rawMediaID)
		if err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment media ID is invalid", map[string]any{"comment_id": commentID.String()})
		}
		if !mimeType.Valid || mimeType.String == "" || !validStoredMediaDimension(width) || !validStoredMediaDimension(height) {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment media dimensions are invalid", map[string]any{"comment_id": commentID.String(), "media_id": mediaID.String()})
		}
		usage := app.MediaUsageProjection{MediaID: mediaID, Position: position, MIMEType: mimeType.String, Width: uint32(width.Int64), Height: uint32(height.Int64)}
		if alt.Valid {
			value := alt.String
			usage.AltText = &value
		}
		usagesByComment[commentID] = append(usagesByComment[commentID], usage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate comment media usages: %w", err)
	}
	for commentID, usages := range usagesByComment {
		if err := domain.ValidateCommentMediaUsages(mediaProjectionInputs(usages)); err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment media usages violate the ordered contract", map[string]any{"comment_id": commentID.String()})
		}
	}
	return usagesByComment, nil
}

func (store commentStore) CommentMediaUsages(ctx context.Context, commentID domain.ID) ([]app.MediaUsageProjection, error) {
	rows, err := store.queries.QueryContext(ctx, `
SELECT usage.media_id, usage.position, usage.alt_text, media.mime_type, media.width, media.height
FROM comment_media AS usage
LEFT JOIN media ON media.id = usage.media_id
WHERE usage.comment_id = ?
ORDER BY usage.position`, commentID.String())
	if err != nil {
		return nil, fmt.Errorf("query comment media usages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	usages := make([]app.MediaUsageProjection, 0, domain.MaxCommentMediaUsages)
	for rows.Next() {
		var rawID string
		var position int
		var alt sql.NullString
		var mimeType sql.NullString
		var width, height sql.NullInt64
		if err := rows.Scan(&rawID, &position, &alt, &mimeType, &width, &height); err != nil {
			return nil, fmt.Errorf("scan comment media usage: %w", err)
		}
		id, err := domain.ParseID(rawID)
		if err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment media ID is invalid", map[string]any{"comment_id": commentID.String()})
		}
		if !mimeType.Valid || mimeType.String == "" || !validStoredMediaDimension(width) || !validStoredMediaDimension(height) {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment media dimensions are invalid", map[string]any{"comment_id": commentID.String(), "media_id": id.String()})
		}
		usage := app.MediaUsageProjection{MediaID: id, Position: position, MIMEType: mimeType.String, Width: uint32(width.Int64), Height: uint32(height.Int64)}
		if alt.Valid {
			value := alt.String
			usage.AltText = &value
		}
		usages = append(usages, usage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate comment media usages: %w", err)
	}
	if err := domain.ValidateCommentMediaUsages(mediaProjectionInputs(usages)); err != nil {
		return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment media usages violate the ordered contract", map[string]any{"comment_id": commentID.String()})
	}
	return usages, nil
}

func (store commentStore) ProjectMediaUsages(ctx context.Context, inputs []domain.EntryMediaUsage) ([]app.MediaUsageProjection, error) {
	return newPostStore(store.queries).ProjectMediaUsages(ctx, inputs)
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
INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, author_user_id, reply_to_comment_id, content_md, created_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`, comment.ID.String(), comment.EntryID.String(), nullableCommentID(comment.ParentID), string(comment.AuthorKind), comment.AuthorName, nullableString(comment.AuthorUserID), nullableCommentID(comment.ReplyToCommentID), comment.ContentMD, domain.TimeToMillis(comment.CreatedAt))
	if err != nil {
		if strings.Contains(err.Error(), "comment parent must be a root in the same entry") {
			return domain.NewError(domain.CodeCommentDepthExceeded, "replies must target a top-level comment on the same post", nil)
		}
		if strings.Contains(err.Error(), "comment reply target must be in the same conversation") {
			return domain.NewError(domain.CodeInvalidArgument, "a reply can only answer its own root or another reply under it", map[string]any{"field": "reply_to_comment_id"})
		}
		return fmt.Errorf("insert comment: %w", err)
	}
	return nil
}

func (store commentStore) ReplaceCommentLocation(ctx context.Context, commentID domain.ID, location *domain.Location) error {
	if location != nil {
		if err := location.Validate(); err != nil {
			return err
		}
	}
	if _, err := store.queries.ExecContext(ctx, `DELETE FROM comment_location WHERE comment_id = ?`, commentID.String()); err != nil {
		return fmt.Errorf("delete comment location: %w", err)
	}
	if location == nil {
		return nil
	}
	var latitude, longitude, system any
	if location.Point != nil {
		latitude = location.Point.LatitudeE7
		longitude = location.Point.LongitudeE7
		system = string(location.Point.System)
	}
	_, err := store.queries.ExecContext(ctx, `
INSERT INTO comment_location (
    comment_id, name, display_name, country_code, country, region, city,
    district, category, provider, provider_place_id, latitude_e7,
    longitude_e7, coordinate_system
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		commentID.String(), location.Name, location.DisplayName, location.CountryCode,
		location.Country, location.Region, location.City, location.District,
		location.Category, location.Provider, location.ProviderPlaceID,
		latitude, longitude, system,
	)
	if err != nil {
		return fmt.Errorf("insert comment location: %w", err)
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

func (store commentStore) ReplacePinnedComment(ctx context.Context, entryID domain.ID, commentID *domain.ID) error {
	if _, err := store.queries.ExecContext(ctx, `DELETE FROM comment_pins WHERE entry_id = ?`, entryID.String()); err != nil {
		return fmt.Errorf("clear pinned comment: %w", err)
	}
	if commentID == nil {
		return nil
	}
	if _, err := store.queries.ExecContext(ctx, `INSERT INTO comment_pins (entry_id, comment_id) VALUES (?, ?)`, entryID.String(), commentID.String()); err != nil {
		if strings.Contains(err.Error(), "pinned comment must be a root in the same entry") {
			return domain.NewError(domain.CodeInvalidArgument, "only a top-level comment on the same post can be pinned", map[string]any{"field": "comment_id"})
		}
		return fmt.Errorf("insert pinned comment: %w", err)
	}
	return nil
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
	var parent, authorUserID, replyTarget sql.NullString
	var createdAt int64
	var locationName, locationDisplayName, countryCode, country, region, city, district, category, provider, providerPlaceID sql.NullString
	var latitude, longitude sql.NullInt64
	var coordinateSystem sql.NullString
	if err := scanner.Scan(
		&idValue, &entryValue, &parent, &authorKind, &authorName, &authorUserID, &replyTarget, &content, &createdAt,
		&locationName, &locationDisplayName, &countryCode, &country, &region, &city,
		&district, &category, &provider, &providerPlaceID, &latitude, &longitude,
		&coordinateSystem,
	); err != nil {
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
	var location *domain.Location
	if locationName.Valid {
		if !locationDisplayName.Valid || !countryCode.Valid || !country.Valid || !region.Valid || !city.Valid ||
			!district.Valid || !category.Valid || !provider.Valid || !providerPlaceID.Valid ||
			latitude.Valid != longitude.Valid || latitude.Valid != coordinateSystem.Valid {
			return domain.Comment{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment location is incomplete", map[string]any{"comment_id": id.String()})
		}
		location = &domain.Location{
			Name: locationName.String, DisplayName: locationDisplayName.String,
			CountryCode: countryCode.String, Country: country.String, Region: region.String,
			City: city.String, District: district.String, Category: category.String,
			Provider: provider.String, ProviderPlaceID: providerPlaceID.String,
		}
		if latitude.Valid {
			location.Point = &domain.GeoPoint{LatitudeE7: latitude.Int64, LongitudeE7: longitude.Int64, System: domain.CoordinateSystem(coordinateSystem.String)}
		}
	}
	var authorUser *string
	if authorUserID.Valid {
		value := authorUserID.String
		authorUser = &value
	}
	var replyTo *domain.ID
	if replyTarget.Valid {
		parsed, parseErr := domain.ParseID(replyTarget.String)
		if parseErr != nil {
			return domain.Comment{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment has an invalid reply target", map[string]any{"comment_id": id.String()})
		}
		replyTo = &parsed
	}
	record := domain.Comment{ID: id, EntryID: entryID, ParentID: parentID, AuthorKind: domain.CommentAuthorKind(authorKind), AuthorName: authorName, AuthorUserID: authorUser, ReplyToCommentID: replyTo, ContentMD: content, Location: location, CreatedAt: domain.TimeFromMillis(createdAt)}
	if err := record.Validate(); err != nil {
		return domain.Comment{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored comment violates the comment contract", map[string]any{"comment_id": id.String()})
	}
	return record, nil
}

func nullableString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableCommentID(id *domain.ID) any {
	if id == nil {
		return nil
	}
	return id.String()
}
