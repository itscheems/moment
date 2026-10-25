package archive

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const mergeReviewLimitPerClassification = 200

// MergeReviewSource identifies which immutable authority a media preview
// belongs to. It is never accepted as a merge decision.
type MergeReviewSource string

const (
	// MergeReviewLocal and MergeReviewIncoming identify the side that owns a
	// display-only media projection during conflict review.
	MergeReviewLocal MergeReviewSource = "local"
	// MergeReviewIncoming marks an archive-owned review projection.
	MergeReviewIncoming MergeReviewSource = "incoming"
)

// MergeReviewMedia is display-only metadata for one referenced original.
type MergeReviewMedia struct {
	Source       MergeReviewSource `json:"source"`
	ID           string            `json:"id"`
	OriginalName string            `json:"original_name"`
	MIMEType     string            `json:"mime_type"`
	ByteSize     int64             `json:"byte_size"`
	Width        uint32            `json:"width"`
	Height       uint32            `json:"height"`
	Position     int               `json:"position"`
	AltText      *string           `json:"alt_text"`
}

// MergeReviewLocation is a stable provider-independent location snapshot for
// display. Coordinates are optional and never required to understand a place.
type MergeReviewLocation struct {
	Name             string                   `json:"name"`
	DisplayName      string                   `json:"display_name"`
	CountryCode      string                   `json:"country_code"`
	Country          string                   `json:"country"`
	Region           string                   `json:"region"`
	City             string                   `json:"city"`
	District         string                   `json:"district"`
	Category         string                   `json:"category"`
	Provider         string                   `json:"provider"`
	ProviderPlaceID  string                   `json:"provider_place_id"`
	Latitude         *float64                 `json:"latitude"`
	Longitude        *float64                 `json:"longitude"`
	CoordinateSystem *domain.CoordinateSystem `json:"coordinate_system"`
}

// MergeReviewLinkCard is a display-only card summary. A card can be the only
// difference between two versions of a post, so a review that omitted it would
// show two identical-looking sides beside the words "link_cards changed".
type MergeReviewLinkCard struct {
	Position     int    `json:"position"`
	SourceURL    string `json:"source_url"`
	CanonicalURL string `json:"canonical_url"`
	Provider     string `json:"provider"`
	CardKind     string `json:"card_kind"`
	Title        string `json:"title"`
	SiteName     string `json:"site_name"`
}

// MergeReviewEntity contains recognizable post, comment or account content.
// The plan ID and selection remain the only commit authority; this projection
// is only for humans and agents to understand a decision.
//
// Every field a changed-field comparator can name must appear here. A field
// that is compared but not projected produces the worst possible review: two
// sides rendered identically under a label saying they differ, with no way to
// tell whether the plan is wrong or the display is. mergeReviewFieldKeys states
// that correspondence and a test holds the two comparators to it.
type MergeReviewEntity struct {
	ID string `json:"id"`
	// ContentMD is null for an article, whose body is not Markdown.
	ContentMD *string         `json:"content_md"`
	MapCard   *domain.MapCard `json:"map_card,omitempty"`
	EntryID   *string         `json:"entry_id,omitempty"`
	ParentID  *string         `json:"parent_id,omitempty"`
	// Kind is the post kind; comments and accounts leave it unset.
	Kind             *string    `json:"kind,omitempty"`
	AuthorName       *string    `json:"author_name,omitempty"`
	AuthorUserID     *string    `json:"author_user_id,omitempty"`
	ReplyToCommentID *string    `json:"reply_to_comment_id,omitempty"`
	Pinned           *bool      `json:"pinned,omitempty"`
	RepostedAt       *time.Time `json:"reposted_at,omitempty"`
	EditedAt         *time.Time `json:"edited_at,omitempty"`
	Visibility       *string    `json:"visibility,omitempty"`
	QuotedEntryID    *string    `json:"quoted_entry_id,omitempty"`
	QuotedCommentID  *string    `json:"quoted_comment_id,omitempty"`
	QuoteUnavailable *bool      `json:"quote_unavailable,omitempty"`
	// Username through UsernameSetAt describe an account rather than content.
	Username    *string `json:"username,omitempty"`
	DisplayName *string `json:"display_name,omitempty"`
	Bio         *string `json:"bio,omitempty"`
	Role        *string `json:"role,omitempty"`
	Status      *string `json:"status,omitempty"`
	// AvatarMediaID and CoverMediaID are shown as identifiers as well as
	// pictures. The picture resolves only when the referenced original is in
	// the same authority, and a changed avatar that resolves on neither side
	// would otherwise be a difference with nothing to see.
	AvatarMediaID    *string               `json:"avatar_media_id,omitempty"`
	CoverMediaID     *string               `json:"cover_media_id,omitempty"`
	UsernameSetAt    *time.Time            `json:"username_set_at,omitempty"`
	UpdatedAt        *time.Time            `json:"updated_at,omitempty"`
	Provider         *string               `json:"provider,omitempty"`
	ProviderUserID   *string               `json:"provider_user_id,omitempty"`
	UserID           *string               `json:"user_id,omitempty"`
	Email            *string               `json:"email,omitempty"`
	EmailVerified    *bool                 `json:"email_verified,omitempty"`
	EmailDeliverable *bool                 `json:"email_deliverable,omitempty"`
	AvatarURL        *string               `json:"avatar_url,omitempty"`
	LastLoginAt      *time.Time            `json:"last_login_at,omitempty"`
	CreatedAt        time.Time             `json:"created_at"`
	PublishedAt      *time.Time            `json:"published_at,omitempty"`
	Location         *MergeReviewLocation  `json:"location,omitempty"`
	Media            []MergeReviewMedia    `json:"media"`
	LinkCards        []MergeReviewLinkCard `json:"link_cards,omitempty"`
	Avatar           *MergeReviewMedia     `json:"avatar,omitempty"`
	Cover            *MergeReviewMedia     `json:"cover,omitempty"`
}

// MergeReviewItem places the actual local and incoming versions beside the
// stable classification from MergePlan.
type MergeReviewItem struct {
	Kind           MergeEntityKind     `json:"kind"`
	ID             string              `json:"id"`
	Classification MergeClassification `json:"classification"`
	ChangedFields  []string            `json:"changed_fields"`
	Local          *MergeReviewEntity  `json:"local,omitempty"`
	Incoming       *MergeReviewEntity  `json:"incoming,omitempty"`
}

// MergeReviewBucket is independently bounded so a recent local-only post is
// still visible even when another classification contains many records.
type MergeReviewBucket struct {
	Total     int               `json:"total"`
	Truncated bool              `json:"truncated"`
	Items     []MergeReviewItem `json:"items"`
}

// MergeReviewSiteField augments canonical field values with recognizable
// avatar/cover media or pinned-post content where applicable.
type MergeReviewSiteField struct {
	Field          string              `json:"field"`
	Classification MergeClassification `json:"classification"`
	LocalValue     json.RawMessage     `json:"local_value"`
	IncomingValue  json.RawMessage     `json:"incoming_value"`
	LocalMedia     *MergeReviewMedia   `json:"local_media,omitempty"`
	IncomingMedia  *MergeReviewMedia   `json:"incoming_media,omitempty"`
	LocalEntry     *MergeReviewEntity  `json:"local_entry,omitempty"`
	IncomingEntry  *MergeReviewEntity  `json:"incoming_entry,omitempty"`
}

// MergeReview is a bounded display projection shared by Web and CLI.
type MergeReview struct {
	IncomingOnly MergeReviewBucket      `json:"incoming_only"`
	LocalOnly    MergeReviewBucket      `json:"local_only"`
	Conflicts    MergeReviewBucket      `json:"conflicts"`
	SiteFields   []MergeReviewSiteField `json:"site_fields"`
}

// mergeReviewFieldKeys maps every name a changed-field comparator can produce
// to the MergeReviewEntity JSON key that shows it.
//
// It is the standing answer to a failure this review has already had: a field
// compared but never projected renders two sides identically under a label
// saying they differ. The table is asserted against the comparators by test, so
// adding a comparison without a projection fails there rather than in a
// reviewer's face.
var mergeReviewFieldKeys = map[MergeEntityKind]map[string]string{
	MergeEntry: {
		"kind": "kind", "content_md": "content_md", "created_at": "created_at",
		"updated_at": "updated_at", "published_at": "published_at", "reposted_at": "reposted_at", "edited_at": "edited_at", "visibility": "visibility",
		"quoted_entry_id": "quoted_entry_id", "quote_unavailable": "quote_unavailable",
		"media": "media", "location": "location", "link_cards": "link_cards", "map_card": "map_card",
	},
	MergeComment: {
		"entry_id": "entry_id", "parent_id": "parent_id",
		"author_name": "author_name", "author_user_id": "author_user_id",
		"reply_to_comment_id": "reply_to_comment_id", "content_md": "content_md",
		"created_at": "created_at", "media": "media", "location": "location", "pinned": "pinned",
	},
	MergeUser: {
		"username": "username", "display_name": "display_name", "bio": "bio",
		"avatar_media_id": "avatar_media_id", "cover_media_id": "cover_media_id", "role": "role",
		"status": "status", "username_set_at": "username_set_at",
		"created_at": "created_at", "updated_at": "updated_at",
	},
	MergeIdentity: {
		"user_id": "user_id", "email": "email", "email_verified": "email_verified",
		"email_deliverable": "email_deliverable", "display_name": "display_name",
		"avatar_url": "avatar_url", "created_at": "created_at", "last_login_at": "last_login_at",
	},
}

func buildMergeReview(plan MergePlan, local, incoming PortableAuthority) (MergeReview, error) {
	localEntries, incomingEntries := indexEntries(local.Entries), indexEntries(incoming.Entries)
	localComments, incomingComments := indexComments(local.Comments), indexComments(incoming.Comments)
	localUsers, incomingUsers := indexUsers(local.Users), indexUsers(incoming.Users)
	localIdentities, incomingIdentities := indexIdentities(local.Identities), indexIdentities(incoming.Identities)
	localMedia, _ := indexMedia(local.Media)
	incomingMedia, _ := indexMedia(incoming.Media)

	review := MergeReview{
		IncomingOnly: MergeReviewBucket{Items: []MergeReviewItem{}},
		LocalOnly:    MergeReviewBucket{Items: []MergeReviewItem{}},
		Conflicts:    MergeReviewBucket{Items: []MergeReviewItem{}},
		SiteFields:   []MergeReviewSiteField{},
	}
	for _, item := range plan.Items {
		if item.Kind == MergeMedia || item.Classification == MergeUnchanged {
			continue
		}
		value := MergeReviewItem{
			Kind: item.Kind, ID: item.ID, Classification: item.Classification,
			ChangedFields: append([]string{}, item.ChangedFields...),
		}
		switch item.Kind {
		case MergeEntry:
			if entry, ok := localEntries[item.ID]; ok {
				entity := reviewEntry(entry, MergeReviewLocal, localMedia)
				value.Local = &entity
			}
			if entry, ok := incomingEntries[item.ID]; ok {
				entity := reviewEntry(entry, MergeReviewIncoming, incomingMedia)
				value.Incoming = &entity
			}
		case MergeComment:
			if comment, ok := localComments[item.ID]; ok {
				entity := reviewComment(comment, MergeReviewLocal, localMedia)
				value.Local = &entity
			}
			if comment, ok := incomingComments[item.ID]; ok {
				entity := reviewComment(comment, MergeReviewIncoming, incomingMedia)
				value.Incoming = &entity
			}
		case MergeUser:
			if account, ok := localUsers[item.ID]; ok {
				entity := reviewUser(account, MergeReviewLocal, localMedia)
				value.Local = &entity
			}
			if account, ok := incomingUsers[item.ID]; ok {
				entity := reviewUser(account, MergeReviewIncoming, incomingMedia)
				value.Incoming = &entity
			}
		case MergeIdentity:
			if identity, ok := localIdentities[item.ID]; ok {
				entity := reviewIdentity(identity)
				value.Local = &entity
			}
			if identity, ok := incomingIdentities[item.ID]; ok {
				entity := reviewIdentity(identity)
				value.Incoming = &entity
			}
		default:
			return MergeReview{}, fmt.Errorf("unsupported review kind %q", item.Kind)
		}
		switch item.Classification {
		case MergeIncomingOnly:
			review.IncomingOnly.Items = append(review.IncomingOnly.Items, value)
		case MergeLocalOnly:
			review.LocalOnly.Items = append(review.LocalOnly.Items, value)
		case MergeConflict:
			review.Conflicts.Items = append(review.Conflicts.Items, value)
		}
	}
	review.IncomingOnly = boundReviewBucket(review.IncomingOnly)
	review.LocalOnly = boundReviewBucket(review.LocalOnly)
	review.Conflicts = boundReviewBucket(review.Conflicts)

	for _, field := range plan.SiteFields {
		value := MergeReviewSiteField{
			Field: field.Field, Classification: field.Classification,
			LocalValue: cloneRawJSON(field.LocalValue), IncomingValue: cloneRawJSON(field.IncomingValue),
		}
		switch field.Field {
		case "avatar_media_id":
			value.LocalMedia = reviewReferencedMedia(local.Site.AvatarMediaID, MergeReviewLocal, localMedia)
			value.IncomingMedia = reviewReferencedMedia(incoming.Site.AvatarMediaID, MergeReviewIncoming, incomingMedia)
		case "cover_media_id":
			value.LocalMedia = reviewReferencedMedia(local.Site.CoverMediaID, MergeReviewLocal, localMedia)
			value.IncomingMedia = reviewReferencedMedia(incoming.Site.CoverMediaID, MergeReviewIncoming, incomingMedia)
		case "pinned_post_id":
			value.LocalEntry = reviewReferencedEntry(local.Site.PinnedPostID, MergeReviewLocal, localEntries, localMedia)
			value.IncomingEntry = reviewReferencedEntry(incoming.Site.PinnedPostID, MergeReviewIncoming, incomingEntries, incomingMedia)
		}
		review.SiteFields = append(review.SiteFields, value)
	}
	if err := ValidateMergeReview(plan, review); err != nil {
		return MergeReview{}, err
	}
	return review, nil
}

func boundReviewBucket(bucket MergeReviewBucket) MergeReviewBucket {
	sort.Slice(bucket.Items, func(i, j int) bool {
		left, right := reviewSortTime(bucket.Items[i]), reviewSortTime(bucket.Items[j])
		if !left.Equal(right) {
			return left.After(right)
		}
		if bucket.Items[i].Kind != bucket.Items[j].Kind {
			return bucket.Items[i].Kind < bucket.Items[j].Kind
		}
		return bucket.Items[i].ID > bucket.Items[j].ID
	})
	bucket.Total = len(bucket.Items)
	if len(bucket.Items) > mergeReviewLimitPerClassification {
		bucket.Items = bucket.Items[:mergeReviewLimitPerClassification]
		bucket.Truncated = true
	}
	return bucket
}

func reviewSortTime(item MergeReviewItem) time.Time {
	for _, value := range []*MergeReviewEntity{item.Incoming, item.Local} {
		if value == nil {
			continue
		}
		if value.PublishedAt != nil {
			return *value.PublishedAt
		}
		return value.CreatedAt
	}
	return time.Time{}
}

func reviewEntry(value sqlitestore.ArchiveEntry, source MergeReviewSource, mediaByID map[string]sqlitestore.ArchiveMedia) MergeReviewEntity {
	created, updated, published := archiveTime(value.CreatedAtMS), archiveTime(value.UpdatedAtMS), archiveTime(value.PublishedAtMS)
	kind, quoteUnavailable := value.Kind, value.QuoteUnavailable
	entity := MergeReviewEntity{
		ID: value.ID, ContentMD: value.ContentMD, MapCard: domain.CloneMapCard(value.MapCard), CreatedAt: created, Kind: &kind,
		UpdatedAt: &updated, PublishedAt: &published, Location: reviewLocation(value.Location),
		QuotedEntryID: cloneString(value.QuotedEntryID), QuotedCommentID: cloneString(value.QuotedCommentID), QuoteUnavailable: &quoteUnavailable,
		Media: reviewUsages(value.Media, source, mediaByID), LinkCards: reviewLinkCards(value.LinkCards),
	}
	if value.EditedAtMS != nil {
		edited := archiveTime(*value.EditedAtMS)
		entity.EditedAt = &edited
	}
	visibility := string(value.Visibility)
	entity.Visibility = &visibility
	if value.RepostedAtMS != nil {
		reposted := archiveTime(*value.RepostedAtMS)
		entity.RepostedAt = &reposted
	}
	return entity
}

func reviewComment(value sqlitestore.ArchiveComment, source MergeReviewSource, mediaByID map[string]sqlitestore.ArchiveMedia) MergeReviewEntity {
	entryID, authorName, pinned := value.EntryID, value.AuthorName, value.Pinned
	body := value.ContentMD
	return MergeReviewEntity{
		ID: value.ID, ContentMD: &body, EntryID: &entryID,
		ParentID: cloneString(value.ParentID), AuthorName: &authorName,
		AuthorUserID: cloneString(value.AuthorUserID), ReplyToCommentID: cloneString(value.ReplyToCommentID),
		Pinned: &pinned, Location: reviewLocation(value.Location),
		CreatedAt: archiveTime(value.CreatedAtMS), Media: reviewUsages(value.Media, source, mediaByID),
	}
}

func reviewUser(value sqlitestore.ArchiveUser, source MergeReviewSource, mediaByID map[string]sqlitestore.ArchiveMedia) MergeReviewEntity {
	username, displayName, bio, role, status := value.Username, value.DisplayName, value.Bio, value.Role, value.Status
	updated := archiveTime(value.UpdatedAtMS)
	entity := MergeReviewEntity{
		ID: value.ID, Username: &username, DisplayName: &displayName, Bio: &bio,
		Role: &role, Status: &status, CreatedAt: archiveTime(value.CreatedAtMS), UpdatedAt: &updated,
		AvatarMediaID: cloneString(value.AvatarMediaID), CoverMediaID: cloneString(value.CoverMediaID),
		Media:  []MergeReviewMedia{},
		Avatar: reviewReferencedMedia(value.AvatarMediaID, source, mediaByID),
		Cover:  reviewReferencedMedia(value.CoverMediaID, source, mediaByID),
	}
	if value.UsernameSetAtMS != nil {
		stamped := archiveTime(*value.UsernameSetAtMS)
		entity.UsernameSetAt = &stamped
	}
	return entity
}

func reviewIdentity(value sqlitestore.ArchiveIdentity) MergeReviewEntity {
	provider, providerUserID, userID := value.Provider, value.ProviderUserID, value.UserID
	displayName, verified, deliverable := value.DisplayName, value.EmailVerified, value.EmailDeliverable
	lastLogin := archiveTime(value.LastLoginAtMS)
	return MergeReviewEntity{
		ID: mergeIdentityID(value.Provider, value.ProviderUserID), Provider: &provider,
		ProviderUserID: &providerUserID, UserID: &userID, DisplayName: &displayName,
		Email: cloneString(value.Email), EmailVerified: &verified, EmailDeliverable: &deliverable,
		AvatarURL: cloneString(value.AvatarURL), LastLoginAt: &lastLogin,
		CreatedAt: archiveTime(value.CreatedAtMS), Media: []MergeReviewMedia{},
	}
}

func reviewLinkCards(values []sqlitestore.ArchiveLinkCard) []MergeReviewLinkCard {
	if len(values) == 0 {
		return nil
	}
	result := make([]MergeReviewLinkCard, 0, len(values))
	for _, value := range values {
		result = append(result, MergeReviewLinkCard{
			Position: value.Card.Position, SourceURL: value.Card.SourceURL,
			CanonicalURL: value.Card.CanonicalURL, Provider: string(value.Card.Provider),
			CardKind: string(value.Card.Kind), Title: value.Card.Title, SiteName: value.Card.SiteName,
		})
	}
	return result
}

func reviewUsages(values []sqlitestore.ArchiveUsage, source MergeReviewSource, mediaByID map[string]sqlitestore.ArchiveMedia) []MergeReviewMedia {
	result := make([]MergeReviewMedia, 0, len(values))
	for _, usage := range values {
		value, ok := mediaByID[usage.MediaID]
		if !ok {
			continue
		}
		result = append(result, reviewMedia(value, source, usage.Position, usage.AltText))
	}
	return result
}

func reviewMedia(value sqlitestore.ArchiveMedia, source MergeReviewSource, position int, alt *string) MergeReviewMedia {
	return MergeReviewMedia{
		Source: source, ID: value.ID, OriginalName: value.OriginalName, MIMEType: value.MIMEType,
		ByteSize: value.ByteSize, Width: value.Width, Height: value.Height,
		Position: position, AltText: cloneString(alt),
	}
}

func reviewReferencedMedia(id *string, source MergeReviewSource, values map[string]sqlitestore.ArchiveMedia) *MergeReviewMedia {
	if id == nil {
		return nil
	}
	value, ok := values[*id]
	if !ok {
		return nil
	}
	result := reviewMedia(value, source, 0, nil)
	return &result
}

func reviewReferencedEntry(id *string, source MergeReviewSource, entries map[string]sqlitestore.ArchiveEntry, mediaByID map[string]sqlitestore.ArchiveMedia) *MergeReviewEntity {
	if id == nil {
		return nil
	}
	value, ok := entries[*id]
	if !ok {
		return nil
	}
	result := reviewEntry(value, source, mediaByID)
	return &result
}

func archiveTime(milliseconds int64) time.Time {
	return time.UnixMilli(milliseconds).UTC()
}

func reviewLocation(value *domain.Location) *MergeReviewLocation {
	if value == nil {
		return nil
	}
	result := MergeReviewLocation{
		Name: value.Name, DisplayName: value.DisplayName, CountryCode: value.CountryCode,
		Country: value.Country, Region: value.Region, City: value.City, District: value.District,
		Category: value.Category, Provider: value.Provider, ProviderPlaceID: value.ProviderPlaceID,
	}
	if value.Point != nil {
		latitude, longitude, system := value.Point.Latitude(), value.Point.Longitude(), value.Point.System
		result.Latitude, result.Longitude, result.CoordinateSystem = &latitude, &longitude, &system
	}
	return &result
}

func cloneRawJSON(value json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), value...)
}

// ValidateMergeReview proves that bounded display data only references the
// matching plan classification and never claims more records than exist.
func ValidateMergeReview(plan MergePlan, review MergeReview) error {
	planItems := make(map[string]MergeItem, len(plan.Items))
	for _, item := range plan.Items {
		planItems[mergeChoiceKey(item.Kind, item.ID)] = item
	}
	seen := map[string]struct{}{}
	for classification, bucket := range map[MergeClassification]MergeReviewBucket{
		MergeIncomingOnly: review.IncomingOnly,
		MergeLocalOnly:    review.LocalOnly,
		MergeConflict:     review.Conflicts,
	} {
		if bucket.Total < len(bucket.Items) || len(bucket.Items) > mergeReviewLimitPerClassification || bucket.Truncated != (bucket.Total > len(bucket.Items)) {
			return errors.New("merge review bucket bounds are invalid")
		}
		for _, item := range bucket.Items {
			key := mergeChoiceKey(item.Kind, item.ID)
			planned, ok := planItems[key]
			if !ok || planned.Classification != classification || item.Classification != classification || item.Kind == MergeMedia {
				return errors.New("merge review item does not match its plan")
			}
			if _, duplicate := seen[key]; duplicate {
				return errors.New("merge review contains a duplicate item")
			}
			seen[key] = struct{}{}
			if classification == MergeIncomingOnly && (item.Local != nil || item.Incoming == nil) || classification == MergeLocalOnly && (item.Local == nil || item.Incoming != nil) || classification == MergeConflict && (item.Local == nil || item.Incoming == nil) {
				return errors.New("merge review item sides are invalid")
			}
		}
	}
	if len(review.SiteFields) != len(plan.SiteFields) {
		return errors.New("merge review site fields are incomplete")
	}
	for index, field := range review.SiteFields {
		if field.Field != plan.SiteFields[index].Field || field.Classification != plan.SiteFields[index].Classification || !json.Valid(field.LocalValue) || !json.Valid(field.IncomingValue) {
			return errors.New("merge review site field does not match its plan")
		}
	}
	return nil
}
