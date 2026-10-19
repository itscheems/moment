package sqlite

import (
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestHiddenPostsReachOnlyTheAuthorThroughEveryReadPath(t *testing.T) {
	ctx := testAccountContext()
	db, runtimeState := openTestDatabase(t, Options{})
	clock := time.Date(2024, 3, 10, 12, 0, 0, 0, time.UTC)
	posts := newTestPostService(t, db, func() time.Time { return clock }, sequenceIDs(entryOne, entryTwo, entryThree))

	public, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "shoreline public"})
	if err != nil {
		t.Fatal(err)
	}
	if public.Post.Entry.Visibility != domain.VisibilityEveryone {
		t.Fatalf("default visibility = %q", public.Post.Entry.Visibility)
	}
	clock = clock.Add(time.Minute)
	hidden, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "shoreline hidden", Visibility: domain.VisibilityOnlyMe})
	if err != nil {
		t.Fatal(err)
	}
	if hidden.Post.Entry.Visibility != domain.VisibilityOnlyMe {
		t.Fatalf("stored visibility = %q", hidden.Post.Entry.Visibility)
	}
	if _, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "bad", Visibility: domain.Visibility("friends")}); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("unknown visibility accepted: %v", err)
	}

	// Detail.
	if _, err := posts.Get(ctx, hidden.Post.Entry.ID, app.PublicViewer()); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("visitor detail of hidden post: %v", err)
	}
	if got, err := posts.Get(ctx, hidden.Post.Entry.ID, app.AccountViewer(testAccountID)); err != nil || got.Entry.Visibility != domain.VisibilityOnlyMe {
		t.Fatalf("owner detail of hidden post: %+v %v", got, err)
	}
	if _, err := posts.Get(ctx, public.Post.Entry.ID, app.PublicViewer()); err != nil {
		t.Fatalf("visitor detail of public post: %v", err)
	}

	// Timeline, including the repost projection of the hidden post.
	if _, err := posts.SetReposted(ctx, hidden.Post.Entry.ID, true); err != nil {
		t.Fatal(err)
	}
	visitorPage, err := posts.List(ctx, app.ListPostsInput{Timezone: "UTC"})
	if err != nil {
		t.Fatal(err)
	}
	if ids := timelineIDs(visitorPage); strings.Join(ids, ",") != entryOne {
		t.Fatalf("visitor timeline = %v", ids)
	}
	authorPage, err := posts.List(ctx, app.ListPostsInput{Timezone: "UTC", Viewer: app.AccountViewer(testAccountID)})
	if err != nil {
		t.Fatal(err)
	}
	if ids := timelineIDs(authorPage); strings.Join(ids, ",") != strings.Join([]string{entryTwo, entryTwo, entryOne}, ",") {
		t.Fatalf("owner timeline = %v", ids)
	}

	// Profile totals count originals across pages and filters, under the same
	// visibility rules as the author feed. Reposts never inflate the number.
	for _, test := range []struct {
		name   string
		viewer app.Viewer
		kind   domain.EntryKind
		from   string
		want   int
	}{
		{name: "visitor", want: 1},
		{name: "author", viewer: app.AccountViewer(testAccountID), want: 2},
		{name: "another account", viewer: app.AccountViewer("11111111111111111111111111111111"), want: 1},
		{name: "empty article filter", viewer: app.AccountViewer(testAccountID), kind: domain.KindArticle, want: 2},
		{name: "empty date window", viewer: app.AccountViewer(testAccountID), from: "2030-01-01", want: 2},
	} {
		page, err := posts.List(ctx, app.ListPostsInput{AuthorUserID: testAccountID, Timezone: "UTC", Limit: 1, Viewer: test.viewer, Kind: test.kind, FromDate: test.from})
		if err != nil || page.AuthorPostCount == nil || *page.AuthorPostCount != test.want {
			t.Fatalf("%s count = %+v, %v", test.name, page, err)
		}
		if page.NextCursor != "" {
			next, err := posts.List(ctx, app.ListPostsInput{AuthorUserID: testAccountID, Timezone: "UTC", Cursor: page.NextCursor, Viewer: test.viewer})
			if err != nil || next.AuthorPostCount == nil || *next.AuthorPostCount != test.want {
				t.Fatalf("%s continuation count = %+v, %v", test.name, next, err)
			}
		}
	}
	if visitorPage.AuthorPostCount != nil {
		t.Fatal("unscoped timeline published an author count")
	}

	// Search and the year counts that travel with it; archive counts too.
	searchRepository, err := NewSearchRepository(db, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	search, err := app.NewSearchService(searchRepository)
	if err != nil {
		t.Fatal(err)
	}
	visitorSearch, err := search.Search(ctx, app.SearchInput{Query: "shoreline"})
	if err != nil {
		t.Fatal(err)
	}
	if len(visitorSearch.Items) != 1 || visitorSearch.Items[0].Post.Entry.ID.String() != entryOne || visitorSearch.YearCounts[0].Count != 1 {
		t.Fatalf("visitor search = %+v", visitorSearch)
	}
	authorSearch, err := search.Search(ctx, app.SearchInput{Query: "shoreline", Viewer: app.AccountViewer(testAccountID)})
	if err != nil {
		t.Fatal(err)
	}
	if len(authorSearch.Items) != 2 || authorSearch.YearCounts[0].Count != 2 {
		t.Fatalf("owner search = %+v", authorSearch)
	}
	visitorMonths, err := search.ArchiveMonths(ctx, app.ArchiveMonthsInput{})
	if err != nil || len(visitorMonths.Items) != 1 || visitorMonths.Items[0].Count != 1 {
		t.Fatalf("visitor months = %+v %v", visitorMonths, err)
	}
	authorMonths, err := search.ArchiveMonths(ctx, app.ArchiveMonthsInput{Viewer: app.AccountViewer(testAccountID)})
	if err != nil || authorMonths.Items[0].Count != 2 {
		t.Fatalf("owner months = %+v %v", authorMonths, err)
	}
	visitorMonth, err := search.ArchiveMonth(ctx, "2024-03", app.PublicViewer())
	if err != nil || visitorMonth.Days[0].Count != 1 {
		t.Fatalf("visitor month = %+v %v", visitorMonth, err)
	}
	authorMonth, err := search.ArchiveMonth(ctx, "2024-03", app.AccountViewer(testAccountID))
	if err != nil || authorMonth.Days[0].Count != 2 {
		t.Fatalf("owner month = %+v %v", authorMonth, err)
	}

	clock = clock.Add(time.Minute)
	target := hidden.Post.Entry.ID
	quote, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "look", QuotedPostID: &target})
	if err != nil {
		t.Fatal(err)
	}
	visitorQuote, err := posts.Get(ctx, quote.Post.Entry.ID, app.PublicViewer())
	if err != nil || visitorQuote.Quoted == nil || !visitorQuote.Quoted.Unavailable {
		t.Fatalf("visitor quote = %+v %v", visitorQuote.Quoted, err)
	}
	authorQuote, err := posts.Get(ctx, quote.Post.Entry.ID, app.AccountViewer(testAccountID))
	if err != nil || authorQuote.Quoted == nil || authorQuote.Quoted.Unavailable {
		t.Fatalf("owner quote = %+v %v", authorQuote.Quoted, err)
	}
	if quotedBody, ok := authorQuote.Quoted.Entry.Markdown(); !ok || quotedBody != "shoreline hidden" {
		t.Fatalf("owner quote body = %q, present = %t", quotedBody, ok)
	}

	// Comments under a hidden post: a member is a visitor here.
	comments := newTestCommentService(t, db, runtimeState, func() time.Time { return clock }, commentIDs(commentOne, commentTwo))
	primaryAccount := testCommentAuthor(t, db, runtimeState)
	member := testCommentMember(t, db, runtimeState, "reader", "Reader")
	if _, err := comments.Create(ctx, app.CreateCommentInput{Author: member, EntryID: target, ContentMD: "hi"}); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("member comment on hidden post: %v", err)
	}
	if _, err := comments.Create(ctx, app.CreateCommentInput{Author: primaryAccount, EntryID: target, ContentMD: "note to self"}); err != nil {
		t.Fatalf("owner comment on hidden post: %v", err)
	}
	if _, err := comments.ListPage(ctx, app.ListCommentsInput{EntryID: target}); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("visitor thread of hidden post: %v", err)
	}
	if _, err := comments.ListReplies(ctx, app.ListRepliesInput{EntryID: target, RootID: mustID(t, commentOne)}); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("visitor replies of hidden post: %v", err)
	}
	if thread, err := comments.ListPage(ctx, app.ListCommentsInput{EntryID: target, Viewer: app.AccountViewer(testAccountID)}); err != nil || len(thread.Items) != 1 {
		t.Fatalf("owner thread of hidden post: %+v %v", thread, err)
	}

	// Widening the audience is a plain update; it moves updated_at but is not
	// an edit of the words.
	clock = clock.Add(time.Minute)
	everyone := domain.VisibilityEveryone
	revealed, err := posts.Update(ctx, app.UpdatePostInput{ID: target, PreserveContent: true, Visibility: &everyone})
	if err != nil || revealed.Entry.Visibility != domain.VisibilityEveryone || revealed.Entry.EditedAt != nil || !revealed.Entry.UpdatedAt.After(hidden.Post.Entry.UpdatedAt) {
		t.Fatalf("reveal = %+v %v", revealed.Entry, err)
	}
	if _, err := posts.Get(ctx, target, app.PublicViewer()); err != nil {
		t.Fatalf("visitor detail after reveal: %v", err)
	}
	if _, err := posts.Update(ctx, app.UpdatePostInput{ID: target, PreserveContent: true}); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("empty update accepted: %v", err)
	}
}

// A create retried with the same key replays the same audience, and a key
// minted before visibility existed still matches its default-audience body.
func TestCreateReplayPreservesVisibility(t *testing.T) {
	ctx := testAccountContext()
	db, _ := openTestDatabase(t, Options{})
	posts := newTestPostService(t, db, func() time.Time { return time.UnixMilli(1_700_000_000_000) }, sequenceIDs(entryOne, entryTwo))
	first, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "secret", Visibility: domain.VisibilityOnlyMe, IdempotencyKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "secret", Visibility: domain.VisibilityOnlyMe, IdempotencyKey: "k"})
	if err != nil || !replay.Replayed || replay.Post.Entry.ID != first.Post.Entry.ID || replay.Post.Entry.Visibility != domain.VisibilityOnlyMe {
		t.Fatalf("replay = %+v %v", replay, err)
	}
	if _, err := posts.Create(ctx, app.CreatePostInput{ContentMD: "secret", IdempotencyKey: "k"}); !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("audience change under a reused key: %v", err)
	}
}

// Rows written before the column existed read as public.
func TestEntriesWithoutAVisibilityColumnValueReadAsEveryone(t *testing.T) {
	ctx := testAccountContext()
	db, _ := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "legacy")
	posts := newTestPostService(t, db, time.Now, nil)
	post, err := posts.Get(ctx, mustID(t, entryOne), app.PublicViewer())
	if err != nil || post.Entry.Visibility != domain.VisibilityEveryone {
		t.Fatalf("legacy row = %+v %v", post.Entry, err)
	}
}

// A file is as visible as the posts that reference it.
func TestMediaVisibilityFollowsItsReferences(t *testing.T) {
	ctx := testAccountContext()
	db, _ := openTestDatabase(t, Options{})
	references, err := NewMediaReferenceRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	insertEntry(t, db, entryOne, "public")
	insertEntry(t, db, entryTwo, "hidden")
	mustWrite(t, db, `UPDATE entries SET visibility = 'only_me' WHERE id = ?`, entryTwo)
	mustWrite(t, db, `
INSERT INTO media (id, sha256, storage_path, original_name, mime_type, byte_size, width, height, created_at_ms, unreferenced_since_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`, mediaOne, strings.Repeat("a", 64), "media/original/aa/"+strings.Repeat("a", 64)+".jpg", "a.jpg", "image/jpeg", 100, 10, 10, 1)
	mustWrite(t, db, `
INSERT INTO media (id, sha256, storage_path, original_name, mime_type, byte_size, width, height, created_at_ms, unreferenced_since_ms)
VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, NULL)`, mediaTwo, strings.Repeat("b", 64), "media/original/bb/"+strings.Repeat("b", 64)+".png", "b.png", "image/png", 200, 20, 20, 1)

	expect := func(id string, want domain.Visibility, why string) {
		t.Helper()
		got, err := references.Visibility(ctx, mustID(t, id))
		if err != nil || got != want {
			t.Fatalf("%s: visibility = %q %v, want %q", why, got, err, want)
		}
	}
	expect(mediaOne, domain.VisibilityOnlyMe, "unreferenced upload")
	mustWrite(t, db, `INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 0, NULL)`, entryTwo, mediaOne)
	expect(mediaOne, domain.VisibilityOnlyMe, "referenced only by a hidden post")
	mustWrite(t, db, `INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 0, NULL)`, entryOne, mediaOne)
	expect(mediaOne, domain.VisibilityEveryone, "also referenced by a public post")

	mustWrite(t, db, `INSERT INTO comments (id, entry_id, parent_id, author_name, content_md, created_at_ms) VALUES (?, ?, NULL, 'Owner', 'note', 4)`, commentOne, entryTwo)
	mustWrite(t, db, `INSERT INTO comment_media (comment_id, media_id, position, alt_text) VALUES (?, ?, 0, NULL)`, commentOne, mediaTwo)
	expect(mediaTwo, domain.VisibilityOnlyMe, "comment media under a hidden post")
	mustWrite(t, db, `UPDATE entries SET visibility = 'everyone' WHERE id = ?`, entryTwo)
	expect(mediaTwo, domain.VisibilityEveryone, "the post was revealed")
	mustWrite(t, db, `UPDATE entries SET visibility = 'only_me' WHERE id = ?`, entryTwo)
	mustWrite(t, db, `UPDATE users SET avatar_media_id = ? WHERE id = (SELECT profile_user_id FROM site_settings WHERE id = 1)`, mediaTwo)
	expect(mediaTwo, domain.VisibilityEveryone, "an avatar is always public")
}

func timelineIDs(page app.PostPage) []string {
	ids := make([]string, 0, len(page.Items))
	for _, item := range page.Items {
		ids = append(ids, item.Entry.ID.String())
	}
	return ids
}
