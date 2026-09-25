package sqlite

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

// PortableImporter is a current-schema-only bulk writer used by archive
// inspection. It never accepts SQLite bytes from an archive and owns one
// transaction until the complete portable graph has been validated.
type PortableImporter struct {
	tx             *sql.Tx
	closed         bool
	entries        int64
	comments       int64
	media          int64
	users          int64
	identities     int64
	pendingQuotes  []pendingQuote
	pendingReplies []pendingReplyTarget
}

// portableOwnerPlaceholder is the handle the schema-generated owner account is
// parked under while the archive's own owner takes its place.
//
// A fresh generation is not accountless: the migrations create one owner named
// "owner", which is also the default handle of every instance that never
// renamed itself -- so the incoming owner collides with it on the unique
// username index far more often than not. Renaming the generated row first, and
// deleting it once nothing references it, is what keeps that ordinary case from
// failing. Both candidates are valid handles, and the second exists only so the
// step still frees the name when the archive's owner is literally called the
// first one.
const (
	portableOwnerPlaceholder          = "zzportableownerplaceholder1"
	portableOwnerPlaceholderAlternate = "zzportableownerplaceholder2"
)

type pendingQuote struct {
	entryID  string
	targetID *string
}

// pendingReplyTarget defers one reply_to_comment_id link until every comment
// exists. The column has a foreign key and an insert guard that both require
// the target row to be present, but a reply may answer a sibling that sorts
// after it in canonical archive order: the two share a millisecond and the
// tie is broken by a random ULID suffix. Writing the link on insert would
// reject such an archive, so the row goes in unlinked and the link is applied
// once ordering can no longer matter. The update guard enforces the same
// same-conversation rule the insert guard would have.
type pendingReplyTarget struct {
	commentID string
	targetID  string
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

// InsertUser writes one portable account.
//
// The owner must arrive first, because it does not arrive into an empty table:
// a freshly migrated generation already holds the account the schema created
// for its generated site row, and that account holds both the single-owner
// index and, by default, the handle "owner". Adopting it before any other
// account is inserted is what makes the remaining inserts ordinary.
func (importer *PortableImporter) InsertUser(ctx context.Context, value ArchiveUser) error {
	if err := importer.requireOpen(); err != nil {
		return err
	}
	if err := validatePortableUser(value); err != nil {
		return err
	}
	owner := domain.UserRole(value.Role) == domain.UserRoleOwner
	if importer.users == 0 {
		if !owner {
			return errors.New("the owner account must be the first portable account imported")
		}
		if err := importer.adoptPortableOwner(ctx, value); err != nil {
			return err
		}
		importer.users++
		return nil
	}
	if owner {
		return errors.New("portable accounts contain more than one owner")
	}
	if _, err := importer.tx.ExecContext(ctx, `
INSERT INTO users (
    id, username, display_name, bio, avatar_media_id, cover_media_id,
    role, status, username_set_at_ms, created_at_ms, updated_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		value.ID, value.Username, value.DisplayName, value.Bio, value.AvatarMediaID, value.CoverMediaID,
		value.Role, value.Status, value.UsernameSetAtMS, value.CreatedAtMS, value.UpdatedAtMS); err != nil {
		return fmt.Errorf("insert portable account %s: %w", value.ID, err)
	}
	importer.users++
	return nil
}

// adoptPortableOwner replaces the generated owner account with the archive's.
//
// Every step is ordered so no foreign key is ever left dangling: RESTRICT is
// checked the moment a row changes and is not deferrable, so the site row is
// repointed at the new account before the generated one is deleted, and the new
// account only becomes the owner once the old one is gone.
func (importer *PortableImporter) adoptPortableOwner(ctx context.Context, value ArchiveUser) error {
	var generated string
	if err := importer.tx.QueryRowContext(ctx, `SELECT owner_id FROM site_settings WHERE id = 1`).Scan(&generated); err != nil {
		return fmt.Errorf("resolve generated owner account: %w", err)
	}
	if generated == value.ID {
		return importer.updatePortableUser(ctx, value)
	}
	placeholder := portableOwnerPlaceholder
	if value.Username == placeholder {
		placeholder = portableOwnerPlaceholderAlternate
	}
	steps := []struct {
		description string
		query       string
		args        []any
	}{
		{"park the generated owner handle", `UPDATE users SET username = ? WHERE id = ?`, []any{placeholder, generated}},
		{"insert the portable owner account", `
INSERT INTO users (
    id, username, display_name, bio, avatar_media_id, cover_media_id,
    role, status, username_set_at_ms, created_at_ms, updated_at_ms
) VALUES (?, ?, ?, ?, ?, ?, 'member', ?, ?, ?, ?)`, []any{
			value.ID, value.Username, value.DisplayName, value.Bio, value.AvatarMediaID, value.CoverMediaID,
			value.Status, value.UsernameSetAtMS, value.CreatedAtMS, value.UpdatedAtMS,
		}},
		{"point the site at the portable owner", `UPDATE site_settings SET owner_id = ? WHERE id = 1`, []any{value.ID}},
		{"remove the generated owner account", `DELETE FROM users WHERE id = ?`, []any{generated}},
		{"promote the portable owner", `UPDATE users SET role = ? WHERE id = ?`, []any{value.Role, value.ID}},
	}
	for _, step := range steps {
		result, err := importer.tx.ExecContext(ctx, step.query, step.args...)
		if err != nil {
			return fmt.Errorf("%s: %w", step.description, err)
		}
		changed, err := result.RowsAffected()
		if err != nil || changed != 1 {
			return errors.Join(fmt.Errorf("%s changed %d rows", step.description, changed), err)
		}
	}
	return nil
}

func (importer *PortableImporter) updatePortableUser(ctx context.Context, value ArchiveUser) error {
	result, err := importer.tx.ExecContext(ctx, `
UPDATE users
SET username = ?, display_name = ?, bio = ?, avatar_media_id = ?, cover_media_id = ?,
    role = ?, status = ?, username_set_at_ms = ?, created_at_ms = ?, updated_at_ms = ?
WHERE id = ?`,
		value.Username, value.DisplayName, value.Bio, value.AvatarMediaID, value.CoverMediaID,
		value.Role, value.Status, value.UsernameSetAtMS, value.CreatedAtMS, value.UpdatedAtMS, value.ID)
	if err != nil {
		return fmt.Errorf("replace portable account %s: %w", value.ID, err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return errors.Join(fmt.Errorf("replace portable account %s changed %d rows", value.ID, changed), err)
	}
	return nil
}

// KeepGeneratedOwner accepts the account the migrations generated as this
// import's owner.
//
// An archive written before owner identity existed names no owner at all, so
// there is nothing to adopt and nothing to synthesize from. The generated
// account is then the only answer to "who owns this site", and counting it here
// keeps the importer's own accounting equal to what the database holds.
func (importer *PortableImporter) KeepGeneratedOwner(ctx context.Context) error {
	if err := importer.requireOpen(); err != nil {
		return err
	}
	if importer.users != 0 {
		return errors.New("the generated owner can only be kept before any portable account is imported")
	}
	var total, owners int64
	if err := importer.tx.QueryRowContext(ctx, `
SELECT
  (SELECT count(*) FROM users),
  (SELECT count(*) FROM users AS account JOIN site_settings AS site ON site.owner_id = account.id
    WHERE site.id = 1 AND account.role = 'owner')`).Scan(&total, &owners); err != nil {
		return fmt.Errorf("inspect generated owner account: %w", err)
	}
	if total != 1 || owners != 1 {
		return errors.New("the generated generation does not hold exactly one owner account")
	}
	importer.users = 1
	return nil
}

// InsertIdentity writes one external credential. Its account must already exist
// so the foreign key names a real row rather than a future one.
func (importer *PortableImporter) InsertIdentity(ctx context.Context, value ArchiveIdentity) error {
	if err := importer.requireOpen(); err != nil {
		return err
	}
	if err := validatePortableIdentity(value); err != nil {
		return err
	}
	if _, err := importer.tx.ExecContext(ctx, `
INSERT INTO identities (
    provider, provider_user_id, user_id, email, email_verified, email_deliverable,
    display_name, avatar_url, created_at_ms, last_login_at_ms
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		value.Provider, value.ProviderUserID, value.UserID, value.Email,
		boolInteger(value.EmailVerified), boolInteger(value.EmailDeliverable),
		value.DisplayName, value.AvatarURL, value.CreatedAtMS, value.LastLoginAtMS); err != nil {
		return fmt.Errorf("insert portable identity %s/%s: %w", value.Provider, value.ProviderUserID, err)
	}
	importer.identities++
	return nil
}

func validatePortableUser(value ArchiveUser) error {
	if err := domain.ValidateUserID(value.ID); err != nil {
		return err
	}
	if err := domain.ValidateStoredUsername(value.Username); err != nil {
		return err
	}
	name, err := domain.NormalizeDisplayName(value.DisplayName)
	if err != nil {
		return err
	}
	if name != value.DisplayName {
		return errors.New("portable account display name is not canonical")
	}
	if err := domain.ValidateUserRole(domain.UserRole(value.Role)); err != nil {
		return err
	}
	if err := domain.ValidateAccountStatus(domain.AccountStatus(value.Status)); err != nil {
		return err
	}
	for _, reference := range []*string{value.AvatarMediaID, value.CoverMediaID} {
		if reference == nil {
			continue
		}
		if _, err := domain.ParseID(*reference); err != nil {
			return err
		}
	}
	if value.CreatedAtMS <= 0 || value.UpdatedAtMS <= 0 || (value.UsernameSetAtMS != nil && *value.UsernameSetAtMS <= 0) {
		return errors.New("portable account timestamps must be positive")
	}
	return nil
}

func validatePortableIdentity(value ArchiveIdentity) error {
	if err := domain.ValidateUserID(value.UserID); err != nil {
		return err
	}
	if value.Provider == "" || value.Provider != strings.ToLower(value.Provider) || value.ProviderUserID == "" {
		return errors.New("portable identity provider key is incomplete")
	}
	if value.CreatedAtMS <= 0 || value.LastLoginAtMS <= 0 {
		return errors.New("portable identity timestamps must be positive")
	}
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
		ID: id, Kind: domain.EntryKind(value.Kind), ContentMD: value.ContentMD, Visibility: value.Visibility,
		CreatedAt: time.UnixMilli(value.CreatedAtMS).UTC(), UpdatedAt: time.UnixMilli(value.UpdatedAtMS).UTC(),
		PublishedAt: time.UnixMilli(value.PublishedAtMS).UTC(),
	}
	if value.EditedAtMS != nil {
		edited := time.UnixMilli(*value.EditedAtMS).UTC()
		entry.EditedAt = &edited
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
	// Only a moment travels through this layout. Importing an article envelope
	// without the body that belongs to it would restore a blank post, so both
	// directions refuse until a member carries that body; the export side
	// refuses in validateArchiveEntry for the same reason.
	body, isMoment := entry.Markdown()
	if !isMoment {
		return fmt.Errorf("portable entry %s is an article, which this archive layout does not carry yet", value.ID)
	}
	if err := domain.ValidatePostMarkdown(body, len(usages)); err != nil && value.QuotedEntryID == nil && !value.QuoteUnavailable {
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
	// Indexed text is the Markdown and the cards combined, as the write path
	// composes it; the cards are inserted below, but their text belongs to
	// the entry row from the start.
	cards := make([]domain.LinkCard, 0, len(value.LinkCards))
	for _, card := range value.LinkCards {
		cards = append(cards, card.Card)
	}
	contentText, err := app.DerivedSearchText(body, cards)
	if err != nil {
		return err
	}
	if _, err := importer.tx.ExecContext(ctx, `
INSERT INTO entries (
    id, kind, content_md, visibility, created_at_ms, updated_at_ms, published_at_ms, reposted_at_ms, edited_at_ms, content_text
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		value.ID, value.Kind, value.ContentMD, string(entry.Visibility), value.CreatedAtMS, value.UpdatedAtMS, value.PublishedAtMS, value.RepostedAtMS, value.EditedAtMS, contentText); err != nil {
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
	for _, card := range value.LinkCards {
		if err := card.Card.Validate(); err != nil {
			return err
		}
		// The archive names images by content hash because media identifiers
		// are reassigned on import. Resolve them back here; a hash the archive
		// never packaged is a broken reference, not a silently dropped image.
		thumbnail, err := importer.resolvePortableMedia(ctx, card.ThumbnailSHA256)
		if err != nil {
			return err
		}
		favicon, err := importer.resolvePortableMedia(ctx, card.FaviconSHA256)
		if err != nil {
			return err
		}
		var duration any
		if card.Card.DurationSeconds != nil {
			duration = *card.Card.DurationSeconds
		}
		if _, err := importer.tx.ExecContext(ctx, `
INSERT INTO entry_link_card (
    entry_id, position, source_url, canonical_url, provider, card_kind, title,
    description, site_name, author_name, duration_seconds, thumbnail_media_id,
    embed_url, captured_at_ms, view_count, comment_count, like_count,
    favicon_media_id
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			value.ID, card.Card.Position, card.Card.SourceURL, card.Card.CanonicalURL,
			string(card.Card.Provider), string(card.Card.Kind), card.Card.Title,
			card.Card.Description, card.Card.SiteName, card.Card.AuthorName,
			duration, thumbnail, card.Card.EmbedURL, card.Card.CapturedAt,
			countArgument(card.Card.ViewCount), countArgument(card.Card.CommentCount),
			countArgument(card.Card.LikeCount), favicon,
		); err != nil {
			return fmt.Errorf("insert portable entry link card: %w", err)
		}
	}
	importer.entries++
	return nil
}

func (importer *PortableImporter) resolvePortableMedia(ctx context.Context, sha string) (any, error) {
	if sha == "" {
		return nil, nil
	}
	var mediaID string
	if err := importer.tx.QueryRowContext(ctx, `SELECT id FROM media WHERE sha256 = ?`, sha).Scan(&mediaID); err != nil {
		return nil, fmt.Errorf("resolve portable link card media %s: %w", sha, err)
	}
	return mediaID, nil
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
	var replyToID *domain.ID
	if value.ReplyToCommentID != nil {
		parsed, err := domain.ParseID(*value.ReplyToCommentID)
		if err != nil {
			return err
		}
		replyToID = &parsed
	}
	comment := domain.Comment{
		ID: id, EntryID: entryID, ParentID: parentID,
		AuthorKind: domain.CommentAuthorKind(value.AuthorKind), AuthorName: value.AuthorName,
		AuthorUserID: value.AuthorUserID, ReplyToCommentID: replyToID, ContentMD: value.ContentMD,
		Location: domain.CloneLocation(value.Location), CreatedAt: time.UnixMilli(value.CreatedAtMS).UTC(),
	}
	if err := comment.Validate(); err != nil {
		return err
	}
	if replyToID != nil {
		importer.pendingReplies = append(importer.pendingReplies, pendingReplyTarget{commentID: value.ID, targetID: replyToID.String()})
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
INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, author_user_id, content_md, created_at_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, value.ID, value.EntryID, value.ParentID, value.AuthorKind, value.AuthorName, value.AuthorUserID, value.ContentMD, value.CreatedAtMS); err != nil {
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
//
// The portable site record still carries the owner's handle, name, bio and
// pictures because that is the v1 wire contract, but those columns now live on
// the account. The write therefore lands in two places, on one transaction, so
// the site row and the account it names cannot end up describing different
// people.
//
// An archive that predates owner identity carries no owner_id, and there is
// nothing to name: the account the migrations generated keeps its handle and
// display name, and only the fields the archive actually states are applied.
func (importer *PortableImporter) SetSite(ctx context.Context, value ArchiveSite) error {
	if err := importer.requireOpen(); err != nil {
		return err
	}
	commentsEnabled := 0
	if value.CommentsEnabled {
		commentsEnabled = 1
	}
	result, err := importer.tx.ExecContext(ctx, `
UPDATE site_settings
SET title = ?, timezone = ?, public_base_url = ?, pinned_entry_id = ?, comments_enabled = ?
WHERE id = 1`, value.Title, value.Timezone, value.PublicBaseURL, value.PinnedPostID, commentsEnabled)
	if err != nil {
		return fmt.Errorf("replace portable site settings: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("replace portable site settings changed %d rows: %w", changed, err)
	}
	var owner string
	if err := importer.tx.QueryRowContext(ctx, `SELECT owner_id FROM site_settings WHERE id = 1`).Scan(&owner); err != nil {
		return fmt.Errorf("resolve portable site owner: %w", err)
	}
	if value.OwnerID != "" && value.OwnerID != owner {
		return errors.New("portable site owner does not name the imported owner account")
	}
	profile := `UPDATE users SET bio = ?, avatar_media_id = ?, cover_media_id = ? WHERE id = ?`
	arguments := []any{value.Bio, value.AvatarMediaID, value.CoverMediaID, owner}
	if value.OwnerID != "" {
		profile = `UPDATE users SET username = ?, display_name = ?, bio = ?, avatar_media_id = ?, cover_media_id = ? WHERE id = ?`
		arguments = []any{value.Username, value.DisplayName, value.Bio, value.AvatarMediaID, value.CoverMediaID, owner}
	}
	result, err = importer.tx.ExecContext(ctx, profile, arguments...)
	if err != nil {
		return fmt.Errorf("replace portable owner profile: %w", err)
	}
	changed, err = result.RowsAffected()
	if err != nil || changed != 1 {
		return fmt.Errorf("replace portable owner profile changed %d rows: %w", changed, err)
	}
	return nil
}

// Commit verifies counts, relational integrity, and the rebuilt FTS identity
// set before publishing the isolated transaction inside the staging database.
func (importer *PortableImporter) Commit(ctx context.Context, expected ArchiveCounts) error {
	if err := importer.requireOpen(); err != nil {
		return err
	}
	if importer.entries != expected.Entries || importer.comments != expected.Comments || importer.media != expected.Media ||
		importer.users != expected.Users || importer.identities != expected.Identities {
		return errors.New("portable import counts do not match the manifest")
	}
	for _, quote := range importer.pendingQuotes {
		if _, err := importer.tx.ExecContext(ctx, `INSERT INTO entry_quotes (entry_id, quoted_entry_id) VALUES (?, ?)`, quote.entryID, quote.targetID); err != nil {
			return fmt.Errorf("insert portable entry quote: %w", err)
		}
	}
	for _, reply := range importer.pendingReplies {
		if _, err := importer.tx.ExecContext(ctx, `
UPDATE comments SET reply_to_comment_id = ? WHERE id = ?`, reply.targetID, reply.commentID); err != nil {
			return fmt.Errorf("link portable comment %s to its reply target: %w", reply.commentID, err)
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
	var users, identities, ownerAccounts, sessions int64
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
	  (SELECT count(*) FROM (SELECT entry_id FROM entry_fts GROUP BY entry_id HAVING count(*) <> 1)),
	  (SELECT count(*) FROM users),
	  (SELECT count(*) FROM identities),
	  (SELECT count(*) FROM users AS account JOIN site_settings AS site ON site.owner_id = account.id
	    WHERE site.id = 1 AND account.role = 'owner'),
	  (SELECT count(*) FROM sessions)`).Scan(
		&entries, &comments, &mediaCount, &mediaGC, &ftsRows, &missingFTS, &extraFTS, &mismatchedFTS, &duplicateFTS,
		&users, &identities, &ownerAccounts, &sessions,
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
	// Sessions are excluded from the archive on purpose, so a staged generation
	// that somehow holds one is carrying a sign-in a restore was supposed to
	// end.
	if users != expected.Users || identities != expected.Identities || ownerAccounts != 1 || sessions != 0 {
		return errors.New("staged portable accounts are inconsistent")
	}
	rows, err = pool.QueryContext(ctx, `SELECT e.id, e.content_md, e.content_text, `+searchSourceCardsSQL+` FROM entries AS e ORDER BY e.id`)
	if err != nil {
		return fmt.Errorf("query staged normalized content: %w", err)
	}
	for rows.Next() {
		var id, contentMD, contentText, cardsJSON string
		if err := rows.Scan(&id, &contentMD, &contentText, &cardsJSON); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan staged normalized content: %w", err)
		}
		cards, err := decodeSearchSourceCards(cardsJSON)
		if err != nil {
			_ = rows.Close()
			return err
		}
		expectedText, err := app.DerivedSearchText(contentMD, cards)
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
