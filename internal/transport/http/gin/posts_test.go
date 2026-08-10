package ginhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

var postHTTPIDs = []domain.ID{
	"01ARZ3NDEKTSV4RRFFQ69G5FAV",
	"01ARZ3NDEKTSV4RRFFQ69G5FAW",
	"01ARZ3NDEKTSV4RRFFQ69G5FAX",
	"01ARZ3NDEKTSV4RRFFQ69G5FAY",
}

type postHTTPFixture struct {
	ownerTestFixture
	database *sqlitestore.Database
	posts    *app.PostService
	search   *app.SearchService
	site     *app.SiteService
	now      *time.Time
}

func newPostHTTPFixture(t *testing.T, jsonLimit int64) postHTTPFixture {
	t.Helper()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(runtimeState.Root, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	postRepository, err := sqlitestore.NewPostRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 10, 15, 0, 0, 0, time.UTC)
	nextID := 0
	posts, err := app.NewPostService(postRepository,
		app.WithPostClock(func() time.Time { return now }),
		app.WithPostIDGenerator(func(time.Time) (domain.ID, error) {
			id := postHTTPIDs[nextID]
			nextID++
			return id, nil
		}),
	)
	if err != nil {
		t.Fatal(err)
	}
	siteRepository, err := sqlitestore.NewSiteRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	site, err := app.NewSiteService(siteRepository, app.SiteOptions{AllowDevelopmentLocalHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	timezone := "Asia/Shanghai"
	if _, err := site.Update(context.Background(), app.SiteUpdate{Timezone: &timezone}); err != nil {
		t.Fatal(err)
	}
	searchRepository, err := sqlitestore.NewSearchRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	search, err := app.NewSearchService(searchRepository)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{
		Assets:       testAssets(),
		OwnerAuth:    manager,
		SiteSettings: site,
		Posts:        posts,
		Search:       search,
		RequestID:    func() string { return "req_posts" },
		Limits:       BodyLimits{JSON: jsonLimit},
		Readiness: func(ctx context.Context) error {
			if err := database.Ready(ctx); err != nil {
				return err
			}
			return site.Ready(ctx)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	return postHTTPFixture{
		ownerTestFixture: ownerTestFixture{manager: manager, token: credential.Token, engine: engine, now: &now},
		database:         database,
		posts:            posts,
		search:           search,
		site:             site,
		now:              &now,
	}
}

func TestPostHTTPCRUDIdempotencyAndPublicDTO(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	unauthorized := performRequest(fixture.engine, ownerRequest(http.MethodPost, "/api/v1/owner/posts", `{"content_md":"blocked"}`))
	if unauthorized.Code != http.StatusUnauthorized || decodeError(t, unauthorized.Body).Error.Code != "UNAUTHORIZED" {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}

	create := ownerRequest(http.MethodPost, "/api/v1/owner/posts", `{"content_md":"first **post**"}`)
	create.Header.Set("Authorization", "Bearer "+fixture.token)
	create.Header.Set("Idempotency-Key", "post-http-001")
	created := performRequest(fixture.engine, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	createdData := decodeDataMap(t, created)
	id := createdData["id"].(string)
	if createdData["content_md"] != "first **post**" || createdData["content_html"] == "" || createdData["kind"] != nil {
		t.Fatalf("created DTO = %#v", createdData)
	}
	duplicatePatch := ownerRequest(http.MethodPatch, "/api/v1/owner/posts/"+id, `{"content_md":"one","content_md":"two"}`)
	duplicatePatch.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, duplicatePatch); response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("duplicate PATCH = %d %s", response.Code, response.Body.String())
	}
	invalidPatch := ownerRequest(http.MethodPatch, "/api/v1/owner/posts/"+id, string([]byte{'{', '"', 'c', 'o', 'n', 't', 'e', 'n', 't', '_', 'm', 'd', '"', ':', '"', 0xff, '"', '}'}))
	invalidPatch.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, invalidPatch); response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("invalid UTF-8 PATCH = %d %s", response.Code, response.Body.String())
	}
	unchanged := performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts/"+id, ""))
	if decodeDataMap(t, unchanged)["content_md"] != "first **post**" {
		t.Fatalf("invalid PATCH changed post: %s", unchanged.Body.String())
	}

	update := ownerRequest(http.MethodPatch, "/api/v1/owner/posts/"+id, `{"content_md":"edited"}`)
	update.Header.Set("Authorization", "Bearer "+fixture.token)
	updated := performRequest(fixture.engine, update)
	if updated.Code != http.StatusOK || decodeDataMap(t, updated)["content_md"] != "edited" {
		t.Fatalf("update = %d %s", updated.Code, updated.Body.String())
	}

	replay := ownerRequest(http.MethodPost, "/api/v1/owner/posts", `{"content_md":"first **post**"}`)
	replay.Header.Set("Authorization", "Bearer "+fixture.token)
	replay.Header.Set("Idempotency-Key", "post-http-001")
	replayed := performRequest(fixture.engine, replay)
	if replayed.Code != http.StatusOK || decodeDataMap(t, replayed)["content_md"] != "first **post**" {
		t.Fatalf("replay after edit = %d %s", replayed.Code, replayed.Body.String())
	}

	conflict := ownerRequest(http.MethodPost, "/api/v1/owner/posts", `{"content_md":"different"}`)
	conflict.Header.Set("Authorization", "Bearer "+fixture.token)
	conflict.Header.Set("Idempotency-Key", "post-http-001")
	conflicted := performRequest(fixture.engine, conflict)
	if conflicted.Code != http.StatusConflict || decodeError(t, conflicted.Body).Error.Code != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("conflict = %d %s", conflicted.Code, conflicted.Body.String())
	}

	deletedRequest := ownerRequest(http.MethodDelete, "/api/v1/owner/posts/"+id, "")
	deletedRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	deleted := performRequest(fixture.engine, deletedRequest)
	if deleted.Code != http.StatusOK || decodeDataMap(t, deleted)["content_md"] != "edited" {
		t.Fatalf("delete = %d %s", deleted.Code, deleted.Body.String())
	}
	replayAfterDeleteRequest := ownerRequest(http.MethodPost, "/api/v1/owner/posts", `{"content_md":"first **post**"}`)
	replayAfterDeleteRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	replayAfterDeleteRequest.Header.Set("Idempotency-Key", "post-http-001")
	replayAfterDelete := performRequest(fixture.engine, replayAfterDeleteRequest)
	if replayAfterDelete.Code != http.StatusOK || decodeDataMap(t, replayAfterDelete)["content_md"] != "first **post**" {
		t.Fatalf("replay after delete = %d %s", replayAfterDelete.Code, replayAfterDelete.Body.String())
	}
	missing := performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts/"+id, ""))
	if missing.Code != http.StatusNotFound || decodeError(t, missing.Body).Error.Code != "POST_NOT_FOUND" {
		t.Fatalf("missing = %d %s", missing.Code, missing.Body.String())
	}
}

func TestPostHTTPDateCursorStrictBindingAndQueryValidation(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	*fixture.now = time.Date(2026, time.August, 9, 15, 59, 59, 0, time.UTC) // 2026-08-09 23:59:59 +08
	if _, err := fixture.posts.Create(context.Background(), app.CreatePostInput{ContentMD: "before"}); err != nil {
		t.Fatal(err)
	}
	*fixture.now = time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC) // 2026-08-10 00:00 +08
	if _, err := fixture.posts.Create(context.Background(), app.CreatePostInput{ContentMD: "inside one"}); err != nil {
		t.Fatal(err)
	}
	*fixture.now = time.Date(2026, time.August, 10, 3, 0, 0, 0, time.UTC)
	if _, err := fixture.posts.Create(context.Background(), app.CreatePostInput{ContentMD: "inside two"}); err != nil {
		t.Fatal(err)
	}

	first := performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts?from=2026-08-10&to=2026-08-11&limit=1", ""))
	if first.Code != http.StatusOK {
		t.Fatalf("first = %d %s", first.Code, first.Body.String())
	}
	var page struct {
		Items []map[string]any `json:"items"`
	}
	var envelope struct {
		Data json.RawMessage `json:"data"`
		Meta struct {
			NextCursor *string `json:"next_cursor"`
		} `json:"meta"`
	}
	if err := json.Unmarshal(first.Body.Bytes(), &envelope); err != nil || json.Unmarshal(envelope.Data, &page) != nil {
		t.Fatal("decode page")
	}
	if len(page.Items) != 1 || page.Items[0]["content_md"] != "inside two" || envelope.Meta.NextCursor == nil {
		t.Fatalf("page = %#v, meta = %#v", page, envelope.Meta)
	}
	continued := performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts?from=2026-08-10&to=2026-08-11&limit=1&cursor="+*envelope.Meta.NextCursor, ""))
	if continued.Code != http.StatusOK || decodeDataMap(t, continued)["kind"] != nil {
		t.Fatalf("continued = %d %s", continued.Code, continued.Body.String())
	}
	reused := performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts?cursor="+*envelope.Meta.NextCursor, ""))
	if reused.Code != http.StatusBadRequest || decodeError(t, reused.Body).Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("filter-mismatched cursor = %d %s", reused.Code, reused.Body.String())
	}

	for _, endpoint := range []string{
		"/api/v1/posts?cursor=invalid",
		"/api/v1/posts?limit=101",
		"/api/v1/posts?from=08-10-2026",
		"/api/v1/posts?from=2026-08-11&to=2026-08-10",
		"/api/v1/posts?limit=1&limit=2",
		"/api/v1/posts?offset=1",
		"/api/v1/posts?cursor=",
		"/api/v1/posts?from=",
		"/api/v1/posts?limit=0",
		"/api/v1/posts?limit=1;to=2",
	} {
		response := performRequest(fixture.engine, ownerRequest(http.MethodGet, endpoint, ""))
		if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("%s = %d %s", endpoint, response.Code, response.Body.String())
		}
	}
	malformedPercent := ownerRequest(http.MethodGet, "/api/v1/posts", "")
	malformedPercent.URL.RawQuery = "limit=%ZZ"
	if response := performRequest(fixture.engine, malformedPercent); response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("malformed query = %d %s", response.Code, response.Body.String())
	}
}

func TestPostHTTPStrictBodyCSRFBodyLimitAndCancellation(t *testing.T) {
	fixture := newPostHTTPFixture(t, 64)
	for _, body := range []string{`{}`, `{"content_md":null}`, `{"content_md":"ok","extra":true}`, `{"content_md":"first","content_md":"second"}`, `{"content_md":"ok"} {}`} {
		request := ownerRequest(http.MethodPost, "/api/v1/owner/posts", body)
		request.Header.Set("Authorization", "Bearer "+fixture.token)
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("strict body %q = %d %s", body, response.Code, response.Body.String())
		}
	}
	invalidUTF8 := ownerRequest(http.MethodPost, "/api/v1/owner/posts", string([]byte{'{', '"', 'c', 'o', 'n', 't', 'e', 'n', 't', '_', 'm', 'd', '"', ':', '"', 0xff, '"', '}'}))
	invalidUTF8.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, invalidUTF8); response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("invalid UTF-8 = %d %s", response.Code, response.Body.String())
	}
	wrongContentType := ownerRequest(http.MethodPost, "/api/v1/owner/posts", `{"content_md":"must not publish"}`)
	wrongContentType.Header.Set("Authorization", "Bearer "+fixture.token)
	wrongContentType.Header.Set("Content-Type", "text/plain")
	if response := performRequest(fixture.engine, wrongContentType); response.Code != http.StatusUnsupportedMediaType || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("wrong content type = %d %s", response.Code, response.Body.String())
	}
	duplicateHeader := ownerRequest(http.MethodPost, "/api/v1/owner/posts", `{"content_md":"duplicate header"}`)
	duplicateHeader.Header.Set("Authorization", "Bearer "+fixture.token)
	duplicateHeader.Header.Add("Idempotency-Key", "one")
	duplicateHeader.Header.Add("Idempotency-Key", "two")
	if response := performRequest(fixture.engine, duplicateHeader); response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("duplicate idempotency header = %d %s", response.Code, response.Body.String())
	}
	tooLarge := ownerRequest(http.MethodPost, "/api/v1/owner/posts", `{"content_md":"this request is intentionally longer than the configured body limit"}`)
	tooLarge.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, tooLarge); response.Code != http.StatusRequestEntityTooLarge || decodeError(t, response.Body).Error.Code != "REQUEST_BODY_TOO_LARGE" {
		t.Fatalf("body limit = %d %s", response.Code, response.Body.String())
	}

	cookie, session := exchangeOwnerSession(t, fixture.ownerTestFixture, fixture.token)
	withoutCSRF := ownerRequest(http.MethodPost, "/api/v1/owner/posts", `{"content_md":"blocked"}`)
	withoutCSRF.AddCookie(cookie)
	if response := performRequest(fixture.engine, withoutCSRF); response.Code != http.StatusForbidden || decodeError(t, response.Body).Error.Code != "CSRF_INVALID" {
		t.Fatalf("missing CSRF = %d %s", response.Code, response.Body.String())
	}
	withCSRF := ownerRequest(http.MethodPost, "/api/v1/owner/posts", `{"content_md":"session post"}`)
	withCSRF.AddCookie(cookie)
	withCSRF.Header.Set(CSRFHeaderName, session["csrf_token"].(string))
	if response := performRequest(fixture.engine, withCSRF); response.Code != http.StatusCreated {
		t.Fatalf("session create = %d %s", response.Code, response.Body.String())
	}
	page := performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts", ""))
	var listEnvelope struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(page.Body.Bytes(), &listEnvelope); err != nil || len(listEnvelope.Data.Items) != 1 || listEnvelope.Data.Items[0]["content_md"] != "session post" {
		t.Fatalf("invalid writes changed posts: err=%v body=%s", err, page.Body.String())
	}

	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := ownerRequest(http.MethodGet, "/api/v1/posts", "").WithContext(cancelledContext)
	if response := performRequest(fixture.engine, cancelled); response.Code != http.StatusRequestTimeout || decodeError(t, response.Body).Error.Code != "CANCELLED" {
		t.Fatalf("cancelled = %d %s", response.Code, response.Body.String())
	}
}

func TestPostRoutesRequireAuthAndSiteAuthority(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	if _, err := New(Config{Assets: testAssets(), Posts: fixture.posts, SiteSettings: fixture.site}); err == nil {
		t.Fatal("post routes without owner auth were accepted")
	}
	if _, err := New(Config{Assets: testAssets(), Posts: fixture.posts, OwnerAuth: fixture.manager}); err == nil {
		t.Fatal("post routes without site settings were accepted")
	}
}
