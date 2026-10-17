package sqlite

import (
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestPostMutationsRequireItsAuthorEvenForAdministrators(t *testing.T) {
	ctx := testAccountContext()
	db, posts := articleServiceFixture(t, time.UnixMilli(1000))
	alice := addContentTestAccount(t, db, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice")
	bob := addContentTestAccount(t, db, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "bob")
	admin := domain.User{ID: "cccccccccccccccccccccccccccccccc", Role: domain.UserRoleAdmin, Status: domain.AccountActive}
	a := app.WithContentActor(ctx, alice)
	created, err := posts.Create(a, app.CreatePostInput{ContentMD: "Alice's original words"})
	if err != nil {
		t.Fatal(err)
	}
	for _, actor := range []domain.User{bob, admin} {
		acting := app.WithContentActor(ctx, actor)
		if _, err := posts.Update(acting, app.UpdatePostInput{ID: created.Post.Entry.ID, ContentMD: "someone else"}); !domain.HasCode(err, domain.CodeUnauthorized) {
			t.Fatalf("cross-author edit: %v", err)
		}
		if _, err := posts.SetReposted(acting, created.Post.Entry.ID, true); !domain.HasCode(err, domain.CodeUnauthorized) {
			t.Fatalf("cross-author repost: %v", err)
		}
	}
	if _, err := posts.Delete(app.WithContentActor(ctx, bob), created.Post.Entry.ID); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("member deleting another author's post: %v", err)
	}
	read, err := posts.Get(ctx, created.Post.Entry.ID, app.PublicViewer())
	if err != nil || read.Entry.ContentMD == nil || *read.Entry.ContentMD != "Alice's original words" {
		t.Fatalf("rejected writes changed content: %+v, %v", read, err)
	}
	if _, err := posts.Delete(app.WithContentActor(ctx, admin), created.Post.Entry.ID); err != nil {
		t.Fatalf("administrator moderation: %v", err)
	}
}

func TestModerationCannotDeleteAnotherAuthorsPrivateOrUnpublishedContent(t *testing.T) {
	ctx := testAccountContext()
	db, posts := articleServiceFixture(t, time.UnixMilli(1000))
	alice := addContentTestAccount(t, db, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice")
	admin := domain.User{ID: "cccccccccccccccccccccccccccccccc", Role: domain.UserRoleAdmin, Status: domain.AccountActive}
	a := app.WithContentActor(ctx, alice)
	private, err := posts.Create(a, app.CreatePostInput{ContentMD: "private", Visibility: domain.VisibilityOnlyMe})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := posts.Delete(app.WithContentActor(ctx, admin), private.Post.Entry.ID); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("private moderation: %v", err)
	}
	draft, err := posts.SaveArticleDraft(a, app.SaveArticleDraftInput{Title: "article", BodyJSON: draftBodyJSON("body")})
	if err != nil {
		t.Fatal(err)
	}
	article, err := posts.PublishArticleDraft(a, app.PublishArticleDraftInput{DraftID: draft.ID})
	if err != nil {
		t.Fatal(err)
	}
	working, err := posts.WithdrawArticle(a, article.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := posts.Delete(app.WithContentActor(ctx, admin), article.Entry.ID); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("unpublished moderation: %v", err)
	}
	article, err = posts.PublishArticleDraft(a, app.PublishArticleDraftInput{DraftID: working.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := posts.Delete(app.WithContentActor(ctx, admin), article.Entry.ID); err != nil {
		t.Fatalf("public article moderation: %v", err)
	}
	if _, err := posts.Get(ctx, private.Post.Entry.ID, app.ViewerFor(alice, true)); err != nil {
		t.Fatalf("private content lost: %v", err)
	}
}

func TestPostRetryKeysAreIndependentAcrossAccounts(t *testing.T) {
	ctx := testAccountContext()
	db, posts := articleServiceFixture(t, time.UnixMilli(1000))
	alice := addContentTestAccount(t, db, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice")
	bob := addContentTestAccount(t, db, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "bob")
	input := app.CreatePostInput{ContentMD: "same text", IdempotencyKey: "same-retry-key"}
	first, err := posts.Create(app.WithContentActor(ctx, alice), input)
	if err != nil {
		t.Fatal(err)
	}
	second, err := posts.Create(app.WithContentActor(ctx, bob), input)
	if err != nil || second.Replayed || second.Post.Entry.ID == first.Post.Entry.ID || second.Post.Entry.AuthorUserID != bob.ID {
		t.Fatalf("another account's key: %+v, %v", second, err)
	}
	replay, err := posts.Create(app.WithContentActor(ctx, alice), input)
	if err != nil || !replay.Replayed || replay.Post.Entry.ID != first.Post.Entry.ID || replay.Post.Author == nil || replay.Post.Author.UserID != alice.ID {
		t.Fatalf("author's replay: %+v, %v", replay, err)
	}
}

func TestQuotesUseThePublishingAccountsVisibility(t *testing.T) {
	ctx := testAccountContext()
	db, posts := articleServiceFixture(t, time.UnixMilli(1000))
	alice := addContentTestAccount(t, db, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice")
	bob := addContentTestAccount(t, db, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "bob")
	a := app.WithContentActor(ctx, alice)
	private, err := posts.Create(a, app.CreatePostInput{ContentMD: "private original", Visibility: domain.VisibilityOnlyMe})
	if err != nil {
		t.Fatal(err)
	}
	input := app.CreatePostInput{ContentMD: "quoted", QuotedPostID: &private.Post.Entry.ID}
	if _, err := posts.Create(app.WithContentActor(ctx, bob), input); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("another account quoted a private post: %v", err)
	}
	own, err := posts.Create(a, input)
	if err != nil || own.Post.Quoted == nil || own.Post.Quoted.Unavailable {
		t.Fatalf("author cannot see their own quote: %+v, %v", own, err)
	}
}

func TestCommentModerationCannotReachAnotherAuthorsPrivatePost(t *testing.T) {
	ctx := testAccountContext()
	db, runtime := openTestDatabase(t, Options{})
	clock := func() time.Time { return time.UnixMilli(1000) }
	posts := newTestPostService(t, db, clock, nil)
	comments := newTestCommentService(t, db, runtime, clock, commentIDs(commentOne))
	alice := addContentTestAccount(t, db, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice")
	admin := testCommentAuthor(t, db, runtime)
	a := app.WithContentActor(ctx, alice)
	private, err := posts.Create(a, app.CreatePostInput{ContentMD: "private", Visibility: domain.VisibilityOnlyMe})
	if err != nil {
		t.Fatal(err)
	}
	written, err := comments.Create(a, app.CreateCommentInput{EntryID: private.Post.Entry.ID, Author: alice, ContentMD: "private reply"})
	if err != nil {
		t.Fatal(err)
	}
	for _, dryRun := range []bool{true, false} {
		if _, err := comments.DeleteBy(app.WithContentActor(ctx, admin), written.Comment.Comment.ID, admin, dryRun); !domain.HasCode(err, domain.CodePostNotFound) {
			t.Fatalf("private moderation dryRun=%v: %v", dryRun, err)
		}
	}
	if _, err := comments.DeleteBy(a, written.Comment.Comment.ID, alice, false); err != nil {
		t.Fatalf("author cannot remove private reply: %v", err)
	}
}
