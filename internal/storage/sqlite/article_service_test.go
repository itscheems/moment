package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func articleServiceFixture(t *testing.T, at time.Time) (*Database, *app.PostService) {
	t.Helper()
	db, runtimeState := openTestDatabase(t, Options{})
	repository, err := NewPostRepository(db, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	clock := at
	service, err := app.NewPostService(repository, app.WithPostClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatal(err)
	}
	return db, service
}

func draftBodyJSON(prose string) []byte {
	return []byte(`{"schema_version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"` + prose + `"}]}]}`)
}

func TestPublishingADraftLeavesOneArticleAndNoDraft(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, service := articleServiceFixture(t, time.UnixMilli(1_000).UTC())

	draft, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{
		Title: "发布测试", BodyJSON: draftBodyJSON("正文内容"),
	})
	if err != nil {
		t.Fatalf("save draft: %v", err)
	}

	post, err := service.PublishArticleDraft(ctx, app.PublishArticleDraftInput{DraftID: draft.ID})
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if post.Entry.Kind != domain.KindArticle || post.Entry.ContentMD != nil {
		t.Fatalf("published entry = %+v", post.Entry)
	}
	if post.Article == nil || post.Article.Title != "发布测试" {
		t.Fatalf("published article = %+v", post.Article)
	}
	if post.ContentHTML == "" {
		t.Fatal("a published article must carry rendered content")
	}

	// The draft is consumed, and the derived text is what search will read.
	if _, err := service.ArticleDraft(ctx, draft.ID); err == nil {
		t.Fatal("publishing must consume the draft")
	}
	var contentText string
	if err := db.ReadTx(ctx, func(queries Queries) error {
		return queries.QueryRowContext(ctx, `SELECT content_text FROM entries WHERE id = ?`, post.Entry.ID.String()).Scan(&contentText)
	}); err != nil {
		t.Fatal(err)
	}
	if contentText != app.DerivedArticleSearchText("发布测试", post.Article.Body) {
		t.Fatalf("derived text = %q", contentText)
	}
}

func TestRepublishingWithTheSameKeyReturnsTheSameArticle(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, service := articleServiceFixture(t, time.UnixMilli(1_000).UTC())

	draft, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{
		Title: "只发一次", BodyJSON: draftBodyJSON("正文"),
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.PublishArticleDraft(ctx, app.PublishArticleDraftInput{DraftID: draft.ID, IdempotencyKey: "article-001"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.PublishArticleDraft(ctx, app.PublishArticleDraftInput{DraftID: draft.ID, IdempotencyKey: "article-001"})
	if err != nil {
		t.Fatalf("a retry must replay rather than fail: %v", err)
	}
	if first.Entry.ID != second.Entry.ID {
		t.Fatalf("retry produced a different article: %s vs %s", first.Entry.ID, second.Entry.ID)
	}
	var articles int
	if err := db.ReadTx(ctx, func(queries Queries) error {
		return queries.QueryRowContext(ctx, `SELECT count(*) FROM entries WHERE kind = 'article'`).Scan(&articles)
	}); err != nil {
		t.Fatal(err)
	}
	if articles != 1 {
		t.Fatalf("a retried publish produced %d articles", articles)
	}
}

// Editing happens on a working copy; the published article does not move until
// that copy is published in turn.
func TestEditingAPublishedArticleGoesThroughAWorkingCopy(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	db, service := articleServiceFixture(t, time.UnixMilli(1_000).UTC())

	draft, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{
		Title: "初版", BodyJSON: draftBodyJSON("初版正文"),
	})
	if err != nil {
		t.Fatal(err)
	}
	published, err := service.PublishArticleDraft(ctx, app.PublishArticleDraftInput{DraftID: draft.ID})
	if err != nil {
		t.Fatal(err)
	}

	working, err := service.EditArticle(ctx, published.Entry.ID)
	if err != nil {
		t.Fatalf("open working copy: %v", err)
	}
	if working.PublishedEntryID == nil || *working.PublishedEntryID != published.Entry.ID {
		t.Fatalf("working copy = %+v", working)
	}
	if working.Draft.Title != "初版" {
		t.Fatalf("a working copy starts from what is published, got %q", working.Draft.Title)
	}
	again, err := service.EditArticle(ctx, published.Entry.ID)
	if err != nil || again.ID != working.ID {
		t.Fatalf("reopening must reuse the same copy: %s vs %s (%v)", again.ID, working.ID, err)
	}

	// Saving the copy must not change what a reader sees.
	if _, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{
		ID: &working.ID, Title: "改过的标题", BodyJSON: draftBodyJSON("改过的正文"),
		ExpectedUpdatedAt: &working.UpdatedAt,
	}); err != nil {
		t.Fatalf("save working copy: %v", err)
	}
	var liveTitle string
	if err := db.ReadTx(ctx, func(queries Queries) error {
		return queries.QueryRowContext(ctx, `SELECT title FROM entry_article WHERE entry_id = ?`, published.Entry.ID.String()).Scan(&liveTitle)
	}); err != nil {
		t.Fatal(err)
	}
	if liveTitle != "初版" {
		t.Fatalf("editing changed the live article before publishing: %q", liveTitle)
	}

	updated, err := service.ArticleDraft(ctx, working.ID)
	if err != nil {
		t.Fatal(err)
	}
	republished, err := service.PublishArticleDraft(ctx, app.PublishArticleDraftInput{DraftID: updated.ID})
	if err != nil {
		t.Fatalf("republish: %v", err)
	}
	if republished.Entry.ID != published.Entry.ID {
		t.Fatalf("republishing must not create a second article")
	}
	if republished.Article.Title != "改过的标题" {
		t.Fatalf("republished title = %q", republished.Article.Title)
	}
	if republished.Entry.EditedAt == nil {
		t.Fatal("an edited article must say it was edited")
	}
	if !republished.Entry.PublishedAt.Equal(published.Entry.PublishedAt) {
		t.Fatal("editing must not move the publication instant")
	}
}

func TestSavingADraftRefusesAStaleRevisionAndAnOverfullSite(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, service := articleServiceFixture(t, time.UnixMilli(1_000).UTC())

	draft, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{Title: "第一版", BodyJSON: draftBodyJSON("内容")})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{
		ID: &draft.ID, Title: "第二版", BodyJSON: draftBodyJSON("内容"), ExpectedUpdatedAt: &draft.UpdatedAt,
	}); err != nil {
		t.Fatalf("the first write must land: %v", err)
	}
	_, err = service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{
		ID: &draft.ID, Title: "另一个页签", BodyJSON: draftBodyJSON("内容"), ExpectedUpdatedAt: &draft.UpdatedAt,
	})
	if !domain.HasCode(err, domain.CodeArticleDraftStale) {
		t.Fatalf("a stale write must be refused with a reason, got %v", err)
	}
	if _, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{
		ID: &draft.ID, Title: "无版本", BodyJSON: draftBodyJSON("内容"),
	}); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("a replacement must say what it was based on, got %v", err)
	}

	for range app.MaxArticleDrafts - 1 {
		if _, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{Title: "占位", BodyJSON: draftBodyJSON("内容")}); err != nil {
			t.Fatalf("filling up to the limit must work: %v", err)
		}
	}
	_, err = service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{Title: "超出", BodyJSON: draftBodyJSON("内容")})
	if !domain.HasCode(err, domain.CodeArticleDraftLimit) {
		t.Fatalf("the draft limit must be named, got %v", err)
	}
}

func TestAnEmptyDraftCannotBePublished(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, service := articleServiceFixture(t, time.UnixMilli(1_000).UTC())

	draft, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{
		Title: "", BodyJSON: []byte(`{"schema_version":1,"type":"doc","content":[]}`),
	})
	if err != nil {
		t.Fatalf("an empty draft must be saveable: %v", err)
	}
	if _, err := service.PublishArticleDraft(ctx, app.PublishArticleDraftInput{DraftID: draft.ID}); err == nil {
		t.Fatal("an empty draft must not become a post")
	}
	// And it is still there to go back to.
	if _, err := service.ArticleDraft(ctx, draft.ID); err != nil {
		t.Fatalf("a refused publish must leave the draft alone: %v", err)
	}
}

// A working copy is a draft, so it answers to the same ceiling. Before, opening
// published articles one after another walked straight past a limit the other
// door refuses at.
func TestOpeningAWorkingCopyAnswersToTheDraftCeiling(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, service := articleServiceFixture(t, time.UnixMilli(1_000).UTC())

	draft, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{
		Title: "已经发布的", BodyJSON: draftBodyJSON("正文"),
	})
	if err != nil {
		t.Fatal(err)
	}
	published, err := service.PublishArticleDraft(ctx, app.PublishArticleDraftInput{DraftID: draft.ID})
	if err != nil {
		t.Fatal(err)
	}

	for range app.MaxArticleDrafts {
		if _, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{
			Title: "占位", BodyJSON: draftBodyJSON("内容"),
		}); err != nil {
			t.Fatalf("filling up to the limit must work: %v", err)
		}
	}
	if _, err := service.EditArticle(ctx, published.Entry.ID); !domain.HasCode(err, domain.CodeArticleDraftLimit) {
		t.Fatalf("a full site must refuse another working copy by name, got %v", err)
	}

	// The copy that already exists is still reachable: the ceiling stops new
	// drafts, not the one an owner is in the middle of.
	if err := service.DiscardArticleDraft(ctx, draftIDOfNewest(t, ctx, service)); err != nil {
		t.Fatal(err)
	}
	working, err := service.EditArticle(ctx, published.Entry.ID)
	if err != nil {
		t.Fatalf("room again means a working copy opens: %v", err)
	}
	if again, err := service.EditArticle(ctx, published.Entry.ID); err != nil || again.ID != working.ID {
		t.Fatalf("reopening must reuse the copy rather than make a second, got %v %v", again, err)
	}
}

// The audience is part of the request, so a key reused for a different audience
// is a mistake rather than a retry. Answering it with the first publish would
// silently put an article in front of people the caller did not name.
func TestRepublishingWithTheSameKeyButADifferentAudienceIsRefused(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, service := articleServiceFixture(t, time.UnixMilli(1_000).UTC())

	draft, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{
		Title: "只给自己看", BodyJSON: draftBodyJSON("正文"),
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.PublishArticleDraft(ctx, app.PublishArticleDraftInput{
		DraftID: draft.ID, Visibility: domain.VisibilityOnlyMe, IdempotencyKey: "same-key",
	}); err != nil {
		t.Fatalf("publish: %v", err)
	}
	_, err = service.PublishArticleDraft(ctx, app.PublishArticleDraftInput{
		DraftID: draft.ID, Visibility: domain.VisibilityEveryone, IdempotencyKey: "same-key",
	})
	if !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("a different audience under the same key must be named, got %v", err)
	}
}

// A body naming a picture nobody uploaded is the caller's mistake. The foreign
// key would catch it either way, as an INTERNAL storage failure advising a
// retry that can never work.
func TestADraftNamingAMissingPictureIsRefusedByName(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	_, service := articleServiceFixture(t, time.UnixMilli(1_000).UTC())

	absent := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	inline := []byte(`{"schema_version":1,"type":"doc","content":[{"type":"image","attrs":{"media_id":"` + absent + `"}}]}`)
	if _, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{
		Title: "引用不存在的图", BodyJSON: inline,
	}); !domain.HasCode(err, domain.CodeMediaNotFound) {
		t.Fatalf("an absent inline picture must be named, got %v", err)
	}

	coverID, err := domain.ParseID(absent)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{
		Title: "封面不存在", BodyJSON: draftBodyJSON("正文"), CoverMediaID: &coverID,
	}); !domain.HasCode(err, domain.CodeMediaNotFound) {
		t.Fatalf("an absent cover must be named, got %v", err)
	}
}

func draftIDOfNewest(t *testing.T, ctx context.Context, service *app.PostService) domain.ID {
	t.Helper()
	drafts, err := service.ArticleDrafts(ctx)
	if err != nil || len(drafts) == 0 {
		t.Fatalf("list drafts: %v", err)
	}
	return drafts[0].ID
}
