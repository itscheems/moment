package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/moment/moment/internal/domain"
)

// PortableImporter is a current-schema-only bulk writer used by archive
// inspection. It never accepts SQLite bytes from an archive and owns one
// transaction until the complete portable graph has been validated.
type PortableImporter struct {
	tx            *sql.Tx
	closed        bool
	entries       int64
	comments      int64
	media         int64
	pendingQuotes []pendingQuote
}

type pendingQuote struct {
	entryID  string
	targetID *string
}

// BeginPortableImport starts one isolated current-schema rebuild.
func (db *Database) BeginPortableImport(ctx context.Context) (*PortableImporter, error) {
	if err := db.requireReady(); err != nil {
		return nil, err
	}
	tx, err := db.write.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin portable import: %w", err)
	}
	return &PortableImporter{tx: tx}, nil
}

// InsertMedia writes one already verified immutable original metadata record.
func (importer *PortableImporter) InsertMedia(ctx context.Context, value ArchiveMedia) error {
	if err := importer.requireOpen(); err != nil {
		return err
	}
	if _, err := domain.ParseID(value.ID); err != nil {
		return err
	}
	if value.ByteSize <= 0 || value.Width == 0 || value.Height == 0 || value.CreatedAtMS <= 0 {
		return errors.New("portable media metadata is incomplete")
	}
	_, err := importer.tx.ExecContext(ctx, `
INSERT INTO media (
    id, sha256, storage_path, original_name, mime_type,
    byte_size, width, height, created_at_ms, unreferenced_since_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`,
		value.ID, value.SHA256, value.StoragePath, value.OriginalName, value.MIMEType,
		value.ByteSize, value.Width, value.Height, value.CreatedAtMS)
	if err != nil {
		return fmt.Errorf("insert portable media %s: %w", value.ID, err)
	}
	importer.media++
	return nil
}

// InsertEntry writes one authority entry and its ordered media usages. Search
// text and FTS rows are rebuilt from content_md through the current normalizer
// and current schema triggers.
func (importer *PortableImporter) InsertEntry(ctx context.Context, value ArchiveEntry) error {
	if err := importer.requireOpen(); err != nil {
		return err
	}
	id, err := domain.ParseID(value.ID)
	if err != nil {
		return err
	}
	entry := domain.Entry{
		ID: id, Kind: domain.EntryKind(value.Kind), ContentMD: value.ContentMD,
		CreatedAt: time.UnixMilli(value.CreatedAtMS).UTC(), UpdatedAt: time.UnixMilli(value.UpdatedAtMS).UTC(),
		PublishedAt: time.UnixMilli(value.PublishedAtMS).UTC(),
	}
	if value.RepostedAtMS != nil {
		repostedAt := time.UnixMilli(*value.RepostedAtMS).UTC()
		entry.RepostedAt = &repostedAt
	}
	if err := entry.Validate(); err != nil {
		return err
	}
	usages := make([]domain.EntryMediaUsage, len(value.Media))
	for index, usage := range value.Media {
		mediaID, err := domain.ParseID(usage.MediaID)
		if err != nil {
			return err
		}
		usages[index] = domain.EntryMediaUsage{MediaID: mediaID, Position: usage.Position, AltText: usage.AltText}
	}
	if err := domain.ValidateEntryMediaUsages(usages); err != nil {
		return err
	}
	if err := domain.ValidatePostMarkdown(value.ContentMD, len(usages)); err != nil && value.QuotedEntryID == nil && !value.QuoteUnavailable {
		return err
	}
	if value.QuoteUnavailable && value.QuotedEntryID != nil {
		return errors.New("portable entry quote cannot be both available and unavailable")
	}
	if value.QuotedEntryID != nil {
		if _, err := domain.ParseID(*value.QuotedEntryID); err != nil {
			return err
		}
		if *value.QuotedEntryID == value.ID {
			return errors.New("portable entry cannot quote itself")
		}
		target := *value.QuotedEntryID
		importer.pendingQuotes = append(importer.pendingQuotes, pendingQuote{entryID: value.ID, targetID: &target})
	} else if value.QuoteUnavailable {
		importer.pendingQuotes = append(importer.pendingQuotes, pendingQuote{entryID: value.ID})
	}
	if value.Location != nil {
		if err := value.Location.Validate(); err != nil {
			return err
		}
	}
	contentText, err := domain.NormalizeContentV2(value.ContentMD)
	if err != nil {
		return err
	}
	if _, err := importer.tx.ExecContext(ctx, `
INSERT INTO entries (
    id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, reposted_at_ms, content_text
) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		value.ID, value.Kind, value.ContentMD, value.CreatedAtMS, value.UpdatedAtMS, value.PublishedAtMS, value.RepostedAtMS, contentText); err != nil {
		return fmt.Errorf("insert portable entry %s: %w", value.ID, err)
	}
	var aggregateBytes int64
	for _, usage := range value.Media {
		var byteSize int64
		if err := importer.tx.QueryRowContext(ctx, `SELECT byte_size FROM media WHERE id = ?`, usage.MediaID).Scan(&byteSize); err != nil {
			return fmt.Errorf("resolve portable entry media %s: %w", usage.MediaID, err)
		}
		if byteSize > domain.MaxEntryMediaBytes-aggregateBytes {
			return domain.NewError(domain.CodeMediaTooLarge, "post media exceed the 50 MiB aggregate budget", map[string]any{"field": "media", "max_bytes": domain.MaxEntryMediaBytes})
		}
		aggregateBytes += byteSize
		if _, err := importer.tx.ExecContext(ctx, `
INSERT INTO entry_media (entry_id, media_id, position, alt_text)
VALUES (?, ?, ?, ?)`, value.ID, usage.MediaID, usage.Position, usage.AltText); err != nil {
			return fmt.Errorf("insert portable entry media usage: %w", err)
		}
	}
	if value.Location != nil {
		var latitude, longitude, coordinateSystem any
		if value.Location.Point != nil {
			latitude = value.Location.Point.LatitudeE7
			longitude = value.Location.Point.LongitudeE7
			coordinateSystem = string(value.Location.Point.System)
		}
		if _, err := importer.tx.ExecContext(ctx, `
INSERT INTO entry_location (
    entry_id, name, display_name, country_code, country, region, city, district,
    category, provider, provider_place_id, latitude_e7, longitude_e7, coordinate_system
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			value.ID, value.Location.Name, value.Location.DisplayName, value.Location.CountryCode,
			value.Location.Country, value.Location.Region, value.Location.City, value.Location.District,
			value.Location.Category, value.Location.Provider, value.Location.ProviderPlaceID,
			latitude, longitude, coordinateSystem,
		); err != nil {
			return fmt.Errorf("insert portable entry location: %w", err)
		}
	}
	importer.entries++
	return nil
}

// InsertComment writes one domain-validated root or one-level reply.
func (importer *PortableImporter) InsertComment(ctx context.Context, value ArchiveComment) error {
	if err := importer.requireOpen(); err != nil {
		return err
	}
	id, err := domain.ParseID(value.ID)
	if err != nil {
		return err
	}
	entryID, err := domain.ParseID(value.EntryID)
	if err != nil {
		return err
	}
	var parentID *domain.ID
	if value.ParentID != nil {
		parsed, err := domain.ParseID(*value.ParentID)
		if err != nil {
			return err
		}
		parentID = &parsed
	}
	comment := domain.Comment{
		ID: id, EntryID: entryID, ParentID: parentID,
		AuthorKind: domain.CommentAuthorKind(value.AuthorKind), AuthorName: value.AuthorName,
		ContentMD: value.ContentMD, Location: domain.CloneLocation(value.Location), CreatedAt: time.UnixMilli(value.CreatedAtMS).UTC(),
	}
	if err := comment.Validate(); err != nil {
		return err
	}
	usages := make([]domain.EntryMediaUsage, len(value.Media))
	for index, usage := range value.Media {
		mediaID, err := domain.ParseID(usage.MediaID)
		if err != nil {
			return err
		}
		usages[index] = domain.EntryMediaUsage{MediaID: mediaID, Position: usage.Position, AltText: usage.AltText}
	}
	if err := domain.ValidateCommentMediaUsages(usages); err != nil {
		return err
	}
	if _, err := importer.tx.ExecContext(ctx, `
INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?)`, value.ID, value.EntryID, value.ParentID, value.AuthorKind, value.AuthorName, value.ContentMD, value.CreatedAtMS); err != nil {
		return fmt.Errorf("insert portable comment %s: %w", value.ID, err)
	}
	if value.Location != nil {
		var latitude, longitude, coordinateSystem any
		if value.Location.Point != nil {
			latitude = value.Location.Point.LatitudeE7
			longitude = value.Location.Point.LongitudeE7
			coordinateSystem = string(value.Location.Point.System)
		}
		if _, err := importer.tx.ExecContext(ctx, `
INSERT INTO comment_location (
    comment_id, name, display_name, country_code, country, region, city, district,
    category, provider, provider_place_id, latitude_e7, longitude_e7, coordinate_system
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			value.ID, value.Location.Name, value.Location.DisplayName, value.Location.CountryCode,
			value.Location.Country, value.Location.Region, value.Location.City, value.Location.District,
			value.Location.Category, value.Location.Provider, value.Location.ProviderPlaceID,
			latitude, longitude, coordinateSystem,
		); err != nil {
			return fmt.Errorf("insert portable comment location: %w", err)
		}
	}
	var aggregateBytes int64
	for _, usage := range value.Media {
		var byteSize int64
		if err := importer.tx.QueryRowContext(ctx, `SELECT byte_size FROM media WHERE id = ?`, usage.MediaID).Scan(&byteSize); err != nil {
			return fmt.Errorf("resolve portable comment media %s: %w", usage.MediaID, err)
		}
		if byteSize > domain.MaxCommentMediaBytes-aggregateBytes {
			return domain.NewError(domain.CodeMediaTooLarge, "comment media exceed the 40 MiB aggregate budget", map[string]any{"field": "media", "max_bytes": domain.MaxCommentMediaBytes})
		}
		aggregateBytes += byteSize
		if _, err := importer.tx.ExecContext(ctx, `
INSERT INTO comment_media (comment_id, media_id, position, alt_text)
VALUES (?, ?, ?, ?)`, value.ID, usage.MediaID, usage.Position, usage.AltText); err != nil {
			return fmt.Errorf("insert portable comment media usage: %w", err)
		}
	}
	if value.Pinned {
		if value.ParentID != nil {
			return errors.New("only a top-level portable comment can be pinned")
		}
		if _, err := importer.tx.ExecContext(ctx, `INSERT INTO comment_pins (entry_id, comment_id) VALUES (?, ?)`, value.EntryID, value.ID); err != nil {
			return fmt.Errorf("insert portable pinned comment %s: %w", value.ID, err)
		}
	}
	importer.comments++
	return nil
}

// SetSite replaces the generated singleton with portable public settings.
func (importer *PortableImporter) SetSite(ctx context.Context, value ArchiveSite) error {
	if err := importer.requireOpen(); err != nil {
		return err
	}
	commentsEnabled := 0
	if value.CommentsEnabled {
		commentsEnabled = 1
	}
	if value.OwnerID != "" {
		result, err := importer.tx.ExecContext(ctx, `
UPDATE site_settings
SET owner_id = ?, username = ?, display_name = ?, title = ?, bio = ?, timezone = ?, public_base_url = ?, avatar_media_id = ?, cover_media_id = ?, pinned_entry_id = ?, comments_enabled = ?
WHERE id = 1`, value.OwnerID, value.Username, value.DisplayName, value.Title, value.Bio, value.Timezone, value.PublicBaseURL, value.AvatarMediaID, value.CoverMediaID, value.PinnedPostID, commentsEnabled)
		if err != nil {
			return fmt.Errorf("replace portable site settings: %w", err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return fmt.Errorf("replace portable site settings changed %d rows: %w", changed, err)
		}
		return nil
	}
	result, err := importer.tx.ExecContext(ctx, `
UPDATE site_settings
SET title = ?, bio = ?, timezone = ?, public_base_url = ?, avatar_media_id = ?, cover_media_id = ?, pinned_entry_id = ?, comments_enabled = ?
WHERE id = 1`, value.Title, value.Bio, value.Timezone, value.PublicBaseURL, value.AvatarMediaID, value.CoverMediaID, value.PinnedPostID, commentsEnabled)
	if err != nil {
		return fmt.Errorf("replace portable site settings: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("replace portable site settings changed %d rows: %w", changed, err)
	}
	return nil
}

// Commit verifies counts, relational integrity, and the rebuilt FTS identity
// set before publishing the isolated transaction inside the staging database.
func (importer *PortableImporter) Commit(ctx context.Context, expected ArchiveCounts) error {
	if err := importer.requireOpen(); err != nil {
		return err
	}
	if importer.entries != expected.Entries || importer.comments != expected.Comments || importer.media != expected.Media {
		return errors.New("portable import counts do not match the manifest")
	}
	for _, quote := range importer.pendingQuotes {
		if _, err := importer.tx.ExecContext(ctx, `INSERT INTO entry_quotes (entry_id, quoted_entry_id) VALUES (?, ?)`, quote.entryID, quote.targetID); err != nil {
			return fmt.Errorf("insert portable entry quote: %w", err)
		}
	}
	rows, err := importer.tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check portable import foreign keys: %w", err)
	}
	if rows.Next() {
		_ = rows.Close()
		return errors.New("portable import contains a foreign-key violation")
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("iterate portable import foreign keys: %w", err)
	}
	var ftsRows, missingFTS, extraFTS, mismatchedFTS, duplicateFTS int64
	if err := importer.tx.QueryRowContext(ctx, `
SELECT
	  (SELECT count(*) FROM entry_fts),
  (SELECT count(*) FROM entries AS e WHERE NOT EXISTS (SELECT 1 FROM entry_fts AS f WHERE f.entry_id = e.id)),
	  (SELECT count(*) FROM entry_fts AS f WHERE NOT EXISTS (SELECT 1 FROM entries AS e WHERE e.id = f.entry_id)),
	  (SELECT count(*) FROM entry_fts AS f JOIN entries AS e ON e.id = f.entry_id WHERE f.content_text <> e.content_text),
	  (SELECT count(*) FROM (SELECT entry_id FROM entry_fts GROUP BY entry_id HAVING count(*) <> 1))`).Scan(
		&ftsRows, &missingFTS, &extraFTS, &mismatchedFTS, &duplicateFTS,
	); err != nil {
		return fmt.Errorf("check portable import FTS identity: %w", err)
	}
	var normalizerVersion int
	if err := importer.tx.QueryRowContext(ctx, `SELECT normalizer_version FROM search_index_state WHERE id = 1`).Scan(&normalizerVersion); err != nil {
		return fmt.Errorf("check portable import normalizer version: %w", err)
	}
	if ftsRows != expected.Entries || missingFTS != 0 || extraFTS != 0 || mismatchedFTS != 0 || duplicateFTS != 0 ||
		normalizerVersion != domain.ContentNormalizerVersion {
		return errors.New("portable import FTS content or normalizer version is inconsistent")
	}
	if err := importer.tx.Commit(); err != nil {
		return fmt.Errorf("commit portable import: %w", err)
	}
	importer.closed = true
	return nil
}

// Close rolls back an unfinished rebuild.
func (importer *PortableImporter) Close() error {
	if importer == nil || importer.closed {
		return nil
	}
	importer.closed = true
	return importer.tx.Rollback()
}

func (importer *PortableImporter) requireOpen() error {
	if importer == nil || importer.tx == nil || importer.closed {
		return errors.New("portable importer is closed")
	}
	return nil
}

// ValidatePortableReady runs the authoritative post-rebuild checks used before
// a staging generation may receive a ready token.
func (db *Database) ValidatePortableReady(ctx context.Context, expected ArchiveCounts) error {
	if err := db.Ready(ctx); err != nil {
		return err
	}
	return validatePortableReady(ctx, db.read, expected, db.ProbeCapabilities)
}

// ValidatePortableReady runs the same complete post-rebuild checks against a
// private query-only diagnostic copy. It never migrates or rebuilds derived
// state beside the staged authority.
func (database *DiagnosticDatabase) ValidatePortableReady(ctx context.Context, expected ArchiveCounts) error {
	if database == nil || database.pool == nil {
		return ErrClosed
	}
	schema, err := database.Schema(ctx)
	if err != nil || !schema.Exact {
		return errors.Join(errors.New("staged diagnostic schema is not exact"), err)
	}
	return validatePortableReady(ctx, database.pool, expected, func(ctx context.Context) error {
		capability, err := database.FTSCapability(ctx)
		if err != nil || !capability.FTS5 || !capability.Trigram {
			return errors.Join(ErrFTSUnavailable, err)
		}
		return nil
	})
}

func validatePortableReady(ctx context.Context, pool *sql.DB, expected ArchiveCounts, probe func(context.Context) error) error {
	var integrity string
	if err := pool.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&integrity); err != nil {
		return fmt.Errorf("check staged SQLite integrity: %w", err)
	}
	if integrity != "ok" {
		return errors.New("staged SQLite integrity check failed")
	}
	rows, err := pool.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check staged foreign keys: %w", err)
	}
	if rows.Next() {
		_ = rows.Close()
		return errors.New("staged SQLite contains a foreign-key violation")
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("iterate staged foreign keys: %w", err)
	}
	var entries, comments, mediaCount, mediaGC, ftsRows, missingFTS, extraFTS, mismatchedFTS, duplicateFTS int64
	if err := pool.QueryRowContext(ctx, `
SELECT
  (SELECT count(*) FROM entries),
  (SELECT count(*) FROM comments),
	  (SELECT count(*) FROM media),
	  (SELECT count(*) FROM media WHERE unreferenced_since_ms IS NOT NULL),
	  (SELECT count(*) FROM entry_fts),
  (SELECT count(*) FROM entries AS e WHERE NOT EXISTS (SELECT 1 FROM entry_fts AS f WHERE f.entry_id = e.id)),
	  (SELECT count(*) FROM entry_fts AS f WHERE NOT EXISTS (SELECT 1 FROM entries AS e WHERE e.id = f.entry_id)),
	  (SELECT count(*) FROM entry_fts AS f JOIN entries AS e ON e.id = f.entry_id WHERE f.content_text <> e.content_text),
	  (SELECT count(*) FROM (SELECT entry_id FROM entry_fts GROUP BY entry_id HAVING count(*) <> 1))`).Scan(
		&entries, &comments, &mediaCount, &mediaGC, &ftsRows, &missingFTS, &extraFTS, &mismatchedFTS, &duplicateFTS,
	); err != nil {
		return fmt.Errorf("check staged portable counts: %w", err)
	}
	var normalizerVersion int
	if err := pool.QueryRowContext(ctx, `SELECT normalizer_version FROM search_index_state WHERE id = 1`).Scan(&normalizerVersion); err != nil {
		return fmt.Errorf("check staged normalizer version: %w", err)
	}
	if entries != expected.Entries || comments != expected.Comments || mediaCount != expected.Media || mediaGC != 0 ||
		ftsRows != entries || missingFTS != 0 || extraFTS != 0 || mismatchedFTS != 0 || duplicateFTS != 0 ||
		normalizerVersion != domain.ContentNormalizerVersion {
		return errors.New("staged portable counts, FTS content, or normalizer version are inconsistent")
	}
	rows, err = pool.QueryContext(ctx, `SELECT id, content_md, content_text FROM entries ORDER BY id`)
	if err != nil {
		return fmt.Errorf("query staged normalized content: %w", err)
	}
	for rows.Next() {
		var id, contentMD, contentText string
		if err := rows.Scan(&id, &contentMD, &contentText); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan staged normalized content: %w", err)
		}
		expectedText, err := domain.NormalizeContentV2(contentMD)
		if err != nil || contentText != expectedText {
			_ = rows.Close()
			return errors.Join(fmt.Errorf("staged normalized content for %s is inconsistent", id), err)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return fmt.Errorf("iterate staged normalized content: %w", err)
	}
	return probe(ctx)
}
