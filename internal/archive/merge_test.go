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
