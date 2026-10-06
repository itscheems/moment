package ginhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

var postHTTPIDs = []domain.ID{
	"01ARZ3NDEKTSV4RRFFQ69G5FAV",
	"01ARZ3NDEKTSV4RRFFQ69G5FAW",
	"01ARZ3NDEKTSV4RRFFQ69G5FAX",
	"01ARZ3NDEKTSV4RRFFQ69G5FAY",
}

const (
	postHTTPMediaOne = "01ARZ3NDEKTSV4RRFFQ69G5FAZ"
	postHTTPMediaTwo = "01ARZ3NDEKTSV4RRFFQ69G5FB0"
)

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
	mediaStore, err := media.NewStore(runtimeState, database, media.Options{})
	if err != nil {
		t.Fatal(err)
	}
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
		Accounts:     testAccounts(t, database, runtimeState),
		SiteSettings: site,
		Posts:        posts,
		Media:        mediaStore,
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
	// kind is on the wire now that a post can be a moment or an article.
	if createdData["content_md"] != "first **post**" || createdData["content_html"] == "" || createdData["kind"] != "moment" {
		t.Fatalf("created DTO = %#v", createdData)
	}
	if media, ok := createdData["media"].([]any); !ok || media == nil || len(media) != 0 {
		t.Fatalf("text-only post media = %#v", createdData["media"])
	}
	quote := ownerRequest(http.MethodPost, "/api/v1/owner/posts", `{"content_md":"my take","quoted_post_id":"`+id+`"}`)
	quote.Header.Set("Authorization", "Bearer "+fixture.token)
	quote.Header.Set("Idempotency-Key", "post-http-quote-001")
	quoted := performRequest(fixture.engine, quote)
	if quoted.Code != http.StatusCreated {
		t.Fatalf("quote create = %d %s", quoted.Code, quoted.Body.String())
	}
	quotedData := decodeDataMap(t, quoted)
	quotedID := quotedData["id"].(string)
	quotedPost, ok := quotedData["quoted_post"].(map[string]any)
	if !ok || quotedData["content_md"] != "my take" || quotedPost["id"] != id || quotedPost["content_md"] != "first **post**" {
		t.Fatalf("quote DTO = %#v", quotedData)
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

	repostRequest := ownerRequest(http.MethodPut, "/api/v1/owner/posts/"+id+"/repost", "")
	repostRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	reposted := performRequest(fixture.engine, repostRequest)
	repostedData := decodeDataMap(t, reposted)
	if reposted.Code != http.StatusOK || repostedData["reposted_at"] == nil || repostedData["timeline_item_kind"] != "original" {
		t.Fatalf("repost = %d %s", reposted.Code, reposted.Body.String())
	}
	timeline := decodeDataMap(t, performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts?limit=10", "")))["items"].([]any)
	linkedKinds := map[string]bool{}
	for _, raw := range timeline {
		item := raw.(map[string]any)
		if item["id"] == id {
			linkedKinds[item["timeline_item_kind"].(string)] = true
		}
	}
	if len(linkedKinds) != 2 || !linkedKinds["original"] || !linkedKinds["repost"] {
		t.Fatalf("repost timeline = %#v", timeline)
	}
	unrepostRequest := ownerRequest(http.MethodDelete, "/api/v1/owner/posts/"+id+"/repost", "")
	unrepostRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	unreposted := performRequest(fixture.engine, unrepostRequest)
	if unreposted.Code != http.StatusOK || decodeDataMap(t, unreposted)["reposted_at"] != nil {
		t.Fatalf("unrepost = %d %s", unreposted.Code, unreposted.Body.String())
	}
	timeline = decodeDataMap(t, performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts?limit=10", "")))["items"].([]any)
	linkedCount := 0
	for _, raw := range timeline {
		if raw.(map[string]any)["id"] == id {
			linkedCount++
		}
	}
	if linkedCount != 1 {
		t.Fatalf("unrepost timeline = %#v", timeline)
	}

	pinRequest := ownerRequest(http.MethodPut, "/api/v1/owner/posts/"+id+"/pin", "")
	pinRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	pinned := performRequest(fixture.engine, pinRequest)
	if pinned.Code != http.StatusOK || decodeDataMap(t, pinned)["pinned_post_id"] != id {
		t.Fatalf("pin = %d %s", pinned.Code, pinned.Body.String())
	}
	if settings, err := fixture.site.Get(context.Background()); err != nil || settings.PinnedPostID == nil || *settings.PinnedPostID != id {
		t.Fatalf("stored pin = %#v, err=%v", settings.PinnedPostID, err)
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
	if settings, err := fixture.site.Get(context.Background()); err != nil || settings.PinnedPostID != nil {
		t.Fatalf("delete did not clear pin = %#v, err=%v", settings.PinnedPostID, err)
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
	quoteAfterDelete := performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts/"+quotedID, ""))
	quoteAfterDeleteData := decodeDataMap(t, quoteAfterDelete)
	quoteTombstone, _ := quoteAfterDeleteData["quoted_post"].(map[string]any)
	if quoteAfterDelete.Code != http.StatusOK || quoteAfterDeleteData["content_md"] != "my take" || quoteTombstone["unavailable"] != true || quoteTombstone["id"] != nil {
		t.Fatalf("quote after target delete = %d %#v", quoteAfterDelete.Code, quoteAfterDeleteData)
	}
}

func TestPostHTTPMediaCanonicalPatchAndImmutableReplay(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	insertPostHTTPMedia(t, fixture.database, postHTTPMediaOne, "a")
	insertPostHTTPMedia(t, fixture.database, postHTTPMediaTwo, "b")
	body := `{"content_md":"media post","media":[` +
		`{"media_id":"` + postHTTPMediaOne + `","position":0,"alt_text":null},` +
		`{"media_id":"` + postHTTPMediaOne + `","position":1,"alt_text":""},` +
		`{"media_id":"` + postHTTPMediaTwo + `","position":2,"alt_text":"street at night"}]}`
	request := ownerRequest(http.MethodPost, "/api/v1/owner/posts", body)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	request.Header.Set("Idempotency-Key", "http-media-replay")
	createdResponse := performRequest(fixture.engine, request)
	if createdResponse.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", createdResponse.Code, createdResponse.Body.String())
	}
	created := decodeDataMap(t, createdResponse)
	id := created["id"].(string)
	assertCanonicalHTTPMedia(t, created["media"], []any{nil, "", "street at night"}, []string{postHTTPMediaOne, postHTTPMediaOne, postHTTPMediaTwo})

	get := decodeDataMap(t, performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts/"+id, "")))
	assertCanonicalHTTPMedia(t, get["media"], []any{nil, "", "street at night"}, []string{postHTTPMediaOne, postHTTPMediaOne, postHTTPMediaTwo})
	listResponse := performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts", ""))
	var list struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(listResponse.Body.Bytes(), &list); err != nil || len(list.Data.Items) != 1 {
		t.Fatalf("list = %s, %v", listResponse.Body.String(), err)
	}
	assertCanonicalHTTPMedia(t, list.Data.Items[0]["media"], []any{nil, "", "street at night"}, []string{postHTTPMediaOne, postHTTPMediaOne, postHTTPMediaTwo})

	contentOnly := ownerRequest(http.MethodPatch, "/api/v1/owner/posts/"+id, `{"content_md":"edited content"}`)
	contentOnly.Header.Set("Authorization", "Bearer "+fixture.token)
	contentUpdated := performRequest(fixture.engine, contentOnly)
	if contentUpdated.Code != http.StatusOK {
		t.Fatalf("content patch = %d %s", contentUpdated.Code, contentUpdated.Body.String())
	}
	assertCanonicalHTTPMedia(t, decodeDataMap(t, contentUpdated)["media"], []any{nil, "", "street at night"}, []string{postHTTPMediaOne, postHTTPMediaOne, postHTTPMediaTwo})
	nullMedia := ownerRequest(http.MethodPatch, "/api/v1/owner/posts/"+id, `{"media":null}`)
	nullMedia.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, nullMedia); response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("null media patch = %d %s", response.Code, response.Body.String())
	}
	preservedAfterNull := decodeDataMap(t, performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts/"+id, "")))
	assertCanonicalHTTPMedia(t, preservedAfterNull["media"], []any{nil, "", "street at night"}, []string{postHTTPMediaOne, postHTTPMediaOne, postHTTPMediaTwo})

	mediaOnly := ownerRequest(http.MethodPatch, "/api/v1/owner/posts/"+id,
		`{"media":[{"media_id":"`+postHTTPMediaTwo+`","position":0,"alt_text":"replacement"}]}`)
	mediaOnly.Header.Set("Authorization", "Bearer "+fixture.token)
	replaced := performRequest(fixture.engine, mediaOnly)
	replacedData := decodeDataMap(t, replaced)
	if replaced.Code != http.StatusOK || replacedData["content_md"] != "edited content" {
		t.Fatalf("media patch = %d %s", replaced.Code, replaced.Body.String())
	}
	assertCanonicalHTTPMedia(t, replacedData["media"], []any{"replacement"}, []string{postHTTPMediaTwo})

	clearRequest := ownerRequest(http.MethodPatch, "/api/v1/owner/posts/"+id, `{"media":[]}`)
	clearRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	cleared := performRequest(fixture.engine, clearRequest)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear = %d %s", cleared.Code, cleared.Body.String())
	}
	if media := decodeDataMap(t, cleared)["media"].([]any); media == nil || len(media) != 0 {
		t.Fatalf("cleared media = %#v", media)
	}

	replay := ownerRequest(http.MethodPost, "/api/v1/owner/posts", body)
	replay.Header.Set("Authorization", "Bearer "+fixture.token)
	replay.Header.Set("Idempotency-Key", "http-media-replay")
	replayed := performRequest(fixture.engine, replay)
	if replayed.Code != http.StatusOK {
		t.Fatalf("replay = %d %s", replayed.Code, replayed.Body.String())
	}
	assertCanonicalHTTPMedia(t, decodeDataMap(t, replayed)["media"], []any{nil, "", "street at night"}, []string{postHTTPMediaOne, postHTTPMediaOne, postHTTPMediaTwo})

	deleteRequest := ownerRequest(http.MethodDelete, "/api/v1/owner/posts/"+id, "")
	deleteRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, deleteRequest); response.Code != http.StatusOK {
		t.Fatalf("delete = %d %s", response.Code, response.Body.String())
	}
	replayAfterDelete := ownerRequest(http.MethodPost, "/api/v1/owner/posts", body)
	replayAfterDelete.Header.Set("Authorization", "Bearer "+fixture.token)
	replayAfterDelete.Header.Set("Idempotency-Key", "http-media-replay")
	response := performRequest(fixture.engine, replayAfterDelete)
	if response.Code != http.StatusOK {
		t.Fatalf("replay after delete = %d %s", response.Code, response.Body.String())
	}
	assertCanonicalHTTPMedia(t, decodeDataMap(t, response)["media"], []any{nil, "", "street at night"}, []string{postHTTPMediaOne, postHTTPMediaOne, postHTTPMediaTwo})
}

func TestPostHTTPMediaStrictValidationAndRollback(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	insertPostHTTPMedia(t, fixture.database, postHTTPMediaOne, "c")
	tenth := make([]string, 0, 10)
	for index := range 10 {
		tenth = append(tenth, `{"media_id":"`+postHTTPMediaOne+`","position":`+string(rune('0'+index))+`,"alt_text":null}`)
	}
	invalidBodies := []string{
		`{"content_md":"x","media":null}`,
		`{"content_md":"x","media":{}}`,
		`{"content_md":"x","media":[{"media_id":"` + postHTTPMediaOne + `","position":0}]}`,
		`{"content_md":"x","media":[{"media_id":"` + postHTTPMediaOne + `","position":0,"alt_text":null,"extra":true}]}`,
		`{"content_md":"x","media":[{"media_id":"` + postHTTPMediaOne + `","position":0,"alt_text":null,"alt_text":"x"}]}`,
		`{"content_md":"x","media":[{"media_id":"` + postHTTPMediaOne + `","position":1,"alt_text":null}]}`,
		`{"content_md":"x","media":[{"media_id":"` + postHTTPMediaOne + `","position":9,"alt_text":null}]}`,
		`{"content_md":"x","media":[{"media_id":"` + postHTTPMediaOne + `","position":0,"alt_text":"   "}]}`,
		`{"content_md":"x","media":[{"media_id":"` + postHTTPMediaOne + `","position":0,"alt_text":"` + strings.Repeat("界", 1001) + `"}]}`,
		`{"content_md":"x","media":[` + strings.Join(tenth, ",") + `]}`,
	}
	for _, body := range invalidBodies {
		request := ownerRequest(http.MethodPost, "/api/v1/owner/posts", body)
		request.Header.Set("Authorization", "Bearer "+fixture.token)
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("invalid body = %d %s for %s", response.Code, response.Body.String(), body)
		}
	}
	assertHTTPTableCount(t, fixture.database, "entries", 0)
	assertHTTPTableCount(t, fixture.database, "entry_media", 0)

	missing := ownerRequest(http.MethodPost, "/api/v1/owner/posts",
		`{"content_md":"missing","media":[{"media_id":"`+postHTTPMediaTwo+`","position":0,"alt_text":null}]}`)
	missing.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, missing); response.Code != http.StatusNotFound || decodeError(t, response.Body).Error.Code != "MEDIA_NOT_FOUND" {
		t.Fatalf("missing = %d %s", response.Code, response.Body.String())
	}

	if err := fixture.database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		_, err := queries.ExecContext(context.Background(), `
CREATE TRIGGER reject_http_post_media
BEFORE INSERT ON entry_media
BEGIN
    SELECT RAISE(ABORT, 'injected HTTP usage failure');
END`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	rollback := ownerRequest(http.MethodPost, "/api/v1/owner/posts",
		`{"content_md":"rollback","media":[{"media_id":"`+postHTTPMediaOne+`","position":0,"alt_text":null}]}`)
	rollback.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, rollback); response.Code != http.StatusInternalServerError || decodeError(t, response.Body).Error.Code != "INTERNAL" {
		t.Fatalf("rollback = %d %s", response.Code, response.Body.String())
	}
	assertHTTPTableCount(t, fixture.database, "entries", 0)
	assertHTTPTableCount(t, fixture.database, "entry_media", 0)
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

	// An anchored day reads back from its end, and its prev_cursor walks
	// forward from it one page at a time, nearest rows first, until the
	// direction is proved empty.
	type cursors struct {
		NextCursor *string `json:"next_cursor"`
		PrevCursor *string `json:"prev_cursor"`
	}
	readPage := func(path string) ([]map[string]any, cursors) {
		t.Helper()
		response := performRequest(fixture.engine, ownerRequest(http.MethodGet, path, ""))
		if response.Code != http.StatusOK {
			t.Fatalf("%s = %d %s", path, response.Code, response.Body.String())
		}
		var body struct {
			Data struct {
				Items []map[string]any `json:"items"`
			} `json:"data"`
			Meta cursors `json:"meta"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		return body.Data.Items, body.Meta
	}
	anchoredItems, anchoredMeta := readPage("/api/v1/posts?anchor=2026-08-09&limit=5")
	if len(anchoredItems) != 1 || anchoredItems[0]["content_md"] != "before" || anchoredMeta.NextCursor != nil || anchoredMeta.PrevCursor == nil {
		t.Fatalf("anchored page = %#v, meta = %#v", anchoredItems, anchoredMeta)
	}
	newerItems, newerMeta := readPage("/api/v1/posts?limit=1&cursor=" + *anchoredMeta.PrevCursor)
	if len(newerItems) != 1 || newerItems[0]["content_md"] != "inside one" || newerMeta.PrevCursor == nil || newerMeta.NextCursor == nil {
		t.Fatalf("newer page = %#v, meta = %#v", newerItems, newerMeta)
	}
	newestItems, newestMeta := readPage("/api/v1/posts?limit=1&cursor=" + *newerMeta.PrevCursor)
	if len(newestItems) != 1 || newestItems[0]["content_md"] != "inside two" || newestMeta.PrevCursor != nil {
		t.Fatalf("newest page = %#v, meta = %#v", newestItems, newestMeta)
	}
	for _, endpoint := range []string{
		"/api/v1/posts?anchor=2026-08-09&from=2026-08-01",
		"/api/v1/posts?anchor=2026-08-09&to=2026-09-01",
		"/api/v1/posts?anchor=2026-08-09&cursor=" + *anchoredMeta.PrevCursor,
		"/api/v1/posts?anchor=",
		"/api/v1/posts?anchor=08-09-2026",
	} {
		response := performRequest(fixture.engine, ownerRequest(http.MethodGet, endpoint, ""))
		if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("%s = %d %s", endpoint, response.Code, response.Body.String())
		}
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
	if _, err := New(Config{Assets: testAssets(), Posts: fixture.posts, OwnerAuth: fixture.manager, Accounts: fixture.accounts}); err == nil {
		t.Fatal("post routes without site settings were accepted")
	}
}

func insertPostHTTPMedia(t *testing.T, database *sqlitestore.Database, id, fill string) {
	t.Helper()
	sha := strings.Repeat(fill, 64)
	path := "media/original/" + fill + fill + "/" + sha + ".jpg"
	if err := database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		_, err := queries.ExecContext(context.Background(), `
INSERT INTO media (
    id, sha256, storage_path, original_name, mime_type, byte_size,
    width, height, created_at_ms, unreferenced_since_ms
) VALUES (?, ?, ?, 'fixture.jpg', 'image/jpeg', 10, 8, 8, 1, 1)`, id, sha, path)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func assertCanonicalHTTPMedia(t *testing.T, raw any, expectedAlts []any, expectedIDs []string) {
	t.Helper()
	items, ok := raw.([]any)
	if !ok || items == nil || len(items) != len(expectedIDs) || len(expectedAlts) != len(expectedIDs) {
		t.Fatalf("media = %#v", raw)
	}
	for index, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		if !ok || len(item) != 6 || item["media_id"] != expectedIDs[index] || item["position"] != float64(index) || item["mime_type"] != "image/jpeg" || !reflect.DeepEqual(item["alt_text"], expectedAlts[index]) || item["width"] != float64(8) || item["height"] != float64(8) {
			t.Fatalf("media[%d] = %#v", index, rawItem)
		}
		for _, field := range []string{"media_id", "position", "alt_text", "mime_type", "width", "height"} {
			if _, present := item[field]; !present {
				t.Fatalf("media[%d] omitted %s: %#v", index, field, item)
			}
		}
	}
}

func assertHTTPTableCount(t *testing.T, database *sqlitestore.Database, table string, expected int) {
	t.Helper()
	var count int
	if err := database.ReadTx(context.Background(), func(queries sqlitestore.Queries) error {
		return queries.QueryRowContext(context.Background(), `SELECT count(*) FROM `+table).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != expected {
		t.Fatalf("%s count = %d, want %d", table, count, expected)
	}
}

func TestPostHTTPRefreshCursorKeepsAnEmptyTimelineRefreshable(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	head := performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts?limit=20&kind=article", ""))
	var envelope struct {
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(head.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	cursor, ok := envelope.Meta["refresh_cursor"].(string)
	if head.Code != http.StatusOK || !ok || cursor == "" {
		t.Fatalf("head: %s", head.Body.String())
	}
	newer := performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/posts?limit=20&kind=article&cursor="+cursor, ""))
	if err := json.Unmarshal(newer.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if newer.Code != http.StatusOK || envelope.Meta["refresh_cursor"] != cursor || envelope.Meta["prev_cursor"] != nil {
		t.Fatalf("refresh: %s", newer.Body.String())
	}
}
