package ginhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

type commentHTTPFixture struct {
	ownerTestFixture
	comments *app.CommentService
	site     *app.SiteService
	postID   domain.ID
	clock    *time.Time
}

type recordingCommentLimiter struct{ identities chan string }

func (limiter recordingCommentLimiter) Allow(identity string) (bool, time.Duration) {
	limiter.identities <- identity
	return true, 0
}

func newCommentHTTPFixture(t *testing.T, limit int, log LogFunc) commentHTTPFixture {
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
	postRepository, _ := sqlitestore.NewPostRepository(database, runtimeState.Locks)
	postID := domain.ID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	posts, err := app.NewPostService(postRepository, app.WithPostIDGenerator(func(time.Time) (domain.ID, error) { return postID, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := posts.Create(context.Background(), app.CreatePostInput{ContentMD: "comment host"}); err != nil {
		t.Fatal(err)
	}
	siteRepository, _ := sqlitestore.NewSiteRepository(database, runtimeState.Locks)
	site, err := app.NewSiteService(siteRepository, app.SiteOptions{AllowDevelopmentLocalHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	commentRepository, _ := sqlitestore.NewCommentRepository(database, runtimeState.Locks)
	now := time.Date(2026, time.August, 10, 10, 0, 0, 0, time.UTC)
	comments, err := app.NewCommentService(commentRepository, app.WithCommentClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	limiter, err := NewCommentRateLimiter(limit, time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{Assets: testAssets(), OwnerAuth: manager, SiteSettings: site, Posts: posts, Comments: comments, CommentLimiter: limiter, Log: log, RequestID: func() string { return "req_comments" }})
	if err != nil {
		t.Fatal(err)
	}
	return commentHTTPFixture{ownerTestFixture: ownerTestFixture{manager: manager, token: credential.Token, engine: engine, now: &now}, comments: comments, site: site, postID: postID, clock: &now}
}

func commentRequest(method, path, body string) *http.Request {
	request := ownerRequest(method, path, body)
	request.RemoteAddr = "192.0.2.25:4000"
	return request
}

func commentData(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if response.Code < 200 || response.Code >= 300 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	return decodeDataMap(t, response)
}

func TestCommentHTTPGuestOwnerThreadAndModerationContract(t *testing.T) {
	fixture := newCommentHTTPFixture(t, 100, nil)
	publicPath := "/api/v1/posts/" + fixture.postID.String() + "/comments"
	guest := commentRequest(http.MethodPost, publicPath, `{"author_name":"  访客  ","content_md":"first **comment**","website":""}`)
	guest.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "invalid-cookie-must-not-affect-guest"})
	guestResponse := performRequest(fixture.engine, guest)
	guestData := commentData(t, guestResponse)
	if guestResponse.Code != http.StatusCreated || guestData["author_kind"] != "guest" || guestData["author_name"] != "访客" || !strings.Contains(guestData["content_html"].(string), "<strong>comment</strong>") || guestData["depth"] != float64(0) {
		t.Fatalf("guest = %s", guestResponse.Body.String())
	}
	rootID := guestData["id"].(string)

	owner := commentRequest(http.MethodPost, "/api/v1/owner/posts/"+fixture.postID.String()+"/comments", `{"content_md":"owner reply","parent_id":"`+rootID+`"}`)
	owner.Header.Set("Authorization", "Bearer "+fixture.token)
	ownerResponse := performRequest(fixture.engine, owner)
	ownerData := commentData(t, ownerResponse)
	if ownerData["author_kind"] != "owner" || ownerData["author_name"] != "Owner" || ownerData["parent_id"] != rootID || ownerData["depth"] != float64(1) {
		t.Fatalf("owner = %s", ownerResponse.Body.String())
	}

	list := performRequest(fixture.engine, commentRequest(http.MethodGet, publicPath, ""))
	var envelope struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(list.Body.Bytes(), &envelope); err != nil || len(envelope.Data.Items) != 2 || envelope.Data.Items[0]["id"] != rootID || envelope.Data.Items[1]["id"] != ownerData["id"] {
		t.Fatalf("thread err=%v body=%s", err, list.Body.String())
	}

	unauthorized := performRequest(fixture.engine, commentRequest(http.MethodDelete, "/api/v1/owner/comments/"+rootID, ""))
	if unauthorized.Code != http.StatusUnauthorized || decodeError(t, unauthorized.Body).Error.Code != "UNAUTHORIZED" {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	cookie, session := exchangeOwnerSession(t, fixture.ownerTestFixture, fixture.token)
	missingCSRF := commentRequest(http.MethodDelete, "/api/v1/owner/comments/"+rootID, "")
	missingCSRF.AddCookie(cookie)
	if response := performRequest(fixture.engine, missingCSRF); response.Code != http.StatusForbidden || decodeError(t, response.Body).Error.Code != "CSRF_INVALID" {
		t.Fatalf("csrf = %d %s", response.Code, response.Body.String())
	}

	dryRun := commentRequest(http.MethodDelete, "/api/v1/owner/comments/"+rootID+"?dry_run=true", "")
	dryRun.AddCookie(cookie)
	dryRun.Header.Set(CSRFHeaderName, session["csrf_token"].(string))
	dryResponse := performRequest(fixture.engine, dryRun)
	var dryEnvelope struct {
		Meta map[string]any `json:"meta"`
	}
	_ = json.Unmarshal(dryResponse.Body.Bytes(), &dryEnvelope)
	if dryResponse.Code != http.StatusOK || dryEnvelope.Meta["dry_run"] != true || dryEnvelope.Meta["deleted"] != false || dryEnvelope.Meta["replies_affected"] != float64(1) {
		t.Fatalf("dry-run = %s", dryResponse.Body.String())
	}
	deleteRequest := commentRequest(http.MethodDelete, "/api/v1/owner/comments/"+rootID, "")
	deleteRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	deleted := performRequest(fixture.engine, deleteRequest)
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"replies_affected":1`) {
		t.Fatalf("delete = %d %s", deleted.Code, deleted.Body.String())
	}
	empty := performRequest(fixture.engine, commentRequest(http.MethodGet, publicPath, ""))
	if !strings.Contains(empty.Body.String(), `"items":[]`) {
		t.Fatalf("cascade = %s", empty.Body.String())
	}
}

func TestCommentHTTPAbuseSafetyStrictJSONAndClosedComments(t *testing.T) {
	var logsMu sync.Mutex
	logs := make([]map[string]any, 0)
	fixture := newCommentHTTPFixture(t, 2, func(_ string, _ string, fields map[string]any) {
		logsMu.Lock()
		defer logsMu.Unlock()
		logs = append(logs, fields)
	})
	path := "/api/v1/posts/" + fixture.postID.String() + "/comments"
	secret := "private-comment-body"
	honeypot := "trap-value"
	hit := performRequest(fixture.engine, commentRequest(http.MethodPost, path, `{"author_name":"Bot","content_md":"`+secret+`","website":"`+honeypot+`"}`))
	if hit.Code != http.StatusUnprocessableEntity || decodeError(t, hit.Body).Error.Code != "COMMENT_REJECTED" || strings.Contains(hit.Body.String(), honeypot) {
		t.Fatalf("honeypot = %d %s", hit.Code, hit.Body.String())
	}
	if list := performRequest(fixture.engine, commentRequest(http.MethodGet, path, "")); !strings.Contains(list.Body.String(), `"items":[]`) {
		t.Fatalf("honeypot persisted: %s", list.Body.String())
	}

	for _, body := range []string{`{}`, `{"author_name":"A","content_md":null}`, `{"author_name":"A","content_md":"x","extra":true}`, `{"author_name":"A","author_name":"B","content_md":"x"}`, `{"author_name":"A","content_md":"x"} {}`} {
		response := performRequest(fixture.engine, commentRequest(http.MethodPost, path, body))
		if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("strict %q = %d %s", body, response.Code, response.Body.String())
		}
	}
	for _, test := range []struct {
		body, code string
		status     int
	}{
		{body: `{"author_name":"A","content_md":"<b>raw</b>"}`, code: "INVALID_ARGUMENT", status: http.StatusBadRequest},
		{body: `{"author_name":"A","content_md":"![x](https://example.test/x.png)"}`, code: "EXTERNAL_IMAGE_NOT_ALLOWED", status: http.StatusUnprocessableEntity},
	} {
		response := performRequest(fixture.engine, commentRequest(http.MethodPost, path, test.body))
		if response.Code != test.status || decodeError(t, response.Body).Error.Code != test.code {
			t.Fatalf("unsafe %q = %d %s", test.body, response.Code, response.Body.String())
		}
	}
	*fixture.clock = fixture.clock.Add(time.Minute + time.Millisecond)
	for index := 0; index < 2; index++ {
		request := commentRequest(http.MethodPost, path, `{"author_name":"A","content_md":"allowed"}`)
		request.Header.Set("X-Forwarded-For", "203.0.113."+strconv.Itoa(index+1))
		if response := performRequest(fixture.engine, request); response.Code != http.StatusCreated {
			t.Fatalf("rate seed %d = %d %s", index, response.Code, response.Body.String())
		}
	}
	blocked := commentRequest(http.MethodPost, path, `{"author_name":"A","content_md":"blocked"}`)
	blocked.Header.Set("X-Forwarded-For", "203.0.113.200")
	blockedResponse := performRequest(fixture.engine, blocked)
	if blockedResponse.Code != http.StatusTooManyRequests || decodeError(t, blockedResponse.Body).Error.Code != "RATE_LIMITED" || blockedResponse.Header().Get("Retry-After") == "" {
		t.Fatalf("rate = %d %s", blockedResponse.Code, blockedResponse.Body.String())
	}
	*fixture.clock = fixture.clock.Add(time.Minute + time.Millisecond)
	if response := performRequest(fixture.engine, commentRequest(http.MethodPost, path, `{"author_name":"A","content_md":"new window"}`)); response.Code != http.StatusCreated {
		t.Fatalf("new window = %d %s", response.Code, response.Body.String())
	}

	commentsEnabled := false
	if _, err := fixture.site.Update(context.Background(), app.SiteUpdate{CommentsEnabled: &commentsEnabled}); err != nil {
		t.Fatal(err)
	}
	closed := performRequest(fixture.engine, commentRequest(http.MethodPost, path, `{"author_name":"A","content_md":"closed"}`))
	if closed.Code != http.StatusForbidden || decodeError(t, closed.Body).Error.Code != "COMMENTS_DISABLED" {
		t.Fatalf("closed = %d %s", closed.Code, closed.Body.String())
	}
	if existing := performRequest(fixture.engine, commentRequest(http.MethodGet, path, "")); existing.Code != http.StatusOK || !strings.Contains(existing.Body.String(), `"items":[`) {
		t.Fatalf("closed list = %d %s", existing.Code, existing.Body.String())
	}

	logsMu.Lock()
	defer logsMu.Unlock()
	for _, fields := range logs {
		encoded, _ := json.Marshal(fields)
		if strings.Contains(string(encoded), secret) || strings.Contains(string(encoded), "192.0.2.25") {
			t.Fatalf("sensitive request data logged: %s", encoded)
		}
	}
}

func TestCommentHTTPCancellationAndRouteRequirements(t *testing.T) {
	fixture := newCommentHTTPFixture(t, 10, nil)
	path := "/api/v1/posts/" + fixture.postID.String() + "/comments"
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := commentRequest(http.MethodGet, path, "").WithContext(ctx)
	if response := performRequest(fixture.engine, request); response.Code != http.StatusRequestTimeout || decodeError(t, response.Body).Error.Code != "CANCELLED" {
		t.Fatalf("cancelled = %d %s", response.Code, response.Body.String())
	}
	if _, err := New(Config{Assets: testAssets(), Comments: fixture.comments}); err == nil {
		t.Fatal("comment routes without owner auth accepted")
	}
}

func TestCommentRateLimiterIsConcurrentAndWindowed(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	limiter, err := NewCommentRateLimiter(10, time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	var allowed int
	var mu sync.Mutex
	var group sync.WaitGroup
	for range 64 {
		group.Add(1)
		go func() {
			defer group.Done()
			if ok, _ := limiter.Allow("192.0.2.1"); ok {
				mu.Lock()
				allowed++
				mu.Unlock()
			}
		}()
	}
	group.Wait()
	if allowed != 10 {
		t.Fatalf("allowed = %d, want 10", allowed)
	}
	if ok, retry := limiter.Allow("192.0.2.1"); ok || retry != time.Minute {
		t.Fatalf("blocked = %t retry=%s", ok, retry)
	}
	if ok, _ := limiter.Allow("192.0.2.2"); !ok {
		t.Fatal("independent client was blocked")
	}
	now = now.Add(time.Minute + time.Millisecond)
	if ok, retry := limiter.Allow("192.0.2.1"); !ok || retry != 0 {
		t.Fatalf("new window = %t retry=%s", ok, retry)
	}
	if _, err := NewCommentRateLimiter(0, time.Minute, nil); err == nil {
		t.Fatal("zero rate accepted")
	}
	bounded, err := NewCommentRateLimiter(1, time.Minute, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	for index := 0; index < maxCommentRateIdentities; index++ {
		if ok, _ := bounded.Allow("identity-" + strconv.Itoa(index)); !ok {
			t.Fatalf("identity %d blocked before capacity", index)
		}
	}
	if ok, retry := bounded.Allow("identity-over-capacity"); ok || retry != time.Minute {
		t.Fatalf("capacity = %t retry=%s", ok, retry)
	}
}

func TestCommentRateIdentityTrustsOnlyConfiguredProxy(t *testing.T) {
	fixture := newCommentHTTPFixture(t, 100, nil)
	path := "/api/v1/posts/" + fixture.postID.String() + "/comments"
	for _, test := range []struct {
		name    string
		proxies []string
		want    string
	}{
		{name: "socket", want: "192.0.2.25"},
		{name: "trusted proxy", proxies: []string{"192.0.2.0/24"}, want: "198.51.100.8"},
	} {
		t.Run(test.name, func(t *testing.T) {
			identities := make(chan string, 1)
			engine, err := New(Config{Assets: testAssets(), OwnerAuth: fixture.manager, Comments: fixture.comments, CommentLimiter: recordingCommentLimiter{identities: identities}, TrustedProxies: test.proxies})
			if err != nil {
				t.Fatal(err)
			}
			request := commentRequest(http.MethodPost, path, `{"author_name":"Guest","content_md":"identity"}`)
			request.Header.Set("X-Forwarded-For", "198.51.100.8")
			if response := performRequest(engine, request); response.Code != http.StatusCreated {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
			if got := <-identities; got != test.want {
				t.Fatalf("identity = %q, want %q", got, test.want)
			}
		})
	}
}
