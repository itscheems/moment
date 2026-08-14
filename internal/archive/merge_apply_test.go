package archive

import (
	"bytes"
	"testing"
	"time"

	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestResolvePortableMergeKeepsLocalByDefaultAndAddsIncoming(t *testing.T) {
	localMediaID := "01H00000000000000000000001"
	incomingMediaID := "01H00000000000000000000002"
	localURL, incomingURL := "https://local.test", "https://incoming.test"
	local := PortableAuthority{
		Site:    sqlitestore.ArchiveSite{OwnerID: "owner", Username: "local", DisplayName: "Local", Title: "Local", PublicBaseURL: &localURL, Timezone: "UTC", CommentsEnabled: true},
		Entries: []sqlitestore.ArchiveEntry{{ID: "01H00000000000000000000003", ContentMD: "local conflict"}},
		Media:   []sqlitestore.ArchiveMedia{{ID: localMediaID, SHA256: repeatHex("a"), StoragePath: "media/original/aa/local.jpg"}},
	}
	incoming := PortableAuthority{
		Site: sqlitestore.ArchiveSite{OwnerID: "owner", Username: "incoming", DisplayName: "Incoming", Title: "Incoming", PublicBaseURL: &incomingURL, Timezone: "Asia/Shanghai", CommentsEnabled: false},
		Entries: []sqlitestore.ArchiveEntry{
			{ID: "01H00000000000000000000003", ContentMD: "incoming conflict", Media: []sqlitestore.ArchiveUsage{{MediaID: incomingMediaID}}},
			{ID: "01H00000000000000000000004", ContentMD: "incoming only", Media: []sqlitestore.ArchiveUsage{{MediaID: incomingMediaID}}},
		},
		Comments: []sqlitestore.ArchiveComment{{ID: "01H00000000000000000000005", EntryID: "01H00000000000000000000004", ContentMD: "new"}},
		Media:    []sqlitestore.ArchiveMedia{{ID: incomingMediaID, SHA256: repeatHex("b"), StoragePath: "media/original/bb/incoming.jpg"}},
	}
	plan, err := ComparePortableAuthorities(MergePlanInput{ArchiveSHA256: repeatHex("c"), BaseGeneration: "generation", ExpiresAt: time.Unix(20, 0).UTC(), Local: local, Incoming: incoming})
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := ResolvePortableMerge(plan, plan.DefaultSelection, local, incoming, MergeResolveOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if resolved.Authority.Site.Username != "local" || resolved.Authority.Site.PublicBaseURL == nil || *resolved.Authority.Site.PublicBaseURL != localURL {
		t.Fatalf("site = %#v", resolved.Authority.Site)
	}
	entries := indexEntries(resolved.Authority.Entries)
	if len(entries) != 2 || entries["01H00000000000000000000003"].ContentMD != "local conflict" || entries["01H00000000000000000000004"].ContentMD != "incoming only" {
		t.Fatalf("entries = %#v", entries)
	}
	if len(resolved.Authority.Comments) != 1 || len(resolved.Authority.Media) != 1 || resolved.Authority.Media[0].ID != incomingMediaID {
		t.Fatalf("resolved graph = %#v", resolved.Authority)
	}
}

func TestResolvePortableMergeKeepBothRemapsPostReplyAndMediaCollision(t *testing.T) {
	entryID := "01H00000000000000000000010"
	commentID := "01H00000000000000000000011"
	mediaID := "01H00000000000000000000012"
	local := PortableAuthority{
		Site:     sqlitestore.ArchiveSite{OwnerID: "owner", Username: "owner", DisplayName: "Owner", Timezone: "UTC"},
		Entries:  []sqlitestore.ArchiveEntry{{ID: entryID, ContentMD: "local", Media: []sqlitestore.ArchiveUsage{{MediaID: mediaID}}}},
		Comments: []sqlitestore.ArchiveComment{{ID: commentID, EntryID: entryID, ContentMD: "local"}},
		Media:    []sqlitestore.ArchiveMedia{{ID: mediaID, SHA256: repeatHex("a")}},
	}
	incoming := PortableAuthority{
		Site:     sqlitestore.ArchiveSite{OwnerID: "owner", Username: "owner", DisplayName: "Owner", Timezone: "UTC"},
		Entries:  []sqlitestore.ArchiveEntry{{ID: entryID, ContentMD: "incoming", Media: []sqlitestore.ArchiveUsage{{MediaID: mediaID}}}},
		Comments: []sqlitestore.ArchiveComment{{ID: commentID, EntryID: entryID, ContentMD: "incoming"}},
		Media:    []sqlitestore.ArchiveMedia{{ID: mediaID, SHA256: repeatHex("b")}},
	}
	plan, err := ComparePortableAuthorities(MergePlanInput{ArchiveSHA256: repeatHex("d"), BaseGeneration: "generation", ExpiresAt: time.Unix(30, 0).UTC(), Local: local, Incoming: incoming})
	if err != nil {
		t.Fatal(err)
	}
	selection := plan.DefaultSelection
	for index := range selection.Items {
		if selection.Items[index].ID == entryID || selection.Items[index].ID == commentID {
			selection.Items[index].Action = MergeKeepBoth
		}
	}
	resolved, err := ResolvePortableMerge(plan, selection, local, incoming, MergeResolveOptions{
		Clock:   func() time.Time { return time.UnixMilli(1_700_000_000_000) },
		Entropy: bytes.NewReader([]byte{0, 0, 0, 0, 0, 0, 0, 0, 0, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 0, 0, 0, 0, 0, 0, 0, 0, 0, 3}),
	})
	if err != nil {
		t.Fatal(err)
	}
	newEntryID := resolved.EntryRemap[entryID]
	newCommentID := resolved.CommentRemap[commentID]
	if newEntryID == entryID || newCommentID == commentID || len(resolved.Authority.Entries) != 2 || len(resolved.Authority.Comments) != 2 || len(resolved.Authority.Media) != 2 {
		t.Fatalf("remaps = %#v / %#v, graph = %#v", resolved.EntryRemap, resolved.CommentRemap, resolved.Authority)
	}
	comments := indexComments(resolved.Authority.Comments)
	if comments[newCommentID].EntryID != newEntryID {
		t.Fatalf("remapped comment = %#v", comments[newCommentID])
	}
	entries := indexEntries(resolved.Authority.Entries)
	if entries[newEntryID].Media[0].MediaID == mediaID {
		t.Fatalf("media collision was not remapped: %#v", entries[newEntryID])
	}
}

func TestResolvePortableMergeRejectsIncompleteOrStaleSelection(t *testing.T) {
	local := PortableAuthority{Site: sqlitestore.ArchiveSite{OwnerID: "owner"}}
	incoming := PortableAuthority{Site: sqlitestore.ArchiveSite{OwnerID: "owner"}, Entries: []sqlitestore.ArchiveEntry{{ID: "01H00000000000000000000020"}}}
	plan, err := ComparePortableAuthorities(MergePlanInput{ArchiveSHA256: repeatHex("e"), BaseGeneration: "generation", ExpiresAt: time.Unix(40, 0).UTC(), Local: local, Incoming: incoming})
	if err != nil {
		t.Fatal(err)
	}
	stale := plan.DefaultSelection
	stale.BaseAuthoritySHA256 = repeatHex("f")
	if _, err := ResolvePortableMerge(plan, stale, local, incoming, MergeResolveOptions{}); err == nil {
		t.Fatal("expected stale selection rejection")
	}
	incomplete := plan.DefaultSelection
	incomplete.Items = nil
	if _, err := ResolvePortableMerge(plan, incomplete, local, incoming, MergeResolveOptions{}); err == nil {
		t.Fatal("expected incomplete selection rejection")
	}
}
