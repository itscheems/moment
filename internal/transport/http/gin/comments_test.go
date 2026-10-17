package ginhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/auth/oauth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// legacyGuestCommentID identifies a row of the shape an archive written before
// commenting required an account contains. No service method can produce one
// any more, so the tests that read it insert it with SQL.
const legacyGuestCommentID = "01ARZ3NDEKTSV4RRFFQ69G5FB9"

type commentHTTPFixture struct {
	runtime *runtimefs.Runtime
	authenticationTestFixture
	accountService *app.AccountService
	comments       *app.CommentService
	site           *app.SiteService
	postID         domain.ID
	clock          *time.Time
	database       *sqlitestore.Database
}

func newCommentHTTPFixture(t *testing.T, log LogFunc) commentHTTPFixture {
	t.Helper()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	_, err = auth.InitializeOffline(context.Background(), runtimeState, auth.Options{})
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
	testAccounts(t, database, runtimeState)
	t.Cleanup(func() { _ = database.Close() })
	postRepository, _ := sqlitestore.NewPostRepository(database, runtimeState.Locks)
	postID := domain.ID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	posts, err := app.NewPostService(postRepository, app.WithPostIDGenerator(func(time.Time) (domain.ID, error) { return postID, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := posts.Create(fixtureContentContext(t, testAccounts(t, database, runtimeState)), app.CreatePostInput{ContentMD: "comment host"}); err != nil {
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
	mediaStore, err := media.NewStore(runtimeState, database, media.Options{
		Clock:   func() time.Time { return now },
		Entropy: bytes.NewReader(bytes.Repeat([]byte{0x37}, 256)),
	})
	if err != nil {
		t.Fatal(err)
	}
	accountRepository, err := sqlitestore.NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	commentAccounts, err := app.NewAccountService(accountRepository)
	if err != nil {
		t.Fatal(err)
	}
	// Federated sign-in is wired in so a test can mint a real member account
	// against this same engine; the provider is substituted, not reached.
	engine, err := New(Config{Assets: testAssets(), SessionSigner: manager, Accounts: commentAccounts, SiteSettings: site, Posts: posts, Comments: comments, Media: mediaStore, OAuthProviders: oauth.NewRegistry(newStubProvider()), Log: log, RequestID: func() string { return "req_comments" }})
	if err != nil {
		t.Fatal(err)
	}
	return commentHTTPFixture{
		runtime:                   runtimeState,
		authenticationTestFixture: authenticationTestFixture{manager: manager, accounts: commentAccounts, gate: NewGate(manager, commentAccounts), token: issueTestAPIToken(t, commentAccounts), engine: engine, now: &now},
		accountService:            commentAccounts,
		comments:                  comments, site: site, postID: postID, clock: &now, database: database,
	}
}

// publicCommentPath is the one comment route that never needed an account.
func publicCommentPath(fixture commentHTTPFixture) string {
	return "/api/v1/posts/" + fixture.postID.String() + "/comments"
}

// ownerCommentPath is the only route that writes a comment.
func ownerCommentPath(fixture commentHTTPFixture) string {
	return "/api/v1/owner/posts/" + fixture.postID.String() + "/comments"
}

func commentRequest(method, path, body string) *http.Request {
	request := jsonRequest(method, path, body)
	request.RemoteAddr = "192.0.2.25:4000"
	return request
}

// ownerCommentRequest authenticates the way every other owner-route test in
// this package does: a bearer token, which needs no CSRF header.
func ownerCommentRequest(fixture commentHTTPFixture, method, path, body string) *http.Request {
	request := commentRequest(method, path, body)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	return request
}

func commentData(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if response.Code < 200 || response.Code >= 300 {
		t.Fatalf("status=%d body=%s", response.Code, response.Body.String())
	}
	return decodeDataMap(t, response)
}

func postOwnerComment(t *testing.T, fixture commentHTTPFixture, body string) map[string]any {
	t.Helper()
	response := performRequest(fixture.engine, ownerCommentRequest(fixture, http.MethodPost, ownerCommentPath(fixture), body))
	if response.Code != http.StatusCreated {
		t.Fatalf("owner create = %d %s", response.Code, response.Body.String())
	}
	return commentData(t, response)
}

func uploadOwnerMedia(t *testing.T, fixture commentHTTPFixture) string {
	t.Helper()
	request := mediaUploadRequest(t, testGIF(t))
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	response := performRequest(fixture.engine, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("owner media upload = %d %s", response.Code, response.Body.String())
	}
	uploaded, ok := decodeDataMap(t, response)["media"].(map[string]any)
	if !ok {
		t.Fatalf("upload data = %s", response.Body.String())
	}
	id, ok := uploaded["id"].(string)
	if !ok {
		t.Fatalf("uploaded media = %#v", uploaded)
	}
	return id
}

// insertLegacyGuestComment writes the row an anonymous visitor used to leave:
// author_kind 'guest', a name the visitor typed, and no account reference.
// The write path can no longer produce it, so the regression guard for reading
// one has to build it directly.
func insertLegacyGuestComment(t *testing.T, fixture commentHTTPFixture, id, name, content string) {
	t.Helper()
	ctx := context.Background()
	err := fixture.database.WriteTx(ctx, func(queries sqlitestore.Queries) error {
		_, execErr := queries.ExecContext(ctx, `
INSERT INTO comments (id, entry_id, parent_id, author_name, content_md, created_at_ms)
VALUES (?, ?, NULL, ?, ?, ?)`, id, fixture.postID.String(), name, content, fixture.clock.Add(-time.Hour).UnixMilli())
		return execErr
	})
	if err != nil {
		t.Fatal(err)
	}
}

func decodeCommentItems(t *testing.T, response *httptest.ResponseRecorder) []map[string]any {
	t.Helper()
	var envelope struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatalf("decode thread: %v, body = %s", err, response.Body.String())
	}
	return envelope.Data.Items
}

func TestCommentHTTPOwnerMediaUploadCreateAndReadContract(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	mediaID := uploadOwnerMedia(t, fixture)
	data := postOwnerComment(t, fixture,
		`{"content_md":"image reply","media":[{"media_id":"`+mediaID+`","position":0,"alt_text":"night sky"}]}`)
	mediaItems, ok := data["media"].([]any)
	if !ok || len(mediaItems) != 1 {
		t.Fatalf("created media = %#v", data["media"])
	}
	usage := mediaItems[0].(map[string]any)
	width, widthOK := usage["width"].(float64)
	height, heightOK := usage["height"].(float64)
	if usage["media_id"] != mediaID || usage["position"] != float64(0) || usage["alt_text"] != "night sky" || usage["mime_type"] != "image/gif" || !widthOK || width <= 0 || !heightOK || height <= 0 || len(usage) != 6 {
		t.Fatalf("usage = %#v", usage)
	}
	listed := performRequest(fixture.engine, commentRequest(http.MethodGet, publicCommentPath(fixture), ""))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"media_id":"`+mediaID+`"`) {
		t.Fatalf("list = %d %s", listed.Code, listed.Body.String())
	}
}

func TestCommentHTTPOwnerThreadAndModerationContract(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	publicPath := publicCommentPath(fixture)
	root := postOwnerComment(t, fixture, `{"content_md":"first **comment**"}`)
	if root["author_kind"] != nil || root["author_name"] != "writer" || root["parent_id"] != nil ||
		root["depth"] != float64(0) || !strings.Contains(root["content_html"].(string), "<strong>comment</strong>") {
		t.Fatalf("root = %#v", root)
	}
	rootID := root["id"].(string)
	reply := postOwnerComment(t, fixture, `{"content_md":"owner reply","parent_id":"`+rootID+`"}`)
	if reply["author_kind"] != nil || reply["author_name"] != "writer" || reply["parent_id"] != rootID || reply["depth"] != float64(1) {
		t.Fatalf("reply = %#v", reply)
	}
	deep := performRequest(fixture.engine, ownerCommentRequest(fixture, http.MethodPost, ownerCommentPath(fixture),
		`{"content_md":"reply to a reply","parent_id":"`+reply["id"].(string)+`"}`))
	if deep.Code != http.StatusConflict || decodeError(t, deep.Body).Error.Code != "COMMENT_DEPTH_EXCEEDED" {
		t.Fatalf("depth = %d", deep.Code)
	}

	list := performRequest(fixture.engine, commentRequest(http.MethodGet, publicPath, ""))
	items := decodeCommentItems(t, list)
	if len(items) != 2 || items[0]["id"] != rootID || items[1]["id"] != reply["id"] {
		t.Fatalf("thread = %#v", items)
	}
	postResponse := performRequest(fixture.engine, commentRequest(http.MethodGet, "/api/v1/posts/"+fixture.postID.String(), ""))
	if count := decodeDataMap(t, postResponse)["comment_count"]; count != float64(2) {
		t.Fatalf("post comment_count = %#v", count)
	}

	unauthorized := performRequest(fixture.engine, commentRequest(http.MethodDelete, "/api/v1/owner/comments/"+rootID, ""))
	if unauthorized.Code != http.StatusUnauthorized || decodeError(t, unauthorized.Body).Error.Code != "UNAUTHORIZED" {
		t.Fatalf("unauthorized = %d", unauthorized.Code)
	}
	cookie, session := exchangeAccountSession(t, fixture.authenticationTestFixture, fixture.token)
	missingCSRF := commentRequest(http.MethodDelete, "/api/v1/owner/comments/"+rootID, "")
	missingCSRF.AddCookie(cookie)
	if response := performRequest(fixture.engine, missingCSRF); response.Code != http.StatusForbidden || decodeError(t, response.Body).Error.Code != "CSRF_INVALID" {
		t.Fatalf("csrf = %d", response.Code)
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
	deleted := performRequest(fixture.engine, ownerCommentRequest(fixture, http.MethodDelete, "/api/v1/owner/comments/"+rootID, ""))
	if deleted.Code != http.StatusOK || !strings.Contains(deleted.Body.String(), `"replies_affected":1`) {
		t.Fatalf("delete = %d %s", deleted.Code, deleted.Body.String())
	}
	empty := performRequest(fixture.engine, commentRequest(http.MethodGet, publicPath, ""))
	if !strings.Contains(empty.Body.String(), `"items":[]`) {
		t.Fatalf("cascade = %s", empty.Body.String())
	}
	emptyPost := performRequest(fixture.engine, commentRequest(http.MethodGet, "/api/v1/posts/"+fixture.postID.String(), ""))
	if count := decodeDataMap(t, emptyPost)["comment_count"]; count != float64(0) {
		t.Fatalf("empty post comment_count = %#v", count)
	}
}

func TestCommentHTTPOwnerCreateReplaysOneIdempotencyKey(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	const body = `{"content_md":"retried once"}`
	first := ownerCommentRequest(fixture, http.MethodPost, ownerCommentPath(fixture), body)
	first.Header.Set("Idempotency-Key", "owner-comment-retry")
	created := performRequest(fixture.engine, first)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	createdID := commentData(t, created)["id"]

	repeat := ownerCommentRequest(fixture, http.MethodPost, ownerCommentPath(fixture), body)
	repeat.Header.Set("Idempotency-Key", "owner-comment-retry")
	replayed := performRequest(fixture.engine, repeat)
	var envelope struct {
		Data map[string]any `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(replayed.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if replayed.Code != http.StatusOK || envelope.Meta["replayed"] != true || envelope.Data["id"] != createdID {
		t.Fatalf("replay = %d %s", replayed.Code, replayed.Body.String())
	}

	changed := ownerCommentRequest(fixture, http.MethodPost, ownerCommentPath(fixture), `{"content_md":"different body"}`)
	changed.Header.Set("Idempotency-Key", "owner-comment-retry")
	conflict := performRequest(fixture.engine, changed)
	if conflict.Code != http.StatusConflict || decodeError(t, conflict.Body).Error.Code != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("conflict = %d", conflict.Code)
	}
	if listed := performRequest(fixture.engine, commentRequest(http.MethodGet, publicCommentPath(fixture), "")); len(decodeCommentItems(t, listed)) != 1 {
		t.Fatalf("replay wrote a second comment: %s", listed.Body.String())
	}
}

func TestCommentHTTPPinContractExposesOneTopLevelSelection(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	publicPath := publicCommentPath(fixture)
	rootID := postOwnerComment(t, fixture, `{"content_md":"root"}`)["id"].(string)
	replyID := postOwnerComment(t, fixture, `{"content_md":"reply","parent_id":"`+rootID+`"}`)["id"].(string)
	pinPath := "/api/v1/owner/posts/" + fixture.postID.String() + "/comments/" + rootID + "/pin"

	unauthorized := performRequest(fixture.engine, commentRequest(http.MethodPut, pinPath, ""))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized pin = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	pinned := performRequest(fixture.engine, ownerCommentRequest(fixture, http.MethodPut, pinPath, ""))
	data := commentData(t, pinned)
	if pinned.Code != http.StatusOK || data["post_id"] != fixture.postID.String() || data["pinned_comment_id"] != rootID {
		t.Fatalf("pin = %d %s", pinned.Code, pinned.Body.String())
	}
	listed := performRequest(fixture.engine, commentRequest(http.MethodGet, publicPath, ""))
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), `"pinned_comment_id":"`+rootID+`"`) {
		t.Fatalf("listed pin = %d %s", listed.Code, listed.Body.String())
	}

	rejected := performRequest(fixture.engine, ownerCommentRequest(fixture, http.MethodPut,
		"/api/v1/owner/posts/"+fixture.postID.String()+"/comments/"+replyID+"/pin", ""))
	if rejected.Code != http.StatusBadRequest || decodeError(t, rejected.Body).Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("reply pin = %d %s", rejected.Code, rejected.Body.String())
	}

	unpinned := performRequest(fixture.engine, ownerCommentRequest(fixture, http.MethodDelete, pinPath, ""))
	if unpinned.Code != http.StatusOK || commentData(t, unpinned)["pinned_comment_id"] != nil {
		t.Fatalf("unpin = %d %s", unpinned.Code, unpinned.Body.String())
	}
}

func TestCommentHTTPStrictJSONUnsupportedFieldsAndClosedComments(t *testing.T) {
	var logsMu sync.Mutex
	logs := make([]map[string]any, 0)
	fixture := newCommentHTTPFixture(t, func(_ string, _ string, fields map[string]any) {
		logsMu.Lock()
		defer logsMu.Unlock()
		logs = append(logs, fields)
	})
	ownerPath := ownerCommentPath(fixture)
	secret := "private-comment-body"

	// author_name and website were the anonymous author's identity and the
	// honeypot that guarded it. Neither is part of the write body any more, so
	// supplying either is a plain unsupported field.
	for _, test := range []struct{ body, field, value string }{
		{body: `{"author_name":"Guest","content_md":"` + secret + `"}`, field: "author_name", value: "Guest"},
		{body: `{"content_md":"` + secret + `","website":"trap-value"}`, field: "website", value: "trap-value"},
	} {
		response := performRequest(fixture.engine, ownerCommentRequest(fixture, http.MethodPost, ownerPath, test.body))
		raw := response.Body.String()
		failure := decodeError(t, strings.NewReader(raw))
		if response.Code != http.StatusBadRequest || failure.Error.Code != "INVALID_ARGUMENT" || failure.Error.Details["field"] != test.field {
			t.Fatalf("retired field %q = %d %s", test.field, response.Code, raw)
		}
		if strings.Contains(raw, test.value) || strings.Contains(raw, secret) {
			t.Fatalf("rejection echoed the submitted value: %s", raw)
		}
	}

	for _, body := range []string{`{}`, `{"content_md":null}`, `{"content_md":"x","extra":true}`, `{"content_md":"x","content_md":"y"}`, `{"content_md":"x"} {}`} {
		response := performRequest(fixture.engine, ownerCommentRequest(fixture, http.MethodPost, ownerPath, body))
		if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("strict %q = %d %s", body, response.Code, response.Body.String())
		}
	}
	mediaID := "01ARZ3NDEKTSV4RRFFQ69G5FAY"
	for _, body := range []string{
		`{"content_md":"x","media":null}`,
		`{"content_md":"x","media":[{"media_id":"` + mediaID + `","position":1,"alt_text":null}]}`,
		`{"content_md":"x","media":[{"media_id":"` + mediaID + `","position":0,"alt_text":null,"extra":true}]}`,
		`{"content_md":"x","media":[` + strings.TrimSuffix(strings.Repeat(`{"media_id":"`+mediaID+`","position":0,"alt_text":null},`, domain.MaxCommentMediaUsages+1), ",") + `]}`,
	} {
		response := performRequest(fixture.engine, ownerCommentRequest(fixture, http.MethodPost, ownerPath, body))
		if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("strict media %q = %d %s", body, response.Code, response.Body.String())
		}
	}
	for _, test := range []struct {
		body, code string
		status     int
	}{
		{body: `{"content_md":"<b>raw</b>"}`, code: "INVALID_ARGUMENT", status: http.StatusBadRequest},
		{body: `{"content_md":"![x](https://example.test/x.png)"}`, code: "EXTERNAL_IMAGE_NOT_ALLOWED", status: http.StatusUnprocessableEntity},
	} {
		response := performRequest(fixture.engine, ownerCommentRequest(fixture, http.MethodPost, ownerPath, test.body))
		if response.Code != test.status || decodeError(t, response.Body).Error.Code != test.code {
			t.Fatalf("unsafe %q = %d %s", test.body, response.Code, response.Body.String())
		}
	}
	if list := performRequest(fixture.engine, commentRequest(http.MethodGet, publicCommentPath(fixture), "")); !strings.Contains(list.Body.String(), `"items":[]`) {
		t.Fatalf("a rejected write persisted: %s", list.Body.String())
	}

	accepted := postOwnerComment(t, fixture, `{"content_md":"accepted"}`)
	commentsEnabled := false
	if _, err := fixture.site.Update(context.Background(), app.SiteUpdate{CommentsEnabled: &commentsEnabled}); err != nil {
		t.Fatal(err)
	}
	closed := performRequest(fixture.engine, ownerCommentRequest(fixture, http.MethodPost, ownerPath, `{"content_md":"closed"}`))
	if closed.Code != http.StatusForbidden || decodeError(t, closed.Body).Error.Code != "COMMENTS_DISABLED" {
		t.Fatalf("closed = %d %s", closed.Code, closed.Body.String())
	}
	existing := performRequest(fixture.engine, commentRequest(http.MethodGet, publicCommentPath(fixture), ""))
	if items := decodeCommentItems(t, existing); existing.Code != http.StatusOK || len(items) != 1 || items[0]["id"] != accepted["id"] {
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

// TestCommentHTTPRetiredAnonymousWriteRoutesRejectEveryCaller pins the shape of
// the removal: the two anonymous write routes are not merely guarded, they are
// not routes. An owner token does not bring them back either -- the owner
// group is the only place a comment write is mounted.
func TestCommentHTTPRetiredAnonymousWriteRoutesRejectEveryCaller(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	for _, test := range []struct {
		name, path, code string
		status           int
	}{
		{
			// The path still exists for GET, so the router answers the write
			// with "not this method" rather than "no such route".
			name:   "create",
			path:   "/api/v1/posts/" + fixture.postID.String() + "/comments",
			status: http.StatusMethodNotAllowed,
			code:   "METHOD_NOT_ALLOWED",
		},
		{
			name:   "media upload",
			path:   "/api/v1/posts/" + fixture.postID.String() + "/comment-media",
			status: http.StatusNotFound,
			code:   "API_ROUTE_NOT_FOUND",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			anonymous := performRequest(fixture.engine, commentRequest(http.MethodPost, test.path, `{"content_md":"anonymous"}`))
			if anonymous.Code != test.status || decodeError(t, anonymous.Body).Error.Code != test.code {
				t.Fatalf("anonymous = %d %s", anonymous.Code, anonymous.Body.String())
			}
			authenticated := performRequest(fixture.engine, ownerCommentRequest(fixture, http.MethodPost, test.path, `{"content_md":"authenticated"}`))
			if authenticated.Code != test.status || decodeError(t, authenticated.Body).Error.Code != test.code {
				t.Fatalf("authenticated = %d %s", authenticated.Code, authenticated.Body.String())
			}
		})
	}
	if listed := performRequest(fixture.engine, commentRequest(http.MethodGet, publicCommentPath(fixture), "")); !strings.Contains(listed.Body.String(), `"items":[]`) {
		t.Fatalf("a retired route wrote a comment: %s", listed.Body.String())
	}
}

// TestCommentHTTPPublicThreadReadStaysOpenToAnyone guards the half of the
// surface that was deliberately kept: reading a thread never needed an account
// and still does not.
func TestCommentHTTPPublicThreadReadStaysOpenToAnyone(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	created := postOwnerComment(t, fixture, `{"content_md":"published"}`)

	anonymous := commentRequest(http.MethodGet, publicCommentPath(fixture), "")
	if anonymous.Header.Get("Authorization") != "" || len(anonymous.Cookies()) != 0 {
		t.Fatal("the public read carried credentials")
	}
	response := performRequest(fixture.engine, anonymous)
	items := decodeCommentItems(t, response)
	if response.Code != http.StatusOK || len(items) != 1 || items[0]["id"] != created["id"] {
		t.Fatalf("public read = %d %s", response.Code, response.Body.String())
	}

	// A stale or forged session must fail the read open, not closed.
	rejectedSession := commentRequest(http.MethodGet, publicCommentPath(fixture), "")
	rejectedSession.AddCookie(&http.Cookie{Name: SessionCookieName, Value: "invalid-session-cookie"})
	if withCookie := performRequest(fixture.engine, rejectedSession); withCookie.Code != http.StatusOK {
		t.Fatalf("rejected session closed the public read = %d %s", withCookie.Code, withCookie.Body.String())
	}
}

// TestCommentHTTPListsLegacyGuestRowsWrittenBeforeAccountsWereRequired is the
// regression guard for archives taken before commenting required an account.
// Guest rows are read-only history now, and history has to render.
func TestCommentHTTPListsLegacyGuestRowsWrittenBeforeAccountsWereRequired(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	const legacyName = "访客"
	insertLegacyGuestComment(t, fixture, legacyGuestCommentID, legacyName, "left before accounts existed")
	current := postOwnerComment(t, fixture, `{"content_md":"left after accounts existed"}`)

	listed := performRequest(fixture.engine, commentRequest(http.MethodGet, publicCommentPath(fixture), ""))
	items := decodeCommentItems(t, listed)
	if listed.Code != http.StatusOK || len(items) != 2 {
		t.Fatalf("thread = %d %s", listed.Code, listed.Body.String())
	}
	legacy := items[0]
	if legacy["id"] != legacyGuestCommentID || legacy["author_kind"] != nil || legacy["author_name"] != legacyName ||
		legacy["depth"] != float64(0) || legacy["parent_id"] != nil ||
		!strings.Contains(legacy["content_html"].(string), "left before accounts existed") {
		t.Fatalf("legacy guest comment = %#v", legacy)
	}
	if items[1]["id"] != current["id"] || items[1]["author_kind"] != nil || items[1]["author_name"] != "writer" {
		t.Fatalf("owner comment = %#v", items[1])
	}
	postResponse := performRequest(fixture.engine, commentRequest(http.MethodGet, "/api/v1/posts/"+fixture.postID.String(), ""))
	if count := decodeDataMap(t, postResponse)["comment_count"]; count != float64(2) {
		t.Fatalf("legacy rows missing from comment_count = %#v", count)
	}
}

func TestCommentHTTPCancellationAndRouteRequirements(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	request := commentRequest(http.MethodGet, publicCommentPath(fixture), "").WithContext(ctx)
	if response := performRequest(fixture.engine, request); response.Code != http.StatusRequestTimeout || decodeError(t, response.Body).Error.Code != "CANCELLED" {
		t.Fatalf("cancelled = %d %s", response.Code, response.Body.String())
	}
	if _, err := New(Config{Assets: testAssets(), Comments: fixture.comments}); err == nil {
		t.Fatal("comment routes without owner auth accepted")
	}
}
