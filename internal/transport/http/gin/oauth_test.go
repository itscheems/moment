package ginhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth/oauth"
	"github.com/moment/moment/internal/domain"
)

// stubProvider stands in for a real identity provider.
//
// The Google and GitHub adapters are verified against their own protocols
// elsewhere. What these tests are about is everything after a provider answers:
// whether the flow can be replayed, whether it can be pointed somewhere else,
// and what a brand new account is allowed to do. Substituting the provider is
// what lets those be asserted without a network.
type stubProvider struct {
	name     string
	identity app.ExternalIdentity
	fail     error
	lastCall struct {
		state    string
		verifier string
		nonce    string
	}
}

func (provider *stubProvider) Name() string  { return provider.name }
func (provider *stubProvider) Label() string { return strings.ToUpper(provider.name) }

func (provider *stubProvider) AuthCodeURL(state, verifier, nonce string) string {
	provider.lastCall.state = state
	provider.lastCall.verifier = verifier
	provider.lastCall.nonce = nonce
	return "https://provider.test/authorize?state=" + url.QueryEscape(state)
}

func (provider *stubProvider) Exchange(context.Context, string, string, string) (app.ExternalIdentity, error) {
	if provider.fail != nil {
		return app.ExternalIdentity{}, provider.fail
	}
	return provider.identity, nil
}

func newStubProvider() *stubProvider {
	return &stubProvider{
		name: "google",
		identity: app.ExternalIdentity{
			Provider:         "google",
			ProviderUserID:   "10769150350006150715113082367",
			Email:            "visitor@example.test",
			EmailVerified:    true,
			EmailDeliverable: true,
			DisplayName:      "Visitor Example",
		},
	}
}

// oauthFixture wires a real engine to a substituted provider.
type oauthFixture struct {
	authenticationTestFixture
	provider *stubProvider
	engine   http.Handler
}

func newOAuthFixture(t *testing.T) oauthFixture {
	t.Helper()
	base := newAuthenticationTestFixture(t, false)
	provider := newStubProvider()
	engine, err := New(Config{
		Assets:         testAssets(),
		SessionSigner:      base.manager,
		Accounts:       base.accounts,
		OAuthProviders: oauth.NewRegistry(provider),
		Comments:       nil,
		RequestID:      func() string { return "req_oauth" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return oauthFixture{authenticationTestFixture: base, provider: provider, engine: engine}
}

func (fixture oauthFixture) do(t *testing.T, request *http.Request) *httptest.ResponseRecorder {
	t.Helper()
	return performRequest(fixture.engine, request)
}

// startFlow performs the first leg and returns the state cookie the browser
// would now hold.
func (fixture oauthFixture) startFlow(t *testing.T, query string, cookies ...*http.Cookie) (*http.Cookie, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/login/oauth/google/start"+query, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := fixture.do(t, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("start = %d, body = %s", response.Code, response.Body.String())
	}
	var flow *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == oauthCookieName {
			flow = cookie
		}
	}
	if flow == nil || flow.Value == "" {
		t.Fatal("start did not set the flow cookie")
	}
	return flow, fixture.provider.lastCall.state
}

func callbackRequest(state, code string, flow *http.Cookie) *http.Request {
	target := "/api/v1/login/oauth/google/callback?code=" + url.QueryEscape(code) + "&state=" + url.QueryEscape(state)
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if flow != nil {
		request.AddCookie(flow)
	}
	return request
}

func sessionCookieFrom(response *httptest.ResponseRecorder) *http.Cookie {
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == SessionCookieName && cookie.Value != "" {
			return cookie
		}
	}
	return nil
}

func redirectError(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	if response.Code != http.StatusSeeOther {
		t.Fatalf("expected a redirect, got %d: %s", response.Code, response.Body.String())
	}
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	return location.Query().Get("auth_error")
}

func TestOAuthFlowIsBrowserBoundSingleUseAndCannotBeRedirectedOffSite(t *testing.T) {
	for name, probe := range map[string]struct {
		mutate func(state string, flow *http.Cookie) (string, *http.Cookie)
		want   domain.Code
	}{
		"a callback with no flow cookie is not a flow this server started": {
			mutate: func(state string, _ *http.Cookie) (string, *http.Cookie) { return state, nil },
			want:   domain.CodeOAuthStateInvalid,
		},
		"a state the browser did not receive is refused": {
			mutate: func(_ string, flow *http.Cookie) (string, *http.Cookie) { return "attacker-chosen-state", flow },
			want:   domain.CodeOAuthStateInvalid,
		},
		"a tampered flow cookie fails its signature": {
			mutate: func(state string, flow *http.Cookie) (string, *http.Cookie) {
				return state, &http.Cookie{Name: oauthCookieName, Value: flow.Value + "x"}
			},
			want: domain.CodeOAuthStateInvalid,
		},
		"a prefix of the state is not the state": {
			mutate: func(state string, flow *http.Cookie) (string, *http.Cookie) { return state[:len(state)-1], flow },
			want:   domain.CodeOAuthStateInvalid,
		},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newOAuthFixture(t)
			flow, state := fixture.startFlow(t, "")
			mutatedState, mutatedCookie := probe.mutate(state, flow)
			response := fixture.do(t, callbackRequest(mutatedState, "code", mutatedCookie))
			if got := redirectError(t, response); got != string(probe.want) {
				t.Fatalf("auth_error = %q, want %q", got, probe.want)
			}
			if sessionCookieFrom(response) != nil {
				t.Fatal("a refused sign-in still issued a session")
			}
		})
	}

	t.Run("a completed flow cannot be replayed", func(t *testing.T) {
		fixture := newOAuthFixture(t)
		flow, state := fixture.startFlow(t, "")
		first := fixture.do(t, callbackRequest(state, "code", flow))
		if sessionCookieFrom(first) == nil {
			t.Fatalf("first callback did not sign in: %d %s", first.Code, first.Header().Get("Location"))
		}
		// The browser is told to drop the cookie, so a replay arrives without
		// one. Presenting the old value again must also fail, which it does
		// because the code is spent at the provider -- but the state check is
		// what this asserts.
		cleared := false
		for _, cookie := range first.Result().Cookies() {
			if cookie.Name == oauthCookieName && cookie.MaxAge < 0 {
				cleared = true
			}
		}
		if !cleared {
			t.Fatal("the flow cookie was not cleared by the callback")
		}
	})

	t.Run("the return path cannot leave this site", func(t *testing.T) {
		for _, hostile := range []string{
			"https://evil.test/steal",
			"//evil.test/steal",
			"/\\evil.test",
			"/ok\r\nSet-Cookie: x=1",
		} {
			fixture := newOAuthFixture(t)
			flow, state := fixture.startFlow(t, "?return="+url.QueryEscape(hostile))
			response := fixture.do(t, callbackRequest(state, "code", flow))
			location := response.Header().Get("Location")
			if strings.Contains(location, "evil.test") || strings.Contains(location, "\n") {
				t.Fatalf("return path %q escaped to %q", hostile, location)
			}
		}
	})
}

func TestOAuthSignInCreatesAPendingAccountThatCannotWrite(t *testing.T) {
	fixture := newOAuthFixture(t)
	flow, state := fixture.startFlow(t, "")
	response := fixture.do(t, callbackRequest(state, "code", flow))

	session := sessionCookieFrom(response)
	if session == nil {
		t.Fatalf("sign-in issued no session: %d %s", response.Code, response.Header().Get("Location"))
	}
	if !strings.HasPrefix(response.Header().Get("Location"), "/?auth=ok") {
		t.Fatalf("location = %q", response.Header().Get("Location"))
	}

	// The account exists, is signed in, and is waiting for approval.
	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	request.AddCookie(session)
	current := fixture.do(t, request)
	if current.Code != http.StatusOK {
		t.Fatalf("account session = %d %s", current.Code, current.Body.String())
	}
	var envelope struct {
		Data struct {
			CSRFToken string `json:"csrf_token"`
			Account   struct {
				Username       string `json:"username"`
				Role           string `json:"role"`
				Status         string `json:"status"`
				UsernameChosen bool   `json:"username_chosen"`
			} `json:"account"`
		} `json:"data"`
	}
	if err := json.NewDecoder(current.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	account := envelope.Data.Account
	if account.Status != string(domain.AccountPending) {
		t.Fatalf("status = %q, want pending", account.Status)
	}
	if account.Role != string(domain.UserRoleMember) {
		t.Fatalf("role = %q, want member", account.Role)
	}
	if account.UsernameChosen {
		t.Fatal("a generated username was reported as chosen by the person")
	}
	if account.Username != "visitorexample" {
		t.Fatalf("username = %q, want one derived from the provider display name", account.Username)
	}
	if envelope.Data.CSRFToken == "" {
		t.Fatal("the browser was given no CSRF token to finish signing in with")
	}

	// Owner-only routes stay closed to it, with a valid session and a valid
	// CSRF token in hand. This is the whole point of approval.
	for _, probe := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/owner/accounts"},
		{http.MethodGet, "/api/v1/owner/accounts"},
		{http.MethodPost, "/api/v1/account/tokens"},
	} {
		request := httptest.NewRequest(probe.method, probe.path, nil)
		request.AddCookie(session)
		request.Header.Set(CSRFHeaderName, envelope.Data.CSRFToken)
		denied := fixture.do(t, request)
		if denied.Code != http.StatusUnauthorized && denied.Code != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want a refusal: %s", probe.method, probe.path, denied.Code, denied.Body.String())
		}
	}
}

func TestPendingAccountCannotPromoteItself(t *testing.T) {
	fixture := newOAuthFixture(t)
	flow, state := fixture.startFlow(t, "")
	signIn := fixture.do(t, callbackRequest(state, "code", flow))
	session := sessionCookieFrom(signIn)
	if session == nil {
		t.Fatal("sign-in issued no session")
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	request.AddCookie(session)
	var envelope struct {
		Data struct {
			CSRFToken string `json:"csrf_token"`
			Account   struct {
				UserID string `json:"user_id"`
			} `json:"account"`
		} `json:"data"`
	}
	if err := json.NewDecoder(fixture.do(t, request).Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	csrf := envelope.Data.CSRFToken

	// Every shape of "make me active" a profile edit could carry.
	for name, body := range map[string]string{
		"status through the profile editor": `{"status":"active"}`,
		"role through the profile editor":   `{"role":"owner"}`,
		"both at once":                      `{"status":"active","role":"owner"}`,
		"smuggled beside a legal field":     `{"display_name":"Visitor","status":"active"}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPatch, "/api/v1/account/profile", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			request.Header.Set(CSRFHeaderName, csrf)
			request.AddCookie(session)
			response := fixture.do(t, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400: %s", response.Code, response.Body.String())
			}
		})
	}

	// And the owner's own control refuses it too.
	escalate := httptest.NewRequest(http.MethodPatch,
		"/api/v1/owner/accounts/"+envelope.Data.Account.UserID, strings.NewReader(`{"status":"active"}`))
	escalate.Header.Set("Content-Type", "application/json")
	escalate.Header.Set(CSRFHeaderName, csrf)
	escalate.AddCookie(session)
	response := fixture.do(t, escalate)
	if response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
		t.Fatalf("self-approval = %d, want a refusal: %s", response.Code, response.Body.String())
	}
}

func TestOAuthStartedWhileSignedInLinksInsteadOfCreatingAnAccount(t *testing.T) {
	fixture := newOAuthFixture(t)
	ownerCookie, _ := exchangeAccountSession(t, fixture.authenticationTestFixture, fixture.token)

	flow, state := fixture.startFlow(t, "?return="+url.QueryEscape("/owner/settings"), ownerCookie)
	response := fixture.do(t, callbackRequest(state, "code", flow))
	if response.Code != http.StatusSeeOther {
		t.Fatalf("callback = %d", response.Code)
	}
	if location := response.Header().Get("Location"); !strings.HasPrefix(location, "/owner/settings?auth=linked") {
		t.Fatalf("location = %q, want a link outcome", location)
	}
	// Linking must not mint a second account.
	accounts, err := fixture.accounts.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 1 {
		t.Fatalf("accounts = %d, want the owner alone", len(accounts))
	}
	identities, err := fixture.accounts.Identities(context.Background(), accounts[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 || identities[0].Provider != "google" {
		t.Fatalf("identities = %+v", identities)
	}
}

func TestSuspendingAnAccountEndsItsSessionsImmediately(t *testing.T) {
	fixture := newOAuthFixture(t)
	flow, state := fixture.startFlow(t, "")
	session := sessionCookieFrom(fixture.do(t, callbackRequest(state, "code", flow)))
	if session == nil {
		t.Fatal("sign-in issued no session")
	}
	accounts, err := fixture.accounts.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var memberID string
	for _, account := range accounts {
		if account.Role == domain.UserRoleMember {
			memberID = account.ID
		}
	}
	if memberID == "" {
		t.Fatal("no member account was created")
	}
	if _, err := fixture.accounts.SetStatus(context.Background(), memberID, domain.AccountSuspended); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	request.AddCookie(session)
	response := fixture.do(t, request)
	if response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
		t.Fatalf("suspended account still reached its session: %d %s", response.Code, response.Body.String())
	}
}

func TestProviderListingReportsOnlyConfiguredProviders(t *testing.T) {
	fixture := newOAuthFixture(t)
	response := fixture.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/login/oauth/providers", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("providers = %d", response.Code)
	}
	var envelope struct {
		Data struct {
			Providers []oauth.Descriptor `json:"providers"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data.Providers) != 1 || envelope.Data.Providers[0].Name != "google" {
		t.Fatalf("providers = %+v", envelope.Data.Providers)
	}

	// An unconfigured provider is not a route that half works.
	unknown := fixture.do(t, httptest.NewRequest(http.MethodGet, "/api/v1/login/oauth/apple/start", nil))
	if got := redirectError(t, unknown); got != string(domain.CodeOAuthProviderUnavailable) {
		t.Fatalf("auth_error = %q", got)
	}
}

// startOAuthFlowOn performs the first leg against any engine that has federated
// sign-in registered, and reports the cookie and state a browser would hold.
func startOAuthFlowOn(t *testing.T, engine http.Handler) (*http.Cookie, string) {
	t.Helper()
	response := performRequest(engine, httptest.NewRequest(http.MethodGet, "/api/v1/login/oauth/google/start", nil))
	if response.Code != http.StatusSeeOther {
		t.Fatalf("start = %d, body = %s", response.Code, response.Body.String())
	}
	location, err := url.Parse(response.Header().Get("Location"))
	if err != nil {
		t.Fatal(err)
	}
	var flow *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == oauthCookieName {
			flow = cookie
		}
	}
	if flow == nil {
		t.Fatal("start did not set the flow cookie")
	}
	return flow, location.Query().Get("state")
}

// A sign-in re-reads the provider, and what it learns has to be able to repair
// a previous answer without being able to destroy one.
//
// The first sign-in is the only one that used to write these fields, so a
// transient failure at that moment -- GitHub rate limiting the address call, or
// an app registration that had lost the address scope -- became a permanent
// fact about the account. Refreshing fixes that, but the fold has to be one
// way: the same failure recurring must not erase an address that was already
// read successfully.
func TestSignInRepairsAnIdentityWithoutErasingWhatItAlreadyKnew(t *testing.T) {
	fixture := newOAuthFixture(t)

	// A first sign-in that could not read an address at all.
	fixture.provider.identity.Email = ""
	fixture.provider.identity.EmailVerified = false
	fixture.provider.identity.EmailDeliverable = false
	flow, state := fixture.startFlow(t, "")
	if sessionCookieFrom(fixture.do(t, callbackRequest(state, "code", flow))) == nil {
		t.Fatal("a sign-in with no address should still sign in")
	}
	member := memberAccount(t, fixture)
	if stored := onlyIdentity(t, fixture, member.ID); stored.Email != nil {
		t.Fatalf("an address appeared from nowhere: %v", *stored.Email)
	}

	// The next one reads it. The gap is repaired rather than carried forever.
	fixture.provider.identity.Email = "visitor@example.test"
	fixture.provider.identity.EmailVerified = true
	fixture.provider.identity.EmailDeliverable = true
	flow, state = fixture.startFlow(t, "")
	fixture.do(t, callbackRequest(state, "code", flow))
	repaired := onlyIdentity(t, fixture, member.ID)
	if repaired.Email == nil || *repaired.Email != "visitor@example.test" || !repaired.EmailVerified {
		t.Fatalf("a later sign-in did not repair the address: %+v", repaired)
	}

	// And the failure recurring does not take it back out again.
	fixture.provider.identity.Email = ""
	fixture.provider.identity.EmailVerified = false
	fixture.provider.identity.EmailDeliverable = false
	flow, state = fixture.startFlow(t, "")
	fixture.do(t, callbackRequest(state, "code", flow))
	kept := onlyIdentity(t, fixture, member.ID)
	if kept.Email == nil || *kept.Email != "visitor@example.test" {
		t.Fatalf("a transient failure erased a known address: %+v", kept)
	}
	if kept.LastLoginAtMS < repaired.LastLoginAtMS {
		t.Fatal("the sign-in was not recorded")
	}
}

func memberAccount(t *testing.T, fixture oauthFixture) domain.User {
	t.Helper()
	accounts, err := fixture.accounts.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if account.Role == domain.UserRoleMember {
			return account
		}
	}
	t.Fatal("no member account exists")
	return domain.User{}
}

func onlyIdentity(t *testing.T, fixture oauthFixture, userID string) domain.Identity {
	t.Helper()
	identities, err := fixture.accounts.Identities(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 {
		t.Fatalf("identities = %d, want exactly one", len(identities))
	}
	return identities[0]
}
