package sqlite

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

const storedMapJSON = `{"places":[{"name":"Ginza","display_name":"Tokyo","latitude":35.6712345,"longitude":139.7654321,"coordinate_system":"wgs84"},{"name":"静安寺","display_name":"上海","latitude":31.2231861,"longitude":121.4450667,"coordinate_system":"gcj02"}],"connect_points":true}`

func TestMapOnlyPostLifecyclePrivacyRetryAndSearch(t *testing.T) {
	ctx := testAccountContext()
	db, runtime := openTestDatabase(t, Options{})
	service := newTestPostService(t, db, func() time.Time { return time.UnixMilli(1700000000000) }, sequenceIDs(entryOne, entryTwo))
	card, err := domain.ParseMapCard([]byte(storedMapJSON))
	if err != nil {
		t.Fatal(err)
	}
	created, err := service.Create(ctx, app.CreatePostInput{MapCard: card, Visibility: domain.VisibilityOnlyMe, IdempotencyKey: "map-only"})
	if err != nil {
		t.Fatal(err)
	}
	id := created.Post.Entry.ID
	replay, err := service.Create(ctx, app.CreatePostInput{MapCard: card, Visibility: domain.VisibilityOnlyMe, IdempotencyKey: "map-only"})
	if err != nil || !replay.Replayed || !reflect.DeepEqual(replay.Post.Entry, created.Post.Entry) {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	changed := domain.CloneMapCard(card)
	changed.ConnectPoints = false
	if _, err := service.Create(ctx, app.CreatePostInput{MapCard: changed, Visibility: domain.VisibilityOnlyMe, IdempotencyKey: "map-only"}); !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("retry conflict=%v", err)
	}
	// The request and returned snapshot cannot mutate the durable map.
	card.Places[0].Name = "caller changed"
	got, err := service.Get(ctx, id, app.AccountViewer(testAccountID))
	if err != nil || got.Entry.MapCard.Places[0].Name != "Ginza" {
		t.Fatalf("get=%+v %v", got, err)
	}
	if _, err := service.Get(ctx, id, app.PublicViewer()); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("private read=%v", err)
	}
	if _, err := service.SetReposted(ctx, id, true); err != nil {
		t.Fatal(err)
	}
	page, err := service.List(ctx, app.ListPostsInput{Viewer: app.AccountViewer(testAccountID)})
	if err != nil || len(page.Items) != 2 || page.Items[0].Entry.MapCard == nil {
		t.Fatalf("list=%+v %v", page, err)
	}
	public, err := service.List(ctx, app.ListPostsInput{})
	if err != nil || len(public.Items) != 0 {
		t.Fatalf("private list=%+v %v", public, err)
	}
	repo, err := NewSearchRepository(db, runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	search, err := app.NewSearchService(repo)
	if err != nil {
		t.Fatal(err)
	}
	hits, err := search.Search(ctx, app.SearchInput{Query: "Ginza", Viewer: app.AccountViewer(testAccountID)})
	if err != nil || len(hits.Items) != 1 || hits.Items[0].Post.Entry.MapCard == nil {
		t.Fatalf("search=%+v %v", hits, err)
	}
	hidden, err := search.Search(ctx, app.SearchInput{Query: "Ginza"})
	if err != nil || len(hidden.Items) != 0 {
		t.Fatalf("private search=%+v %v", hidden, err)
	}
	diagnostic, err := search.DiagnoseDerived(ctx, 100)
	if err != nil || diagnostic.Drifted != 0 {
		t.Fatalf("derived=%+v %v", diagnostic, err)
	}
	other := app.WithContentActor(context.Background(), domain.User{ID: "fedcba9876543210fedcba9876543210", Role: domain.UserRoleAdmin, Status: domain.AccountActive})
	if _, err := service.Update(other, app.UpdatePostInput{ID: id, PreserveContent: true, MapCard: &changed}); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("non-author map update=%v", err)
	}
	if _, err := service.Get(other, id, app.AccountViewer("fedcba9876543210fedcba9876543210")); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("non-author map read=%v", err)
	}
	var clearedMap *domain.MapCard
	if _, err := service.Update(ctx, app.UpdatePostInput{ID: id, PreserveContent: true, MapCard: &clearedMap}); !domain.HasCode(err, domain.CodeContentEmpty) {
		t.Fatalf("empty clear=%v", err)
	}
	updated, err := service.Update(ctx, app.UpdatePostInput{ID: id, ContentMD: "words"})
	if err != nil || updated.Entry.MapCard == nil {
		t.Fatalf("preserve=%+v %v", updated, err)
	}
	quote, err := service.Create(ctx, app.CreatePostInput{ContentMD: "quote", QuotedPostID: &id})
	if err != nil {
		t.Fatal(err)
	}
	quoteRead, err := service.Get(ctx, quote.Post.Entry.ID, app.AccountViewer(testAccountID))
	if err != nil || quoteRead.Quoted == nil || quoteRead.Quoted.Entry.MapCard == nil {
		t.Fatalf("quote=%+v %v", quoteRead, err)
	}
	publicQuote, err := service.Get(ctx, quote.Post.Entry.ID, app.PublicViewer())
	if err != nil || publicQuote.Quoted == nil || !publicQuote.Quoted.Unavailable || publicQuote.Quoted.Entry.MapCard != nil {
		t.Fatalf("hidden quote=%+v %v", publicQuote, err)
	}
	updated, err = service.Update(ctx, app.UpdatePostInput{ID: id, PreserveContent: true, MapCard: &clearedMap})
	if err != nil || updated.Entry.MapCard != nil {
		t.Fatalf("clear=%+v %v", updated, err)
	}
	// Replacing restores source precision and records an edit.
	updated, err = service.Update(ctx, app.UpdatePostInput{ID: id, PreserveContent: true, MapCard: &changed})
	if err != nil || updated.Entry.EditedAt == nil || updated.Entry.MapCard.Places[1].Point.LongitudeE7 != 1214450667 {
		t.Fatalf("replace=%+v %v", updated, err)
	}
	if _, err := service.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.ReadTx(context.Background(), func(q Queries) error {
		return q.QueryRowContext(ctx, `SELECT count(*) FROM entry_map_card WHERE entry_id = ?`, id.String()).Scan(&count)
	}); err != nil || count != 0 {
		t.Fatalf("cascade=%d %v", count, err)
	}
}
func TestArticleMapDraftDoesNotChangePublishedSnapshot(t *testing.T) {
	ctx := testAccountContext()
	_, service := articleServiceFixture(t, time.UnixMilli(1700000000000))
	body := []byte(`{"schema_version":3,"type":"doc","content":[{"type":"mapCard","attrs":{"id":"trip-map","map_card":` + storedMapJSON + `}}]}`)
	draft, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{Title: "trip", BodyJSON: body})
	if err != nil {
		t.Fatal(err)
	}
	list, err := service.List(ctx, app.ListPostsInput{})
	if err != nil || len(list.Items) != 0 {
		t.Fatalf("draft leaked=%+v %v", list, err)
	}
	published, err := service.PublishArticleDraft(ctx, app.PublishArticleDraftInput{DraftID: draft.ID})
	if err != nil {
		t.Fatal(err)
	}
	working, err := service.EditArticle(ctx, published.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	replacement := []byte(strings.ReplaceAll(string(body), "Ginza", "Ueno"))
	if _, err := service.SaveArticleDraft(ctx, app.SaveArticleDraftInput{ID: &working.ID, Title: "trip", BodyJSON: replacement, ExpectedUpdatedAt: &working.UpdatedAt}); err != nil {
		t.Fatal(err)
	}
	live, err := service.Get(ctx, published.Entry.ID, app.PublicViewer())
	if err != nil || live.Article.Body.Content[0].MapCard.Places[0].Name != "Ginza" {
		t.Fatalf("live changed=%+v %v", live, err)
	}
	if _, err := service.PublishArticleDraft(ctx, app.PublishArticleDraftInput{DraftID: working.ID}); err != nil {
		t.Fatal(err)
	}
	live, err = service.Get(ctx, published.Entry.ID, app.PublicViewer())
	if err != nil || live.Article.Body.Content[0].MapCard.Places[0].Name != "Ueno" {
		t.Fatalf("publish lost map=%+v %v", live, err)
	}
}
