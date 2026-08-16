package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/moment/moment/internal/domain"
)

// ArchiveCounts are captured by the first statement in an archive read
// transaction. That statement establishes the WAL snapshot used by every
// subsequent archive query.
type ArchiveCounts struct {
	Entries  int64
	Comments int64
	Media    int64
}

// ArchiveImpact describes authoritative portable data currently selected by
// one SQLite snapshot. It is used only for replacement preflight estimates.
type ArchiveImpact struct {
	ArchiveCounts
	MediaBytes int64
}

// ArchiveSite is the complete portable site singleton.
type ArchiveSite struct {
	OwnerID         string
	Username        string
	DisplayName     string
	Title           string
	Bio             string
	Timezone        string
	PublicBaseURL   *string
	AvatarMediaID   *string
	CoverMediaID    *string
	PinnedPostID    *string
	CommentsEnabled bool
}

// ArchiveUsage is one ordered, nullable-alt media use.
type ArchiveUsage struct {
	MediaID  string
	Position int
	AltText  *string
}

// ArchiveEntry excludes derived content_text and embeds ordered media uses.
type ArchiveEntry struct {
	ID            string
	Kind          string
	ContentMD     string
	CreatedAtMS   int64
	UpdatedAtMS   int64
	PublishedAtMS int64
	RepostedAtMS  *int64
	QuotedEntryID *string
	Media         []ArchiveUsage
	Location      *domain.Location
}

// ArchiveComment is one portable comment record.
type ArchiveComment struct {
	ID          string
	EntryID     string
	ParentID    *string
	AuthorKind  string
	AuthorName  string
	ContentMD   string
	CreatedAtMS int64
	Media       []ArchiveUsage
}

// ArchiveMedia contains portable metadata plus the private source path needed
// by the exporter. StoragePath is never serialized into the archive.
type ArchiveMedia struct {
	ID           string
	SHA256       string
	StoragePath  string
	OriginalName string
	MIMEType     string
	ByteSize     int64
	Width        uint32
	Height       uint32
	CreatedAtMS  int64
}

// ArchiveSnapshot holds one query-only SQLite transaction until every
// authoritative record and reachable immutable original has been copied.
type ArchiveSnapshot struct {
	tx     *sql.Tx
	counts ArchiveCounts
	closed bool
}

// BeginArchiveSnapshot starts and establishes one consistent read snapshot.
// The caller must close it after media copying, not merely after JSONL output.
func (db *Database) BeginArchiveSnapshot(ctx context.Context) (*ArchiveSnapshot, error) {
	if err := db.requireReady(); err != nil {
		return nil, err
	}
	return beginArchiveSnapshot(ctx, db.read)
}

// BeginArchiveSnapshot starts an archive snapshot without invoking the normal
// writable startup path. Import inspection uses this surface so replacement
// impact calculation cannot migrate or otherwise mutate the active database.
func (database *SearchReadOnlyDatabase) BeginArchiveSnapshot(ctx context.Context) (*ArchiveSnapshot, error) {
	if database == nil || database.pool == nil {
		return nil, ErrClosed
	}
	return beginArchiveSnapshot(ctx, database.pool)
}

// BeginArchiveSnapshot starts the portable authority reader on a private
// diagnostic copy without invoking writable SQLite startup.
func (database *DiagnosticDatabase) BeginArchiveSnapshot(ctx context.Context) (*ArchiveSnapshot, error) {
	if database == nil || database.pool == nil {
		return nil, ErrClosed
	}
	return beginArchiveSnapshot(ctx, database.pool)
}

func beginArchiveSnapshot(ctx context.Context, pool *sql.DB) (*ArchiveSnapshot, error) {
	tx, err := pool.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin archive snapshot: %w", err)
	}
	snapshot := &ArchiveSnapshot{tx: tx}
	// This is deliberately the first read. SQLite pins the WAL view here. The
	// reachable count composes the one central mediaReferencedSQL definition.
	err = tx.QueryRowContext(ctx, `
SELECT
    (SELECT count(*) FROM entries),
    (SELECT count(*) FROM comments),
    (SELECT count(*) FROM media WHERE `+mediaReferencedSQL+`)`).Scan(
		&snapshot.counts.Entries,
		&snapshot.counts.Comments,
		&snapshot.counts.Media,
	)
	if err != nil {
		_ = tx.Rollback()
		return nil, fmt.Errorf("establish archive snapshot: %w", err)
	}
	return snapshot, nil
}

// Counts returns the values captured when the snapshot was established.
func (snapshot *ArchiveSnapshot) Counts() ArchiveCounts { return snapshot.counts }

// Impact returns replacement counts and reachable original bytes from the
// already established snapshot.
func (snapshot *ArchiveSnapshot) Impact(ctx context.Context) (ArchiveImpact, error) {
	if err := snapshot.requireOpen(); err != nil {
		return ArchiveImpact{}, err
	}
	impact := ArchiveImpact{ArchiveCounts: snapshot.counts}
	if err := snapshot.tx.QueryRowContext(ctx, `
SELECT COALESCE(sum(byte_size), 0)
FROM media
WHERE `+mediaReferencedSQL).Scan(&impact.MediaBytes); err != nil {
		return ArchiveImpact{}, fmt.Errorf("read archive impact: %w", err)
	}
	return impact, nil
}

// ValidateIntegrity proves the archive's cross-record graph inside the same
// SQLite snapshot used for serialization. Domain validation alone cannot see
// missing foreign rows or a reply that targets another reply/entry.
func (snapshot *ArchiveSnapshot) ValidateIntegrity(ctx context.Context) error {
	if err := snapshot.requireOpen(); err != nil {
		return err
	}
	rows, err := snapshot.tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
	if err != nil {
		return fmt.Errorf("check archive foreign keys: %w", err)
	}
	defer func() { _ = rows.Close() }()
	if rows.Next() {
		return errors.New("archive snapshot contains a foreign-key violation")
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate archive foreign keys: %w", err)
	}
	var invalidComments int64
	err = snapshot.tx.QueryRowContext(ctx, `
SELECT count(*)
FROM comments AS child
LEFT JOIN comments AS parent ON parent.id = child.parent_id
WHERE child.parent_id IS NOT NULL
  AND (parent.id IS NULL OR parent.parent_id IS NOT NULL OR parent.entry_id <> child.entry_id)`).Scan(&invalidComments)
	if err != nil {
		return fmt.Errorf("check archive comment relationships: %w", err)
	}
	if invalidComments != 0 {
		return errors.New("archive snapshot contains an invalid comment relationship")
	}
	var siteRows int64
	if err := snapshot.tx.QueryRowContext(ctx, `SELECT count(*) FROM site_settings WHERE id = 1`).Scan(&siteRows); err != nil {
		return fmt.Errorf("check archive site singleton: %w", err)
	}
	if siteRows != 1 {
		return errors.New("archive snapshot does not contain exactly one site singleton")
	}
	return nil
}

// Site reads the singleton from the established snapshot.
func (snapshot *ArchiveSnapshot) Site(ctx context.Context) (ArchiveSite, error) {
	if err := snapshot.requireOpen(); err != nil {
		return ArchiveSite{}, err
	}
	var value ArchiveSite
	var publicURL, avatar, cover, pinned sql.NullString
	var comments int
	err := snapshot.tx.QueryRowContext(ctx, `
SELECT owner_id, username, display_name, title, bio, timezone, public_base_url, avatar_media_id, cover_media_id, pinned_entry_id, comments_enabled
FROM site_settings
WHERE id = 1`).Scan(&value.OwnerID, &value.Username, &value.DisplayName, &value.Title, &value.Bio, &value.Timezone, &publicURL, &avatar, &cover, &pinned, &comments)
	if err != nil {
		return ArchiveSite{}, fmt.Errorf("read archive site settings: %w", err)
	}
	if publicURL.Valid {
		value.PublicBaseURL = &publicURL.String
	}
	if avatar.Valid {
		value.AvatarMediaID = &avatar.String
	}
	if cover.Valid {
		value.CoverMediaID = &cover.String
	}
	if pinned.Valid {
		value.PinnedPostID = &pinned.String
	}
	value.CommentsEnabled = comments == 1
	return value, nil
}

// Entries streams stable ID order while folding the bounded 0..8 usage join.
func (snapshot *ArchiveSnapshot) Entries(ctx context.Context, yield func(ArchiveEntry) error) error {
	if err := snapshot.requireOpen(); err != nil {
		return err
	}
	if yield == nil {
		return errors.New("archive entry callback is required")
	}
	rows, err := snapshot.tx.QueryContext(ctx, `
SELECT e.id, e.kind, e.content_md, e.created_at_ms, e.updated_at_ms, e.published_at_ms, e.reposted_at_ms, quote.quoted_entry_id,
       usage.media_id, usage.position, usage.alt_text,
       location.name, location.display_name, location.country_code, location.country,
       location.region, location.city, location.district, location.category,
       location.provider, location.provider_place_id, location.latitude_e7,
       location.longitude_e7, location.coordinate_system
FROM entries AS e
LEFT JOIN entry_media AS usage ON usage.entry_id = e.id
LEFT JOIN entry_location AS location ON location.entry_id = e.id
LEFT JOIN entry_quotes AS quote ON quote.entry_id = e.id
ORDER BY e.id, usage.position`)
	if err != nil {
		return fmt.Errorf("query archive entries: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var current *ArchiveEntry
	for rows.Next() {
		var entry ArchiveEntry
		var mediaID, alt sql.NullString
		var position sql.NullInt64
		var repostedAt sql.NullInt64
		var quotedEntryID sql.NullString
		var name, displayName, countryCode, country, region, city, district, category, provider, providerID, coordinateSystem sql.NullString
		var latitude, longitude sql.NullInt64
		if err := rows.Scan(
			&entry.ID, &entry.Kind, &entry.ContentMD, &entry.CreatedAtMS, &entry.UpdatedAtMS, &entry.PublishedAtMS, &repostedAt, &quotedEntryID,
			&mediaID, &position, &alt, &name, &displayName, &countryCode, &country, &region, &city,
			&district, &category, &provider, &providerID, &latitude, &longitude, &coordinateSystem,
		); err != nil {
			return fmt.Errorf("scan archive entry: %w", err)
		}
		if repostedAt.Valid {
			value := repostedAt.Int64
			entry.RepostedAtMS = &value
		}
		if quotedEntryID.Valid {
			value := quotedEntryID.String
			entry.QuotedEntryID = &value
		}
		if current == nil || current.ID != entry.ID {
			if current != nil {
				if err := yield(*current); err != nil {
					return err
				}
			}
			entry.Media = make([]ArchiveUsage, 0, 9)
			if name.Valid {
				if !displayName.Valid || !countryCode.Valid || !country.Valid || !region.Valid || !city.Valid || !district.Valid || !category.Valid || !provider.Valid || !providerID.Valid || latitude.Valid != longitude.Valid || latitude.Valid != coordinateSystem.Valid {
					return errors.New("archive entry location is incomplete")
				}
				entry.Location = &domain.Location{
					Name: name.String, DisplayName: displayName.String, CountryCode: countryCode.String,
					Country: country.String, Region: region.String, City: city.String, District: district.String,
					Category: category.String, Provider: provider.String, ProviderPlaceID: providerID.String,
				}
				if latitude.Valid {
					entry.Location.Point = &domain.GeoPoint{LatitudeE7: latitude.Int64, LongitudeE7: longitude.Int64, System: domain.CoordinateSystem(coordinateSystem.String)}
				}
				if err := entry.Location.Validate(); err != nil {
					return errors.New("archive entry location is invalid")
				}
			}
			current = &entry
		}
		if mediaID.Valid {
			if !position.Valid {
				return errors.New("archive entry usage has no position")
			}
			usage := ArchiveUsage{MediaID: mediaID.String, Position: int(position.Int64)}
			if alt.Valid {
				usage.AltText = &alt.String
			}
			current.Media = append(current.Media, usage)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate archive entries: %w", err)
	}
	if current != nil {
		return yield(*current)
	}
	return nil
}

// Comments streams one canonical flat thread per entry: root groups by root
// ID, each root first, then its replies by creation time and ID. The explicit
// root rank preserves parent-before-child even when both share a millisecond
// and a reply's random ULID suffix sorts before its parent.
func (snapshot *ArchiveSnapshot) Comments(ctx context.Context, yield func(ArchiveComment) error) error {
	if err := snapshot.requireOpen(); err != nil {
		return err
	}
	if yield == nil {
		return errors.New("archive comment callback is required")
	}
	rows, err := snapshot.tx.QueryContext(ctx, `
SELECT comment.id, comment.entry_id, comment.parent_id, comment.author_kind,
       comment.author_name, comment.content_md, comment.created_at_ms,
       usage.media_id, usage.position, usage.alt_text
FROM comments AS comment
LEFT JOIN comment_media AS usage ON usage.comment_id = comment.id
ORDER BY comment.entry_id,
         CASE WHEN comment.parent_id IS NULL THEN comment.id ELSE comment.parent_id END,
         CASE WHEN comment.parent_id IS NULL THEN 0 ELSE 1 END,
         comment.created_at_ms,
         comment.id,
         usage.position`)
	if err != nil {
		return fmt.Errorf("query archive comments: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var current *ArchiveComment
	for rows.Next() {
		var value ArchiveComment
		var parent sql.NullString
		var mediaID, alt sql.NullString
		var position sql.NullInt64
		if err := rows.Scan(&value.ID, &value.EntryID, &parent, &value.AuthorKind, &value.AuthorName, &value.ContentMD, &value.CreatedAtMS, &mediaID, &position, &alt); err != nil {
			return fmt.Errorf("scan archive comment: %w", err)
		}
		if parent.Valid {
			value.ParentID = &parent.String
		}
		if current == nil || current.ID != value.ID {
			if current != nil {
				if err := yield(*current); err != nil {
					return err
				}
			}
			value.Media = make([]ArchiveUsage, 0, 4)
			current = &value
		}
		if mediaID.Valid {
			if !position.Valid {
				return errors.New("archive comment usage has no position")
			}
			usage := ArchiveUsage{MediaID: mediaID.String, Position: int(position.Int64)}
			if alt.Valid {
				value := alt.String
				usage.AltText = &value
			}
			current.Media = append(current.Media, usage)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate archive comments: %w", err)
	}
	if current != nil {
		return yield(*current)
	}
	return nil
}

// Media streams exactly the objects selected by the central resolver
// predicate. It is intentionally safe to call twice in one snapshot: once for
// media.jsonl and once for immutable blob copying.
func (snapshot *ArchiveSnapshot) Media(ctx context.Context, yield func(ArchiveMedia) error) error {
	if err := snapshot.requireOpen(); err != nil {
		return err
	}
	if yield == nil {
		return errors.New("archive media callback is required")
	}
	rows, err := snapshot.tx.QueryContext(ctx, `
SELECT id, sha256, storage_path, original_name, mime_type, byte_size, width, height, created_at_ms
FROM media
WHERE `+mediaReferencedSQL+`
ORDER BY sha256, id`)
	if err != nil {
		return fmt.Errorf("query archive media: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var value ArchiveMedia
		if err := rows.Scan(&value.ID, &value.SHA256, &value.StoragePath, &value.OriginalName, &value.MIMEType, &value.ByteSize, &value.Width, &value.Height, &value.CreatedAtMS); err != nil {
			return fmt.Errorf("scan archive media: %w", err)
		}
		if err := yield(value); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate archive media: %w", err)
	}
	return nil
}

// Close ends the read transaction. Rollback is the correct read-only close and
// avoids treating a cancelled context as a failed write commit.
func (snapshot *ArchiveSnapshot) Close() error {
	if snapshot == nil || snapshot.closed {
		return nil
	}
	snapshot.closed = true
	return snapshot.tx.Rollback()
}

func (snapshot *ArchiveSnapshot) requireOpen() error {
	if snapshot == nil || snapshot.tx == nil || snapshot.closed {
		return errors.New("archive snapshot is closed")
	}
	return nil
}
