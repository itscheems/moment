package sqlite

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestSearchMatchesCompatibilityLettersAcrossSpaces(t *testing.T) {
	db, runtime := openTestDatabase(t, Options{})
	ctx := testAccountContext()
	posts := newTestPostService(t, db, func() time.Time { return time.Date(2024, 3, 10, 12, 0, 0, 0, time.UTC) }, sequenceIDs(entryOne, entryTwo, entryThree))
	for _, input := range []app.CreatePostInput{{ContentMD: "𝐁𝐋𝐔𝐄 𝐁𝐈𝐑𝐃 alpha"}, {ContentMD: "bluebird beta"}, {ContentMD: "blue bird private", Visibility: domain.VisibilityOnlyMe}} {
		if _, err := posts.Create(ctx, input); err != nil {
			t.Fatal(err)
		}
	}
	repository, err := NewSearchRepository(db, runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSearchService(repository)
	if err != nil {
		t.Fatal(err)
	}
	for _, q := range []string{"bluebird", "blue bird", "ＢＬＵＥ　ＢＩＲＤ", "𝐁𝐋𝐔𝐄 𝐁𝐈𝐑𝐃"} {
		for _, author := range []bool{false, true} {
			viewer := app.PublicViewer()
			want := 2
			if author {
				viewer = app.AccountViewer(testAccountID)
				want = 3
			}
			page, err := service.Search(ctx, app.SearchInput{Query: q, Viewer: viewer})
			if err != nil || len(page.Items) != want || len(page.YearCounts) != 1 || page.YearCounts[0].Count != want {
				t.Fatalf("%q author=%t: %+v %v", q, author, page, err)
			}
		}
	}
	// Paged reads and aggregate counts must use the same expanded match set.
	for _, sortBy := range []app.SearchSort{app.SearchSortRelevance, app.SearchSortLatest} {
		first, err := service.Search(ctx, app.SearchInput{Query: "bluebird", Limit: 1, Sort: sortBy})
		if err != nil || len(first.Items) != 1 || first.NextCursor == "" {
			t.Fatalf("first compact page: %+v %v", first, err)
		}
		second, err := service.Search(ctx, app.SearchInput{Query: "bluebird", Limit: 1, Sort: sortBy, Cursor: first.NextCursor})
		if err != nil || len(second.Items) != 1 || second.NextCursor != "" || first.Items[0].Post.Entry.ID == second.Items[0].Post.Entry.ID || second.YearCounts[0].Count != 2 {
			t.Fatalf("second compact page: %+v %v", second, err)
		}
	}
	// Compact matching must not turn FTS operators into executable query syntax.
	if page, err := service.Search(ctx, app.SearchInput{Query: `bluebird OR private`}); err != nil || len(page.Items) != 0 {
		t.Fatalf("literal query interpreted as operators: %+v %v", page, err)
	}
	// Changes and rebuilds maintain both indexed representations.
	if _, err := posts.Update(ctx, app.UpdatePostInput{ID: mustID(t, entryOne), ContentMD: "unrelated text"}); err != nil {
		t.Fatal(err)
	}
	page, err := service.Search(ctx, app.SearchInput{Query: "blue bird"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("stale alias after update: %+v %v", page, err)
	}
	if _, err := service.Rebuild(ctx, false); err != nil {
		t.Fatal(err)
	}
	page, err = service.Search(ctx, app.SearchInput{Query: "bluebird"})
	if err != nil || len(page.Items) != 1 {
		t.Fatalf("rebuild lost alias: %+v %v", page, err)
	}
	// A match deep in a long body must still be visible in its snippet.
	if _, err := posts.Update(ctx, app.UpdatePostInput{ID: mustID(t, entryOne), ContentMD: strings.Repeat("prefix ", 40) + "blue bird destination"}); err != nil {
		t.Fatal(err)
	}
	page, err = service.Search(ctx, app.SearchInput{Query: "bluebird"})
	if err != nil {
		t.Fatal(err)
	}
	for _, hit := range page.Items {
		if hit.Post.Entry.ID.String() == entryOne && !strings.Contains(hit.Snippet, "blue bird destination") {
			t.Fatalf("snippet misses compact match: %q", hit.Snippet)
		}
	}
}

func TestCompactSearchPreservesShortQueryRules(t *testing.T) {
	db, runtime := openTestDatabase(t, Options{})
	ctx := testAccountContext()
	posts := newTestPostService(t, db, time.Now, sequenceIDs(entryOne, entryTwo))
	for _, body := range []string{"a b destination", "ab destination"} {
		if _, err := posts.Create(ctx, app.CreatePostInput{ContentMD: body}); err != nil {
			t.Fatal(err)
		}
	}
	repo, err := NewSearchRepository(db, runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSearchService(repo)
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct{ query, id string }{{"ab", entryTwo}, {"a b", entryOne}, {"Ａ　Ｂ", entryOne}} {
		page, err := service.Search(ctx, app.SearchInput{Query: test.query})
		if err != nil || len(page.Items) != 1 || page.Items[0].Post.Entry.ID.String() != test.id {
			t.Fatalf("short query %q widened: %+v %v", test.query, page, err)
		}
	}
}

func TestCompactSearchMigrationPreservesBodiesAndRebuildsExistingIndex(t *testing.T) {
	pool := openRawMigrationPool(t)
	ctx := testAccountContext()
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	cut := 0
	for i, m := range migrations {
		if m.version == 12 {
			cut = i
			break
		}
	}
	if cut == 0 {
		t.Fatal("compact search migration missing")
	}
	if err := applyMigrations(ctx, pool, migrations[:cut], time.UnixMilli(1)); err != nil {
		t.Fatal(err)
	}
	statements := []string{
		`INSERT INTO users(id,username,display_name,role,status,created_at_ms,updated_at_ms) VALUES('0123456789abcdef0123456789abcdef','writer','Writer','admin','active',1,1)`,
		`INSERT INTO site_settings(id,title,timezone,comments_enabled,profile_user_id,registration_mode) VALUES(1,'Moment','UTC',1,'0123456789abcdef0123456789abcdef','approval')`,
		`INSERT INTO entries(id,kind,content_md,content_text,created_at_ms,updated_at_ms,published_at_ms,author_user_id) VALUES('01ARZ3NDEKTSV4RRFFQ69G5FAV','moment','𝐁𝐋𝐔𝐄 𝐁𝐈𝐑𝐃','blue bird',11,12,13,'0123456789abcdef0123456789abcdef')`,
	}
	for _, q := range statements {
		if _, err := pool.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	var revision int64
	if err := pool.QueryRowContext(ctx, `SELECT revision FROM search_index_state WHERE id=1`).Scan(&revision); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, pool, migrations, time.UnixMilli(2)); err != nil {
		t.Fatal(err)
	}
	var body string
	var created, updated, published, next int64
	var matches int
	if err := pool.QueryRowContext(ctx, `SELECT content_md,created_at_ms,updated_at_ms,published_at_ms FROM entries`).Scan(&body, &created, &updated, &published); err != nil {
		t.Fatal(err)
	}
	if body != "𝐁𝐋𝐔𝐄 𝐁𝐈𝐑𝐃" || created != 11 || updated != 12 || published != 13 {
		t.Fatalf("migration rewrote source: %q %d %d %d", body, created, updated, published)
	}
	if err := pool.QueryRowContext(ctx, `SELECT count(*) FROM entry_fts WHERE entry_fts MATCH '"bluebird"'`).Scan(&matches); err != nil || matches != 1 {
		t.Fatalf("legacy entry not indexed: %d %v", matches, err)
	}
	if err := pool.QueryRowContext(ctx, `SELECT revision FROM search_index_state WHERE id=1`).Scan(&next); err != nil || next <= revision {
		t.Fatalf("old cursors not invalidated: %d %d %v", revision, next, err)
	}
	if err := applyMigrations(ctx, pool, migrations[:cut], time.UnixMilli(3)); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("old migration set accepted upgraded database: %v", err)
	}
}
