package ginhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth/oauth"
	"github.com/moment/moment/internal/domain"
)

// stubAssertionProvider is a provider whose client obtains its own credential.
//
// It embeds the plain stub so the browser flow keeps working through the same
// object, which is the arrangement in production: one adapter answering both
// ways rather than two adapters that could drift about who a subject is.
type stubAssertionProvider struct {
	*stubProvider
	accepted string
	native   bool
}

func newStubAssertionProvider() *stubAssertionProvider {
	return &stubAssertionProvider{stubProvider: newStubProvider(), accepted: "good-token", native: true}
}

func (provider *stubAssertionProvider) AcceptsAssertions() bool { return provider.native }

func (provider *stubAssertionProvider) NativeClientID() string {
	if !provider.native {
		return ""
	}
	return "native.apps.googleusercontent.com"
}

func (provider *stubAssertionProvider) VerifyAssertion(_ context.Context, assertion string) (app.ExternalIdentity, error) {
	if !provider.native {
		return app.ExternalIdentity{}, errors.New("no native client registered")
	}
	if assertion != provider.accepted {
		return app.ExternalIdentity{}, errors.New("assertion did not verify")
	}
	return provider.identity, nil
}

// assertionFixture wires an engine whose google speaks assertions and whose
// github does not.
type assertionFixture struct {
	authenticationTestFixture
	google *stubAssertionProvider
	github *stubProvider
	engine http.Handler
}

func newAssertionFixture(t *testing.T) assertionFixture {
	t.Helper()
	base := newAuthenticationTestFixture(t, false)
	google := newStubAssertionProvider()
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
		RequestID:      func() string { return "req_assert" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return assertionFixture{authenticationTestFixture: base, google: google, github: github, engine: engine}
}

func (fixture assertionFixture) postJSON(t *testing.T, target, body string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, target, strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json")
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	return performRequest(fixture.engine, request)
}

// A client that ran the authorization itself is signed in, and the session it
// is handed is the one every other path issues.
func TestAssertionSignInIssuesAUsableSession(t *testing.T) {
	fixture := newAssertionFixture(t)

	response := fixture.postJSON(t, "/api/v1/login/oauth/google/assertion", `{"id_token":"good-token"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("assertion sign-in = %d, body = %s", response.Code, response.Body.String())
	}
	value, _ := decodeDataMap(t, response)["value"].(string)
	if value == "" {
		t.Fatal("no session value was handed back")
	}

	// The whole claim of this route is that it is not a second way to be signed
	// in, so the credential has to be admitted by the ordinary cookie path.
	whoami := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	whoami.AddCookie(&http.Cookie{Name: SessionCookieName, Value: value})
	confirmed := performRequest(fixture.engine, whoami)
	if confirmed.Code != http.StatusOK {
		t.Fatalf("the issued session was not admitted: %d %s", confirmed.Code, confirmed.Body.String())
	}
	account, _ := decodeDataMap(t, confirmed)["account"].(map[string]any)
	if account["display_name"] != "Visitor Example" {
		t.Fatalf("the session named %#v", account)
	}
}

// A provider that issues nothing this deployment can verify answers on its own
// name rather than pretending the route is missing.
func TestAssertionSignInRefusesAProviderWithoutOne(t *testing.T) {
	fixture := newAssertionFixture(t)

	response := fixture.postJSON(t, "/api/v1/login/oauth/github/assertion", `{"id_token":"good-token"}`)
	if response.Code != http.StatusNotFound {
		t.Fatalf("github assertion = %d, body = %s", response.Code, response.Body.String())
	}

	// And the same is true of a provider that could, but has no native client
	// registered on this deployment.
	fixture.google.native = false
	unregistered := fixture.postJSON(t, "/api/v1/login/oauth/google/assertion", `{"id_token":"good-token"}`)
	if unregistered.Code != http.StatusNotFound {
		t.Fatalf("unregistered google assertion = %d, body = %s", unregistered.Code, unregistered.Body.String())
	}
}

// A credential that does not verify signs nobody in and creates no account.
func TestAssertionSignInRefusesAnUnverifiableToken(t *testing.T) {
	fixture := newAssertionFixture(t)

	response := fixture.postJSON(t, "/api/v1/login/oauth/google/assertion", `{"id_token":"forged"}`)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("forged assertion = %d, body = %s", response.Code, response.Body.String())
	}
	accounts, err := fixture.accounts.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if account.Role == domain.UserRoleMember {
			t.Fatal("a refused assertion created an account")
		}
	}
}

// The provider list says what each provider can actually be used for here, so a
// client stops carrying its own copy of that answer.
func TestProviderListPublishesFlows(t *testing.T) {
	fixture := newAssertionFixture(t)

	response := performRequest(fixture.engine,
		httptest.NewRequest(http.MethodGet, "/api/v1/login/oauth/providers", nil))
	if response.Code != http.StatusOK {
		t.Fatalf("provider list = %d", response.Code)
	}
	listed, _ := decodeDataMap(t, response)["providers"].([]any)
	flows := map[string][]string{}
	for _, entry := range listed {
		described, _ := entry.(map[string]any)
		name, _ := described["name"].(string)
		raw, _ := described["flows"].([]any)
		for _, flow := range raw {
			text, _ := flow.(string)
			flows[name] = append(flows[name], text)
		}
	}
	if !slices.Contains(flows["google"], oauth.FlowAssertion) {
		t.Fatalf("google did not advertise the assertion flow: %v", flows)
	}
	if slices.Contains(flows["github"], oauth.FlowAssertion) {
		t.Fatalf("github advertised an assertion flow it cannot serve: %v", flows)
	}
	// The native client id travels with the flow, because a self-hosted app
	// cannot carry one deployment's id and still reach another.
	for _, entry := range listed {
		described, _ := entry.(map[string]any)
		name, _ := described["name"].(string)
		published, present := described["native_client_id"]
		switch name {
		case "google":
			if published != "native.apps.googleusercontent.com" {
				t.Fatalf("google published %#v as its native client", published)
			}
		default:
			if present {
				t.Fatalf("%s published a native client id it has none of: %#v", name, published)
			}
		}
	}
	for name, advertised := range flows {
		if !slices.Contains(advertised, oauth.FlowBrowser) {
			t.Fatalf("%s did not advertise the browser flow: %v", name, advertised)
		}
	}
}

// signInAsMember completes one assertion sign-in and returns the account it
// made, the credential it was handed, and that session's CSRF token.
func (fixture assertionFixture) signInAsMember(t *testing.T) (domain.User, *http.Cookie, string) {
	t.Helper()
	response := fixture.postJSON(t, "/api/v1/login/oauth/google/assertion", `{"id_token":"good-token"}`)
	if response.Code != http.StatusOK {
		t.Fatalf("assertion sign-in = %d, body = %s", response.Code, response.Body.String())
	}
	value, _ := decodeDataMap(t, response)["value"].(string)
	session := &http.Cookie{Name: SessionCookieName, Value: value}
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

// A link request that arrives without the CSRF token the gate requires is
// refused.
//
// This is the case the route exists to make representable. The browser flow
// decides between signing in and linking by whether the caller was signed in,
// and VerifiedAccount -- which is how it asks -- reports "nobody" for a cookie
// session on an unsafe method with no CSRF token. Had this been one route
// deciding the same way, a link missing that header would not have failed: it
// would have signed a second account in and left the first one un-linked, with
// a 200 to say so.
func TestLinkIdentityWithoutCSRFIsRefusedRatherThanSigningIn(t *testing.T) {
	fixture := newAssertionFixture(t)
	member, session, _ := fixture.signInAsMember(t)

	before, err := fixture.accounts.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	response := fixture.postJSON(t, "/api/v1/account/identities/github", `{"device_code":"whatever"}`, session)
	if response.Code != http.StatusForbidden {
		t.Fatalf("a link with no CSRF token = %d, body = %s", response.Code, response.Body.String())
	}

	after, err := fixture.accounts.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(after) != len(before) {
		t.Fatalf("a refused link changed the account list: %d then %d", len(before), len(after))
	}
	identities, err := fixture.accounts.Identities(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(identities) != 1 {
		t.Fatalf("a refused link changed the account's identities: %#v", identities)
	}
}

// A second provider attaches to the account making the request, rather than
// becoming a second account.
func TestLinkIdentityByAssertionAttachesToTheCurrentAccount(t *testing.T) {
	fixture := newAssertionFixture(t)
	// Sign in with github through the browser so the account already exists,
	// then attach google to it with a credential the app obtained itself.
	base := newLinkFixtureFrom(t, fixture)
	member, session, csrf := base.member, base.session, base.csrf

	request := httptest.NewRequest(http.MethodPost, "/api/v1/account/identities/google",
		strings.NewReader(`{"id_token":"good-token"}`))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set(CSRFHeaderName, csrf)
	request.AddCookie(session)
	request.AddCookie(testConfirmationCookie(t, fixture.authenticationTestFixture, session))
	response := performRequest(fixture.engine, request)
	if response.Code != http.StatusOK {
		t.Fatalf("link = %d, body = %s", response.Code, response.Body.String())
	}
	if status, _ := decodeDataMap(t, response)["status"].(string); status != deviceLinked {
		t.Fatalf("link reported %q", status)
	}

	identities, err := fixture.accounts.Identities(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	linked := make([]string, 0, len(identities))
	for _, identity := range identities {
		linked = append(linked, identity.Provider)
	}
	slices.Sort(linked)
	if !slices.Equal(linked, []string{"github", "google"}) {
		t.Fatalf("account signs in with %v", linked)
	}

	accounts, err := fixture.accounts.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	members := 0
	for _, account := range accounts {
		if account.Role == domain.UserRoleMember {
			members++
		}
	}
	if members != 1 {
		t.Fatalf("linking produced %d member accounts", members)
	}
}

// linkedFixture is one signed-in member reached through the browser flow, which
// is the only way to arrive holding github rather than google.
type linkedFixture struct {
	member  domain.User
	session *http.Cookie
	csrf    string
}

func newLinkFixtureFrom(t *testing.T, fixture assertionFixture) linkedFixture {
	t.Helper()
	start := httptest.NewRequest(http.MethodGet, "/api/v1/login/oauth/github/start", nil)
	started := performRequest(fixture.engine, start)
	var flow *http.Cookie
	for _, cookie := range started.Result().Cookies() {
		if cookie.Name == oauthCookieName && cookie.Value != "" {
			flow = cookie
		}
	}
	if flow == nil {
		t.Fatal("github start set no flow cookie")
	}
	callback := httptest.NewRequest(http.MethodGet,
		"/api/v1/login/oauth/github/callback?code=code&state="+fixture.github.lastCall.state, nil)
	callback.AddCookie(flow)
	completed := performRequest(fixture.engine, callback)
	session := sessionCookieFrom(completed)
	if session == nil {
		t.Fatalf("github sign-in issued no session: %d", completed.Code)
	}
	accounts, err := fixture.accounts.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, account := range accounts {
		if account.Role == domain.UserRoleMember {
			return linkedFixture{member: account, session: session, csrf: csrfFor(t, fixture.engine, session)}
		}
	}
	t.Fatal("no member account was created")
	return linkedFixture{}
}

// A body naming neither credential, or both, is refused before any provider is
// asked.
func TestLinkIdentityRequiresExactlyOneCredential(t *testing.T) {
	fixture := newAssertionFixture(t)
	base := newLinkFixtureFrom(t, fixture)

	for name, body := range map[string]string{
		"neither": `{}`,
		"both":    `{"id_token":"good-token","device_code":"code"}`,
		"blank":   `{"id_token":"   "}`,
		"unknown": `{"assertion":"good-token"}`,
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/account/identities/google", strings.NewReader(body))
		request.Header.Set("Content-Type", "application/json")
		request.Header.Set(CSRFHeaderName, base.csrf)
		request.AddCookie(base.session)
		request.AddCookie(testConfirmationCookie(t, fixture.authenticationTestFixture, base.session))
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusBadRequest {
			t.Fatalf("%s = %d, body = %s", name, response.Code, response.Body.String())
		}
	}
}

func TestUnlinkingAProviderThatIsNotAttachedIsNotAnAuthFailure(t *testing.T) {
	fixture := newAssertionFixture(t)
	base := newLinkFixtureFrom(t, fixture)

	request := httptest.NewRequest(http.MethodDelete, "/api/v1/account/identities/google", nil)
	request.Header.Set(CSRFHeaderName, base.csrf)
	request.AddCookie(base.session)
	request.AddCookie(testConfirmationCookie(t, fixture.authenticationTestFixture, base.session))
	response := performRequest(fixture.engine, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("unlinking an unattached provider = %d, body = %s", response.Code, response.Body.String())
	}
	if code := decodeError(t, response.Body).Error.Code; code != string(domain.CodeIdentityNotLinked) {
		t.Fatalf("code was %q", code)
	}

	// The one that is attached still comes off, so the new code did not narrow
	// what the route does.
	removal := httptest.NewRequest(http.MethodDelete, "/api/v1/account/identities/github", nil)
	removal.Header.Set(CSRFHeaderName, base.csrf)
	removal.AddCookie(base.session)
	removal.AddCookie(testConfirmationCookie(t, fixture.authenticationTestFixture, base.session))
	// The account signs in only with github, so removing it would leave no way
	// back in -- which is its own refusal, and a different one.
	if removed := performRequest(fixture.engine, removal); removed.Code != http.StatusConflict {
		t.Fatalf("removing the only credential = %d, body = %s", removed.Code, removed.Body.String())
	}
}
