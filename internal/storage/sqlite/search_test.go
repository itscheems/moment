package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

func TestSearchArchivePostSyncAndRebuild(t *testing.T) {
	t.Parallel()
	ctx := testAccountContext()
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
	first, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "# 旧城雨\n[label](https://secret.example/path) Straße https://example.com之后 `https://code-only.invalid/path`"})
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
	adjacent, err := service.Search(ctx, app.SearchInput{Query: "之后"})
	if err != nil || len(adjacent.Items) != 1 {
		t.Fatalf("URL-adjacent CJK search = %#v, %v", adjacent, err)
	}
	codeURL, err := service.Search(ctx, app.SearchInput{Query: "code-only.invalid"})
	if err != nil || len(codeURL.Items) != 1 {
		t.Fatalf("code URL search = %#v, %v", codeURL, err)
	}
	for _, removedURL := range []string{"example.com", "secret.example"} {
		removed, err := service.Search(ctx, app.SearchInput{Query: removedURL})
		if err != nil || len(removed.Items) != 0 {
			t.Fatalf("removed URL %q search = %#v, %v", removedURL, removed, err)
		}
	}

	months, err := service.ArchiveMonths(ctx, app.ArchiveMonthsInput{})
	if err != nil || len(months.Items) != 1 || months.Items[0].Count != 3 {
		t.Fatalf("archive months = %#v, %v", months, err)
	}
	march, err := service.ArchiveMonth(ctx, "2024-03", app.AccountViewer(testAccountID))
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

func TestSearchLinkCardTextAgreesBetweenWriteAndRebuild(t *testing.T) {
	// A card contributes its title, site, author, and canonical host to the
	// indexed text on the write path. The drift check and the rebuild must
	// compose exactly that from the stored card, or every entry with a card
	// reads as damaged and a rebuild rewrites it to something Create never
	// wrote. Both of those happened when the rebuild side concatenated the
	// card in SQL instead of calling the combiner.
	t.Parallel()
	ctx := testAccountContext()
	database, runtimeState := openTestDatabase(t, Options{})
	postRepository, err := newPostRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	ids := []domain.ID{mustID(t, entryOne), mustID(t, entryTwo)}
	now := time.Date(2026, 8, 22, 1, 44, 0, 0, time.UTC)
	posts, err := app.NewPostService(postRepository,
		app.WithPostClock(func() time.Time { return now }),
		app.WithPostIDGenerator(func(time.Time) (domain.ID, error) {
			id := ids[0]
			ids = ids[1:]
			return id, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	card := domain.LinkCard{
		SourceURL: "https://www.example.org/posts/on-restraint?ref=feed", CanonicalURL: "https://www.example.org/posts/on-restraint",
		Provider: domain.LinkProviderGeneric, Kind: domain.LinkKindLink, Title: "On Restraint", SiteName: "Example Journal",
		AuthorName: "A. Writer", CapturedAt: domain.TimeToMillis(now),
	}
	withCard, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "worth a read", LinkCards: []domain.LinkCard{card}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "https://www.example.org/posts/on-restraint", LinkCards: []domain.LinkCard{card}}); err != nil {
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

	diagnostic, err := service.DiagnoseDerived(ctx, 10)
	if err != nil || diagnostic.Total != 2 || diagnostic.Drifted != 0 || diagnostic.Ready != 2 {
		t.Fatalf("freshly written cards read as drifted: %#v, %v", diagnostic, err)
	}
	var written string
	if err := database.write.QueryRowContext(ctx, `SELECT content_text FROM entries WHERE id = ?`, withCard.Post.Entry.ID.String()).Scan(&written); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(written, "example.org") || strings.Contains(written, "https://") {
		t.Fatalf("indexed text = %q, want the canonical host and not the URL", written)
	}
	rebuilt, err := service.Rebuild(ctx, false)
	if err != nil || rebuilt.Drifted != 0 || rebuilt.Rebuilt != 0 {
		t.Fatalf("rebuild of agreeing text = %#v, %v", rebuilt, err)
	}
	var after string
	if err := database.write.QueryRowContext(ctx, `SELECT content_text FROM entries WHERE id = ?`, withCard.Post.Entry.ID.String()).Scan(&after); err != nil {
		t.Fatal(err)
	}
	if after != written {
		t.Fatalf("rebuild changed the indexed text: %q -> %q", written, after)
	}
	found, err := service.Search(ctx, app.SearchInput{Query: "example.org"})
	if err != nil || len(found.Items) != 2 {
		t.Fatalf("host search = %#v, %v", found, err)
	}

	// The one-time bootstrap a new normalizer version triggers on open is a
	// rebuild too, and it once re-indexed every entry from its Markdown alone:
	// the cards fell out of the index and the doctor read every carded entry
	// as drifted right after an upgrade.
	if _, err := database.write.ExecContext(ctx, `UPDATE search_index_state SET normalizer_version = ? WHERE id = 1`, domain.ContentNormalizerVersion-1); err != nil {
		t.Fatal(err)
	}
	if err := bootstrapSearchDerived(ctx, database.write); err != nil {
		t.Fatal(err)
	}
	var bootstrapped string
	if err := database.write.QueryRowContext(ctx, `SELECT content_text FROM entries WHERE id = ?`, withCard.Post.Entry.ID.String()).Scan(&bootstrapped); err != nil {
		t.Fatal(err)
	}
	if bootstrapped != written {
		t.Fatalf("version bootstrap changed the indexed text: %q -> %q", written, bootstrapped)
	}
	diagnostic, err = service.DiagnoseDerived(ctx, 10)
	if err != nil || diagnostic.Drifted != 0 || diagnostic.Ready != 2 {
		t.Fatalf("cards read as drifted after a version bootstrap: %#v, %v", diagnostic, err)
	}
}

func TestSearchCursorRejectsCreateUpdateAndDeleteBetweenPages(t *testing.T) {
	t.Parallel()
	ctx := testAccountContext()
	database, runtimeState := openTestDatabase(t, Options{})
	postRepository, err := newPostRepository(database)
	if err != nil {
		t.Fatal(err)
	}
	ids := []domain.ID{mustID(t, entryOne), mustID(t, entryTwo), mustID(t, entryThree)}
	now := time.Date(2026, 8, 10, 10, 0, 0, 0, time.UTC)
	posts, err := app.NewPostService(postRepository,
		app.WithPostClock(func() time.Time { return now }),
		app.WithPostIDGenerator(func(time.Time) (domain.ID, error) {
			id := ids[0]
			ids = ids[1:]
			return id, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	first, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "revision searchable first"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "revision searchable second"})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewSearchRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	search, err := app.NewSearchService(repository)
	if err != nil {
		t.Fatal(err)
	}
	page := cursorPage(t, search)
	if _, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "revision searchable third"}); err != nil {
		t.Fatal(err)
	}
	if _, err := search.Search(ctx, app.SearchInput{Query: "revision searchable", Limit: 1, Cursor: page.NextCursor}); !staleCursorError(err) {
		t.Fatalf("cursor after create error = %v", err)
	}
	page = cursorPage(t, search)
	if _, err := posts.Update(ctx, app.UpdatePostInput{ID: first.Post.Entry.ID, ContentMD: "revision searchable first edited"}); err != nil {
		t.Fatal(err)
	}
	if _, err := search.Search(ctx, app.SearchInput{Query: "revision searchable", Limit: 1, Cursor: page.NextCursor}); !staleCursorError(err) {
		t.Fatalf("cursor after update error = %v", err)
	}
	page = cursorPage(t, search)
	if _, err := posts.Delete(ctx, second.Post.Entry.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := search.Search(ctx, app.SearchInput{Query: "revision searchable", Limit: 1, Cursor: page.NextCursor}); !staleCursorError(err) {
		t.Fatalf("cursor after delete error = %v", err)
	}
	page = cursorPage(t, search)
	if _, err := database.write.ExecContext(ctx, `DELETE FROM entry_fts WHERE entry_id = ?`, first.Post.Entry.ID.String()); err != nil {
		t.Fatal(err)
	}
	if _, err := search.Rebuild(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := search.Search(ctx, app.SearchInput{Query: "revision searchable", Limit: 1, Cursor: page.NextCursor}); !staleCursorError(err) {
		t.Fatalf("cursor after rebuild error = %v", err)
	}
}

func cursorPage(t *testing.T, service *app.SearchService) app.SearchPage {
	t.Helper()
	page, err := service.Search(testAccountContext(), app.SearchInput{Query: "revision searchable", Limit: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("cursor page = %#v, %v", page, err)
	}
	return page
}

func staleCursorError(err error) bool {
	var domainErr *domain.Error
	return errors.As(err, &domainErr) && domainErr.Code == domain.CodeInvalidArgument && domainErr.Details["reason"] == "stale"
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
	ctx := testAccountContext()
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

func TestSearchBootstrapForwardUpgradeFromV1RebuildsDerivedState(t *testing.T) {
	t.Parallel()
	ctx := testAccountContext()
	runtimeState, err := runtimefs.Initialize(ctx, runtimefs.Config{
		DataDir:     t.TempDir(),
		Environment: runtimefs.EnvironmentDevelopment,
	})
	if err != nil {
		t.Fatal(err)
	}
	migrations, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if len(migrations) != 1 {
		t.Fatalf("embedded migrations = %d, want the account/content baseline", len(migrations))
	}
	legacy, err := openPool(sqliteDSN(runtimeState.Current.DatabasePath, defaultBusyTimeout, false), 1)
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, legacy, migrations, time.UnixMilli(1)); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := newAccountStore(legacy).InitializeSiteAccount(ctx, domain.User{ID: testAccountID, Username: "writer", DisplayName: "Writer", Role: domain.UserRoleAdmin, Status: domain.AccountActive, CreatedAtMS: 1, UpdatedAtMS: 1}, "UTC"); err != nil {
		t.Fatal(err)
	}
	const (
		contentMD   = "https://example.com之后 `https://code-only.invalid/path`"
		createdAt   = int64(101)
		updatedAt   = int64(202)
		publishedAt = int64(303)
	)
	if _, err := legacy.ExecContext(ctx, `
INSERT INTO entries (id, kind, content_md, content_text, created_at_ms, updated_at_ms, published_at_ms, author_user_id)
VALUES (?, ?, ?, '', ?, ?, ?, '0123456789abcdef0123456789abcdef')`, entryOne, string(domain.KindMoment), contentMD, createdAt, updatedAt, publishedAt); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	// Version 1 removed both URL-shaped runs, including their adjacent CJK and
	// visible code, leaving this authoritative post with empty derived text.
	if _, err := legacy.ExecContext(ctx, `UPDATE search_index_state SET normalizer_version = 1 WHERE id = 1`); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	var legacyVersion, legacyMatches int
	if err := legacy.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&legacyVersion); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if err := legacy.QueryRowContext(ctx, `SELECT count(*) FROM entry_fts WHERE content_text != ''`).Scan(&legacyMatches); err != nil {
		_ = legacy.Close()
		t.Fatal(err)
	}
	if legacyVersion != 1 || legacyMatches != 0 {
		_ = legacy.Close()
		t.Fatalf("legacy fixture version=%d nonempty_fts=%d", legacyVersion, legacyMatches)
	}
	if err := legacy.Close(); err != nil {
		t.Fatal(err)
	}

	restartedRuntime, err := runtimefs.Open(runtimeState.Root)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := Open(ctx, restartedRuntime, Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if err := reopened.Ready(ctx); err != nil {
		t.Fatalf("database is not ready after normalizer upgrade: %v", err)
	}

	var (
		kind                                          string
		storedContentMD, contentText                  string
		storedCreated, storedUpdated, storedPublished int64
		version                                       int
		revision                                      int64
	)
	if err := reopened.read.QueryRowContext(ctx, `
SELECT kind, content_md, content_text, created_at_ms, updated_at_ms, published_at_ms
FROM entries
WHERE id = ?`, entryOne).Scan(&kind, &storedContentMD, &contentText, &storedCreated, &storedUpdated, &storedPublished); err != nil {
		t.Fatal(err)
	}
	if kind != string(domain.KindMoment) || storedContentMD != contentMD || storedCreated != createdAt || storedUpdated != updatedAt || storedPublished != publishedAt {
		t.Fatalf("normalizer upgrade changed authority: kind=%q content=%q timestamps=%d/%d/%d", kind, storedContentMD, storedCreated, storedUpdated, storedPublished)
	}
	if contentText != "之后 https://code-only.invalid/path" {
		t.Fatalf("upgraded content_text = %q", contentText)
	}
	if err := reopened.read.QueryRowContext(ctx, `SELECT normalizer_version, revision FROM search_index_state WHERE id = 1`).Scan(&version, &revision); err != nil {
		t.Fatal(err)
	}
	if version != domain.ContentNormalizerVersion || revision <= 1 {
		t.Fatalf("upgraded search state version=%d revision=%d", version, revision)
	}

	repository, err := NewSearchRepository(reopened, restartedRuntime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSearchService(repository)
	if err != nil {
		t.Fatal(err)
	}
	for _, query := range []string{"之后", "`https://code-only.invalid/path`"} {
		page, err := service.Search(ctx, app.SearchInput{Query: query})
		if err != nil || len(page.Items) != 1 || page.Items[0].Post.Entry.ID.String() != entryOne {
			t.Fatalf("upgraded search %q = %#v, %v", query, page, err)
		}
	}
}
