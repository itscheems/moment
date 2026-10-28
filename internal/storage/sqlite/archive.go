package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/moment/moment/internal/domain"
)

// ArchiveCounts are captured by the first statement in an archive read
// transaction. That statement establishes the WAL snapshot used by every
// subsequent archive query.
//
// Users and Identities join the three original counts because accounts are now
// authoritative portable records. Articles and ArticleDrafts join them for the
// same reason: an article's body and an unfinished one are authority, not
// derived state. Every field here must be read by that same first statement or
// it describes a different instant than the rest.
type ArchiveCounts struct {
	Entries    int64
	Comments   int64
	Media      int64
	Users      int64
	Identities int64
	// Articles counts published bodies, not article entries. The two are the
	// same number in any consistent database, which is exactly why counting
	// the bodies is worth doing: an envelope whose body was left behind shows
	// up here as a disagreement instead of restoring as a blank post.
	Articles      int64
	ArticleDrafts int64
}

// ArchiveImpact describes authoritative portable data currently selected by
// one SQLite snapshot. It is used only for replacement preflight estimates.
type ArchiveImpact struct {
	ArchiveCounts
	MediaBytes int64
}

// ArchiveSite is the complete portable site singleton.
type ArchiveSite struct {
	ProfileUserID   string
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

// ArchiveUser preserves a real account's profile, status and administrative role.
type ArchiveUser struct {
	ID            string
	Username      string
	DisplayName   string
	Bio           string
	AvatarMediaID *string
	CoverMediaID  *string
	Role          string
	Status        string
	// UsernameSetAtMS is nil while the handle is still the one generated from a
	// provider display name, which is what the one-time rename prompt keys off.
	UsernameSetAtMS *int64
	CreatedAtMS     int64
	UpdatedAtMS     int64
}

// ArchiveIdentity is one external credential bound to an ArchiveUser.
//
// Provider is carried verbatim rather than checked against a known set. The
// importer writes stored values back unchanged, so narrowing the value domain
// later would make every backup naming a retired provider unimportable.
type ArchiveIdentity struct {
	Provider         string
	ProviderUserID   string
	UserID           string
	Email            *string
	EmailVerified    bool
	EmailDeliverable bool
	DisplayName      string
	AvatarURL        *string
	CreatedAtMS      int64
	LastLoginAtMS    int64
}

// ArchiveUsage is one ordered, nullable-alt media use.
type ArchiveUsage struct {
	MediaID  string
	Position int
	AltText  *string
}

// ArchiveEntry excludes derived content_text and embeds ordered media uses.
type ArchiveEntry struct {
	PublicationState string
	AuthorUserID     string
	ID               string
	Kind             string
	// ContentMD is nil for an article, whose body is carried separately.
	ContentMD        *string
	Visibility       domain.Visibility
	CreatedAtMS      int64
	UpdatedAtMS      int64
	PublishedAtMS    int64
	RepostedAtMS     *int64
	EditedAtMS       *int64
	QuotedEntryID    *string
	QuotedCommentID  *string
	QuoteUnavailable bool
	Media            []ArchiveUsage
	Location         *domain.Location
	MapCard          *domain.MapCard
	LinkCards        []ArchiveLinkCard
}

// ArchiveEntryArticle is the body of one published article.
//
// It is a separate record from ArchiveEntry for the same reason it is a
// separate table: the entry is the timeline envelope and its ContentMD is nil
// precisely because the body is here. The two are one post, so anything that
// carries an article entry has to carry this beside it or it is carrying a
// blank post.
//
// Excerpt and the search text are deliberately absent: both are derived from
// the title and the body by one function each, and a stored second answer is
// free to disagree with the body it claims to summarize.
type ArchiveEntryArticle struct {
	EntryID string
	Title   string
	// BodyJSON is the stored document exactly as the column holds it. It is
	// canonicalized by whoever serializes it, not here, because the archive
	// writer is the one that has to promise byte-stable output.
	BodyJSON      string
	SchemaVersion int
	CoverMediaID  *string
}

// ArchiveArticleDraft is one unfinished article and the pictures it places.
//
// A draft is authority a backup cannot skip: it is writing nobody has published
// yet, so losing it in a restore loses work that exists nowhere else. Media is
// the article_draft_media projection rather than something re-derived on read,
// so the archive states what the database states.
type ArchiveArticleDraft struct {
	AuthorUserID  string
	ID            string
	Title         string
	BodyJSON      string
	SchemaVersion int
	CoverMediaID  *string
	// PublishedEntryID names the article this draft is the working copy of,
	// and is nil for a draft that was never published.
	PublishedEntryID *string
	Media            []ArchiveUsage
	CreatedAtMS      int64
	UpdatedAtMS      int64
}

// ArchiveLinkCard pairs a portable card with its thumbnail's content hash.
// Media identifiers are reassigned on import, so the hash is what survives a
// move between instances.
type ArchiveLinkCard struct {
	Card            domain.LinkCard
	ThumbnailSHA256 string
	FaviconSHA256   string
}

// ArchiveLinkCardsOf extracts the domain cards for list-level validation.
func ArchiveLinkCardsOf(values []ArchiveLinkCard) []domain.LinkCard {
	if values == nil {
		return nil
	}
	cards := make([]domain.LinkCard, 0, len(values))
	for _, value := range values {
		cards = append(cards, value.Card)
	}
	return cards
}

// ArchiveComment preserves authorship when known and the historical signature otherwise.
// ReplyToCommentID names another comment in the same conversation.
type ArchiveComment struct {
	AuthorSnapshot   *domain.CommentAuthorSnapshot
	ID               string
	EntryID          string
	ParentID         *string
	AuthorName       string
	AuthorUserID     *string
	ReplyToCommentID *string
	ContentMD        string
	CreatedAtMS      int64
	Media            []ArchiveUsage
	Location         *domain.Location
	Pinned           bool
}

// ArchiveMediaGrant preserves account reuse permission independently of references.
type ArchiveMediaGrant struct {
	UserID      string `json:"user_id"`
	CreatedAtMS int64  `json:"created_at_ms"`
}

// ArchiveMedia includes portable metadata, retention state and a private source path.
type ArchiveMedia struct {
	UnreferencedSinceMS *int64
	Grants              []ArchiveMediaGrant
	ID                  string
	SHA256              string
	StoragePath         string
	OriginalName        string
	MIMEType            string
	ByteSize            int64
	Width               uint32
	Height              uint32
	CreatedAtMS         int64
}

const portableMediaSQL = `(` + mediaReferencedSQL + ` OR EXISTS (SELECT 1 FROM media_grants WHERE media_grants.media_id=media.id))`

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
    (SELECT count(*) FROM media WHERE `+portableMediaSQL+`),
    (SELECT count(*) FROM users),
    (SELECT count(*) FROM identities),
    (SELECT count(*) FROM entry_article),
    (SELECT count(*) FROM article_drafts)`).Scan(
		&snapshot.counts.Entries,
		&snapshot.counts.Comments,
		&snapshot.counts.Media,
		&snapshot.counts.Users,
		&snapshot.counts.Identities,
		&snapshot.counts.Articles,
		&snapshot.counts.ArticleDrafts,
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
WHERE `+portableMediaSQL).Scan(&impact.MediaBytes); err != nil {
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
	var profileAccounts int64
	if err := snapshot.tx.QueryRowContext(ctx, `
SELECT count(*)
FROM users AS account
JOIN site_settings AS site ON site.profile_user_id = account.id
WHERE site.id = 1`).Scan(&profileAccounts); err != nil {
		return fmt.Errorf("check archive profile account: %w", err)
	}
	if profileAccounts != 1 {
		return errors.New("archive snapshot does not resolve its site profile")
	}
	var activeAdmins int64
	if err := snapshot.tx.QueryRowContext(ctx, `SELECT count(*) FROM users WHERE role='admin' AND status='active'`).Scan(&activeAdmins); err != nil {
		return err
	}
	if activeAdmins < 1 {
		return errors.New("archive snapshot has no active administrator")
	}
	// The foreign key runs from entry_article to entries, so PRAGMA
	// foreign_key_check sees a body without an entry. It cannot see the other
	// direction -- an article entry whose body row is missing -- and that is
	// the one that would export as a post with no words in it.
	var bodylessArticles int64
	if err := snapshot.tx.QueryRowContext(ctx, `
SELECT count(*)
FROM entries AS entry
WHERE entry.kind = 'article'
  AND NOT EXISTS (SELECT 1 FROM entry_article AS article WHERE article.entry_id = entry.id)`).Scan(&bodylessArticles); err != nil {
		return fmt.Errorf("check archive article bodies: %w", err)
	}
	if bodylessArticles != 0 {
		return errors.New("archive snapshot contains an article entry with no body")
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
SELECT site.profile_user_id, account.username, account.display_name, site.title, account.bio, site.timezone,
       site.public_base_url, account.avatar_media_id, account.cover_media_id, site.pinned_entry_id,
       site.comments_enabled
FROM site_settings AS site
JOIN users AS account ON account.id = site.profile_user_id
WHERE site.id = 1`).Scan(&value.ProfileUserID, &value.Username, &value.DisplayName, &value.Title, &value.Bio, &value.Timezone, &publicURL, &avatar, &cover, &pinned, &comments)
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

// Users streams every account in stable ID order.
func (snapshot *ArchiveSnapshot) Users(ctx context.Context, yield func(ArchiveUser) error) error {
	if err := snapshot.requireOpen(); err != nil {
		return err
	}
	if yield == nil {
		return errors.New("archive user callback is required")
	}
	rows, err := snapshot.tx.QueryContext(ctx, `
SELECT id, username, display_name, bio, avatar_media_id, cover_media_id,
       role, status, username_set_at_ms, created_at_ms, updated_at_ms
FROM users
ORDER BY id`)
	if err != nil {
		return fmt.Errorf("query archive users: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var value ArchiveUser
		var avatar, cover sql.NullString
		var usernameSetAt sql.NullInt64
		if err := rows.Scan(
			&value.ID, &value.Username, &value.DisplayName, &value.Bio, &avatar, &cover,
			&value.Role, &value.Status, &usernameSetAt, &value.CreatedAtMS, &value.UpdatedAtMS,
		); err != nil {
			return fmt.Errorf("scan archive user: %w", err)
		}
		if avatar.Valid {
			value.AvatarMediaID = &avatar.String
		}
		if cover.Valid {
			value.CoverMediaID = &cover.String
		}
		if usernameSetAt.Valid {
			moment := usernameSetAt.Int64
			value.UsernameSetAtMS = &moment
		}
		if err := yield(value); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate archive users: %w", err)
	}
	return nil
}

// Identities streams every external credential in stable natural-key order.
// Sessions are deliberately excluded: a backup that carried them would revive
// dead sign-ins on restore, which is exactly when that is least wanted.
func (snapshot *ArchiveSnapshot) Identities(ctx context.Context, yield func(ArchiveIdentity) error) error {
	if err := snapshot.requireOpen(); err != nil {
		return err
	}
	if yield == nil {
		return errors.New("archive identity callback is required")
	}
	rows, err := snapshot.tx.QueryContext(ctx, `
SELECT provider, provider_user_id, user_id, email, email_verified, email_deliverable,
       display_name, avatar_url, created_at_ms, last_login_at_ms
FROM identities
ORDER BY provider, provider_user_id`)
	if err != nil {
		return fmt.Errorf("query archive identities: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var value ArchiveIdentity
		var email, avatarURL sql.NullString
		var verified, deliverable int
		if err := rows.Scan(
			&value.Provider, &value.ProviderUserID, &value.UserID, &email, &verified, &deliverable,
			&value.DisplayName, &avatarURL, &value.CreatedAtMS, &value.LastLoginAtMS,
		); err != nil {
			return fmt.Errorf("scan archive identity: %w", err)
		}
		if email.Valid {
			value.Email = &email.String
		}
		if avatarURL.Valid {
			value.AvatarURL = &avatarURL.String
		}
		value.EmailVerified = verified == 1
		value.EmailDeliverable = deliverable == 1
		if err := yield(value); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate archive identities: %w", err)
	}
	return nil
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
SELECT e.publication_state, e.author_user_id, e.id, e.kind, e.content_md, e.visibility, e.created_at_ms, e.updated_at_ms, e.published_at_ms, e.reposted_at_ms, e.edited_at_ms, quote.entry_id, quote.quoted_entry_id, quote.quoted_comment_id,
       usage.media_id, usage.position, usage.alt_text,
       location.name, location.display_name, location.country_code, location.country,
       location.region, location.city, location.district, location.category,
       location.provider, location.provider_place_id, location.latitude_e7,
       location.longitude_e7, location.coordinate_system, map_card.map_card_json
FROM entries AS e
LEFT JOIN entry_media AS usage ON usage.entry_id = e.id
LEFT JOIN entry_location AS location ON location.entry_id = e.id
LEFT JOIN entry_quotes AS quote ON quote.entry_id = e.id
LEFT JOIN entry_map_card AS map_card ON map_card.entry_id = e.id
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
		var repostedAt, editedAt sql.NullInt64
		var quoteEntryID, quotedEntryID, quotedCommentID sql.NullString
		var name, displayName, countryCode, country, region, city, district, category, provider, providerID, coordinateSystem sql.NullString
		var latitude, longitude sql.NullInt64
		var visibility string
		var mapJSON sql.NullString
		if err := rows.Scan(
			&entry.PublicationState, &entry.AuthorUserID, &entry.ID, &entry.Kind, &entry.ContentMD, &visibility, &entry.CreatedAtMS, &entry.UpdatedAtMS, &entry.PublishedAtMS, &repostedAt, &editedAt, &quoteEntryID, &quotedEntryID, &quotedCommentID,
			&mediaID, &position, &alt, &name, &displayName, &countryCode, &country, &region, &city,
			&district, &category, &provider, &providerID, &latitude, &longitude, &coordinateSystem, &mapJSON,
		); err != nil {
			return fmt.Errorf("scan archive entry: %w", err)
		}
		if mapJSON.Valid {
			card, err := domain.ParseMapCard([]byte(mapJSON.String))
			if err != nil {
				return fmt.Errorf("invalid archived map: %w", err)
			}
			entry.MapCard = card
		}
		entry.Visibility = domain.Visibility(visibility)
		if editedAt.Valid {
			value := editedAt.Int64
			entry.EditedAtMS = &value
		}
		if repostedAt.Valid {
			value := repostedAt.Int64
			entry.RepostedAtMS = &value
		}
		if quotedEntryID.Valid {
			value := quotedEntryID.String
			entry.QuotedEntryID = &value
		}
		if quotedCommentID.Valid {
			value := quotedCommentID.String
			entry.QuotedCommentID = &value
		}
		entry.QuoteUnavailable = quoteEntryID.Valid && !quotedEntryID.Valid && !quotedCommentID.Valid
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
			cards, cardErr := snapshot.entryLinkCards(ctx, entry.ID)
			if cardErr != nil {
				return cardErr
			}
			entry.LinkCards = cards
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

// EntryArticles streams every published body in the entry order Entries uses,
// so a reader consuming both members in lockstep never has to seek.
func (snapshot *ArchiveSnapshot) EntryArticles(ctx context.Context, yield func(ArchiveEntryArticle) error) error {
	if err := snapshot.requireOpen(); err != nil {
		return err
	}
	if yield == nil {
		return errors.New("archive article callback is required")
	}
	rows, err := snapshot.tx.QueryContext(ctx, `
SELECT entry_id, title, body_json, body_schema_version, cover_media_id
FROM entry_article
ORDER BY entry_id`)
	if err != nil {
		return fmt.Errorf("query archive articles: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var value ArchiveEntryArticle
		var cover sql.NullString
		if err := rows.Scan(&value.EntryID, &value.Title, &value.BodyJSON, &value.SchemaVersion, &cover); err != nil {
			return fmt.Errorf("scan archive article: %w", err)
		}
		if cover.Valid {
			value.CoverMediaID = &cover.String
		}
		if err := yield(value); err != nil {
			return err
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate archive articles: %w", err)
	}
	return nil
}

// ArticleDrafts streams every unfinished article in stable ID order while
// folding its bounded picture projection, the way Entries folds entry_media.
func (snapshot *ArchiveSnapshot) ArticleDrafts(ctx context.Context, yield func(ArchiveArticleDraft) error) error {
	if err := snapshot.requireOpen(); err != nil {
		return err
	}
	if yield == nil {
		return errors.New("archive article draft callback is required")
	}
	rows, err := snapshot.tx.QueryContext(ctx, `
SELECT draft.author_user_id, draft.id, draft.title, draft.body_json, draft.body_schema_version,
       draft.cover_media_id, draft.published_entry_id,
       draft.created_at_ms, draft.updated_at_ms,
       usage.media_id, usage.position, usage.alt_text
FROM article_drafts AS draft
LEFT JOIN article_draft_media AS usage ON usage.draft_id = draft.id
ORDER BY draft.id, usage.position`)
	if err != nil {
		return fmt.Errorf("query archive article drafts: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var current *ArchiveArticleDraft
	for rows.Next() {
		var draft ArchiveArticleDraft
		var cover, published, mediaID, alt sql.NullString
		var position sql.NullInt64
		if err := rows.Scan(
			&draft.AuthorUserID, &draft.ID, &draft.Title, &draft.BodyJSON, &draft.SchemaVersion,
			&cover, &published, &draft.CreatedAtMS, &draft.UpdatedAtMS,
			&mediaID, &position, &alt,
		); err != nil {
			return fmt.Errorf("scan archive article draft: %w", err)
		}
		if current == nil || current.ID != draft.ID {
			if current != nil {
				if err := yield(*current); err != nil {
					return err
				}
			}
			if cover.Valid {
				draft.CoverMediaID = &cover.String
			}
			if published.Valid {
				draft.PublishedEntryID = &published.String
			}
			draft.Media = make([]ArchiveUsage, 0, 4)
			current = &draft
		}
		if mediaID.Valid {
			if !position.Valid {
				return errors.New("archive article draft usage has no position")
			}
			usage := ArchiveUsage{MediaID: mediaID.String, Position: int(position.Int64)}
			if alt.Valid {
				text := alt.String
				usage.AltText = &text
			}
			current.Media = append(current.Media, usage)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate archive article drafts: %w", err)
	}
	if current != nil {
		return yield(*current)
	}
	return nil
}

// entryLinkCards reads one entry's cards. It is a separate statement because
// the entry stream already fans out to one row per media usage; joining a
// second one-to-many table would multiply those rows.
func (snapshot *ArchiveSnapshot) entryLinkCards(ctx context.Context, entryID string) ([]ArchiveLinkCard, error) {
	rows, err := snapshot.tx.QueryContext(ctx, `
SELECT card.position, card.source_url, card.canonical_url, card.provider,
       card.card_kind, card.title, card.description, card.site_name,
       card.author_name, card.duration_seconds, card.embed_url,
       card.captured_at_ms, card.view_count, card.comment_count,
       card.like_count, thumbnail.sha256, favicon.sha256
FROM entry_link_card AS card
LEFT JOIN media AS thumbnail ON thumbnail.id = card.thumbnail_media_id
LEFT JOIN media AS favicon ON favicon.id = card.favicon_media_id
WHERE card.entry_id = ?
ORDER BY card.position`, entryID)
	if err != nil {
		return nil, fmt.Errorf("query archive entry link cards: %w", err)
	}
	defer func() { _ = rows.Close() }()
	var cards []ArchiveLinkCard
	for rows.Next() {
		var card domain.LinkCard
		var duration, views, comments, likes sql.NullInt64
		var sha, faviconSHA sql.NullString
		if err := rows.Scan(
			&card.Position, &card.SourceURL, &card.CanonicalURL, &card.Provider,
			&card.Kind, &card.Title, &card.Description, &card.SiteName,
			&card.AuthorName, &duration, &card.EmbedURL, &card.CapturedAt,
			&views, &comments, &likes, &sha, &faviconSHA,
		); err != nil {
			return nil, fmt.Errorf("scan archive entry link card: %w", err)
		}
		if duration.Valid {
			seconds := int(duration.Int64)
			card.DurationSeconds = &seconds
		}
		card.ViewCount = nullableCount(views)
		card.CommentCount = nullableCount(comments)
		card.LikeCount = nullableCount(likes)
		if err := card.Validate(); err != nil {
			return nil, errors.New("archive entry link card is invalid")
		}
		cards = append(cards, ArchiveLinkCard{
			Card:            card,
			ThumbnailSHA256: sha.String,
			FaviconSHA256:   faviconSHA.String,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate archive entry link cards: %w", err)
	}
	return cards, nil
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
SELECT comment.id, comment.entry_id, comment.parent_id,
       comment.author_name, comment.author_user_id, comment.reply_to_comment_id,
       comment.content_md, comment.created_at_ms,
       location.name, location.display_name, location.country_code,
       location.country, location.region, location.city, location.district,
       location.category, location.provider, location.provider_place_id,
       location.latitude_e7, location.longitude_e7, location.coordinate_system,
       usage.media_id, usage.position, usage.alt_text,
       pin.comment_id IS NOT NULL, comment.author_username, comment.author_display_name, comment.author_avatar_media_id
FROM comments AS comment
LEFT JOIN comment_media AS usage ON usage.comment_id = comment.id
LEFT JOIN comment_location AS location ON location.comment_id = comment.id
LEFT JOIN comment_pins AS pin ON pin.comment_id = comment.id
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
		var snapshotUsername, snapshotName, snapshotAvatar sql.NullString
		var authorUserID, replyToCommentID sql.NullString
		var mediaID, alt sql.NullString
		var position sql.NullInt64
		var locationName, locationDisplayName, countryCode, country, region, city, district, category, provider, providerPlaceID sql.NullString
		var latitude, longitude sql.NullInt64
		var coordinateSystem sql.NullString
		if err := rows.Scan(
			&value.ID, &value.EntryID, &parent, &value.AuthorName,
			&authorUserID, &replyToCommentID, &value.ContentMD, &value.CreatedAtMS,
			&locationName, &locationDisplayName,
			&countryCode, &country, &region, &city, &district, &category, &provider,
			&providerPlaceID, &latitude, &longitude, &coordinateSystem,
			&mediaID, &position, &alt, &value.Pinned, &snapshotUsername, &snapshotName, &snapshotAvatar,
		); err != nil {
			return fmt.Errorf("scan archive comment: %w", err)
		}
		if parent.Valid {
			value.ParentID = &parent.String
		}
		if authorUserID.Valid {
			value.AuthorUserID = &authorUserID.String
		}
		if replyToCommentID.Valid {
			value.ReplyToCommentID = &replyToCommentID.String
		}
		if locationName.Valid {
			if !locationDisplayName.Valid || !countryCode.Valid || !country.Valid || !region.Valid || !city.Valid ||
				!district.Valid || !category.Valid || !provider.Valid || !providerPlaceID.Valid ||
				latitude.Valid != longitude.Valid || latitude.Valid != coordinateSystem.Valid {
				return errors.New("archive comment location is incomplete")
			}
			value.Location = &domain.Location{
				Name: locationName.String, DisplayName: locationDisplayName.String,
				CountryCode: countryCode.String, Country: country.String, Region: region.String,
				City: city.String, District: district.String, Category: category.String,
				Provider: provider.String, ProviderPlaceID: providerPlaceID.String,
			}
			if latitude.Valid {
				value.Location.Point = &domain.GeoPoint{LatitudeE7: latitude.Int64, LongitudeE7: longitude.Int64, System: domain.CoordinateSystem(coordinateSystem.String)}
			}
			if err := value.Location.Validate(); err != nil {
				return errors.New("archive comment location is invalid")
			}
		}
		if snapshotName.Valid {
			value.AuthorSnapshot = &domain.CommentAuthorSnapshot{Username: snapshotUsername.String, DisplayName: snapshotName.String}
			if snapshotAvatar.Valid {
				value.AuthorSnapshot.AvatarMediaID = &snapshotAvatar.String
			}
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
SELECT id, sha256, storage_path, original_name, mime_type, byte_size, width, height, created_at_ms, unreferenced_since_ms,
 (SELECT json_group_array(json_object('user_id',granted.user_id,'created_at_ms',granted.created_at_ms)) FROM
  (SELECT user_id,created_at_ms FROM media_grants WHERE media_id=media.id ORDER BY user_id) AS granted)
FROM media
WHERE `+portableMediaSQL+`
ORDER BY sha256, id`)
	if err != nil {
		return fmt.Errorf("query archive media: %w", err)
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var value ArchiveMedia
		var grants string
		var unreferenced sql.NullInt64
		if err := rows.Scan(&value.ID, &value.SHA256, &value.StoragePath, &value.OriginalName, &value.MIMEType, &value.ByteSize, &value.Width, &value.Height, &value.CreatedAtMS, &unreferenced, &grants); err != nil {
			return fmt.Errorf("scan archive media: %w", err)
		}
		if unreferenced.Valid {
			value.UnreferencedSinceMS = &unreferenced.Int64
		}
		if err := json.Unmarshal([]byte(grants), &value.Grants); err != nil {
			return fmt.Errorf("decode media grants: %w", err)
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
