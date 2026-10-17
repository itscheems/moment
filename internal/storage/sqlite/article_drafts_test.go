package sqlite

import (
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

const (
	draftOneID = "01ARZ3NDEKTSV4RRFFQ69G5FG1"
	draftTwoID = "01ARZ3NDEKTSV4RRFFQ69G5FG2"
)

func makeDraftRecord(t *testing.T, db *Database, id, title, content string, at time.Time) app.ArticleDraftRecord {
	t.Helper()
	_ = db
	parsed, err := domain.ParseID(id)
	if err != nil {
		t.Fatal(err)
	}
	body, err := domain.ParseArticleDraftDocument([]byte(`{"schema_version":1,"type":"doc","content":[` + content + `]}`))
	if err != nil {
		t.Fatalf("parse draft body: %v", err)
	}
	draft, err := domain.NewArticleDraft(title, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	return app.ArticleDraftRecord{AuthorUserID: testAccountID, ID: parsed, Draft: draft, CreatedAt: at, UpdatedAt: at}
}

func withPostStore(t *testing.T, db *Database, write bool, operation func(store postStore) error) error {
	t.Helper()
	run := db.ReadTx
	if write {
		run = db.WriteTx
	}
	return run(testAccountContext(), func(queries Queries) error {
		return operation(newPostStore(queries))
	})
}

// A draft is allowed to be nothing yet: no title, no words. That is most of
// what being a draft means, and the published article rules do not apply.
func TestArticleDraftKeepsAnUnfinishedArticleOutsideTheTimeline(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	at := domain.TimeFromMillis(1_000)
	record := makeDraftRecord(t, db, draftOneID, "", "", at)

	if err := withPostStore(t, db, true, func(store postStore) error {
		return store.InsertArticleDraft(testAccountContext(), record)
	}); err != nil {
		t.Fatalf("insert: %v", err)
	}

	var read app.ArticleDraftRecord
	var found bool
	if err := withPostStore(t, db, false, func(store postStore) error {
		var err error
		read, found, err = store.ArticleDraftByID(testAccountContext(), record.ID)
		return err
	}); err != nil || !found {
		t.Fatalf("read back: found=%v err=%v", found, err)
	}
	if read.Draft.Title != "" || read.PublishedEntryID != nil || !read.UpdatedAt.Equal(at) {
		t.Fatalf("record = %+v", read)
	}

	// And it is not an entry, so nothing that reads the timeline can see it.
	var entries int
	if err := db.ReadTx(testAccountContext(), func(queries Queries) error {
		return queries.QueryRowContext(testAccountContext(), `SELECT count(*) FROM entries`).Scan(&entries)
	}); err != nil {
		t.Fatal(err)
	}
	if entries != 0 {
		t.Fatalf("a draft must not create an entry, found %d", entries)
	}
}

func TestArticleDraftRefusesAStaleOverwrite(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	first := domain.TimeFromMillis(1_000)
	record := makeDraftRecord(t, db, draftOneID, "初稿", `{"type":"paragraph","content":[{"type":"text","text":"第一版"}]}`, first)
	if err := withPostStore(t, db, true, func(store postStore) error {
		return store.InsertArticleDraft(testAccountContext(), record)
	}); err != nil {
		t.Fatal(err)
	}

	second := domain.TimeFromMillis(2_000)
	winner := makeDraftRecord(t, db, draftOneID, "第二版", `{"type":"paragraph","content":[{"type":"text","text":"第二版"}]}`, second)
	var applied bool
	if err := withPostStore(t, db, true, func(store postStore) error {
		var err error
		applied, err = store.UpdateArticleDraft(testAccountContext(), winner, first)
		return err
	}); err != nil || !applied {
		t.Fatalf("a current write must land: applied=%v err=%v", applied, err)
	}

	// The other tab still believes the draft is at its first revision.
	loser := makeDraftRecord(t, db, draftOneID, "另一个页签", `{"type":"paragraph","content":[{"type":"text","text":"不该覆盖"}]}`, domain.TimeFromMillis(3_000))
	if err := withPostStore(t, db, true, func(store postStore) error {
		var err error
		applied, err = store.UpdateArticleDraft(testAccountContext(), loser, first)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if applied {
		t.Fatal("a stale write must be refused rather than overwrite")
	}

	var read app.ArticleDraftRecord
	if err := withPostStore(t, db, false, func(store postStore) error {
		var err error
		read, _, err = store.ArticleDraftByID(testAccountContext(), record.ID)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if read.Draft.Title != "第二版" {
		t.Fatalf("the refused write still changed the draft: %q", read.Draft.Title)
	}
}

func TestArticleDraftMediaFollowsItsDraft(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	mediaID := articleMediaID(t, db, "c")
	record := makeDraftRecord(t, db, draftOneID, "带图草稿",
		`{"type":"image","attrs":{"media_id":"`+mediaID+`","alt":"草稿配图"}}`, domain.TimeFromMillis(1_000))
	if err := withPostStore(t, db, true, func(store postStore) error {
		return store.InsertArticleDraft(testAccountContext(), record)
	}); err != nil {
		t.Fatal(err)
	}

	count := func() int {
		t.Helper()
		var rows int
		if err := db.ReadTx(testAccountContext(), func(queries Queries) error {
			return queries.QueryRowContext(testAccountContext(),
				`SELECT count(*) FROM article_draft_media WHERE draft_id = ?`, draftOneID).Scan(&rows)
		}); err != nil {
			t.Fatal(err)
		}
		return rows
	}
	if count() != 1 {
		t.Fatalf("draft pictures must be projected, found %d", count())
	}

	var deleted bool
	if err := withPostStore(t, db, true, func(store postStore) error {
		var err error
		deleted, err = store.DeleteArticleDraft(testAccountContext(), record.ID)
		return err
	}); err != nil || !deleted {
		t.Fatalf("delete: %v %v", deleted, err)
	}
	if count() != 0 {
		t.Fatalf("draft pictures must go with the draft, %d left", count())
	}
}

func TestArticleDraftListsNewestFirstAndCounts(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	older := makeDraftRecord(t, db, draftOneID, "旧的", "", domain.TimeFromMillis(1_000))
	newer := makeDraftRecord(t, db, draftTwoID, "新的", "", domain.TimeFromMillis(2_000))
	if err := withPostStore(t, db, true, func(store postStore) error {
		if err := store.InsertArticleDraft(testAccountContext(), older); err != nil {
			return err
		}
		return store.InsertArticleDraft(testAccountContext(), newer)
	}); err != nil {
		t.Fatal(err)
	}

	var listed []app.ArticleDraftRecord
	var total int
	if err := withPostStore(t, db, false, func(store postStore) error {
		var err error
		if listed, err = store.ListArticleDrafts(testAccountContext(), 0); err != nil {
			return err
		}
		total, err = store.CountArticleDrafts(testAccountContext())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if total != 2 || len(listed) != 2 || listed[0].Draft.Title != "新的" || listed[1].Draft.Title != "旧的" {
		t.Fatalf("listed = %+v total = %d", listed, total)
	}
}

// Editing a published article happens on a working copy, and there is at most
// one of those per article.
func TestArticleDraftWorkingCopyIsUniquePerPublishedArticle(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	seedArticleEntry(t, db)
	entryID, err := domain.ParseID(articleEntryID)
	if err != nil {
		t.Fatal(err)
	}

	first := makeDraftRecord(t, db, draftOneID, "工作副本", "", domain.TimeFromMillis(1_000))
	first.PublishedEntryID = &entryID
	second := makeDraftRecord(t, db, draftTwoID, "第二个副本", "", domain.TimeFromMillis(1_000))
	second.PublishedEntryID = &entryID

	if err := withPostStore(t, db, true, func(store postStore) error {
		return store.InsertArticleDraft(testAccountContext(), first)
	}); err != nil {
		t.Fatal(err)
	}
	if err := withPostStore(t, db, true, func(store postStore) error {
		return store.InsertArticleDraft(testAccountContext(), second)
	}); err == nil {
		t.Fatal("one published article may have at most one working copy")
	}

	var found bool
	var read app.ArticleDraftRecord
	if err := withPostStore(t, db, false, func(store postStore) error {
		var err error
		read, found, err = store.ArticleDraftForEntry(testAccountContext(), entryID)
		return err
	}); err != nil || !found || read.ID != first.ID {
		t.Fatalf("working copy lookup = %+v found=%v err=%v", read, found, err)
	}

	mustWrite(t, db, `DELETE FROM entries WHERE id = ?`, articleEntryID)
	if err := withPostStore(t, db, false, func(store postStore) error {
		var err error
		read, found, err = store.ArticleDraftByID(testAccountContext(), first.ID)
		return err
	}); err != nil || !found {
		t.Fatalf("the draft must survive its article: found=%v err=%v", found, err)
	}
	if read.PublishedEntryID != nil {
		t.Fatalf("the draft should no longer name a deleted article: %v", read.PublishedEntryID)
	}
}

// Publishing applies the rules a draft was exempt from.
func TestArticleDraftCannotPublishWhenItSaysNothing(t *testing.T) {
	t.Parallel()
	empty, err := domain.ParseArticleDraftDocument([]byte(`{"schema_version":1,"type":"doc","content":[]}`))
	if err != nil {
		t.Fatal(err)
	}
	draft, err := domain.NewArticleDraft("", empty, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := draft.Publish(); err == nil {
		t.Fatal("an empty draft must not become a post")
	}

	titled, err := domain.NewArticleDraft("", articleTestBody(t, `{"type":"paragraph","content":[{"type":"text","text":"有内容"}]}`), nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := titled.Publish(); err == nil {
		t.Fatal("a draft without a title must not become a post")
	}
	named, err := domain.NewArticleDraft("标题", titled.Body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := named.Publish(); err != nil {
		t.Fatalf("a finished draft must publish: %v", err)
	}
}

// recordFor wraps a draft for the store without restating timestamps that no
// assertion depends on.
func recordFor(id domain.ID, draft domain.ArticleDraft) app.ArticleDraftRecord {
	at := domain.TimeFromMillis(1_000)
	return app.ArticleDraftRecord{AuthorUserID: testAccountID, ID: id, Draft: draft, CreatedAt: at, UpdatedAt: at}
}
