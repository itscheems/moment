package sqlite

import (
	"context"
	"testing"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

func TestArchiveSnapshotRejectsCrossRecordCorruption(t *testing.T) {
	for _, test := range []struct {
		name    string
		corrupt func(*testing.T, *Database)
	}{
		{
			name: "missing media reference",
			corrupt: func(t *testing.T, database *Database) {
				t.Helper()
				if _, err := database.write.Exec(`PRAGMA foreign_keys = OFF`); err != nil {
					t.Fatal(err)
				}
				if _, err := database.write.Exec(`INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 0, NULL)`, archiveEntryID, archiveMissingMediaID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			// A body row points at its entry, so a foreign key check sees a
			// body with no entry. It cannot see the other direction, and that
			// is the one that exports as a post with no words in it.
			name: "article entry with no body",
			corrupt: func(t *testing.T, database *Database) {
				t.Helper()
				if _, err := database.write.Exec(`INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms) VALUES (?, 'article', NULL, 1, 1, 1)`, archiveArticleID); err != nil {
					t.Fatal(err)
				}
			},
		},
		{
			name: "reply targets another reply",
			corrupt: func(t *testing.T, database *Database) {
				t.Helper()
				if _, err := database.write.Exec(`DROP TRIGGER comments_parent_insert_guard`); err != nil {
					t.Fatal(err)
				}
				if _, err := database.write.Exec(`INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms) VALUES (?, ?, ?, 'owner', 'Owner', 'nested', 3)`, archiveNestedID, archiveEntryID, archiveReplyID); err != nil {
					t.Fatal(err)
				}
			},
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			runtimeState, err := runtimefs.Initialize(ctx, runtimefs.Config{DataDir: t.TempDir()})
			if err != nil {
				t.Fatal(err)
			}
			database, err := Open(ctx, runtimeState, Options{})
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = database.Close() }()
			if _, err := database.write.Exec(`INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms) VALUES (?, 'moment', 'entry', 1, 1, 1)`, archiveEntryID); err != nil {
				t.Fatal(err)
			}
			if _, err := database.write.Exec(`INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms) VALUES (?, ?, NULL, 'guest', 'Guest', 'root', 1)`, archiveRootID, archiveEntryID); err != nil {
				t.Fatal(err)
			}
			if _, err := database.write.Exec(`INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms) VALUES (?, ?, ?, 'owner', 'Owner', 'reply', 2)`, archiveReplyID, archiveEntryID, archiveRootID); err != nil {
				t.Fatal(err)
			}
			test.corrupt(t, database)
			snapshot, err := database.BeginArchiveSnapshot(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = snapshot.Close() }()
			if err := snapshot.ValidateIntegrity(ctx); err == nil {
				t.Fatal("corrupt cross-record graph passed archive validation")
			}
		})
	}
}

const (
	archiveEntryID        = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	archiveRootID         = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	archiveReplyID        = "01ARZ3NDEKTSV4RRFFQ69G5FAX"
	archiveNestedID       = "01ARZ3NDEKTSV4RRFFQ69G5FAY"
	archiveMissingMediaID = "01ARZ3NDEKTSV4RRFFQ69G5FAZ"
	archiveArticleID      = "01ARZ3NDEKTSV4RRFFQ69G5FB0"
)

// The two article members of a backup are written from these streams, so what
// they leave out is what a backup leaves out.
func TestArchiveSnapshotStreamsArticleBodiesAndTheirDrafts(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, _ := openTestDatabase(t, Options{})
	inline, cover := seedArticleEntry(t, db)
	coverID, err := domain.ParseID(cover)
	if err != nil {
		t.Fatal(err)
	}
	article, err := domain.NewArticle("带封面的文章", articleTestBody(t, articleImage(inline, "配图")), &coverID)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArticle(t, db, article, false); err != nil {
		t.Fatal(err)
	}
	entryID, err := domain.ParseID(articleEntryID)
	if err != nil {
		t.Fatal(err)
	}

	picture := articleMediaID(t, db, "f")
	draftBody, err := domain.ParseArticleDraftDocument(
		[]byte(`{"schema_version":1,"type":"doc","content":[` + articleImage(picture, "") + `]}`))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := domain.NewArticleDraft("工作副本", draftBody, nil)
	if err != nil {
		t.Fatal(err)
	}
	draftID, err := domain.ParseID(draftOneID)
	if err != nil {
		t.Fatal(err)
	}
	record := recordFor(draftID, draft)
	record.PublishedEntryID = &entryID
	if err := withPostStore(t, db, true, func(store postStore) error {
		return store.InsertArticleDraft(ctx, record)
	}); err != nil {
		t.Fatal(err)
	}

	snapshot, err := db.BeginArchiveSnapshot(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = snapshot.Close() }()
	if counts := snapshot.Counts(); counts.Articles != 1 || counts.ArticleDrafts != 1 {
		t.Fatalf("snapshot counts = %#v", counts)
	}
	var bodies []ArchiveEntryArticle
	if err := snapshot.EntryArticles(ctx, func(value ArchiveEntryArticle) error {
		bodies = append(bodies, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	canonical, err := domain.MarshalArticleDocument(article.Body)
	if err != nil {
		t.Fatal(err)
	}
	if len(bodies) != 1 || bodies[0].EntryID != articleEntryID || bodies[0].Title != "带封面的文章" ||
		bodies[0].SchemaVersion != 1 || bodies[0].BodyJSON != string(canonical) ||
		bodies[0].CoverMediaID == nil || *bodies[0].CoverMediaID != cover {
		t.Fatalf("streamed article = %#v", bodies)
	}

	var drafts []ArchiveArticleDraft
	if err := snapshot.ArticleDrafts(ctx, func(value ArchiveArticleDraft) error {
		drafts = append(drafts, value)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(drafts) != 1 || drafts[0].ID != draftOneID || drafts[0].Title != "工作副本" ||
		drafts[0].CoverMediaID != nil || drafts[0].PublishedEntryID == nil ||
		*drafts[0].PublishedEntryID != articleEntryID {
		t.Fatalf("streamed draft = %#v", drafts)
	}
	// A draft's pictures are folded in the way an entry's usages are, so a
	// restore puts them back where the body placed them.
	if len(drafts[0].Media) != 1 || drafts[0].Media[0].MediaID != picture ||
		drafts[0].Media[0].Position != 0 || drafts[0].Media[0].AltText != nil {
		t.Fatalf("streamed draft pictures = %#v", drafts[0].Media)
	}
}
