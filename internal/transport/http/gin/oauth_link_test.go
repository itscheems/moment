package ginhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth/oauth"
	"github.com/moment/moment/internal/domain"
)

// linkFixture wires two providers, because attaching one is only expressible
// when the account can already sign in with the other.
type linkFixture struct {
	authenticationTestFixture
	google *stubProvider
	github *stubProvider
	engine http.Handler
}

func newLinkFixture(t *testing.T) linkFixture {
	t.Helper()
	base := newAuthenticationTestFixture(t, false)
	google := newStubProvider()
	github := &stubProvider{
		name: "github",
		identity: app.ExternalIdentity{
			Provider:       "github",
			ProviderUserID: "216108319",
			DisplayName:    "Visitor Elsewhere",
		},
	}
	engine, err := New(Config{
		Assets:         testAssets(),
		SessionSigner:  base.manager,
		Accounts:       base.accounts,
		OAuthProviders: oauth.NewRegistry(google, github),
		RequestID:      func() string { return "req_link" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return linkFixture{authenticationTestFixture: base, google: google, github: github, engine: engine}
}

func (fixture linkFixture) start(t *testing.T, provider *stubProvider, query string, cookies ...*http.Cookie) (*http.Cookie, string) {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/login/oauth/"+provider.name+"/start"+query, nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	response := performRequest(fixture.engine, request)
	if response.Code != http.StatusSeeOther {
		t.Fatalf("start = %d, body = %s", response.Code, response.Body.String())
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == oauthCookieName && cookie.Value != "" {
			return cookie, provider.lastCall.state
		}
	}
	t.Fatal("start did not set the flow cookie")
	return nil, ""
}

func (fixture linkFixture) callback(t *testing.T, provider *stubProvider, state string, flow *http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	target := "/api/v1/login/oauth/" + provider.name + "/callback?code=code&state=" + url.QueryEscape(state)
	request := httptest.NewRequest(http.MethodGet, target, nil)
	if flow != nil {
		request.AddCookie(flow)
	}
	return performRequest(fixture.engine, request)
}

// signInAsMember completes one google sign-in and returns the account it made
// along with the browser credential it was handed.
func (fixture linkFixture) signInAsMember(t *testing.T) (domain.User, *http.Cookie, string) {
	t.Helper()
	flow, state := fixture.start(t, fixture.google, "")
	response := fixture.callback(t, fixture.google, state, flow)
	session := sessionCookieFrom(response)
	if session == nil {
		t.Fatalf("sign-in issued no session: %d %s", response.Code, response.Header().Get("Location"))
	}
	accounts, err := fixture.accounts.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if account.Role == domain.UserRoleMember {
			return account, session, csrfFor(t, fixture.engine, session)
		}
	}
	t.Fatal("no member account was created")
	return domain.User{}, nil, ""
}

func csrfFor(t *testing.T, engine http.Handler, session *http.Cookie) string {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	request.AddCookie(session)
	response := performRequest(engine, request)
	if response.Code != http.StatusOK {
		t.Fatalf("account session = %d, body = %s", response.Code, response.Body.String())
	}
	token, ok := decodeDataMap(t, response)["csrf_token"].(string)
	if !ok || token == "" {
		t.Fatal("the account session carried no CSRF token")
	}
	return token
}

func identitiesOf(t *testing.T, fixture linkFixture, userID string) []domain.Identity {
	t.Helper()
	identities, err := fixture.accounts.Identities(context.Background(), userID)
	if err != nil {
		t.Fatal(err)
	}
	return identities
}

// A cookie somebody has signed out of must not still be able to buy a
// permanent new way into the account it names.
//
// Signing out revokes the row; the signed value in the browser is untouched and
// stays verifiable for the rest of its life. Starting a sign-in used to read
// the account straight out of that value, so a copy of a dead cookie could
// still say "attach my provider account to this one" -- and the credential it
// attached would outlive the logout, the cookie's own expiry, and any later
// suspension.
func TestStartOAuthWithASignedOutCookieAttachesNothing(t *testing.T) {
	fixture := newLinkFixture(t)
	accountCookie, session := exchangeAccountSession(t, fixture.authenticationTestFixture, fixture.token)
	csrf, _ := session["csrf_token"].(string)

	logout := httptest.NewRequest(http.MethodDelete, "/api/v1/login/session", nil)
	logout.AddCookie(accountCookie)
	logout.Header.Set(CSRFHeaderName, csrf)
	if response := performRequest(fixture.engine, logout); response.Code != http.StatusOK {
		t.Fatalf("logout = %d, body = %s", response.Code, response.Body.String())
	}

	// The browser kept its copy. Everything below is that copy being used.
	flow, state := fixture.start(t, fixture.github, "?return="+url.QueryEscape("/owner/settings"), accountCookie)
	response := fixture.callback(t, fixture.github, state, flow)
	if location := response.Header().Get("Location"); strings.Contains(location, "auth=linked") {
		t.Fatalf("a signed-out cookie linked a provider: %q", location)
	}

	primaryAccount, err := fixture.accounts.ProfileAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if attached := identitiesOf(t, fixture, primaryAccount.ID); len(attached) != 0 {
		t.Fatalf("a signed-out cookie attached %d identities to the owner: %+v", len(attached), attached)
	}
}

// The same, for an account suspended while its sign-in was already in flight.
//
// The flow cookie is sealed at /start and answered up to ten minutes later, so
// the authority that authorised it is by then a statement about the past. The
// account layer asks again at the moment it writes, which is the only moment
// the answer has to be true.
func TestLinkingRefusedForAnAccountSuspendedAfterTheFlowBegan(t *testing.T) {
	fixture := newLinkFixture(t)
	member, session, _ := fixture.signInAsMember(t)

	flow, state := fixture.start(t, fixture.github, "", session)
	if _, err := fixture.accounts.SetStatus(context.Background(), member.ID, domain.AccountSuspended); err != nil {
		t.Fatal(err)
	}

	response := fixture.callback(t, fixture.github, state, flow)
	if got := redirectError(t, response); got != string(domain.CodeAccountSuspended) {
		t.Fatalf("auth_error = %q, want %q", got, domain.CodeAccountSuspended)
	}
	identities := identitiesOf(t, fixture, member.ID)
	if len(identities) != 1 || identities[0].Provider != "google" {
		t.Fatalf("a suspended account gained a credential: %+v", identities)
	}
}

// A pending account may still attach a second provider.
//
// This is the same tier RequireAccount already admits pending accounts to:
// adding another way to reach your own account reaches nobody else, and
// somebody waiting on approval is exactly who most wants a second way back in.
func TestPendingAccountMayLinkASecondProvider(t *testing.T) {
	fixture := newLinkFixture(t)
	member, session, _ := fixture.signInAsMember(t)
	if member.Status != domain.AccountPending {
		t.Fatalf("status = %q, want a pending account to link from", member.Status)
	}

	flow, state := fixture.start(t, fixture.github, "?return="+url.QueryEscape("/owner/settings"), session)
	response := fixture.callback(t, fixture.github, state, flow)
	if location := response.Header().Get("Location"); !strings.HasPrefix(location, "/owner/settings?auth=linked") {
		t.Fatalf("location = %q, want a link outcome", location)
	}
	identities := identitiesOf(t, fixture, member.ID)
	if len(identities) != 2 {
		t.Fatalf("identities = %+v, want google and github", identities)
	}
	accounts, err := fixture.accounts.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(accounts) != 2 {
		t.Fatalf("accounts = %d, want the owner and one member", len(accounts))
	}
}

// One provider per account, so every credential an account holds is one the
// person can point at and remove.
//
// The list names a provider and the remove route is keyed by one, so a second
// GitHub identity would be a credential with no way to address it.
func TestAnAccountCannotHoldTwoIdentitiesFromOneProvider(t *testing.T) {
	fixture := newLinkFixture(t)
	member, session, _ := fixture.signInAsMember(t)

	flow, state := fixture.start(t, fixture.github, "", session)
	if response := fixture.callback(t, fixture.github, state, flow); redirectError(t, response) != "" {
		t.Fatalf("the first github link was refused: %s", response.Header().Get("Location"))
	}

	fixture.github.identity.ProviderUserID = "999999999"
	flow, state = fixture.start(t, fixture.github, "", session)
	response := fixture.callback(t, fixture.github, state, flow)
	if got := redirectError(t, response); got != string(domain.CodeIdentityAlreadyLinked) {
		t.Fatalf("auth_error = %q, want %q", got, domain.CodeIdentityAlreadyLinked)
	}
	if identities := identitiesOf(t, fixture, member.ID); len(identities) != 2 {
		t.Fatalf("identities = %+v, want exactly google and one github", identities)
	}
}
