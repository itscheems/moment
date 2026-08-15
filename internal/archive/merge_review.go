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
	MergeReviewLocal    MergeReviewSource = "local"
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

// MergeReviewEntity contains recognizable post/comment content. The plan ID
// and selection remain the only commit authority; this projection is only for
// humans and agents to understand a decision.
type MergeReviewEntity struct {
	ID          string               `json:"id"`
	ContentMD   string               `json:"content_md"`
	EntryID     *string              `json:"entry_id,omitempty"`
	ParentID    *string              `json:"parent_id,omitempty"`
	AuthorKind  *string              `json:"author_kind,omitempty"`
	AuthorName  *string              `json:"author_name,omitempty"`
	CreatedAt   time.Time            `json:"created_at"`
	UpdatedAt   *time.Time           `json:"updated_at,omitempty"`
	PublishedAt *time.Time           `json:"published_at,omitempty"`
	Location    *MergeReviewLocation `json:"location,omitempty"`
	Media       []MergeReviewMedia   `json:"media"`
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

func buildMergeReview(plan MergePlan, local, incoming PortableAuthority) (MergeReview, error) {
	localEntries, incomingEntries := indexEntries(local.Entries), indexEntries(incoming.Entries)
	localComments, incomingComments := indexComments(local.Comments), indexComments(incoming.Comments)
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
	return MergeReviewEntity{
		ID: value.ID, ContentMD: value.ContentMD, CreatedAt: created,
		UpdatedAt: &updated, PublishedAt: &published, Location: reviewLocation(value.Location),
		Media: reviewUsages(value.Media, source, mediaByID),
	}
}

func reviewComment(value sqlitestore.ArchiveComment, source MergeReviewSource, mediaByID map[string]sqlitestore.ArchiveMedia) MergeReviewEntity {
	entryID, authorKind, authorName := value.EntryID, value.AuthorKind, value.AuthorName
	return MergeReviewEntity{
		ID: value.ID, ContentMD: value.ContentMD, EntryID: &entryID,
		ParentID: cloneString(value.ParentID), AuthorKind: &authorKind, AuthorName: &authorName,
		CreatedAt: archiveTime(value.CreatedAtMS), Media: reviewUsages(value.Media, source, mediaByID),
	}
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
