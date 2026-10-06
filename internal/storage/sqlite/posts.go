package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"math"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

// PostRepository adapts SQLite transactions to the post application's ports.
type PostRepository struct {
	transactions Transactor
	locks        *runtimefs.Locker
	maintenance  *runtimefs.Lease
	development  *runtimefs.LeaseSet
	root         string
}

// NewDevelopmentPostRepository is the explicit fixture boundary for a caller
// that continuously owns the complete destructive development lock set.
func NewDevelopmentPostRepository(transactions Transactor, root string, held *runtimefs.LeaseSet) (*PostRepository, error) {
	if transactions == nil || held == nil || !held.BelongsTo(root) ||
		!held.Holds(runtimefs.ImportLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.ServeLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.MaintenanceLock, runtimefs.Exclusive) ||
		!held.Holds(runtimefs.MediaLock, runtimefs.Exclusive) {
		return nil, runtimefs.ErrInsufficientLocks
	}
	return &PostRepository{transactions: transactions, development: held, root: root}, nil
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
	if repository.development != nil {
		if !repository.development.BelongsTo(repository.root) ||
			!repository.development.Holds(runtimefs.MaintenanceLock, runtimefs.Exclusive) ||
			!repository.development.Holds(runtimefs.MediaLock, runtimefs.Exclusive) {
			return runtimefs.ErrInsufficientLocks
		}
		return repository.write(ctx, operation)
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
INSERT INTO entries (id, kind, content_md, visibility, content_text, created_at_ms, updated_at_ms, published_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID.String(), string(entry.Kind), entry.ContentMD, string(entry.Visibility), contentText,
		domain.TimeToMillis(entry.CreatedAt), domain.TimeToMillis(entry.UpdatedAt), domain.TimeToMillis(entry.PublishedAt),
	)
	if err != nil {
		return fmt.Errorf("insert entry: %w", err)
	}
	return nil
}

func (store postStore) EntryByID(ctx context.Context, id domain.ID) (domain.Entry, bool, error) {
	entry, err := scanEntry(store.queries.QueryRowContext(ctx, `
SELECT id, kind, content_md, visibility, created_at_ms, updated_at_ms, published_at_ms, reposted_at_ms, edited_at_ms
FROM entries
WHERE id = ?`, id.String()))
	if errors.Is(err, sql.ErrNoRows) {
		return domain.Entry{}, false, nil
	}
	if err != nil {
		return domain.Entry{}, false, fmt.Errorf("query entry: %w", err)
	}
	return entry, true, nil
}

func (store postStore) ListEntries(ctx context.Context, query app.TimelineQuery, limit int) ([]app.TimelineEntry, error) {
	statement, arguments := timelineEntriesQuery(query, limit)
	rows, err := store.queries.QueryContext(ctx, statement, arguments...)
	if err != nil {
		return nil, fmt.Errorf("query timeline entries: %w", err)
	}
	defer func() { _ = rows.Close() }()

	entries := make([]app.TimelineEntry, 0, limit)
	for rows.Next() {
		entry, scanErr := scanTimelineEntry(rows)
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

// Each projection seeks and reads at most limit rows before the bounded merge.
// A kind equality is present only when requested so SQLite can use that prefix.
func timelineEntriesQuery(query app.TimelineQuery, limit int) (string, []any) {
	visible, visibleArguments := visibilityPredicate(query.Viewer, "visibility")
	order, compare := "DESC", "<"
	newer := query.Direction == app.TimelineNewer
	if newer {
		order, compare = "ASC", ">"
	}
	var branches []string
	var arguments []any
	for rank, column := range []string{"published_at_ms", "reposted_at_ms"} {
		branch := fmt.Sprintf(`SELECT id, kind, content_md, visibility, created_at_ms, updated_at_ms,
 published_at_ms, reposted_at_ms, edited_at_ms, %s AS timeline_at_ms, %d AS timeline_kind
 FROM entries WHERE %s`, column, rank, visible)
		arguments = append(arguments, visibleArguments...)
		if rank == 1 {
			branch += " AND reposted_at_ms IS NOT NULL"
		}
		if query.Kind != "" {
			branch += " AND kind = ?"
			arguments = append(arguments, string(query.Kind))
		}
		if query.FromMS != nil {
			branch += " AND published_at_ms >= ?"
			arguments = append(arguments, *query.FromMS)
		}
		if query.ToMS != nil {
			branch += " AND published_at_ms < ?"
			arguments = append(arguments, *query.ToMS)
		}
		if pos := query.Position; pos != nil {
			if pos.Boundary {
				op := "<"
				if newer {
					op = ">="
				}
				branch += fmt.Sprintf(" AND %s %s ?", column, op)
				arguments = append(arguments, pos.TimelineAtMS)
			} else if rank == timelineKindRank(pos.Kind) {
				branch += fmt.Sprintf(" AND (%s, id) %s (?, ?)", column, compare)
				arguments = append(arguments, pos.TimelineAtMS, pos.ID.String())
			} else {
				op := compare
				if (newer && rank > timelineKindRank(pos.Kind)) || (!newer && rank < timelineKindRank(pos.Kind)) {
					op += "="
				}
				branch += fmt.Sprintf(" AND %s %s ?", column, op)
				arguments = append(arguments, pos.TimelineAtMS)
			}
		}
		branch += fmt.Sprintf(" ORDER BY %s %s, id %s LIMIT ?", column, order, order)
		arguments = append(arguments, limit)
		branches = append(branches, branch)
	}
	statement := "WITH originals AS (" + branches[0] + "), reposts AS (" + branches[1] + `), timeline_entries AS (
 SELECT * FROM originals UNION ALL SELECT * FROM reposts)
 SELECT * FROM timeline_entries ORDER BY timeline_at_ms ` + order + ", timeline_kind " + order + ", id " + order + " LIMIT ?"
	return statement, append(arguments, limit)
}

func timelineKindRank(kind app.TimelineItemKind) int {
	if kind == app.TimelineItemRepost {
		return 1
	}
	return 0
}

func (store postStore) CommentCounts(ctx context.Context, ids []domain.ID) (map[domain.ID]int, error) {
	return queryCommentCounts(ctx, store.queries, ids)
}

func (store postStore) EntryMediaUsages(ctx context.Context, entryID domain.ID) ([]app.MediaUsageProjection, error) {
	rows, err := store.queries.QueryContext(ctx, `
SELECT usage.media_id, usage.position, usage.alt_text, media.mime_type, media.width, media.height
FROM entry_media AS usage
LEFT JOIN media ON media.id = usage.media_id
WHERE usage.entry_id = ?
ORDER BY usage.position`, entryID.String())
	if err != nil {
		return nil, fmt.Errorf("query entry media usages: %w", err)
	}
	defer func() { _ = rows.Close() }()
	usages := make([]app.MediaUsageProjection, 0, domain.MaxEntryMediaUsages)
	for rows.Next() {
		var rawID string
		var position int
		var alt sql.NullString
		var mimeType sql.NullString
		var width, height sql.NullInt64
		if err := rows.Scan(&rawID, &position, &alt, &mimeType, &width, &height); err != nil {
			return nil, fmt.Errorf("scan entry media usage: %w", err)
		}
		id, err := domain.ParseID(rawID)
		if err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored entry media ID is invalid", map[string]any{"post_id": entryID.String()})
		}
		if !mimeType.Valid || mimeType.String == "" || !validStoredMediaDimension(width) || !validStoredMediaDimension(height) {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored entry media dimensions are invalid", map[string]any{"post_id": entryID.String(), "media_id": id.String()})
		}
		usage := app.MediaUsageProjection{MediaID: id, Position: position, MIMEType: mimeType.String, Width: uint32(width.Int64), Height: uint32(height.Int64)}
		if alt.Valid {
			value := alt.String
			usage.AltText = &value
		}
		usages = append(usages, usage)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entry media usages: %w", err)
	}
	if err := domain.ValidateEntryMediaUsages(mediaProjectionInputs(usages)); err != nil {
		return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored entry media usages violate the ordered contract", map[string]any{"post_id": entryID.String()})
	}
	return usages, nil
}

func (store postStore) ProjectMediaUsages(ctx context.Context, inputs []domain.EntryMediaUsage) ([]app.MediaUsageProjection, error) {
	usages := make([]app.MediaUsageProjection, 0, len(inputs))
	for _, input := range inputs {
		var mimeType string
		var width, height int64
		if err := store.queries.QueryRowContext(ctx, `SELECT mime_type, width, height FROM media WHERE id = ?`, input.MediaID.String()).Scan(&mimeType, &width, &height); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil, domain.NewError(domain.CodeMediaNotFound, fmt.Sprintf("media %s does not exist", input.MediaID), map[string]any{"media_id": input.MediaID.String()})
			}
			return nil, fmt.Errorf("project media usage: %w", err)
		}
		if width <= 0 || width > math.MaxUint32 || height <= 0 || height > math.MaxUint32 {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored media dimensions are invalid", map[string]any{"media_id": input.MediaID.String()})
		}
		usage := app.MediaUsageProjection{
			MediaID:  input.MediaID,
			Position: input.Position,
			AltText:  input.AltText,
			MIMEType: mimeType,
			Width:    uint32(width),
			Height:   uint32(height),
		}
		if input.AltText != nil {
			alt := *input.AltText
			usage.AltText = &alt
		}
		usages = append(usages, usage)
	}
	return usages, nil
}

func validStoredMediaDimension(value sql.NullInt64) bool {
	return value.Valid && value.Int64 > 0 && value.Int64 <= math.MaxUint32
}

func mediaProjectionInputs(usages []app.MediaUsageProjection) []domain.EntryMediaUsage {
	inputs := make([]domain.EntryMediaUsage, len(usages))
	for index, usage := range usages {
		inputs[index] = domain.EntryMediaUsage{MediaID: usage.MediaID, Position: usage.Position, AltText: usage.AltText}
	}
	return inputs
}

func (store postStore) ReplaceEntryMediaUsages(ctx context.Context, entryID domain.ID, usages []domain.EntryMediaUsage) error {
	if err := domain.ValidateEntryMediaUsages(usages); err != nil {
		return err
	}
	return store.replaceEntryMedia(ctx, entryID, usages, domain.MaxEntryMediaBytes)
}

// checkMediaUsages refuses a projection that names a picture this site does not
// hold, or one that together exceeds what the caller allows.
//
// Every table that projects media has a foreign key to media, so an absent
// picture would be caught either way -- as a constraint violation, reported as
// an INTERNAL storage failure telling the client to retry something that can
// never work. Naming it here says whose mistake it is.
func (store postStore) checkMediaUsages(ctx context.Context, usages []domain.EntryMediaUsage, maxBytes int64) error {
	var totalBytes int64
	for _, usage := range usages {
		var byteSize int64
		if err := store.queries.QueryRowContext(ctx, `SELECT byte_size FROM media WHERE id = ?`, usage.MediaID.String()).Scan(&byteSize); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return domain.NewError(domain.CodeMediaNotFound, fmt.Sprintf("media %s does not exist", usage.MediaID), map[string]any{"media_id": usage.MediaID.String()})
			}
			return fmt.Errorf("validate entry media usage: %w", err)
		}
		totalBytes += byteSize
		if totalBytes > maxBytes {
			return domain.NewError(domain.CodeMediaTooLarge, "post media exceed the aggregate byte budget", map[string]any{"field": "media", "max_bytes": maxBytes})
		}
	}
	return nil
}

// checkMediaPresent answers the same question for a single optional picture,
// such as the one an article puts on its card.
func (store postStore) checkMediaPresent(ctx context.Context, id *domain.ID) error {
	if id == nil {
		return nil
	}
	var present int
	if err := store.queries.QueryRowContext(ctx,
		`SELECT 1 FROM media WHERE id = ?`, id.String()).Scan(&present); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return domain.NewError(domain.CodeMediaNotFound, fmt.Sprintf("media %s does not exist", id), map[string]any{"media_id": id.String()})
		}
		return fmt.Errorf("validate media reference: %w", err)
	}
	return nil
}

// replaceEntryMedia writes an already-validated usage list. The two callers
// differ only in how many pictures and how many bytes they allow, so the write
// itself stays in one place.
func (store postStore) replaceEntryMedia(ctx context.Context, entryID domain.ID, usages []domain.EntryMediaUsage, maxBytes int64) error {
	if err := store.checkMediaUsages(ctx, usages, maxBytes); err != nil {
		return err
	}
	if _, err := store.queries.ExecContext(ctx, `DELETE FROM entry_media WHERE entry_id = ?`, entryID.String()); err != nil {
		return fmt.Errorf("clear entry media usages: %w", err)
	}
	for _, usage := range usages {
		var alt any
		if usage.AltText != nil {
			alt = *usage.AltText
		}
		if _, err := store.queries.ExecContext(ctx, `
INSERT INTO entry_media (entry_id, media_id, position, alt_text)
VALUES (?, ?, ?, ?)`, entryID.String(), usage.MediaID.String(), usage.Position, alt); err != nil {
			return fmt.Errorf("insert entry media usage: %w", err)
		}
	}
	return nil
}

func (store postStore) EntryLocation(ctx context.Context, entryID domain.ID) (*domain.Location, error) {
	var (
		location  domain.Location
		latitude  sql.NullInt64
		longitude sql.NullInt64
		system    sql.NullString
	)
	err := store.queries.QueryRowContext(ctx, `
SELECT name, display_name, country_code, country, region, city, district,
       category, provider, provider_place_id, latitude_e7, longitude_e7,
       coordinate_system
FROM entry_location
WHERE entry_id = ?`, entryID.String()).Scan(
		&location.Name, &location.DisplayName, &location.CountryCode, &location.Country,
		&location.Region, &location.City, &location.District, &location.Category,
		&location.Provider, &location.ProviderPlaceID, &latitude, &longitude, &system,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("query entry location: %w", err)
	}
	if latitude.Valid != longitude.Valid || latitude.Valid != system.Valid {
		return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored entry location has incomplete coordinates", map[string]any{"post_id": entryID.String()})
	}
	if latitude.Valid {
		location.Point = &domain.GeoPoint{
			LatitudeE7: latitude.Int64, LongitudeE7: longitude.Int64,
			System: domain.CoordinateSystem(system.String),
		}
	}
	if err := location.Validate(); err != nil {
		return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored entry location violates the domain contract", map[string]any{"post_id": entryID.String()})
	}
	return &location, nil
}

func (store postStore) ReplaceEntryLocation(ctx context.Context, entryID domain.ID, location *domain.Location) error {
	if location != nil {
		if err := location.Validate(); err != nil {
			return err
		}
	}
	if _, err := store.queries.ExecContext(ctx, `DELETE FROM entry_location WHERE entry_id = ?`, entryID.String()); err != nil {
		return fmt.Errorf("delete entry location: %w", err)
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
INSERT INTO entry_location (
    entry_id, name, display_name, country_code, country, region, city, district,
    category, provider, provider_place_id, latitude_e7, longitude_e7,
    coordinate_system
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entryID.String(), location.Name, location.DisplayName, location.CountryCode,
		location.Country, location.Region, location.City, location.District,
		location.Category, location.Provider, location.ProviderPlaceID,
		latitude, longitude, system,
	)
	if err != nil {
		return fmt.Errorf("insert entry location: %w", err)
	}
	return nil
}

func (store postStore) EntryLinkCards(ctx context.Context, entryID domain.ID) ([]app.LinkCardProjection, error) {
	// Join the thumbnail so the caller receives authoritative geometry in one
	// read: the browser reserves layout space from it before the image loads.
	rows, err := store.queries.QueryContext(ctx, `
SELECT card.position, card.source_url, card.canonical_url, card.provider,
       card.card_kind, card.title, card.description, card.site_name,
       card.author_name, card.duration_seconds, card.thumbnail_media_id,
       card.embed_url, card.captured_at_ms, card.view_count,
       card.comment_count, card.like_count, card.favicon_media_id,
       favicon.mime_type,
       thumbnail.mime_type, thumbnail.width, thumbnail.height
FROM entry_link_card AS card
LEFT JOIN media AS thumbnail ON thumbnail.id = card.thumbnail_media_id
LEFT JOIN media AS favicon ON favicon.id = card.favicon_media_id
WHERE card.entry_id = ?
ORDER BY card.position`, entryID.String())
	if err != nil {
		return nil, fmt.Errorf("query entry link cards: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var cards []app.LinkCardProjection
	for rows.Next() {
		var (
			card      domain.LinkCard
			duration  sql.NullInt64
			thumbnail sql.NullString
			mimeType  sql.NullString
			width     sql.NullInt64
			height    sql.NullInt64
			views     sql.NullInt64
			comments  sql.NullInt64
			likes     sql.NullInt64
			favicon   sql.NullString
			faviconMT sql.NullString
		)
		if err := rows.Scan(
			&card.Position, &card.SourceURL, &card.CanonicalURL, &card.Provider,
			&card.Kind, &card.Title, &card.Description, &card.SiteName,
			&card.AuthorName, &duration, &thumbnail, &card.EmbedURL, &card.CapturedAt,
			&views, &comments, &likes, &favicon, &faviconMT,
			&mimeType, &width, &height,
		); err != nil {
			return nil, fmt.Errorf("scan entry link card: %w", err)
		}
		if duration.Valid {
			seconds := int(duration.Int64)
			card.DurationSeconds = &seconds
		}
		card.ThumbnailMediaID = thumbnail.String
		card.FaviconMediaID = favicon.String
		card.ViewCount = nullableCount(views)
		card.CommentCount = nullableCount(comments)
		card.LikeCount = nullableCount(likes)
		if err := card.Validate(); err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "stored link card violates the domain contract", map[string]any{"post_id": entryID.String()})
		}
		cards = append(cards, app.LinkCardProjection{
			Card:              card,
			ThumbnailMIMEType: mimeType.String,
			ThumbnailWidth:    uint32(width.Int64),
			ThumbnailHeight:   uint32(height.Int64),
			FaviconMIMEType:   faviconMT.String,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entry link cards: %w", err)
	}
	return cards, nil
}

func (store postStore) ReplaceEntryLinkCards(ctx context.Context, entryID domain.ID, cards []domain.LinkCard) error {
	if err := domain.ValidateLinkCards(cards); err != nil {
		return err
	}
	if _, err := store.queries.ExecContext(ctx, `DELETE FROM entry_link_card WHERE entry_id = ?`, entryID.String()); err != nil {
		return fmt.Errorf("delete entry link cards: %w", err)
	}
	for _, card := range cards {
		var duration, thumbnail any
		if card.DurationSeconds != nil {
			duration = *card.DurationSeconds
		}
		if card.ThumbnailMediaID != "" {
			thumbnail = card.ThumbnailMediaID
		}
		if _, err := store.queries.ExecContext(ctx, `
INSERT INTO entry_link_card (
    entry_id, position, source_url, canonical_url, provider, card_kind, title,
    description, site_name, author_name, duration_seconds, thumbnail_media_id,
    embed_url, captured_at_ms, view_count, comment_count, like_count,
    favicon_media_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			entryID.String(), card.Position, card.SourceURL, card.CanonicalURL,
			string(card.Provider), string(card.Kind), card.Title, card.Description,
			card.SiteName, card.AuthorName, duration, thumbnail, card.EmbedURL,
			card.CapturedAt, countArgument(card.ViewCount),
			countArgument(card.CommentCount), countArgument(card.LikeCount),
			mediaArgument(card.FaviconMediaID),
		); err != nil {
			return fmt.Errorf("insert entry link card: %w", err)
		}
	}
	return nil
}

func nullableCount(value sql.NullInt64) *int64 {
	if !value.Valid {
		return nil
	}
	count := value.Int64
	return &count
}

func mediaArgument(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func countArgument(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

// EntryLinkCardMediaIDs returns the thumbnails an entry currently references so
// the caller can reconcile reference state in the same transaction as a change.
func (store postStore) EntryLinkCardMediaIDs(ctx context.Context, entryID domain.ID) ([]string, error) {
	rows, err := store.queries.QueryContext(ctx, `
SELECT thumbnail_media_id
FROM entry_link_card
WHERE entry_id = ? AND thumbnail_media_id IS NOT NULL`, entryID.String())
	if err != nil {
		return nil, fmt.Errorf("query entry link card media: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan entry link card media: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entry link card media: %w", err)
	}
	return ids, nil
}

func (store postStore) EntryQuote(ctx context.Context, entryID domain.ID) (app.EntryQuoteReference, error) {
	var raw sql.NullString
	err := store.queries.QueryRowContext(ctx, `SELECT quoted_entry_id FROM entry_quotes WHERE entry_id = ?`, entryID.String()).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return app.EntryQuoteReference{}, nil
	}
	if err != nil {
		return app.EntryQuoteReference{}, fmt.Errorf("query entry quote: %w", err)
	}
	if !raw.Valid {
		return app.EntryQuoteReference{Present: true}, nil
	}
	id, err := domain.ParseID(raw.String)
	if err != nil {
		return app.EntryQuoteReference{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored quote target is invalid", map[string]any{"post_id": entryID.String()})
	}
	return app.EntryQuoteReference{Present: true, TargetID: &id}, nil
}

func (store postStore) ReplaceEntryQuote(ctx context.Context, entryID domain.ID, quotedID *domain.ID) error {
	if _, err := store.queries.ExecContext(ctx, `DELETE FROM entry_quotes WHERE entry_id = ?`, entryID.String()); err != nil {
		return fmt.Errorf("clear entry quote: %w", err)
	}
	if quotedID == nil {
		return nil
	}
	if *quotedID == entryID {
		return domain.NewError(domain.CodeInvalidArgument, "post cannot quote itself", map[string]any{"field": "quoted_post_id"})
	}
	if _, err := store.queries.ExecContext(ctx, `INSERT INTO entry_quotes (entry_id, quoted_entry_id) VALUES (?, ?)`, entryID.String(), quotedID.String()); err != nil {
		return fmt.Errorf("insert entry quote: %w", err)
	}
	return nil
}

// UpdateEntry replaces a Markdown body, and the kind in its WHERE clause is
// the point rather than an oversight: an article is rewritten as a whole
// document through ReplaceArticle, and letting this path touch one would leave
// its body and its projection disagreeing.
func (store postStore) UpdateEntry(ctx context.Context, entry domain.Entry, contentText string) (bool, error) {
	result, err := store.queries.ExecContext(ctx, `
UPDATE entries
SET content_md = ?, content_text = ?, visibility = ?, updated_at_ms = ?, edited_at_ms = ?
WHERE id = ? AND kind = ?`, entry.ContentMD, contentText, string(entry.Visibility), domain.TimeToMillis(entry.UpdatedAt), optionalTimeMillis(entry.EditedAt), entry.ID.String(), string(domain.KindMoment))
	if err != nil {
		return false, fmt.Errorf("update entry: %w", err)
	}
	return exactlyOneChanged(result, "update entry")
}

func (store postStore) SetEntryRepostedAt(ctx context.Context, id domain.ID, repostedAt *time.Time, updatedAt time.Time) (bool, error) {
	var reposted any
	if repostedAt != nil {
		reposted = domain.TimeToMillis(*repostedAt)
	}
	result, err := store.queries.ExecContext(ctx, `
UPDATE entries
SET reposted_at_ms = ?, updated_at_ms = ?
WHERE id = ?`, reposted, domain.TimeToMillis(updatedAt), id.String())
	if err != nil {
		return false, fmt.Errorf("update entry repost: %w", err)
	}
	return exactlyOneChanged(result, "update entry repost")
}

func (store postStore) DeleteEntry(ctx context.Context, id domain.ID) (bool, error) {
	result, err := store.queries.ExecContext(ctx, `DELETE FROM entries WHERE id = ?`, id.String())
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
		content                           sql.NullString
		visibility                        string
		createdAt, updatedAt, publishedAt int64
		repostedAt                        sql.NullInt64
		editedAt                          sql.NullInt64
	)
	if err := scanner.Scan(&idString, &kind, &content, &visibility, &createdAt, &updatedAt, &publishedAt, &repostedAt, &editedAt); err != nil {
		return domain.Entry{}, err
	}
	id, err := domain.ParseID(idString)
	if err != nil {
		return domain.Entry{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored entry has an invalid ID", nil)
	}
	entry := domain.Entry{
		ID:          id,
		Kind:        domain.EntryKind(kind),
		ContentMD:   nullableText(content),
		Visibility:  domain.Visibility(visibility),
		CreatedAt:   domain.TimeFromMillis(createdAt),
		UpdatedAt:   domain.TimeFromMillis(updatedAt),
		PublishedAt: domain.TimeFromMillis(publishedAt),
	}
	if editedAt.Valid {
		value := domain.TimeFromMillis(editedAt.Int64)
		entry.EditedAt = &value
	}
	if repostedAt.Valid {
		value := domain.TimeFromMillis(repostedAt.Int64)
		entry.RepostedAt = &value
	}
	if err := entry.Validate(); err != nil {
		return domain.Entry{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored entry violates the domain contract", map[string]any{"post_id": id.String()})
	}
	return entry, nil
}

func scanTimelineEntry(scanner entryScanner) (app.TimelineEntry, error) {
	var (
		idString                          string
		kind                              string
		content                           sql.NullString
		visibility                        string
		createdAt, updatedAt, publishedAt int64
		repostedAt                        sql.NullInt64
		editedAt                          sql.NullInt64
		timelineAt                        int64
		timelineKind                      int
	)
	if err := scanner.Scan(&idString, &kind, &content, &visibility, &createdAt, &updatedAt, &publishedAt, &repostedAt, &editedAt, &timelineAt, &timelineKind); err != nil {
		return app.TimelineEntry{}, err
	}
	id, err := domain.ParseID(idString)
	if err != nil {
		return app.TimelineEntry{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored entry has an invalid ID", nil)
	}
	entry := domain.Entry{
		ID:          id,
		Kind:        domain.EntryKind(kind),
		ContentMD:   nullableText(content),
		Visibility:  domain.Visibility(visibility),
		CreatedAt:   domain.TimeFromMillis(createdAt),
		UpdatedAt:   domain.TimeFromMillis(updatedAt),
		PublishedAt: domain.TimeFromMillis(publishedAt),
	}
	if editedAt.Valid {
		value := domain.TimeFromMillis(editedAt.Int64)
		entry.EditedAt = &value
	}
	if repostedAt.Valid {
		value := domain.TimeFromMillis(repostedAt.Int64)
		entry.RepostedAt = &value
	}
	if err := entry.Validate(); err != nil {
		return app.TimelineEntry{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored entry violates the domain contract", map[string]any{"post_id": id.String()})
	}
	itemKind := app.TimelineItemOriginal
	if timelineKind == 1 {
		itemKind = app.TimelineItemRepost
	} else if timelineKind != 0 {
		return app.TimelineEntry{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored timeline item has an invalid kind", map[string]any{"post_id": id.String()})
	}
	item := app.TimelineEntry{Entry: entry, Kind: itemKind}
	if domain.TimeToMillis(item.TimelineAt()) != timelineAt {
		return app.TimelineEntry{}, domain.NewError(domain.CodeDataIntegrityFailed, "stored timeline item has an invalid activity time", map[string]any{"post_id": id.String()})
	}
	return item, nil
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

func optionalTimeMillis(value *time.Time) any {
	if value == nil {
		return nil
	}
	return domain.TimeToMillis(*value)
}

// nullableText turns a possibly absent column into the pointer the domain uses
// to say "this kind of entry has no Markdown body at all".
func nullableText(value sql.NullString) *string {
	if !value.Valid {
		return nil
	}
	text := value.String
	return &text
}
