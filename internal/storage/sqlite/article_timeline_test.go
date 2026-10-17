package sqlite

import (
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

// publishArticleFixture installs a complete article: the timeline envelope, the
// body beside it, and the picture projection that follows from the body.
func publishArticleFixture(t *testing.T, db *Database, title, prose string) domain.ID {
	t.Helper()
	seedArticleEntry(t, db)
	body := articleTestBody(t, `{"type":"paragraph","content":[{"type":"text","text":"`+prose+`"}]}`)
	article, err := domain.NewArticle(title, body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeArticle(t, db, article, false); err != nil {
		t.Fatal(err)
	}
	id, err := domain.ParseID(articleEntryID)
	if err != nil {
		t.Fatal(err)
	}
	// The write path owns the derived text; a fixture that skipped it would
	// make the search assertions below test nothing.
	text := app.DerivedArticleSearchText(title, body)
	mustWrite(t, db, `UPDATE entries SET content_text = ?, published_at_ms = 5000 WHERE id = ?`, text, articleEntryID)
	mustWrite(t, db, `UPDATE entries SET published_at_ms = 4000 WHERE id = ?`, momentEntryID)
	return id
}

func articleTimelineIDs(t *testing.T, db *Database, query app.TimelineQuery) []string {
	t.Helper()
	var ids []string
	if err := withPostStore(t, db, false, func(store postStore) error {
		entries, err := store.ListEntries(testAccountContext(), query, 20)
		if err != nil {
			return err
		}
		for _, entry := range entries {
			ids = append(ids, entry.Entry.ID.String())
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return ids
}

func TestTimelineCarriesBothKindsAndCanBeNarrowedToOne(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	publishArticleFixture(t, db, "文章标题", "文章正文")

	owner := app.TimelineQuery{Viewer: app.AccountViewer(testAccountID)}
	if ids := articleTimelineIDs(t, db, owner); !equalStrings(ids, []string{articleEntryID, momentEntryID}) {
		t.Fatalf("both kinds share one timeline, newest first: %v", ids)
	}

	owner.Kind = domain.KindArticle
	if ids := articleTimelineIDs(t, db, owner); !equalStrings(ids, []string{articleEntryID}) {
		t.Fatalf("narrowed to articles: %v", ids)
	}
	owner.Kind = domain.KindMoment
	if ids := articleTimelineIDs(t, db, owner); !equalStrings(ids, []string{momentEntryID}) {
		t.Fatalf("narrowed to moments: %v", ids)
	}
}

func TestArticleIsAPostEverywhereAPostIsRead(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	id := publishArticleFixture(t, db, "读得到的文章", "正文")

	if err := withPostStore(t, db, false, func(store postStore) error {
		entry, found, err := store.EntryByID(testAccountContext(), id)
		if err != nil || !found {
			t.Fatalf("an article must be readable by id: found=%v err=%v", found, err)
		}
		if entry.Kind != domain.KindArticle || entry.ContentMD != nil {
			t.Fatalf("entry = %+v", entry)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// Commented on, pinned, reposted, deleted: all of it works on an article.
	var visibility domain.Visibility
	if err := db.ReadTx(testAccountContext(), func(queries Queries) error {
		found := false
		var err error
		visibility, found, err = commentStore{queries: queries}.EntryVisibility(testAccountContext(), id)
		if !found {
			t.Fatal("an article must be a valid comment target")
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if visibility != domain.VisibilityEveryone {
		t.Fatalf("visibility = %q", visibility)
	}

	mustWrite(t, db, `UPDATE site_settings SET pinned_entry_id = ? WHERE id = 1`, articleEntryID)

	if err := withPostStore(t, db, true, func(store postStore) error {
		at := domain.TimeFromMillis(9_000)
		changed, err := store.SetEntryRepostedAt(testAccountContext(), id, &at, at)
		if err != nil || !changed {
			t.Fatalf("an article must be repostable: changed=%v err=%v", changed, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := withPostStore(t, db, true, func(store postStore) error {
		deleted, err := store.DeleteEntry(testAccountContext(), id)
		if err != nil || !deleted {
			t.Fatalf("an article must be deletable: deleted=%v err=%v", deleted, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := db.ReadTx(testAccountContext(), func(queries Queries) error {
		return queries.QueryRowContext(testAccountContext(),
			`SELECT count(*) FROM entry_article WHERE entry_id = ?`, articleEntryID).Scan(&remaining)
	}); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("the body must go with the entry, %d left", remaining)
	}
}

// The Markdown update path is moment-only on purpose: an article is rewritten
// as a whole document, and letting this touch one would leave its body and its
// projection disagreeing.
func TestArticleCannotBeEditedThroughTheMarkdownPath(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	id := publishArticleFixture(t, db, "不该被改", "正文")

	if err := withPostStore(t, db, true, func(store postStore) error {
		entry := domain.Entry{
			ID: id, Kind: domain.KindArticle, ContentMD: domain.MarkdownBody("偷偷塞进来"),
			Visibility: domain.VisibilityEveryone,
			CreatedAt:  domain.TimeFromMillis(1), UpdatedAt: domain.TimeFromMillis(2), PublishedAt: domain.TimeFromMillis(1),
		}
		changed, err := store.UpdateEntry(testAccountContext(), entry, "偷偷塞进来")
		if err != nil {
			return err
		}
		if changed {
			t.Fatal("the Markdown path must not touch an article")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestArticlesAreSearchableByTitleAndByBody(t *testing.T) {
	t.Parallel()
	db, runtimeState := openTestDatabase(t, Options{})
	publishArticleFixture(t, db, "长安的荔枝", "一骑红尘妃子笑")

	search, err := NewSearchRepository(db, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{"荔枝", "妃子笑"} {
		var hits int
		if err := search.Read(testAccountContext(), func(store app.SearchStore) error {
			records, readErr := store.SearchEntries(testAccountContext(), app.SearchRepositoryQuery{
				Mode: app.SearchModeLike, QueryValue: "%" + term + "%", Viewer: app.AccountViewer(testAccountID),
			}, 10)
			hits = len(records)
			return readErr
		}); err != nil {
			t.Fatalf("search %q: %v", term, err)
		}
		if hits != 1 {
			t.Fatalf("searching %q found %d articles, want 1", term, hits)
		}
	}
}

// A hidden article hides everywhere a hidden moment does.
func TestHiddenArticleStaysOutOfPublicReads(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	publishArticleFixture(t, db, "只有我能看", "私密正文")
	mustWrite(t, db, `UPDATE entries SET visibility = 'only_me' WHERE id = ?`, articleEntryID)

	public := app.TimelineQuery{Viewer: app.PublicViewer()}
	for _, id := range articleTimelineIDs(t, db, public) {
		if id == articleEntryID {
			t.Fatal("a hidden article must not appear in a public timeline")
		}
	}
}
