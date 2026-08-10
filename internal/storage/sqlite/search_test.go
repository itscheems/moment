package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestSearchArchivePostSyncAndRebuild(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, runtimeState := openTestDatabase(t, Options{})
	if _, err := database.write.ExecContext(ctx, `UPDATE site_settings SET timezone = 'America/New_York' WHERE id = 1`); err != nil {
		t.Fatal(err)
	}

	postRepository, err := newPostRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	ids := []domain.ID{mustID(t, entryOne), mustID(t, entryTwo), mustID(t, entryThree)}
	clock := time.Date(2024, 3, 10, 4, 30, 0, 0, time.UTC) // Mar 9 in New York.
	posts, err := app.NewPostService(postRepository,
		app.WithPostClock(func() time.Time { return clock }),
		app.WithPostIDGenerator(func(time.Time) (domain.ID, error) {
			id := ids[0]
			ids = ids[1:]
			return id, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	first, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "# 旧城雨\n[label](https://secret.example/path) Straße"})
	if err != nil {
		t.Fatal(err)
	}
	clock = time.Date(2024, 3, 10, 5, 30, 0, 0, time.UTC) // Mar 10 in New York.
	second, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "旧城雨 tie searchable 100%"})
	if err != nil {
		t.Fatal(err)
	}
	// Equal rank, timestamp, and content still paginate by ID.
	third, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "旧城雨 tie searchable 100%"})
	if err != nil {
		t.Fatal(err)
	}

	repository, err := NewSearchRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSearchService(repository)
	if err != nil {
		t.Fatal(err)
	}

	page, err := service.Search(ctx, app.SearchInput{Query: "旧城雨", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != 1 || page.NextCursor == "" || len(page.YearCounts) != 1 || page.YearCounts[0].Count != 3 {
		t.Fatalf("first search page = %#v", page)
	}
	next, err := service.Search(ctx, app.SearchInput{Query: "旧城雨", Limit: 2, Cursor: page.NextCursor})
	if err != nil {
		t.Fatal(err)
	}
	if len(next.Items) != 2 || next.Items[0].Post.Entry.ID == page.Items[0].Post.Entry.ID {
		t.Fatalf("second search page = %#v", next)
	}
	if _, err := service.Search(ctx, app.SearchInput{Query: "different", Cursor: page.NextCursor}); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("cursor reused across query error = %v", err)
	}
	tie, err := service.Search(ctx, app.SearchInput{Query: "tie searchable", Limit: 1})
	if err != nil || len(tie.Items) != 1 || tie.NextCursor == "" {
		t.Fatalf("equal-rank first page = %#v, %v", tie, err)
	}
	tieNext, err := service.Search(ctx, app.SearchInput{Query: "tie searchable", Limit: 1, Cursor: tie.NextCursor})
	if err != nil || len(tieNext.Items) != 1 || tieNext.Items[0].Post.Entry.ID == tie.Items[0].Post.Entry.ID {
		t.Fatalf("equal-rank second page = %#v, %v", tieNext, err)
	}

	short, err := service.Search(ctx, app.SearchInput{Query: "%"})
	if err != nil || len(short.Items) != 2 {
		t.Fatalf("escaped short query = %#v, %v", short, err)
	}
	if _, err := service.Search(ctx, app.SearchInput{Query: `旧城 OR "rain"*`}); err != nil {
		t.Fatalf("FTS metacharacters escaped incorrectly: %v", err)
	}
	label, err := service.Search(ctx, app.SearchInput{Query: "label"})
	if err != nil || len(label.Items) != 1 || strings.Contains(label.Items[0].Snippet, "secret.example") {
		t.Fatalf("link normalization search = %#v, %v", label, err)
	}
	folded, err := service.Search(ctx, app.SearchInput{Query: "STRASSE"})
	if err != nil || len(folded.Items) != 1 {
		t.Fatalf("Unicode case-fold search = %#v, %v", folded, err)
	}

	months, err := service.ArchiveMonths(ctx, app.ArchiveMonthsInput{})
	if err != nil || len(months.Items) != 1 || months.Items[0].Count != 3 {
		t.Fatalf("archive months = %#v, %v", months, err)
	}
	march, err := service.ArchiveMonth(ctx, "2024-03")
	if err != nil || len(march.Days) != 2 || march.Days[0].Day != 9 || march.Days[0].Count != 1 || march.Days[1].Day != 10 || march.Days[1].Count != 2 {
		t.Fatalf("DST archive days = %#v, %v", march, err)
	}
	day, err := service.Search(ctx, app.SearchInput{Query: "旧城雨", FromDate: "2024-03-10", ToDate: "2024-03-11"})
	if err != nil || len(day.Items) != 2 || day.YearCounts[0].Count != 2 {
		t.Fatalf("site-timezone half-open search = %#v, %v", day, err)
	}

	clock = clock.Add(time.Hour)
	if _, err := posts.Update(ctx, app.UpdatePostInput{ID: first.Post.Entry.ID, ContentMD: "edited memory"}); err != nil {
		t.Fatal(err)
	}
	old, err := service.Search(ctx, app.SearchInput{Query: "label"})
	if err != nil || len(old.Items) != 0 {
		t.Fatalf("edited index retained old content = %#v, %v", old, err)
	}
	if _, err := posts.Delete(ctx, second.Post.Entry.ID); err != nil {
		t.Fatal(err)
	}
	remaining, err := service.Search(ctx, app.SearchInput{Query: "tie searchable"})
	if err != nil || len(remaining.Items) != 1 || remaining.Items[0].Post.Entry.ID != third.Post.Entry.ID {
		t.Fatalf("deleted index retained entry = %#v, %v", remaining, err)
	}

	var authoritative string
	if err := database.write.QueryRowContext(ctx, `SELECT content_md FROM entries WHERE id = ?`, third.Post.Entry.ID.String()).Scan(&authoritative); err != nil {
		t.Fatal(err)
	}
	if _, err := database.write.ExecContext(ctx, `DELETE FROM entry_fts WHERE entry_id = ?`, third.Post.Entry.ID.String()); err != nil {
		t.Fatal(err)
	}
	dryRun, err := service.Rebuild(ctx, true)
	if err != nil || !dryRun.DryRun || dryRun.Drifted != 1 || dryRun.Rebuilt != 0 {
		t.Fatalf("rebuild dry run = %#v, %v", dryRun, err)
	}
	rebuilt, err := service.Rebuild(ctx, false)
	if err != nil || rebuilt.Rebuilt != 2 {
		t.Fatalf("rebuild = %#v, %v", rebuilt, err)
	}
	idempotent, err := service.Rebuild(ctx, false)
	if err != nil || idempotent.Rebuilt != 0 || idempotent.Drifted != 0 {
		t.Fatalf("idempotent rebuild = %#v, %v", idempotent, err)
	}
	var after string
	if err := database.write.QueryRowContext(ctx, `SELECT content_md FROM entries WHERE id = ?`, third.Post.Entry.ID.String()).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != authoritative {
		t.Fatalf("rebuild changed authority: %q -> %q", authoritative, after)
	}

	cancelled, cancel := context.WithCancel(ctx)
	cancel()
	if _, err := service.Rebuild(cancelled, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled rebuild error = %v", err)
	}
}

func mustID(t *testing.T, value string) domain.ID {
	t.Helper()
	id, err := domain.ParseID(value)
	if err != nil {
		t.Fatal(err)
	}
	return id
}

func TestSearchBootstrapUsesVersionedNormalizerWithoutMaskingDrift(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, _ := openTestDatabase(t, Options{})
	if _, err := database.write.ExecContext(ctx, entryInsertSQL, entryOne, "moment", "[Visible](https://hidden.example) STRAßE", int64(1), int64(1), int64(1)); err != nil {
		t.Fatal(err)
	}
	if _, err := database.write.ExecContext(ctx, `UPDATE search_index_state SET normalizer_version = 0 WHERE id = 1`); err != nil {
		t.Fatal(err)
	}
	if err := bootstrapSearchDerived(ctx, database.write); err != nil {
		t.Fatal(err)
	}
	var contentText string
	var version, matches int
	if err := database.write.QueryRowContext(ctx, `SELECT content_text FROM entries WHERE id = ?`, entryOne).Scan(&contentText); err != nil {
		t.Fatal(err)
	}
	if err := database.write.QueryRowContext(ctx, `SELECT normalizer_version FROM search_index_state WHERE id = 1`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.write.QueryRowContext(ctx, `SELECT count(*) FROM entry_fts WHERE entry_fts MATCH 'visible'`).Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if contentText != "visible strasse" || version != domain.ContentNormalizerVersion || matches != 1 {
		t.Fatalf("bootstrap content=%q version=%d matches=%d", contentText, version, matches)
	}

	if _, err := database.write.ExecContext(ctx, `DELETE FROM entry_fts WHERE entry_id = ?`, entryOne); err != nil {
		t.Fatal(err)
	}
	if err := bootstrapSearchDerived(ctx, database.write); err != nil {
		t.Fatal(err)
	}
	if err := database.write.QueryRowContext(ctx, `SELECT count(*) FROM entry_fts WHERE entry_id = ?`, entryOne).Scan(&matches); err != nil {
		t.Fatal(err)
	}
	if matches != 0 {
		t.Fatalf("ordinary bootstrap masked later FTS drift: matches=%d", matches)
	}

	if _, err := database.write.ExecContext(ctx, `UPDATE search_index_state SET normalizer_version = ? WHERE id = 1`, domain.ContentNormalizerVersion+1); err != nil {
		t.Fatal(err)
	}
	if err := bootstrapSearchDerived(ctx, database.write); !errors.Is(err, ErrSchemaTooNew) {
		t.Fatalf("future normalizer version error = %v", err)
	}
}
