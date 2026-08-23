package ginhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth/oauth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/observability"
)

// stubDeviceProvider is a provider that can sign a person in with no browser.
//
// The GitHub adapter is verified against GitHub's own protocol elsewhere. What
// these tests are about is everything after it answers: whether a device login
// obeys the same account rules the browser callback does, and whether what it
// hands back is a credential this server will actually honour.
type stubDeviceProvider struct {
	stubProvider
	deviceCode string
	// answers is the queue of poll outcomes, oldest first. A nil entry is an
	// approval, which is what lets a whole wait be staged in one line.
	answers []error
	polls   int
}

func newStubDeviceProvider() *stubDeviceProvider {
	provider := &stubDeviceProvider{deviceCode: "device-code-the-server-issued"}
	provider.name = "github"
	provider.identity = app.ExternalIdentity{
		Provider:         "github",
		ProviderUserID:   "583231",
		Email:            "octocat@example.test",
		EmailVerified:    true,
		EmailDeliverable: true,
		DisplayName:      "The Octocat",
	}
	provider.answers = []error{nil}
	return provider
}

func (provider *stubDeviceProvider) StartDevice(context.Context) (oauth.DeviceAuthorization, error) {
	return oauth.DeviceAuthorization{
		DeviceCode:      provider.deviceCode,
		UserCode:        "WDJB-MJHT",
		VerificationURI: "https://provider.test/device",
		Interval:        5_000_000_000,
		ExpiresIn:       900_000_000_000,
	}, nil
}

func (provider *stubDeviceProvider) PollDevice(_ context.Context, deviceCode string) (app.ExternalIdentity, error) {
	provider.polls++
	if deviceCode != provider.deviceCode {
		return app.ExternalIdentity{}, oauth.ErrDeviceCodeInvalid
	}
	var next error
	if len(provider.answers) > 0 {
		next = provider.answers[0]
		provider.answers = provider.answers[1:]
	}
	if next != nil {
		return app.ExternalIdentity{}, next
	}
	if provider.fail != nil {
		return app.ExternalIdentity{}, provider.fail
	}
	return provider.identity, nil
}

// deviceFixture wires a real engine to a device-capable provider.
type deviceFixture struct {
	ownerTestFixture
	provider *stubDeviceProvider
	engine   http.Handler
	events   *deviceEventLog
}

type deviceEventLog struct {
	mu      sync.Mutex
	records []deviceEvent
}

type deviceEvent struct {
	surface observability.Surface
	event   string
	fields  map[string]any
}

func (log *deviceEventLog) append(surface observability.Surface, _, event string, fields map[string]any) {
	log.mu.Lock()
	defer log.mu.Unlock()
	log.records = append(log.records, deviceEvent{surface: surface, event: event, fields: fields})
}

func (log *deviceEventLog) find(event string) (deviceEvent, bool) {
	log.mu.Lock()
	defer log.mu.Unlock()
	for _, record := range log.records {
		if record.event == event {
			return record, true
		}
	}
	return deviceEvent{}, false
}

func newDeviceFixture(t *testing.T) deviceFixture {
	t.Helper()
	base := newOwnerTestFixture(t, false)
	provider := newStubDeviceProvider()
	// A browser-only provider sits beside it so "this provider cannot sign
	// anyone in without a browser" is a case these tests can actually reach.
	browserOnly := newStubProvider()
	events := &deviceEventLog{}
	engine, err := New(Config{
		Assets:         testAssets(),
		OwnerAuth:      base.manager,
		Accounts:       base.accounts,
		OAuthProviders: oauth.NewRegistry(provider, browserOnly),
		RequestID:      func() string { return "req_device" },
		SurfaceLog:     events.append,
	})
	if err != nil {
		t.Fatal(err)
	}
	return deviceFixture{ownerTestFixture: base, provider: provider, engine: engine, events: events}
}

func (fixture deviceFixture) start(t *testing.T, provider string) *httptest.ResponseRecorder {
	t.Helper()
	return performRequest(fixture.engine,
		httptest.NewRequest(http.MethodPost, "/api/v1/login/device/"+provider+"/start", nil))
}

func (fixture deviceFixture) poll(t *testing.T, provider, deviceCode string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(map[string]string{"device_code": deviceCode})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/login/device/"+provider+"/poll", strings.NewReader(string(body)))
	request.Header.Set("Content-Type", "application/json")
	return performRequest(fixture.engine, request)
}

// deviceStarted runs the first leg and reports the device code a caller holds.
func (fixture deviceFixture) deviceStarted(t *testing.T) string {
	t.Helper()
	response := fixture.start(t, "github")
	if response.Code != http.StatusOK {
		t.Fatalf("start = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data deviceStartView `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data.DeviceCode
}

func decodeDevicePoll(t *testing.T, response *httptest.ResponseRecorder) devicePollView {
	t.Helper()
	if response.Code != http.StatusOK {
		t.Fatalf("poll = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data devicePollView `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	return envelope.Data
}

func deviceFailureCode(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	var envelope errorEnvelope
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode failure: %v", err)
	}
	return envelope.Error.Code
}

// Both routes exist so a caller holding nothing can get a credential. If either
// one required a credential the flow would be unusable by construction, and the
// way that breaks is silent: a gate answers 401 and a person reads it as their
// account being wrong.
func TestDeviceLoginIsReachableWithNoCredentialAtAll(t *testing.T) {
	fixture := newDeviceFixture(t)

	response := fixture.start(t, "github")
	if response.Code != http.StatusOK {
		t.Fatalf("start = %d, body = %s", response.Code, response.Body.String())
	}
	var envelope struct {
		Data deviceStartView `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	started := envelope.Data
	if started.UserCode != "WDJB-MJHT" || started.VerificationURI != "https://provider.test/device" {
		t.Fatalf("started = %+v; a person with no browser has nothing to act on without both", started)
	}
	if started.DeviceCode == "" {
		t.Fatal("the device code was not returned, so nothing can poll for it")
	}
	if started.IntervalSeconds != 5 || started.ExpiresInSeconds != 900 {
		t.Fatalf("interval = %d, expiry = %d", started.IntervalSeconds, started.ExpiresInSeconds)
	}

	// And polling needs no credential either.
	poll := fixture.poll(t, "github", started.DeviceCode)
	if poll.Code == http.StatusUnauthorized {
		t.Fatalf("the poll demanded a credential the caller cannot have yet: %s", poll.Body.String())
	}
}

func TestDeviceLoginWaitsWithoutHandingOutAnythingToSignInWith(t *testing.T) {
	fixture := newDeviceFixture(t)
	fixture.provider.answers = []error{
		oauth.ErrDevicePending,
		&oauth.SlowDown{Interval: 10_000_000_000},
		nil,
	}
	deviceCode := fixture.deviceStarted(t)

	pending := decodeDevicePoll(t, fixture.poll(t, "github", deviceCode))
	if pending.Status != devicePending {
		t.Fatalf("status = %q, want pending", pending.Status)
	}
	// The whole point of the nested object: a caller that ignores status finds
	// nothing to hand on rather than an empty string it could mistake for one.
	if pending.Session != nil {
		t.Fatalf("a pending poll carried session material: %+v", pending.Session)
	}

	slowed := decodeDevicePoll(t, fixture.poll(t, "github", deviceCode))
	if slowed.Status != deviceSlowDown {
		t.Fatalf("status = %q, want slow_down", slowed.Status)
	}
	// The new interval has to reach the caller. A provider that says slow down
	// and is not listened to rate-limits the deployment off its own endpoint.
	if slowed.IntervalSeconds != 10 {
		t.Fatalf("interval = %d, want the 10 the provider asked for", slowed.IntervalSeconds)
	}
	if slowed.Session != nil {
		t.Fatalf("a slow_down poll carried session material: %+v", slowed.Session)
	}

	authorized := decodeDevicePoll(t, fixture.poll(t, "github", deviceCode))
	if authorized.Status != deviceAuthorized || authorized.Session == nil || authorized.Session.Value == "" {
		t.Fatalf("authorized poll = %+v", authorized)
	}
}

// The session handed back as JSON has to be the same credential a browser gets
// in a Set-Cookie header, or the CLI is holding something only this endpoint
// understands.
func TestDeviceLoginSessionAuthenticatesThroughTheGateCookiePath(t *testing.T) {
	fixture := newDeviceFixture(t)
	deviceCode := fixture.deviceStarted(t)
	authorized := decodeDevicePoll(t, fixture.poll(t, "github", deviceCode))
	if authorized.Session == nil {
		t.Fatal("the poll issued no session")
	}
	// Nothing was set on the response as a cookie: a caller with no cookie jar
	// would drop it, and this endpoint answers callers that have none.
	for _, cookie := range fixture.poll(t, "github", deviceCode).Result().Cookies() {
		if cookie.Name == SessionCookieName {
			t.Fatal("the device poll set a session cookie a CLI cannot keep")
		}
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: authorized.Session.Value})
	current := performRequest(fixture.engine, request)
	if current.Code != http.StatusOK {
		t.Fatalf("account session = %d %s", current.Code, current.Body.String())
	}
	var envelope struct {
		Data accountSessionView `json:"data"`
	}
	if err := json.NewDecoder(current.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Data.Account.Username == "" || envelope.Data.Account.Status != string(domain.AccountPending) {
		t.Fatalf("account = %+v", envelope.Data.Account)
	}
	if envelope.Data.CSRFToken != authorized.Session.CSRFToken {
		t.Fatalf("the poll reported a CSRF token the session does not carry: %q vs %q",
			authorized.Session.CSRFToken, envelope.Data.CSRFToken)
	}

	// And an unsafe request works with the CSRF token the poll reported, which
	// is what makes the credential usable for more than reading.
	edit := httptest.NewRequest(http.MethodPatch, "/api/v1/account/profile",
		strings.NewReader(`{"display_name":"Octo"}`))
	edit.Header.Set("Content-Type", "application/json")
	edit.Header.Set(CSRFHeaderName, authorized.Session.CSRFToken)
	edit.AddCookie(&http.Cookie{Name: SessionCookieName, Value: authorized.Session.Value})
	if response := performRequest(fixture.engine, edit); response.Code != http.StatusOK {
		t.Fatalf("profile edit = %d %s", response.Code, response.Body.String())
	}

	// Without the header it is refused, exactly as a browser would be. A device
	// login must not be a way around CSRF either.
	naked := httptest.NewRequest(http.MethodPatch, "/api/v1/account/profile",
		strings.NewReader(`{"display_name":"Octo"}`))
	naked.Header.Set("Content-Type", "application/json")
	naked.AddCookie(&http.Cookie{Name: SessionCookieName, Value: authorized.Session.Value})
	if response := performRequest(fixture.engine, naked); response.Code != http.StatusForbidden {
		t.Fatalf("CSRF-less edit = %d, want 403", response.Code)
	}
}

// Signing in from a terminal is not a way around approval. The account this
// creates is pending exactly as a browser sign-in's would be, and the session
// it holds reaches nothing an approved account reaches.
func TestDeviceLoginCannotBypassApproval(t *testing.T) {
	fixture := newDeviceFixture(t)
	deviceCode := fixture.deviceStarted(t)
	authorized := decodeDevicePoll(t, fixture.poll(t, "github", deviceCode))
	if authorized.Session == nil {
		t.Fatal("the poll issued no session")
	}

	member := memberAccountOf(t, fixture.accounts)
	if member.Status != domain.AccountPending {
		t.Fatalf("a device login created a %s account; approval was skipped", member.Status)
	}
	if member.Role != domain.UserRoleMember {
		t.Fatalf("role = %q, want member", member.Role)
	}

	for _, probe := range []struct {
		method string
		path   string
	}{
		{http.MethodGet, "/api/v1/owner/accounts"},
		{http.MethodGet, "/api/v1/owner/status"},
		{http.MethodPost, "/api/v1/owner/token/rotate"},
	} {
		request := httptest.NewRequest(probe.method, probe.path, nil)
		request.AddCookie(&http.Cookie{Name: SessionCookieName, Value: authorized.Session.Value})
		request.Header.Set(CSRFHeaderName, authorized.Session.CSRFToken)
		denied := performRequest(fixture.engine, request)
		if denied.Code != http.StatusUnauthorized && denied.Code != http.StatusForbidden {
			t.Fatalf("%s %s = %d, want a refusal: %s", probe.method, probe.path, denied.Code, denied.Body.String())
		}
	}
}

func TestDeviceLoginHonoursRegistrationMode(t *testing.T) {
	t.Run("a closed site admits nobody new", func(t *testing.T) {
		fixture := newDeviceFixture(t)
		fixture.store.mode = domain.RegistrationClosed
		deviceCode := fixture.deviceStarted(t)

		response := fixture.poll(t, "github", deviceCode)
		if response.Code != http.StatusForbidden {
			t.Fatalf("poll = %d, want 403: %s", response.Code, response.Body.String())
		}
		if code := deviceFailureCode(t, response); code != string(domain.CodeRegistrationClosed) {
			t.Fatalf("code = %q", code)
		}
		if accounts, err := fixture.accounts.List(context.Background()); err != nil || len(accounts) != 1 {
			t.Fatalf("accounts = %d (err %v), want the owner alone", len(accounts), err)
		}
	})

	t.Run("an open site admits an active account", func(t *testing.T) {
		fixture := newDeviceFixture(t)
		fixture.store.mode = domain.RegistrationOpen
		deviceCode := fixture.deviceStarted(t)

		if decodeDevicePoll(t, fixture.poll(t, "github", deviceCode)).Status != deviceAuthorized {
			t.Fatal("an open site refused a device sign-in")
		}
		if member := memberAccountOf(t, fixture.accounts); member.Status != domain.AccountActive {
			t.Fatalf("status = %q, want active on an open site", member.Status)
		}
	})

	t.Run("a suspended account cannot sign back in", func(t *testing.T) {
		fixture := newDeviceFixture(t)
		fixture.provider.answers = []error{nil, nil}
		deviceCode := fixture.deviceStarted(t)
		if decodeDevicePoll(t, fixture.poll(t, "github", deviceCode)).Status != deviceAuthorized {
			t.Fatal("the first device sign-in failed")
		}
		member := memberAccountOf(t, fixture.accounts)
		if _, err := fixture.accounts.SetStatus(context.Background(), member.ID, domain.AccountSuspended); err != nil {
			t.Fatal(err)
		}

		response := fixture.poll(t, "github", deviceCode)
		if response.Code != http.StatusForbidden {
			t.Fatalf("poll = %d, want 403: %s", response.Code, response.Body.String())
		}
		if code := deviceFailureCode(t, response); code != string(domain.CodeAccountSuspended) {
			t.Fatalf("code = %q", code)
		}
	})
}

func TestDeviceLoginRefusesACodeThisFlowNeverIssued(t *testing.T) {
	fixture := newDeviceFixture(t)
	fixture.deviceStarted(t)

	response := fixture.poll(t, "github", "a-code-nobody-issued")
	if response.Code != http.StatusBadRequest {
		t.Fatalf("poll = %d, want 400: %s", response.Code, response.Body.String())
	}
	if code := deviceFailureCode(t, response); code != string(domain.CodeOAuthStateInvalid) {
		t.Fatalf("code = %q", code)
	}
	if accounts, err := fixture.accounts.List(context.Background()); err != nil || len(accounts) != 1 {
		t.Fatalf("an invented device code created an account: %d (err %v)", len(accounts), err)
	}

	// A missing or malformed body never reaches the provider at all.
	for name, body := range map[string]string{
		"no body":             ``,
		"an empty code":       `{"device_code":"   "}`,
		"a field we invented": `{"device_code":"x","scope":"admin"}`,
	} {
		t.Run(name, func(t *testing.T) {
			before := fixture.provider.polls
			request := httptest.NewRequest(http.MethodPost, "/api/v1/login/device/github/poll", strings.NewReader(body))
			request.Header.Set("Content-Type", "application/json")
			response := performRequest(fixture.engine, request)
			if response.Code != http.StatusBadRequest {
				t.Fatalf("poll = %d, want 400: %s", response.Code, response.Body.String())
			}
			if name != "a field we invented" && fixture.provider.polls != before {
				t.Fatal("a malformed poll reached the provider")
			}
		})
	}
}

func TestDeviceLoginReportsEveryTerminalOutcomeDistinctly(t *testing.T) {
	// Expiry, refusal and a provider fault mean three different things to
	// whoever is waiting. Collapsing them makes a CLI print the wrong advice.
	for name, probe := range map[string]struct {
		answer error
		status int
		code   domain.Code
	}{
		"the code expired":   {answer: oauth.ErrDeviceExpired, status: http.StatusBadRequest, code: domain.CodeOAuthDeviceExpired},
		"a person said no":   {answer: oauth.ErrDeviceDenied, status: http.StatusForbidden, code: domain.CodeOAuthDeviceDenied},
		"the provider broke": {answer: oauth.ErrUnavailable, status: http.StatusBadGateway, code: domain.CodeOAuthExchangeFailed},
	} {
		t.Run(name, func(t *testing.T) {
			fixture := newDeviceFixture(t)
			fixture.provider.answers = []error{probe.answer}
			deviceCode := fixture.deviceStarted(t)

			response := fixture.poll(t, "github", deviceCode)
			if response.Code != probe.status {
				t.Fatalf("poll = %d, want %d: %s", response.Code, probe.status, response.Body.String())
			}
			if code := deviceFailureCode(t, response); code != string(probe.code) {
				t.Fatalf("code = %q, want %q", code, probe.code)
			}
			if accounts, err := fixture.accounts.List(context.Background()); err != nil || len(accounts) != 1 {
				t.Fatalf("a refused poll created an account: %d (err %v)", len(accounts), err)
			}
		})
	}
}

// A provider with only a browser flow is not half available here. Answering
// "start" for it would hand a caller a flow that can never complete.
func TestDeviceLoginRefusesAProviderThatHasNoDeviceFlow(t *testing.T) {
	fixture := newDeviceFixture(t)
	for _, provider := range []string{"google", "apple"} {
		if response := fixture.start(t, provider); response.Code != http.StatusNotFound {
			t.Fatalf("%s start = %d, want 404: %s", provider, response.Code, response.Body.String())
		}
		if response := fixture.poll(t, provider, "anything"); response.Code != http.StatusNotFound {
			t.Fatalf("%s poll = %d, want 404: %s", provider, response.Code, response.Body.String())
		}
	}
	// The browser flow for that provider is untouched.
	if response := performRequest(fixture.engine,
		httptest.NewRequest(http.MethodGet, "/api/v1/login/oauth/google/start", nil)); response.Code != http.StatusSeeOther {
		t.Fatalf("browser start = %d", response.Code)
	}
}

// A session's surface is what the account page shows a person about where they
// are signed in from. A terminal login recorded as web would make that lie, and
// requestSurface cannot tell: this request carries neither of the two things it
// reads.
func TestDeviceLoginRecordsItselfAsATerminalRatherThanTheBrowser(t *testing.T) {
	fixture := newDeviceFixture(t)
	deviceCode := fixture.deviceStarted(t)
	authorized := decodeDevicePoll(t, fixture.poll(t, "github", deviceCode))
	if authorized.Session == nil {
		t.Fatal("the poll issued no session")
	}

	member := memberAccountOf(t, fixture.accounts)
	sessions, err := fixture.accounts.Sessions(context.Background(), member.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(sessions) != 1 {
		t.Fatalf("sessions = %d, want exactly one", len(sessions))
	}
	if sessions[0].Surface != string(observability.SurfaceCLI) {
		t.Fatalf("session surface = %q, want cli", sessions[0].Surface)
	}

	issued, found := fixture.events.find("auth_session_issued")
	if !found {
		t.Fatal("a device sign-in recorded no auth event")
	}
	if issued.surface != observability.SurfaceCLI {
		t.Fatalf("event surface = %q, want cli", issued.surface)
	}
	if issued.fields["method"] != "oauth_device" || issued.fields["provider"] != "github" {
		t.Fatalf("event fields = %#v", issued.fields)
	}
	if _, found := fixture.events.find("account_created"); !found {
		t.Fatal("a first device sign-in recorded no account_created event")
	}
}

func memberAccountOf(t *testing.T, accounts *app.AccountService) domain.User {
	t.Helper()
	users, err := accounts.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, user := range users {
		if user.Role == domain.UserRoleMember {
			return user
		}
	}
	t.Fatal("no member account exists")
	return domain.User{}
}
