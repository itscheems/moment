package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestArticleManagementSeparatesAuthorsAndPreservesWithdrawnWork(t *testing.T) {
	ctx := testAccountContext()
	db, service := articleServiceFixture(t, time.UnixMilli(1000))
	alice := addContentTestAccount(t, db, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice")
	bob := addContentTestAccount(t, db, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "bob")
	a := app.WithContentActor(ctx, alice)
	b := app.WithContentActor(ctx, bob)
	input := app.SaveArticleDraftInput{Title: "article", BodyJSON: draftBodyJSON("public version"), IdempotencyKey: "draft-retry"}
	draft, err := service.SaveArticleDraft(a, input)
	if err != nil {
		t.Fatal(err)
	}
	again, err := service.SaveArticleDraft(a, input)
	if err != nil || again.ID != draft.ID {
		t.Fatalf("create replay = %+v, %v", again, err)
	}
	other, err := service.SaveArticleDraft(b, input)
	if err != nil || other.ID == draft.ID {
		t.Fatalf("another author's independent key = %+v,%v", other, err)
	}
	for _, actor := range []context.Context{b, ctx} {
		if _, err := service.ArticleDraft(actor, draft.ID); err == nil {
			t.Fatal("non-author read a draft")
		}
		if err := service.DiscardArticleDraft(actor, draft.ID); err == nil {
			t.Fatal("non-author discarded a draft")
		}
	}
	summaries, err := service.ArticleDraftSummaries(a)
	if err != nil || len(summaries) != 1 || summaries[0].ID != draft.ID || summaries[0].Excerpt != "public version..." {
		t.Fatalf("summary = %+v,%v", summaries, err)
	}
	post, err := service.PublishArticleDraft(a, app.PublishArticleDraftInput{DraftID: draft.ID, IdempotencyKey: "publish"})
	if err != nil {
		t.Fatal(err)
	}
	if post.Entry.AuthorUserID != alice.ID {
		t.Fatal("published author changed")
	}
	if _, err := service.Delete(b, post.Entry.ID); err == nil {
		t.Fatal("generic delete bypassed authorship")
	}
	if _, err := service.WithdrawArticle(ctx, post.Entry.ID); err == nil {
		t.Fatal("non-author admin withdrew article")
	}
	working, err := service.EditArticle(a, post.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	working, err = service.SaveArticleDraft(a, app.SaveArticleDraftInput{ID: &working.ID, Title: "revised", BodyJSON: draftBodyJSON("unpublished edits"), ExpectedUpdatedAt: &working.UpdatedAt})
	if err != nil {
		t.Fatal(err)
	}
	updatedSummaries, err := service.ArticleDraftSummaries(a)
	if err != nil || len(updatedSummaries) != 1 || updatedSummaries[0].Excerpt != "unpublished edits..." {
		t.Fatalf("saved summary = %+v, %v", updatedSummaries, err)
	}
	withdrawn, err := service.WithdrawArticle(a, post.Entry.ID)
	if err != nil || withdrawn.ID != working.ID || !strings.Contains(domain.ArticleVisibleText(withdrawn.Draft.Body), "unpublished edits") {
		t.Fatalf("withdraw = %+v,%v", withdrawn, err)
	}
	again, err = service.WithdrawArticle(a, post.Entry.ID)
	if err != nil || again.ID != withdrawn.ID {
		t.Fatal("withdraw retry lost its draft")
	}
	for _, viewer := range []app.Viewer{app.PublicViewer(), app.ViewerFor(alice, true), app.ViewerFor(bob, true), app.AccountViewer(testAccountID)} {
		if _, err := service.Get(ctx, post.Entry.ID, viewer); err == nil {
			t.Fatal("withdrawn article exposed through public detail")
		}
		page, err := service.List(ctx, app.ListPostsInput{Viewer: viewer})
		if err != nil || len(page.Items) != 0 {
			t.Fatalf("withdrawn timeline = %+v,%v", page, err)
		}
	}
	if _, err := service.ManagedArticle(a, post.Entry.ID); err != nil {
		t.Fatal(err)
	}
	republished, err := service.PublishArticleDraft(a, app.PublishArticleDraftInput{DraftID: withdrawn.ID, IdempotencyKey: "republish"})
	if err != nil {
		t.Fatal(err)
	}
	if republished.Entry.ID != post.Entry.ID || !republished.Entry.PublishedAt.Equal(post.Entry.PublishedAt) || republished.Article.Title != "revised" {
		t.Fatalf("republish = %+v", republished)
	}
	inventory, err := service.PublishedArticles(a, app.ListPostsInput{Limit: 20})
	if err != nil || len(inventory.Items) != 1 {
		t.Fatalf("inventory = %+v,%v", inventory, err)
	}
	inventory, err = service.PublishedArticles(b, app.ListPostsInput{Limit: 20})
	if err != nil || len(inventory.Items) != 0 {
		t.Fatal("another author saw inventory")
	}
}

func TestArticleWithdrawalQuotaIsAtomicAndScopedToAuthor(t *testing.T) {
	ctx := testAccountContext()
	db, service := articleServiceFixture(t, time.UnixMilli(1000))
	alice := addContentTestAccount(t, db, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice")
	bob := addContentTestAccount(t, db, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "bob")
	a := app.WithContentActor(ctx, alice)
	draft, err := service.SaveArticleDraft(a, app.SaveArticleDraftInput{Title: "article", BodyJSON: draftBodyJSON("body")})
	if err != nil {
		t.Fatal(err)
	}
	post, err := service.PublishArticleDraft(a, app.PublishArticleDraftInput{DraftID: draft.ID})
	if err != nil {
		t.Fatal(err)
	}
	for range app.MaxArticleDrafts {
		if _, err := service.SaveArticleDraft(a, app.SaveArticleDraftInput{BodyJSON: []byte(`{"schema_version":1,"type":"doc","content":[]}`)}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.WithdrawArticle(a, post.Entry.ID); err == nil {
		t.Fatal("withdraw exceeded quota")
	}
	if _, err := service.Get(ctx, post.Entry.ID, app.PublicViewer()); err != nil {
		t.Fatal("quota failure hid public article")
	}
	if _, err := service.SaveArticleDraft(app.WithContentActor(ctx, bob), app.SaveArticleDraftInput{BodyJSON: draftBodyJSON("bob")}); err != nil {
		t.Fatal("alice quota blocked bob")
	}
}

func TestWithdrawnArticleKeepsCommentsAndPrivateMedia(t *testing.T) {
	ctx := testAccountContext()
	db, service := articleServiceFixture(t, time.UnixMilli(1000))
	alice := addContentTestAccount(t, db, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice")
	bob := addContentTestAccount(t, db, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "bob")
	a := app.WithContentActor(ctx, alice)
	cover := domain.ID(articleMediaID(t, db, "a"))
	draft, err := service.SaveArticleDraft(a, app.SaveArticleDraftInput{Title: "with comments", BodyJSON: draftBodyJSON("body"), CoverMediaID: &cover})
	if err != nil {
		t.Fatal(err)
	}
	post, err := service.PublishArticleDraft(a, app.PublishArticleDraftInput{DraftID: draft.ID})
	if err != nil {
		t.Fatal(err)
	}
	mustWrite(t, db, `INSERT INTO comments(id,entry_id,parent_id,author_name,author_user_id,content_md,created_at_ms) VALUES('01ARZ3NDEKTSV4RRFFQ69G5FAX',?,NULL,'alice',?,'retained comment',1000)`, post.Entry.ID.String(), alice.ID)
	withdrawn, err := service.WithdrawArticle(a, post.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if mediaVisibility(t, db, cover.String()) != domain.VisibilityOnlyMe {
		t.Fatal("withdrawn media stayed public")
	}
	refs, _ := NewMediaReferenceRepository(db)
	for _, viewer := range []app.Viewer{app.PublicViewer(), app.ViewerFor(bob, true), app.AccountViewer(testAccountID)} {
		allowed, err := refs.CanRead(ctx, cover, viewer)
		if err != nil || allowed {
			t.Fatalf("non-author media=%v,%v", allowed, err)
		}
	}
	allowed, err := refs.CanRead(ctx, cover, app.ViewerFor(alice, true))
	if err != nil || !allowed {
		t.Fatalf("author lost cover=%v,%v", allowed, err)
	}
	err = db.ReadTx(ctx, func(q Queries) error {
		allowed, err := newCommentStore(q).ReadableEntry(ctx, post.Entry.ID.String(), app.ViewerFor(alice, true))
		if err != nil {
			return err
		}
		if allowed {
			t.Fatal("withdrawn comments public read remained open")
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := service.PublishArticleDraft(a, app.PublishArticleDraftInput{DraftID: withdrawn.ID})
	if err != nil || restored.CommentCount != 1 {
		t.Fatalf("comments lost=%+v,%v", restored, err)
	}
}

func TestEmptyDraftDocumentWireAlwaysUsesAnArray(t *testing.T) {
	_, service := articleServiceFixture(t, time.UnixMilli(1000))
	draft, err := service.SaveArticleDraft(testAccountContext(), app.SaveArticleDraftInput{BodyJSON: []byte(`{"schema_version":1,"type":"doc","content":[]}`)})
	if err != nil {
		t.Fatal(err)
	}
	encoded, err := domain.MarshalArticleDocument(draft.Draft.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(encoded), `"content":[]`) {
		t.Fatalf("empty draft wire=%s", encoded)
	}
	read, err := service.ArticleDraft(testAccountContext(), draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	encoded, err = domain.MarshalArticleDocument(read.Draft.Body)
	if err != nil || !strings.Contains(string(encoded), `"content":[]`) {
		t.Fatalf("empty stored draft wire=%s,%v", encoded, err)
	}
}
