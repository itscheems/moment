package archive

import (
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestMergePlanClassifiesContentDependenciesAndSHAReuse(t *testing.T) {
	localMediaID, incomingMediaID := "media-local", "media-incoming"
	local := PortableAuthority{
		Site: sqlitestore.ArchiveSite{
			OwnerID: "same-owner", Username: "owner", DisplayName: "Owner", Title: "Local",
			Timezone: "UTC", AvatarMediaID: &localMediaID, CommentsEnabled: true,
		},
		Entries: []sqlitestore.ArchiveEntry{
			{ID: "entry-local", Kind: "moment", ContentMD: "local", CreatedAtMS: 1, UpdatedAtMS: 1, PublishedAtMS: 1},
			{ID: "entry-same", Kind: "moment", ContentMD: "same", CreatedAtMS: 2, UpdatedAtMS: 2, PublishedAtMS: 2, Media: []sqlitestore.ArchiveUsage{{MediaID: localMediaID, Position: 0}}},
			{ID: "entry-conflict", Kind: "moment", ContentMD: "local version", CreatedAtMS: 3, UpdatedAtMS: 3, PublishedAtMS: 3},
		},
		Comments: []sqlitestore.ArchiveComment{{ID: "comment-same", EntryID: "entry-same", AuthorKind: "owner", AuthorName: "Owner", ContentMD: "same", CreatedAtMS: 4}},
		Media:    []sqlitestore.ArchiveMedia{{ID: localMediaID, SHA256: repeatHex("a"), OriginalName: "local.jpg", MIMEType: "image/jpeg", ByteSize: 10, Width: 2, Height: 2, CreatedAtMS: 1}},
	}
	incoming := PortableAuthority{
		Site: sqlitestore.ArchiveSite{
			OwnerID: "same-owner", Username: "owner", DisplayName: "Owner", Title: "Incoming",
			Timezone: "UTC", AvatarMediaID: &incomingMediaID, CommentsEnabled: true,
		},
		Entries: []sqlitestore.ArchiveEntry{
			{ID: "entry-same", Kind: "moment", ContentMD: "same", CreatedAtMS: 2, UpdatedAtMS: 2, PublishedAtMS: 2, Media: []sqlitestore.ArchiveUsage{{MediaID: incomingMediaID, Position: 0}}},
			{ID: "entry-conflict", Kind: "moment", ContentMD: "incoming version", CreatedAtMS: 3, UpdatedAtMS: 5, PublishedAtMS: 3},
			{ID: "entry-new", Kind: "moment", ContentMD: "new", CreatedAtMS: 6, UpdatedAtMS: 6, PublishedAtMS: 6, Media: []sqlitestore.ArchiveUsage{{MediaID: incomingMediaID, Position: 0}}},
		},
		Comments: []sqlitestore.ArchiveComment{
			{ID: "comment-same", EntryID: "entry-same", AuthorKind: "owner", AuthorName: "Owner", ContentMD: "same", CreatedAtMS: 4},
			{ID: "comment-new", EntryID: "entry-new", AuthorKind: "guest", AuthorName: "Guest", ContentMD: "new", CreatedAtMS: 7, Media: []sqlitestore.ArchiveUsage{{MediaID: incomingMediaID, Position: 0}}},
		},
		Media: []sqlitestore.ArchiveMedia{{ID: incomingMediaID, SHA256: repeatHex("a"), OriginalName: "renamed.jpg", MIMEType: "image/jpeg", ByteSize: 10, Width: 2, Height: 2, CreatedAtMS: 9}},
	}
	expires := time.Date(2026, 8, 15, 1, 2, 3, 0, time.UTC)
	plan, err := ComparePortableAuthorities(MergePlanInput{
		ArchiveSHA256: repeatHex("b"), BaseGeneration: "generation-local", ExpiresAt: expires,
		Local: local, Incoming: incoming,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateMergePlan(plan); err != nil {
		t.Fatal(err)
	}
	if plan.ExpiresAt != expires || plan.OwnerID != "same-owner" || plan.Summary != (MergeSummary{Unchanged: 11, IncomingOnly: 2, LocalOnly: 1, Conflict: 2}) {
		t.Fatalf("plan summary = %#v", plan)
	}
	items := mergeItemsByKey(plan.Items)
	assertMergeItem(t, items["entry\x00entry-same"], MergeUnchanged, nil)
	assertMergeItem(t, items["entry\x00entry-conflict"], MergeConflict, []string{"content_md", "updated_at"})
	assertMergeItem(t, items["entry\x00entry-new"], MergeIncomingOnly, nil)
	assertMergeItem(t, items["entry\x00entry-local"], MergeLocalOnly, nil)
	media := items["media\x00"+incomingMediaID]
	if media.Classification != MergeUnchanged || media.MatchedLocalID == nil || *media.MatchedLocalID != localMediaID {
		t.Fatalf("reused media = %#v", media)
	}
	comment := items["comment\x00comment-new"]
	if !reflect.DeepEqual(comment.Dependencies, []MergeRef{{Kind: MergeEntry, ID: "entry-new"}, {Kind: MergeMedia, ID: incomingMediaID}}) {
		t.Fatalf("comment dependencies = %#v", comment.Dependencies)
	}
	selection := mergeSelectionByKey(plan.DefaultSelection.Items)
	if selection["entry\x00entry-new"] != MergeAdd || selection["entry\x00entry-conflict"] != MergeKeepLocal || selection["comment\x00comment-same"] != MergeSkip {
		t.Fatalf("default selection = %#v", selection)
	}
	fields := mergeSiteFieldsByName(plan.SiteFields)
	if fields["title"].Classification != MergeConflict || fields["avatar_media_id"].Classification != MergeUnchanged {
		t.Fatalf("site fields = %#v", fields)
	}
	for _, choice := range plan.DefaultSelection.SiteFields {
		if choice.Action != MergeKeepLocal {
			t.Fatalf("site default %s = %s", choice.Field, choice.Action)
		}
	}
}

func TestAuthorityDigestAndMergePlanAreStableAcrossInputOrder(t *testing.T) {
	authority := PortableAuthority{
		Site:     sqlitestore.ArchiveSite{OwnerID: "owner", Username: "owner", DisplayName: "Owner", Title: "Moment", Timezone: "UTC", CommentsEnabled: true},
		Entries:  []sqlitestore.ArchiveEntry{{ID: "b"}, {ID: "a"}},
		Comments: []sqlitestore.ArchiveComment{{ID: "d"}, {ID: "c"}},
		Media:    []sqlitestore.ArchiveMedia{{ID: "f", SHA256: repeatHex("f"), StoragePath: "one"}, {ID: "e", SHA256: repeatHex("e"), StoragePath: "two"}},
	}
	reordered := authority
	reordered.Entries = []sqlitestore.ArchiveEntry{authority.Entries[1], authority.Entries[0]}
	reordered.Comments = []sqlitestore.ArchiveComment{authority.Comments[1], authority.Comments[0]}
	reordered.Media = []sqlitestore.ArchiveMedia{authority.Media[1], authority.Media[0]}
	reordered.Media[0].StoragePath = "different"
	first, err := AuthorityDigest(authority)
	if err != nil {
		t.Fatal(err)
	}
	second, err := AuthorityDigest(reordered)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("digests differ: %s != %s", first, second)
	}
	input := MergePlanInput{ArchiveSHA256: repeatHex("a"), BaseGeneration: "generation", ExpiresAt: time.Unix(10, 0).UTC(), Local: authority, Incoming: reordered}
	planOne, err := ComparePortableAuthorities(input)
	if err != nil {
		t.Fatal(err)
	}
	planTwo, err := ComparePortableAuthorities(input)
	if err != nil {
		t.Fatal(err)
	}
	one, _ := json.Marshal(planOne)
	two, _ := json.Marshal(planTwo)
	if string(one) != string(two) {
		t.Fatalf("plans are not deterministic:\n%s\n%s", one, two)
	}
}

func TestMergePlanRejectsUnknownOrDifferentOwner(t *testing.T) {
	tests := []struct{ local, incoming string }{{"", ""}, {"owner-a", ""}, {"owner-a", "owner-b"}}
	for _, test := range tests {
		_, err := ComparePortableAuthorities(MergePlanInput{
			Local:    PortableAuthority{Site: sqlitestore.ArchiveSite{OwnerID: test.local}},
			Incoming: PortableAuthority{Site: sqlitestore.ArchiveSite{OwnerID: test.incoming}},
		})
		if !domain.HasCode(err, domain.CodeImportOwnerMismatch) {
			t.Fatalf("owners %q/%q error = %v", test.local, test.incoming, err)
		}
	}
}

func TestMergePlanDetectsLocationAltAndMediaIDCollision(t *testing.T) {
	altLocal, altIncoming := "local", "incoming"
	local := PortableAuthority{
		Site:    sqlitestore.ArchiveSite{OwnerID: "owner"},
		Entries: []sqlitestore.ArchiveEntry{{ID: "entry", Media: []sqlitestore.ArchiveUsage{{MediaID: "media", Position: 0, AltText: &altLocal}}, Location: &domain.Location{Name: "A", DisplayName: "A", Provider: "manual"}}},
		Media:   []sqlitestore.ArchiveMedia{{ID: "media", SHA256: repeatHex("a")}},
	}
	incoming := PortableAuthority{
		Site:    sqlitestore.ArchiveSite{OwnerID: "owner"},
		Entries: []sqlitestore.ArchiveEntry{{ID: "entry", Media: []sqlitestore.ArchiveUsage{{MediaID: "media", Position: 0, AltText: &altIncoming}}, Location: &domain.Location{Name: "B", DisplayName: "B", Provider: "manual"}}},
		Media:   []sqlitestore.ArchiveMedia{{ID: "media", SHA256: repeatHex("b")}},
	}
	plan, err := ComparePortableAuthorities(MergePlanInput{Local: local, Incoming: incoming})
	if err != nil {
		t.Fatal(err)
	}
	items := mergeItemsByKey(plan.Items)
	assertMergeItem(t, items["entry\x00entry"], MergeConflict, []string{"media", "location"})
	assertMergeItem(t, items["media\x00media"], MergeConflict, []string{"sha256"})
}

func TestMergeReviewShowsRecognizableContentAndSiteMedia(t *testing.T) {
	localAvatar, incomingAvatar := "media-local", "media-incoming"
	localPinned, incomingPinned := "entry-local", "entry-incoming"
	local := PortableAuthority{
		Site: sqlitestore.ArchiveSite{
			OwnerID: "owner", AvatarMediaID: &localAvatar, PinnedPostID: &localPinned,
		},
		Entries: []sqlitestore.ArchiveEntry{
			{ID: localPinned, ContentMD: "keep this local post", CreatedAtMS: 10, UpdatedAtMS: 10, PublishedAtMS: 10},
			{ID: "entry-conflict", ContentMD: "local version", CreatedAtMS: 20, UpdatedAtMS: 20, PublishedAtMS: 20},
		},
		Comments: []sqlitestore.ArchiveComment{{ID: "comment-local", EntryID: localPinned, AuthorKind: "guest", AuthorName: "Local guest", ContentMD: "local reply", CreatedAtMS: 30}},
		Media:    []sqlitestore.ArchiveMedia{{ID: localAvatar, SHA256: repeatHex("a"), OriginalName: "local-avatar.jpg", MIMEType: "image/jpeg", ByteSize: 42, Width: 32, Height: 24}},
	}
	incoming := PortableAuthority{
		Site: sqlitestore.ArchiveSite{
			OwnerID: "owner", AvatarMediaID: &incomingAvatar, PinnedPostID: &incomingPinned,
		},
		Entries: []sqlitestore.ArchiveEntry{
			{ID: incomingPinned, ContentMD: "incoming post", CreatedAtMS: 40, UpdatedAtMS: 40, PublishedAtMS: 40},
			{ID: "entry-conflict", ContentMD: "incoming version", CreatedAtMS: 20, UpdatedAtMS: 50, PublishedAtMS: 20},
		},
		Media: []sqlitestore.ArchiveMedia{{ID: incomingAvatar, SHA256: repeatHex("b"), OriginalName: "incoming-avatar.png", MIMEType: "image/png", ByteSize: 84, Width: 64, Height: 48}},
	}
	plan, err := ComparePortableAuthorities(MergePlanInput{Local: local, Incoming: incoming})
	if err != nil {
		t.Fatal(err)
	}
	review, err := buildMergeReview(plan, local, incoming)
	if err != nil {
		t.Fatal(err)
	}
	if review.LocalOnly.Total != 2 || review.IncomingOnly.Total != 1 || review.Conflicts.Total != 1 {
		t.Fatalf("review buckets = %#v", review)
	}
	if review.LocalOnly.Items[0].Local == nil || review.LocalOnly.Items[0].Local.ContentMD != "local reply" {
		t.Fatalf("local-only content = %#v", review.LocalOnly.Items)
	}
	if review.Conflicts.Items[0].Local.ContentMD != "local version" || review.Conflicts.Items[0].Incoming.ContentMD != "incoming version" {
		t.Fatalf("conflict sides = %#v", review.Conflicts.Items[0])
	}
	fields := map[string]MergeReviewSiteField{}
	for _, field := range review.SiteFields {
		fields[field.Field] = field
	}
	if fields["avatar_media_id"].LocalMedia == nil || fields["avatar_media_id"].LocalMedia.OriginalName != "local-avatar.jpg" ||
		fields["avatar_media_id"].IncomingMedia == nil || fields["avatar_media_id"].IncomingMedia.OriginalName != "incoming-avatar.png" {
		t.Fatalf("avatar review = %#v", fields["avatar_media_id"])
	}
	if fields["pinned_post_id"].LocalEntry == nil || fields["pinned_post_id"].LocalEntry.ContentMD != "keep this local post" ||
		fields["pinned_post_id"].IncomingEntry == nil || fields["pinned_post_id"].IncomingEntry.ContentMD != "incoming post" {
		t.Fatalf("pinned review = %#v", fields["pinned_post_id"])
	}
}

func assertMergeItem(t *testing.T, item MergeItem, classification MergeClassification, fields []string) {
	t.Helper()
	if item.Classification != classification {
		t.Fatalf("item %#v classification = %s", item, item.Classification)
	}
	if fields != nil && !reflect.DeepEqual(item.ChangedFields, fields) {
		t.Fatalf("item %#v fields = %#v", item, item.ChangedFields)
	}
}

func mergeItemsByKey(items []MergeItem) map[string]MergeItem {
	result := make(map[string]MergeItem, len(items))
	for _, item := range items {
		result[string(item.Kind)+"\x00"+item.ID] = item
	}
	return result
}

func mergeSelectionByKey(items []MergeSelectionItem) map[string]MergeAction {
	result := make(map[string]MergeAction, len(items))
	for _, item := range items {
		result[string(item.Kind)+"\x00"+item.ID] = item.Action
	}
	return result
}

func mergeSiteFieldsByName(items []MergeSiteField) map[string]MergeSiteField {
	result := make(map[string]MergeSiteField, len(items))
	for _, item := range items {
		result[item.Field] = item
	}
	return result
}

func repeatHex(value string) string {
	result := ""
	for len(result) < 64 {
		result += value
	}
	return result[:64]
}

// Migration 0020 backfilled author_user_id from the site owner, so every owner
// comment in a live database carries a reference that the same comment in a
// backup taken before that migration does not: the key is omitted and decodes
// to nil. Both sides still render the same author, because a nil reference on
// an owner row resolves to the site owner. Comparing the pointers instead of
// the identity they resolve to turned every comment present on both sides into
// a conflict, so restoring a pre-migration backup asked the reviewer to decide
// the entire comment table one row at a time.
func TestMergePlanTreatsABackfilledOwnerReferenceAsUnchanged(t *testing.T) {
	ownerID := "0123456789abcdef0123456789abcdef"
	otherID := "fedcba9876543210fedcba9876543210"
	site := sqlitestore.ArchiveSite{OwnerID: ownerID, Username: "owner", DisplayName: "Owner", Timezone: "UTC", CommentsEnabled: true}
	comment := func(id, kind string, authorUserID *string) sqlitestore.ArchiveComment {
		return sqlitestore.ArchiveComment{ID: id, EntryID: "entry", AuthorKind: kind, AuthorName: "Owner", AuthorUserID: authorUserID, ContentMD: "same", CreatedAtMS: 1}
	}
	local := PortableAuthority{
		Site:    site,
		Entries: []sqlitestore.ArchiveEntry{{ID: "entry", Kind: "moment", ContentMD: "post", CreatedAtMS: 1, UpdatedAtMS: 1, PublishedAtMS: 1}},
		Comments: []sqlitestore.ArchiveComment{
			comment("comment-owner", "owner", &ownerID),
			comment("comment-moved", "owner", &ownerID),
			comment("comment-guest", "guest", nil),
		},
	}
	incoming := local
	incoming.Comments = []sqlitestore.ArchiveComment{
		// Written before author_user_id existed: the key is absent and decodes
		// to nil, and the reader resolves it to the site owner.
		comment("comment-owner", "owner", nil),
		// A different account is a real change and has to stay visible.
		comment("comment-moved", "owner", &otherID),
		comment("comment-guest", "guest", nil),
	}
	plan, err := ComparePortableAuthorities(MergePlanInput{ArchiveSHA256: repeatHex("a"), BaseGeneration: "generation", ExpiresAt: time.Unix(70, 0).UTC(), Local: local, Incoming: incoming})
	if err != nil {
		t.Fatal(err)
	}
	items := mergeItemsByKey(plan.Items)
	assertMergeItem(t, items["comment\x00comment-owner"], MergeUnchanged, []string{})
	assertMergeItem(t, items["comment\x00comment-guest"], MergeUnchanged, []string{})
	assertMergeItem(t, items["comment\x00comment-moved"], MergeConflict, []string{"author_user_id"})
}
