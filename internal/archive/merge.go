package archive

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const mergePlanVersion = 1

// MergeClassification is one deterministic two-way comparison result. A
// local-only record is always retained because portable v1 has no tombstones.
type MergeClassification string

// Merge classification values describe the deterministic relationship
// between one local and incoming portable record.
const (
	MergeUnchanged    MergeClassification = "unchanged"
	MergeIncomingOnly MergeClassification = "incoming_only"
	MergeLocalOnly    MergeClassification = "local_only"
	MergeConflict     MergeClassification = "conflict"
)

// MergeEntityKind identifies a portable authority record in a plan.
type MergeEntityKind string

// Merge entity kinds identify records whose references must remain closed.
const (
	MergeEntry   MergeEntityKind = "entry"
	MergeComment MergeEntityKind = "comment"
	MergeMedia   MergeEntityKind = "media"
)

// MergeAction is an explicit selection decision. Media actions are derived
// from selected references and are therefore never independently selectable.
type MergeAction string

// Merge actions are the complete set of explicit review decisions.
const (
	MergeSkip        MergeAction = "skip"
	MergeAdd         MergeAction = "add"
	MergeKeepLocal   MergeAction = "keep_local"
	MergeUseIncoming MergeAction = "use_incoming"
	MergeKeepBoth    MergeAction = "keep_both"
)

// PortableAuthority is the complete portable source-of-truth graph used for
// comparison. It intentionally excludes secrets, previews, FTS, idempotency
// rows, and runtime state.
type PortableAuthority struct {
	Site     sqlitestore.ArchiveSite
	Entries  []sqlitestore.ArchiveEntry
	Comments []sqlitestore.ArchiveComment
	Media    []sqlitestore.ArchiveMedia
}

// MergeRef is a stable dependency or equivalence reference.
type MergeRef struct {
	Kind MergeEntityKind `json:"kind"`
	ID   string          `json:"id"`
}

// MergeItem is one incoming or local portable entity comparison.
type MergeItem struct {
	Kind           MergeEntityKind     `json:"kind"`
	ID             string              `json:"id"`
	Classification MergeClassification `json:"classification"`
	ChangedFields  []string            `json:"changed_fields"`
	Dependencies   []MergeRef          `json:"dependencies"`
	Selectable     bool                `json:"selectable"`
	MatchedLocalID *string             `json:"matched_local_id,omitempty"`
}

// MergeSiteField keeps singleton settings separate from content. Values are
// canonical JSON so HTTP and CLI can render differences without owning types.
type MergeSiteField struct {
	Field          string              `json:"field"`
	Classification MergeClassification `json:"classification"`
	LocalValue     json.RawMessage     `json:"local_value"`
	IncomingValue  json.RawMessage     `json:"incoming_value"`
	Dependencies   []MergeRef          `json:"dependencies"`
}

// MergeSummary counts all planned entities and selectable site fields.
type MergeSummary struct {
	Unchanged    int `json:"unchanged"`
	IncomingOnly int `json:"incoming_only"`
	LocalOnly    int `json:"local_only"`
	Conflict     int `json:"conflict"`
}

// MergeSelectionItem is the editable default decision for one selectable
// entry or comment.
type MergeSelectionItem struct {
	Kind   MergeEntityKind `json:"kind"`
	ID     string          `json:"id"`
	Action MergeAction     `json:"action"`
}

// MergeSiteSelection defaults every profile field to the target instance.
type MergeSiteSelection struct {
	Field  string      `json:"field"`
	Action MergeAction `json:"action"`
}

// MergeSelection is bound to one exact plan. Commit adds a selection hash and
// validates dependency closure before any staged plan is claimed.
type MergeSelection struct {
	PlanID              string               `json:"plan_id"`
	ArchiveSHA256       string               `json:"archive_sha256"`
	BaseGeneration      string               `json:"base_generation"`
	BaseAuthoritySHA256 string               `json:"base_authority_sha256"`
	Items               []MergeSelectionItem `json:"items"`
	SiteFields          []MergeSiteSelection `json:"site_fields"`
}

// MergePlan is the application-owned review contract shared by all adapters.
type MergePlan struct {
	Version                 int              `json:"version"`
	PlanID                  string           `json:"plan_id"`
	ArchiveSHA256           string           `json:"archive_sha256"`
	BaseGeneration          string           `json:"base_generation"`
	BaseAuthoritySHA256     string           `json:"base_authority_sha256"`
	IncomingAuthoritySHA256 string           `json:"incoming_authority_sha256"`
	OwnerID                 string           `json:"owner_id"`
	ExpiresAt               time.Time        `json:"expires_at"`
	Summary                 MergeSummary     `json:"summary"`
	Items                   []MergeItem      `json:"items"`
	SiteFields              []MergeSiteField `json:"site_fields"`
	DefaultSelection        MergeSelection   `json:"default_selection"`
}

// MergePlanInput supplies externally verified archive and runtime identity.
type MergePlanInput struct {
	ArchiveSHA256  string
	BaseGeneration string
	ExpiresAt      time.Time
	Local          PortableAuthority
	Incoming       PortableAuthority
}

// ReadPortableAuthority reads one consistent portable snapshot. Storage paths
// are retained only so the later generation builder can locate originals;
// they are excluded from equality and authority digests.
func ReadPortableAuthority(ctx context.Context, database archiveSnapshotDatabase) (authority PortableAuthority, resultErr error) {
	if database == nil {
		return PortableAuthority{}, errors.New("portable authority database is required")
	}
	snapshot, err := database.BeginArchiveSnapshot(ctx)
	if err != nil {
		return PortableAuthority{}, err
	}
	defer func() { resultErr = errors.Join(resultErr, snapshot.Close()) }()
	if err := snapshot.ValidateIntegrity(ctx); err != nil {
		return PortableAuthority{}, err
	}
	authority.Site, err = snapshot.Site(ctx)
	if err != nil {
		return PortableAuthority{}, err
	}
	if err := snapshot.Entries(ctx, func(value sqlitestore.ArchiveEntry) error {
		authority.Entries = append(authority.Entries, cloneArchiveEntry(value))
		return nil
	}); err != nil {
		return PortableAuthority{}, err
	}
	if err := snapshot.Comments(ctx, func(value sqlitestore.ArchiveComment) error {
		authority.Comments = append(authority.Comments, cloneArchiveComment(value))
		return nil
	}); err != nil {
		return PortableAuthority{}, err
	}
	if err := snapshot.Media(ctx, func(value sqlitestore.ArchiveMedia) error {
		authority.Media = append(authority.Media, value)
		return nil
	}); err != nil {
		return PortableAuthority{}, err
	}
	return authority, nil
}

// AuthorityDigest returns a deterministic digest of portable authority. Media
// original bytes are not re-read: their already verified SHA-256 values are
// the immutable byte authority.
func AuthorityDigest(authority PortableAuthority) (string, error) {
	normalized := normalizeAuthority(authority)
	payload, err := json.Marshal(normalized)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(payload)
	return hex.EncodeToString(digest[:]), nil
}

// ComparePortableAuthorities builds a stable two-way plan without mutating
// either authority source.
func ComparePortableAuthorities(input MergePlanInput) (MergePlan, error) {
	return comparePortableAuthorities(input, true)
}

// comparePortableAuthoritiesForReview creates the same deterministic plan
// while allowing an exact-replacement review to compare a backup from a
// different owner. Merge commit still calls ComparePortableAuthorities and
// therefore retains the same-owner boundary.
func comparePortableAuthoritiesForReview(input MergePlanInput) (MergePlan, bool, error) {
	mergeAllowed := input.Local.Site.OwnerID != "" && input.Incoming.Site.OwnerID != "" && input.Local.Site.OwnerID == input.Incoming.Site.OwnerID
	plan, err := comparePortableAuthorities(input, false)
	return plan, mergeAllowed, err
}

func comparePortableAuthorities(input MergePlanInput, requireSameOwner bool) (MergePlan, error) {
	if input.Local.Site.OwnerID == "" || input.Incoming.Site.OwnerID == "" || input.Local.Site.OwnerID != input.Incoming.Site.OwnerID {
		if requireSameOwner {
			return MergePlan{}, domain.NewError(domain.CodeImportOwnerMismatch, "merge import requires a backup from the same owner", map[string]any{
				"local_owner_id":    input.Local.Site.OwnerID,
				"incoming_owner_id": input.Incoming.Site.OwnerID,
			})
		}
		if input.Local.Site.OwnerID == "" {
			return MergePlan{}, errors.New("local portable authority owner is required")
		}
	}
	localDigest, err := AuthorityDigest(input.Local)
	if err != nil {
		return MergePlan{}, err
	}
	incomingDigest, err := AuthorityDigest(input.Incoming)
	if err != nil {
		return MergePlan{}, err
	}
	plan := MergePlan{
		Version: mergePlanVersion, ArchiveSHA256: input.ArchiveSHA256,
		BaseGeneration: input.BaseGeneration, BaseAuthoritySHA256: localDigest,
		IncomingAuthoritySHA256: incomingDigest, OwnerID: input.Local.Site.OwnerID,
		ExpiresAt: input.ExpiresAt,
	}
	plan.PlanID = mergePlanID(plan)
	localMediaByID, localMediaBySHA := indexMedia(input.Local.Media)
	incomingMediaByID, _ := indexMedia(input.Incoming.Media)
	matchedLocalMedia := make(map[string]struct{})
	for _, incoming := range sortedMedia(input.Incoming.Media) {
		item := MergeItem{Kind: MergeMedia, ID: incoming.ID, ChangedFields: []string{}, Dependencies: []MergeRef{}}
		if local, exists := localMediaBySHA[incoming.SHA256]; exists {
			item.Classification = MergeUnchanged
			matched := local.ID
			item.MatchedLocalID = &matched
			matchedLocalMedia[local.ID] = struct{}{}
		} else if local, exists := localMediaByID[incoming.ID]; exists {
			item.Classification = MergeConflict
			item.ChangedFields = mediaChangedFields(local, incoming)
		} else {
			item.Classification = MergeIncomingOnly
		}
		plan.addItem(item)
	}
	for _, local := range sortedMedia(input.Local.Media) {
		if _, matched := matchedLocalMedia[local.ID]; matched {
			continue
		}
		if _, exists := incomingMediaByID[local.ID]; exists {
			continue
		}
		plan.addItem(MergeItem{Kind: MergeMedia, ID: local.ID, Classification: MergeLocalOnly, ChangedFields: []string{}, Dependencies: []MergeRef{}})
	}

	localEntry := indexEntries(input.Local.Entries)
	incomingEntry := indexEntries(input.Incoming.Entries)
	localSHA := mediaSHAs(input.Local.Media)
	incomingSHA := mediaSHAs(input.Incoming.Media)
	incomingIDBySHA := mediaIDsBySHA(input.Incoming.Media)
	for _, incoming := range sortedEntries(input.Incoming.Entries) {
		item := MergeItem{Kind: MergeEntry, ID: incoming.ID, Selectable: true, ChangedFields: []string{}, Dependencies: entryDependencies(incoming, incomingIDBySHA)}
		if local, exists := localEntry[incoming.ID]; exists {
			item.ChangedFields = entryChangedFields(local, incoming, localSHA, incomingSHA)
			if len(item.ChangedFields) == 0 {
				item.Classification = MergeUnchanged
			} else {
				item.Classification = MergeConflict
			}
		} else {
			item.Classification = MergeIncomingOnly
		}
		plan.addItem(item)
	}
	for _, local := range sortedEntries(input.Local.Entries) {
		if _, exists := incomingEntry[local.ID]; !exists {
			plan.addItem(MergeItem{Kind: MergeEntry, ID: local.ID, Classification: MergeLocalOnly, ChangedFields: []string{}, Dependencies: []MergeRef{}})
		}
	}

	localComment := indexComments(input.Local.Comments)
	incomingComment := indexComments(input.Incoming.Comments)
	for _, incoming := range sortedComments(input.Incoming.Comments) {
		item := MergeItem{Kind: MergeComment, ID: incoming.ID, Selectable: true, ChangedFields: []string{}, Dependencies: commentDependencies(incoming)}
		if local, exists := localComment[incoming.ID]; exists {
			item.ChangedFields = commentChangedFields(local, incoming, localSHA, incomingSHA)
			if len(item.ChangedFields) == 0 {
				item.Classification = MergeUnchanged
			} else {
				item.Classification = MergeConflict
			}
		} else {
			item.Classification = MergeIncomingOnly
		}
		plan.addItem(item)
	}
	for _, local := range sortedComments(input.Local.Comments) {
		if _, exists := incomingComment[local.ID]; !exists {
			plan.addItem(MergeItem{Kind: MergeComment, ID: local.ID, Classification: MergeLocalOnly, ChangedFields: []string{}, Dependencies: []MergeRef{}})
		}
	}
	plan.SiteFields = compareSiteFields(input.Local.Site, input.Incoming.Site, localSHA, incomingSHA)
	for _, field := range plan.SiteFields {
		plan.increment(field.Classification)
	}
	sort.Slice(plan.Items, func(i, j int) bool {
		if plan.Items[i].Kind != plan.Items[j].Kind {
			return plan.Items[i].Kind < plan.Items[j].Kind
		}
		return plan.Items[i].ID < plan.Items[j].ID
	})
	plan.DefaultSelection = defaultMergeSelection(plan)
	return plan, nil
}

func (plan *MergePlan) addItem(item MergeItem) {
	plan.Items = append(plan.Items, item)
	plan.increment(item.Classification)
}

func (plan *MergePlan) increment(classification MergeClassification) {
	switch classification {
	case MergeUnchanged:
		plan.Summary.Unchanged++
	case MergeIncomingOnly:
		plan.Summary.IncomingOnly++
	case MergeLocalOnly:
		plan.Summary.LocalOnly++
	case MergeConflict:
		plan.Summary.Conflict++
	}
}

func mergePlanID(plan MergePlan) string {
	value := strings.Join([]string{strconv.Itoa(plan.Version), plan.ArchiveSHA256, plan.BaseGeneration, plan.BaseAuthoritySHA256, plan.IncomingAuthoritySHA256, plan.OwnerID, plan.ExpiresAt.UTC().Format(time.RFC3339Nano)}, "\x00")
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func defaultMergeSelection(plan MergePlan) MergeSelection {
	selection := MergeSelection{
		PlanID: plan.PlanID, ArchiveSHA256: plan.ArchiveSHA256,
		BaseGeneration: plan.BaseGeneration, BaseAuthoritySHA256: plan.BaseAuthoritySHA256,
		Items: []MergeSelectionItem{}, SiteFields: []MergeSiteSelection{},
	}
	for _, item := range plan.Items {
		if !item.Selectable {
			continue
		}
		action := MergeKeepLocal
		switch item.Classification {
		case MergeIncomingOnly:
			action = MergeAdd
		case MergeUnchanged:
			action = MergeSkip
		}
		selection.Items = append(selection.Items, MergeSelectionItem{Kind: item.Kind, ID: item.ID, Action: action})
	}
	for _, field := range plan.SiteFields {
		selection.SiteFields = append(selection.SiteFields, MergeSiteSelection{Field: field.Field, Action: MergeKeepLocal})
	}
	return selection
}

func normalizeAuthority(authority PortableAuthority) any {
	entries := sortedEntries(authority.Entries)
	comments := sortedComments(authority.Comments)
	mediaValues := sortedMedia(authority.Media)
	for index := range mediaValues {
		mediaValues[index].StoragePath = ""
	}
	return struct {
		Site     sqlitestore.ArchiveSite      `json:"site"`
		Entries  []sqlitestore.ArchiveEntry   `json:"entries"`
		Comments []sqlitestore.ArchiveComment `json:"comments"`
		Media    []sqlitestore.ArchiveMedia   `json:"media"`
	}{authority.Site, entries, comments, mediaValues}
}

func compareSiteFields(local, incoming sqlitestore.ArchiveSite, localSHA, incomingSHA map[string]string) []MergeSiteField {
	avatarLocal, avatarIncoming := referencedSHA(local.AvatarMediaID, localSHA), referencedSHA(incoming.AvatarMediaID, incomingSHA)
	coverLocal, coverIncoming := referencedSHA(local.CoverMediaID, localSHA), referencedSHA(incoming.CoverMediaID, incomingSHA)
	values := []struct {
		field        string
		local        any
		incoming     any
		dependencies []MergeRef
	}{
		{"username", local.Username, incoming.Username, nil},
		{"display_name", local.DisplayName, incoming.DisplayName, nil},
		{"title", local.Title, incoming.Title, nil},
		{"bio", local.Bio, incoming.Bio, nil},
		{"timezone", local.Timezone, incoming.Timezone, nil},
		{"avatar_media_id", avatarLocal, avatarIncoming, mediaDependency(incoming.AvatarMediaID)},
		{"cover_media_id", coverLocal, coverIncoming, mediaDependency(incoming.CoverMediaID)},
		{"pinned_post_id", local.PinnedPostID, incoming.PinnedPostID, entryDependency(incoming.PinnedPostID)},
		{"comments_enabled", local.CommentsEnabled, incoming.CommentsEnabled, nil},
	}
	result := make([]MergeSiteField, 0, len(values))
	for _, value := range values {
		classification := MergeConflict
		if reflect.DeepEqual(value.local, value.incoming) {
			classification = MergeUnchanged
		}
		result = append(result, MergeSiteField{
			Field: value.field, Classification: classification,
			LocalValue: mustRawJSON(value.local), IncomingValue: mustRawJSON(value.incoming),
			Dependencies: nonNilRefs(value.dependencies),
		})
	}
	return result
}

func mediaDependency(id *string) []MergeRef {
	if id == nil {
		return []MergeRef{}
	}
	return []MergeRef{{Kind: MergeMedia, ID: *id}}
}

func entryDependency(id *string) []MergeRef {
	if id == nil {
		return []MergeRef{}
	}
	return []MergeRef{{Kind: MergeEntry, ID: *id}}
}

func referencedSHA(id *string, values map[string]string) *string {
	if id == nil {
		return nil
	}
	value := values[*id]
	return &value
}

func mustRawJSON(value any) json.RawMessage {
	payload, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return payload
}

func entryDependencies(value sqlitestore.ArchiveEntry, mediaIDBySHA map[string]string) []MergeRef {
	result := make([]MergeRef, 0, len(value.Media)+len(value.LinkCards))
	if value.QuotedEntryID != nil {
		result = append(result, MergeRef{Kind: MergeEntry, ID: *value.QuotedEntryID})
	}
	for _, usage := range value.Media {
		result = append(result, MergeRef{Kind: MergeMedia, ID: usage.MediaID})
	}
	// A card thumbnail is a real media dependency. The merge plan enumerates
	// dependencies by media ID while a card names its thumbnail by content
	// hash, so translate here rather than leaving the closure incomplete.
	for _, card := range value.LinkCards {
		if card.ThumbnailSHA256 == "" {
			continue
		}
		if id, exists := mediaIDBySHA[card.ThumbnailSHA256]; exists {
			result = append(result, MergeRef{Kind: MergeMedia, ID: id})
		}
	}
	return uniqueRefs(result)
}

func commentDependencies(value sqlitestore.ArchiveComment) []MergeRef {
	result := []MergeRef{{Kind: MergeEntry, ID: value.EntryID}}
	if value.ParentID != nil {
		result = append(result, MergeRef{Kind: MergeComment, ID: *value.ParentID})
	}
	for _, usage := range value.Media {
		result = append(result, MergeRef{Kind: MergeMedia, ID: usage.MediaID})
	}
	return uniqueRefs(result)
}

func uniqueRefs(values []MergeRef) []MergeRef {
	seen := make(map[string]struct{}, len(values))
	result := make([]MergeRef, 0, len(values))
	for _, value := range values {
		key := string(value.Kind) + "\x00" + value.ID
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, value)
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Kind != result[j].Kind {
			return result[i].Kind < result[j].Kind
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func nonNilRefs(values []MergeRef) []MergeRef {
	if values == nil {
		return []MergeRef{}
	}
	return values
}

func entryChangedFields(local, incoming sqlitestore.ArchiveEntry, localSHA, incomingSHA map[string]string) []string {
	fields := []string{}
	if local.Kind != incoming.Kind {
		fields = append(fields, "kind")
	}
	if local.ContentMD != incoming.ContentMD {
		fields = append(fields, "content_md")
	}
	if local.CreatedAtMS != incoming.CreatedAtMS {
		fields = append(fields, "created_at")
	}
	if local.UpdatedAtMS != incoming.UpdatedAtMS {
		fields = append(fields, "updated_at")
	}
	if local.PublishedAtMS != incoming.PublishedAtMS {
		fields = append(fields, "published_at")
	}
	if !reflect.DeepEqual(local.RepostedAtMS, incoming.RepostedAtMS) {
		fields = append(fields, "reposted_at")
	}
	if !reflect.DeepEqual(local.QuotedEntryID, incoming.QuotedEntryID) {
		fields = append(fields, "quoted_entry_id")
	}
	if local.QuoteUnavailable != incoming.QuoteUnavailable {
		fields = append(fields, "quote_unavailable")
	}
	if !reflect.DeepEqual(usageSignature(local.Media, localSHA), usageSignature(incoming.Media, incomingSHA)) {
		fields = append(fields, "media")
	}
	if !reflect.DeepEqual(local.Location, incoming.Location) {
		fields = append(fields, "location")
	}
	// Cards compare directly because a snapshot card names its thumbnail by
	// content hash, which means the same thing on both sides of the merge.
	if !reflect.DeepEqual(local.LinkCards, incoming.LinkCards) {
		fields = append(fields, "link_cards")
	}
	return fields
}

func commentChangedFields(local, incoming sqlitestore.ArchiveComment, localSHA, incomingSHA map[string]string) []string {
	fields := []string{}
	if local.EntryID != incoming.EntryID {
		fields = append(fields, "entry_id")
	}
	if !reflect.DeepEqual(local.ParentID, incoming.ParentID) {
		fields = append(fields, "parent_id")
	}
	if local.AuthorKind != incoming.AuthorKind {
		fields = append(fields, "author_kind")
	}
	if local.AuthorName != incoming.AuthorName {
		fields = append(fields, "author_name")
	}
	if !reflect.DeepEqual(local.AuthorUserID, incoming.AuthorUserID) {
		fields = append(fields, "author_user_id")
	}
	if !reflect.DeepEqual(local.ReplyToCommentID, incoming.ReplyToCommentID) {
		fields = append(fields, "reply_to_comment_id")
	}
	if local.ContentMD != incoming.ContentMD {
		fields = append(fields, "content_md")
	}
	if local.CreatedAtMS != incoming.CreatedAtMS {
		fields = append(fields, "created_at")
	}
	if !reflect.DeepEqual(usageSignature(local.Media, localSHA), usageSignature(incoming.Media, incomingSHA)) {
		fields = append(fields, "media")
	}
	if !reflect.DeepEqual(local.Location, incoming.Location) {
		fields = append(fields, "location")
	}
	if local.Pinned != incoming.Pinned {
		fields = append(fields, "pinned")
	}
	return fields
}

func mediaChangedFields(local, incoming sqlitestore.ArchiveMedia) []string {
	fields := []string{}
	if local.SHA256 != incoming.SHA256 {
		fields = append(fields, "sha256")
	}
	if local.OriginalName != incoming.OriginalName {
		fields = append(fields, "original_name")
	}
	if local.MIMEType != incoming.MIMEType {
		fields = append(fields, "mime_type")
	}
	if local.ByteSize != incoming.ByteSize {
		fields = append(fields, "byte_size")
	}
	if local.Width != incoming.Width {
		fields = append(fields, "width")
	}
	if local.Height != incoming.Height {
		fields = append(fields, "height")
	}
	if local.CreatedAtMS != incoming.CreatedAtMS {
		fields = append(fields, "created_at")
	}
	return fields
}

type usageDigest struct {
	SHA      string
	Position int
	AltText  *string
}

func usageSignature(values []sqlitestore.ArchiveUsage, shas map[string]string) []usageDigest {
	result := make([]usageDigest, len(values))
	for index, value := range values {
		result[index] = usageDigest{shas[value.MediaID], value.Position, cloneString(value.AltText)}
	}
	return result
}

func indexMedia(values []sqlitestore.ArchiveMedia) (map[string]sqlitestore.ArchiveMedia, map[string]sqlitestore.ArchiveMedia) {
	byID := map[string]sqlitestore.ArchiveMedia{}
	bySHA := map[string]sqlitestore.ArchiveMedia{}
	for _, v := range values {
		byID[v.ID] = v
		bySHA[v.SHA256] = v
	}
	return byID, bySHA
}
func mediaIDsBySHA(values []sqlitestore.ArchiveMedia) map[string]string {
	result := map[string]string{}
	for _, v := range values {
		result[v.SHA256] = v.ID
	}
	return result
}

func mediaSHAs(values []sqlitestore.ArchiveMedia) map[string]string {
	result := map[string]string{}
	for _, v := range values {
		result[v.ID] = v.SHA256
	}
	return result
}
func indexEntries(values []sqlitestore.ArchiveEntry) map[string]sqlitestore.ArchiveEntry {
	result := map[string]sqlitestore.ArchiveEntry{}
	for _, v := range values {
		result[v.ID] = v
	}
	return result
}
func indexComments(values []sqlitestore.ArchiveComment) map[string]sqlitestore.ArchiveComment {
	result := map[string]sqlitestore.ArchiveComment{}
	for _, v := range values {
		result[v.ID] = v
	}
	return result
}

func sortedEntries(values []sqlitestore.ArchiveEntry) []sqlitestore.ArchiveEntry {
	result := make([]sqlitestore.ArchiveEntry, len(values))
	for i, v := range values {
		result[i] = cloneArchiveEntry(v)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func sortedComments(values []sqlitestore.ArchiveComment) []sqlitestore.ArchiveComment {
	result := make([]sqlitestore.ArchiveComment, len(values))
	for i, v := range values {
		result[i] = cloneArchiveComment(v)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}
func sortedMedia(values []sqlitestore.ArchiveMedia) []sqlitestore.ArchiveMedia {
	result := append([]sqlitestore.ArchiveMedia(nil), values...)
	sort.Slice(result, func(i, j int) bool {
		if result[i].SHA256 != result[j].SHA256 {
			return result[i].SHA256 < result[j].SHA256
		}
		return result[i].ID < result[j].ID
	})
	return result
}

func cloneArchiveEntry(value sqlitestore.ArchiveEntry) sqlitestore.ArchiveEntry {
	value.QuotedEntryID = cloneString(value.QuotedEntryID)
	value.Media = cloneUsages(value.Media)
	if value.Location != nil {
		location := *value.Location
		if value.Location.Point != nil {
			point := *value.Location.Point
			location.Point = &point
		}
		value.Location = &location
	}
	return value
}
func cloneArchiveComment(value sqlitestore.ArchiveComment) sqlitestore.ArchiveComment {
	value.ParentID = cloneString(value.ParentID)
	value.AuthorUserID = cloneString(value.AuthorUserID)
	value.ReplyToCommentID = cloneString(value.ReplyToCommentID)
	value.Media = cloneUsages(value.Media)
	value.Location = domain.CloneLocation(value.Location)
	return value
}
func cloneUsages(values []sqlitestore.ArchiveUsage) []sqlitestore.ArchiveUsage {
	result := make([]sqlitestore.ArchiveUsage, len(values))
	for i, v := range values {
		v.AltText = cloneString(v.AltText)
		result[i] = v
	}
	return result
}
func cloneString(value *string) *string {
	if value == nil {
		return nil
	}
	cloned := *value
	return &cloned
}

// ValidateMergePlan checks public invariants before an adapter persists or
// accepts a plan from another transport.
func ValidateMergePlan(plan MergePlan) error {
	if plan.Version != mergePlanVersion || len(plan.PlanID) != sha256.Size*2 || len(plan.BaseAuthoritySHA256) != sha256.Size*2 || len(plan.IncomingAuthoritySHA256) != sha256.Size*2 || plan.OwnerID == "" {
		return fmt.Errorf("merge plan identity is invalid")
	}
	if mergePlanID(plan) != plan.PlanID {
		return fmt.Errorf("merge plan identity does not match its bound inputs")
	}
	return nil
}
