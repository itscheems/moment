package ginhttp

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/auth/oauth"
	"github.com/moment/moment/internal/doctor"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/linkfetch"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// The stubs below exist only so a route group can be registered. A pending
// account must be turned away by the gate before any handler runs, so if one
// of these methods is ever reached the boundary has already been crossed and
// the test should fail loudly rather than quietly succeed.
type stubLocationResolver struct{ t *testing.T }

func (s stubLocationResolver) Search(context.Context, string, *domain.GeoPoint, int) ([]domain.Location, error) {
	s.t.Fatal("location Search reached by an unapproved account")
	return nil, nil
}
func (s stubLocationResolver) Nearby(context.Context, domain.GeoPoint, int) ([]domain.Location, error) {
	s.t.Fatal("location Nearby reached by an unapproved account")
	return nil, nil
}

type stubLinkResolver struct{ t *testing.T }

func (s stubLinkResolver) Resolve(context.Context, string) (linkfetch.Preview, error) {
	s.t.Fatal("link Resolve reached by an unapproved account")
	return linkfetch.Preview{}, nil
}
func (s stubLinkResolver) Fetcher() *linkfetch.Fetcher { return nil }

type stubArchiveExporter struct{ t *testing.T }

func (s stubArchiveExporter) Export(context.Context, io.Writer) (archive.Summary, error) {
	s.t.Fatal("archive Export reached by an unapproved account")
	return archive.Summary{}, nil
}

type stubImportInspector struct{ t *testing.T }

func (s stubImportInspector) Inspect(context.Context, io.Reader, uint64) (archive.Inspection, error) {
	s.t.Fatal("import Inspect reached by an unapproved account")
	return archive.Inspection{}, nil
}

type stubImportCommitter struct{ t *testing.T }

func (s stubImportCommitter) Commit(context.Context, string, uint64) (archive.CommitResult, error) {
	s.t.Fatal("import Commit reached by an unapproved account")
	return archive.CommitResult{}, nil
}
func (s stubImportCommitter) Status(context.Context) (archive.CommitStatus, error) {
	s.t.Fatal("import Status reached by an unapproved account")
	return archive.CommitStatus{}, nil
}

type stubDoctorReporter struct{ t *testing.T }

func (s stubDoctorReporter) Run(context.Context, doctor.Mode) (doctor.Report, error) {
	s.t.Fatal("doctor Run reached by an unapproved account")
	return doctor.Report{}, nil
}

// fullEngineFixture wires every owner-scoped route group onto one engine backed
// by real SQLite, plus a substituted identity provider that mints genuine
// pending accounts and genuine session cookies.
type fullEngineFixture struct {
	engine   http.Handler
	accounts *app.AccountService
	provider *stubProvider
	postID   domain.ID
}

func newFullEngineFixture(t *testing.T) fullEngineFixture {
	t.Helper()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{}); err != nil {
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

	now := time.Date(2026, time.August, 20, 12, 0, 0, 0, time.UTC)
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
	accounts, err := app.NewAccountService(accountRepository)
	if err != nil {
		t.Fatal(err)
	}
	locations, err := app.NewLocationService(stubLocationResolver{t: t})
	if err != nil {
		t.Fatal(err)
	}
	links, err := app.NewLinkPreviewService(stubLinkResolver{t: t}, nil, app.LinkPreviewOptions{})
	if err != nil {
		t.Fatal(err)
	}

	provider := newStubProvider()
	engine, err := New(Config{
		Assets:          testAssets(),
		SessionSigner:       manager,
		Accounts:        accounts,
		OAuthProviders:  oauth.NewRegistry(provider),
		SiteSettings:    site,
		Posts:           posts,
		Comments:        comments,
		Media:           mediaStore,
		Locations:       locations,
		Links:           links,
		Archive:         stubArchiveExporter{t: t},
		ImportInspector: stubImportInspector{t: t},
		ImportCommitter: stubImportCommitter{t: t},
		Doctor:          stubDoctorReporter{t: t},
		RequestID:       func() string { return "req_full" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return fullEngineFixture{engine: engine, accounts: accounts, provider: provider, postID: postID}
}

// signInPending drives the substituted provider through both OAuth legs and
// returns the genuine session cookie, the CSRF token bound into it, and the id
// of the pending account that was created.
func (fixture fullEngineFixture) signInPending(t *testing.T) (*http.Cookie, string, string) {
	t.Helper()
	start := httptest.NewRequest(http.MethodGet, "/api/v1/login/oauth/google/start", nil)
	startResponse := performRequest(fixture.engine, start)
	if startResponse.Code != http.StatusSeeOther {
		t.Fatalf("start = %d", startResponse.Code)
	}
	var flow *http.Cookie
	for _, cookie := range startResponse.Result().Cookies() {
		if cookie.Name == oauthCookieName {
			flow = cookie
		}
	}
	if flow == nil {
		t.Fatal("start set no flow cookie")
	}
	callback := performRequest(fixture.engine, callbackRequest(fixture.provider.lastCall.state, "code", flow))
	session := sessionCookieFrom(callback)
	if session == nil {
		t.Fatalf("callback issued no session: %d %s", callback.Code, callback.Header().Get("Location"))
	}

	sessionRequest := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	sessionRequest.AddCookie(session)
	var envelope struct {
		Data struct {
			CSRFToken string `json:"csrf_token"`
			Account   struct {
				UserID string `json:"user_id"`
				Status string `json:"status"`
			} `json:"account"`
		} `json:"data"`
	}
	if err := json.NewDecoder(performRequest(fixture.engine, sessionRequest).Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Account.Status != string(domain.AccountPending) {
		t.Fatalf("account status = %q, want pending", envelope.Data.Account.Status)
	}
	if envelope.Data.CSRFToken == "" {
		t.Fatal("no CSRF token issued")
	}
	return session, envelope.Data.CSRFToken, envelope.Data.Account.UserID
}

// TestPendingAccountIsRefusedByEveryOwnerScopedRoute enumerates every route in
// every owner-scoped group and proves an approved-content credential is refused
// on all of them, even carrying a valid session cookie and a valid CSRF token.
//
// The point is the claim in gate.go that authorisation has exactly one
// implementation. If any group forgot the gate, the request would reach the
// handler and answer with something other than 401/403 -- a 2xx, or a 400/404
// from the handler parsing input it should never have seen.
func TestPendingAccountIsRefusedByEveryOwnerScopedRoute(t *testing.T) {
	fixture := newFullEngineFixture(t)
	session, csrf, memberID := fixture.signInPending(t)

	const someULID = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	post := fixture.postID.String()
	routes := []struct {
		group  string
		method string
		path   string
	}{
		// comments (RequireActiveAccount)
		{"comments", http.MethodPost, "/api/v1/owner/posts/" + post + "/comments"},
		{"comments", http.MethodPut, "/api/v1/owner/posts/" + post + "/comments/" + someULID + "/pin"},
		{"comments", http.MethodDelete, "/api/v1/owner/posts/" + post + "/comments/" + someULID + "/pin"},
		{"comments", http.MethodDelete, "/api/v1/owner/comments/" + someULID},
		// posts
		{"posts", http.MethodPost, "/api/v1/owner/posts"},
		{"posts", http.MethodPatch, "/api/v1/owner/posts/" + post},
		{"posts", http.MethodDelete, "/api/v1/owner/posts/" + post},
		{"posts", http.MethodPut, "/api/v1/owner/posts/" + post + "/repost"},
		{"posts", http.MethodDelete, "/api/v1/owner/posts/" + post + "/repost"},
		{"posts", http.MethodPut, "/api/v1/owner/posts/" + post + "/pin"},
		{"posts", http.MethodDelete, "/api/v1/owner/posts/" + post + "/pin"},
		// site
		{"site", http.MethodPatch, "/api/v1/owner/site"},
		// media
		{"media", http.MethodPost, "/api/v1/owner/media"},
		{"media", http.MethodGet, "/api/v1/owner/media-uploads/" + someULID + "/poster"},
		// locations
		{"locations", http.MethodGet, "/api/v1/owner/locations/search?q=x"},
		{"locations", http.MethodGet, "/api/v1/owner/locations/nearby?lat=1&lng=1"},
		// links
		{"links", http.MethodGet, "/api/v1/owner/links/unfurl?url=https://example.test"},
		// doctor
		{"doctor", http.MethodGet, "/api/v1/owner/doctor"},
		// export
		{"export", http.MethodGet, "/api/v1/owner/export"},
		// import
		{"import", http.MethodPost, "/api/v1/owner/import/inspect"},
		{"import", http.MethodPost, "/api/v1/owner/import/commit"},
		{"import", http.MethodGet, "/api/v1/owner/import/status"},
		// owner_auth
		{"personal_tokens", http.MethodPost, "/api/v1/account/tokens"},
		// Ending a session is deliberately absent. It used to sit here, and
		// this list dutifully asserted that a pending account was refused it --
		// proving the bug rather than catching it, because the one action that
		// gives up a power was standing behind the refusal that guards powers.
		// TestPendingAccountKeepsItsOwnAllowedPowers covers it now.
		// accounts
		{"accounts", http.MethodGet, "/api/v1/owner/accounts"},
		{"accounts", http.MethodPatch, "/api/v1/owner/accounts/" + memberID},
	}

	for _, route := range routes {
		t.Run(route.group+" "+route.method+" "+route.path, func(t *testing.T) {
			request := httptest.NewRequest(route.method, route.path, bytes.NewReader([]byte(`{"status":"active"}`)))
			request.AddCookie(session)
			request.Header.Set("Content-Type", "application/json")
			// A valid CSRF token is supplied so the refusal is about who the
			// account is, not about a missing anti-forgery token. That is the
			// stronger assertion: even a perfectly-formed request is turned away.
			request.Header.Set(CSRFHeaderName, csrf)
			response := performRequest(fixture.engine, request)
			if response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
				t.Fatalf("%s %s = %d, want 401 or 403; body = %s",
					route.method, route.path, response.Code, response.Body.String())
			}
		})
	}
}

// TestPendingAccountCannotWriteAComment closes the highest-value vector: the
// comment write path is the one owner-scoped group that steps down to
// RequireActiveAccount, and it is exactly where an unapproved account would
// most like to slip a row in. The thread must be empty before and after.
func TestPendingAccountCannotWriteAComment(t *testing.T) {
	fixture := newFullEngineFixture(t)
	session, csrf, _ := fixture.signInPending(t)
	post := fixture.postID.String()

	create := httptest.NewRequest(http.MethodPost, "/api/v1/owner/posts/"+post+"/comments",
		bytes.NewReader([]byte(`{"content_md":"let me in"}`)))
	create.AddCookie(session)
	create.Header.Set("Content-Type", "application/json")
	create.Header.Set(CSRFHeaderName, csrf)
	response := performRequest(fixture.engine, create)
	if response.Code != http.StatusForbidden {
		t.Fatalf("pending comment write = %d, want 403; body = %s", response.Code, response.Body.String())
	}
	if code := decodeError(t, response.Body).Error.Code; code != string(domain.CodeAccountNotApproved) {
		t.Fatalf("error code = %q, want %q", code, domain.CodeAccountNotApproved)
	}

	// The public thread read confirms nothing was written.
	list := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/posts/"+post+"/comments", nil))
	if list.Code != http.StatusOK {
		t.Fatalf("thread read = %d", list.Code)
	}
	var thread struct {
		Data struct {
			Comments []json.RawMessage `json:"comments"`
		} `json:"data"`
	}
	if err := json.NewDecoder(list.Body).Decode(&thread); err != nil {
		t.Fatal(err)
	}
	if len(thread.Data.Comments) != 0 {
		t.Fatalf("thread holds %d comments, want none", len(thread.Data.Comments))
	}
}

// TestForgedSessionCookieIsRefused proves the browser credential is unforgeable:
// the only way to become another account would be to present a cookie whose
// signed payload names them, and the HMAC over that payload is what an attacker
// cannot reproduce. Every tampering shape fails closed at the signature, which
// is why the UserID cross-check inside resolve() is a backstop no request can
// reach rather than a load-bearing test.
func TestForgedSessionCookieIsRefused(t *testing.T) {
	fixture := newFullEngineFixture(t)
	session, _, _ := fixture.signInPending(t)

	owner, err := fixture.accounts.OwnerAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	parts := strings.Split(session.Value, ".")
	if len(parts) != 2 {
		t.Fatalf("session cookie is not two parts: %q", session.Value)
	}
	rawPayload, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]any
	if err := json.Unmarshal(rawPayload, &payload); err != nil {
		t.Fatal(err)
	}
	// Re-aim the payload at the owner, keeping the signature the server issued
	// for the original payload.
	payload["user_id"] = owner.ID
	reaimed, err := json.Marshal(payload)
	if err != nil {
		t.Fatal(err)
	}
	forgedPayload := base64.RawURLEncoding.EncodeToString(reaimed) + "." + parts[1]

	forgeries := map[string]string{
		"payload re-aimed at the owner keeps the old signature": forgedPayload,
		"signature replaced with a plausible value":             parts[0] + "." + base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x01}, 32)),
		"signature truncated":                                   parts[0] + "." + parts[1][:len(parts[1])-2],
		"payload and signature swapped":                         parts[1] + "." + parts[0],
	}
	for name, value := range forgeries {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
			request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: value})
			response := performRequest(fixture.engine, request)
			if response.Code != http.StatusUnauthorized {
				t.Fatalf("forged cookie %q = %d, want 401; body = %s", name, response.Code, response.Body.String())
			}
		})
	}
}

// TestPendingAccountKeepsItsOwnAllowedPowers is the control: the same credential
// that is refused everywhere above still reaches everything a pending account is
// meant to reach, so the refusals prove a boundary rather than a broken session.
func TestPendingAccountKeepsItsOwnAllowedPowers(t *testing.T) {
	fixture := newFullEngineFixture(t)
	session, csrf, _ := fixture.signInPending(t)

	read := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	read.AddCookie(session)
	if response := performRequest(fixture.engine, read); response.Code != http.StatusOK {
		t.Fatalf("account session read = %d", response.Code)
	}

	edit := httptest.NewRequest(http.MethodPatch, "/api/v1/account/profile",
		bytes.NewReader([]byte(`{"display_name":"Chosen Name"}`)))
	edit.AddCookie(session)
	edit.Header.Set("Content-Type", "application/json")
	edit.Header.Set(CSRFHeaderName, csrf)
	if response := performRequest(fixture.engine, edit); response.Code != http.StatusOK {
		t.Fatalf("profile edit = %d, body = %s", response.Code, performRequest(fixture.engine, edit).Body.String())
	}

	// Signing out was owner-scoped, so somebody who signed in through a
	// provider and landed unapproved could not sign out again: the refusal that
	// guards owner powers was standing in front of the one action that gives a
	// power up. Nothing an account does to its own session needs approval.
	out := httptest.NewRequest(http.MethodDelete, "/api/v1/login/session", nil)
	out.AddCookie(session)
	out.Header.Set(CSRFHeaderName, csrf)
	if response := performRequest(fixture.engine, out); response.Code != http.StatusOK {
		t.Fatalf("pending sign-out = %d, body = %s", response.Code, response.Body.String())
	}

	// And the session really ended, rather than the route merely answering.
	after := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	after.AddCookie(session)
	if response := performRequest(fixture.engine, after); response.Code != http.StatusUnauthorized {
		t.Fatalf("session survived its own sign-out = %d", response.Code)
	}
}
