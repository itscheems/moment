package ginhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestPersonalArchiveHTTPKeepsEveryReadWithinAuthenticatedAccount(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	cookie, _, member := signInMember(t, fixture)
	ctx := context.Background()
	member, err := fixture.accountService.SetStatus(ctx, member.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := sqlitestore.NewPostRepository(fixture.database, fixture.runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	posts, err := app.NewPostService(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = posts.Create(app.WithContentActor(ctx, member), app.CreatePostInput{ContentMD: "member archive content"}); err != nil {
		t.Fatal(err)
	}
	searchRepo, err := sqlitestore.NewSearchRepository(fixture.database, fixture.runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	search, err := app.NewSearchService(searchRepo)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{Assets: testAssets(), SessionSigner: fixture.manager, Accounts: fixture.accountService, Posts: posts, Search: search, SiteSettings: fixture.site})
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{"/api/v1/archive/months", "/api/v1/archive/month?month=2026-08", "/api/v1/archive/posts"} {
		if got := performRequest(engine, httptest.NewRequest(http.MethodGet, path, nil)); got.Code != 401 {
			t.Fatalf("guest %s=%d", path, got.Code)
		}
	}
	req := httptest.NewRequest(http.MethodGet, "/api/v1/archive/posts?limit=20", nil)
	req.AddCookie(cookie)
	got := performRequest(engine, req)
	if got.Code != 200 {
		t.Fatalf("own archive=%d %s", got.Code, got.Body.String())
	}
	ownBody := got.Body.String()
	items := decodeDataMap(t, got)["items"].([]any)
	if len(items) != 1 || !strings.Contains(ownBody, "member archive content") {
		t.Fatalf("member archive leaked: %s", got.Body.String())
	}
	owner := performRequest(engine, headerRequest(http.MethodGet, "/api/v1/archive/posts?limit=20", fixture.token, nil))
	if owner.Code != 200 || len(decodeDataMap(t, owner)["items"].([]any)) != 1 {
		t.Fatalf("admin archive leaked: %s", owner.Body.String())
	}
	monthsReq := httptest.NewRequest(http.MethodGet, "/api/v1/archive/months", nil)
	monthsReq.AddCookie(cookie)
	months := performRequest(engine, monthsReq)
	if months.Code != 200 {
		t.Fatal(months.Body.String())
	}
	total := float64(0)
	for _, item := range decodeDataMap(t, months)["items"].([]any) {
		total += item.(map[string]any)["count"].(float64)
	}
	if total != 1 {
		t.Fatalf("member calendar=%s", months.Body.String())
	}
	home := performRequest(engine, httptest.NewRequest(http.MethodGet, "/api/v1/posts?limit=20", nil))
	if home.Code != 200 || len(decodeDataMap(t, home)["items"].([]any)) != 2 {
		t.Fatalf("public feed narrowed: %s", home.Body.String())
	}
}
