package ginhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

type authenticationTestFixture struct {
	manager               *auth.Manager
	accounts              *app.AccountService
	store                 *memoryAccounts
	gate                  *Gate
	token                 string
	engine                http.Handler
	now                   *time.Time
	instanceID            string
	instanceInitializedAt string
}

func newAuthenticationTestFixture(t *testing.T, secure bool) authenticationTestFixture {
	t.Helper()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err = auth.InitializeOffline(context.Background(), runtimeState, auth.Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(runtimeState.Root, auth.Options{
		Now:        func() time.Time { return now },
		SessionTTL: 30 * time.Minute,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	store := newMemoryAccounts(testProfileAccount())
	accounts, err := app.NewAccountService(store)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{
		Assets:        testAssets(),
		SessionSigner: manager,
		Accounts:      accounts,
		SecureCookies: secure,
		Readiness:     func(context.Context) error { return nil },
		RequestID:     func() string { return "req_owner" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return authenticationTestFixture{manager: manager, accounts: accounts, store: store, gate: NewGate(manager, accounts), token: issueTestAPIToken(t, accounts), engine: engine, now: &now, instanceID: runtimeState.Instance.ID}
}

func jsonRequest(method, path string, body string) *http.Request {
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	if body != "" {
		request.Header.Set("Content-Type", "application/json")
	}
	return request
}

func decodeDataMap(t *testing.T, response *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var envelope struct {
		Data map[string]any `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatalf("decode success: %v, body = %s", err, response.Body.String())
	}
	return envelope.Data
}

func exchangeAccountSession(t *testing.T, fixture authenticationTestFixture, token string) (*http.Cookie, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	response := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/login/session", string(body)))
	if response.Code != http.StatusOK {
		t.Fatalf("exchange status = %d, body = %s", response.Code, response.Body.String())
	}
	var sessionCookie *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == SessionCookieName {
			sessionCookie = cookie
		}
	}
	if sessionCookie == nil {
		t.Fatal("session cookie missing")
	}
	return sessionCookie, decodeDataMap(t, response)
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}

func issueTestAPIToken(t *testing.T, accounts *app.AccountService) string {
	t.Helper()
	account, err := accounts.ProfileAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	issued, err := accounts.CreateAPIToken(context.Background(), account.ID, "transport test")
	if err != nil {
		t.Fatal(err)
	}
	return issued.Value
}

func TestAccountSessionCookieCSRFAndLogout(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, true)
	cookie, data := exchangeAccountSession(t, fixture, fixture.token)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" {
		t.Fatal("session cookie protections changed")
	}
	protected := jsonRequest(http.MethodPost, "/api/v1/account/tokens", `{"label":"device"}`)
	protected.AddCookie(cookie)
	if response := performRequest(fixture.engine, protected); response.Code != http.StatusForbidden {
		t.Fatalf("missing CSRF=%d", response.Code)
	}
	allowed := jsonRequest(http.MethodPost, "/api/v1/account/tokens", `{"label":"device"}`)
	allowed.AddCookie(cookie)
	allowed.Header.Set(CSRFHeaderName, data["csrf_token"].(string))
	if response := performRequest(fixture.engine, allowed); response.Code != http.StatusCreated {
		t.Fatalf("account token creation=%d %s", response.Code, response.Body.String())
	}
	logout := jsonRequest(http.MethodDelete, "/api/v1/login/session", "")
	logout.AddCookie(cookie)
	logout.Header.Set(CSRFHeaderName, data["csrf_token"].(string))
	if response := performRequest(fixture.engine, logout); response.Code != http.StatusOK {
		t.Fatalf("logout=%d", response.Code)
	}
	copied := jsonRequest(http.MethodGet, "/api/v1/account/session", "")
	copied.AddCookie(cookie)
	if response := performRequest(fixture.engine, copied); response.Code != http.StatusUnauthorized {
		t.Fatal("copied cookie survived logout")
	}
}

func TestBearerPrecedenceMalformedInputAndSessionExpiryFailClosed(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	cookie, _ := exchangeAccountSession(t, fixture, fixture.token)
	for _, header := range []string{"", "Basic value", "Bearer wrong", "Bearer value extra"} {
		request := jsonRequest(http.MethodGet, "/api/v1/account/security", "")
		request.AddCookie(cookie)
		request.Header["Authorization"] = []string{header}
		if response := performRequest(fixture.engine, request); response.Code != http.StatusUnauthorized {
			t.Fatalf("invalid bearer fell back to cookie: %d", response.Code)
		}
	}
	*fixture.now = fixture.now.Add(time.Hour)
	request := jsonRequest(http.MethodGet, "/api/v1/account/session", "")
	request.AddCookie(cookie)
	if response := performRequest(fixture.engine, request); response.Code != http.StatusUnauthorized {
		t.Fatal("expired signed session accepted")
	}
}

func TestBearerAuthenticatedWriteDoesNotRequireCSRF(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	request := jsonRequest(http.MethodPost, "/api/v1/account/tokens", `{"label":"terminal"}`)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	response := performRequest(fixture.engine, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("bearer write=%d %s", response.Code, response.Body.String())
	}
	token := decodeDataMap(t, response)["token"].(string)
	if _, err := fixture.accounts.ResolveAPIToken(context.Background(), token); err != nil {
		t.Fatal("issued token is not account-backed")
	}
}

func TestPasswordLoginUsesEachAccountsOwnCredential(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	member := domain.User{ID: "fedcba9876543210fedcba9876543210", Username: "alice", DisplayName: "Alice", Role: domain.UserRoleMember, Status: domain.AccountActive, CreatedAtMS: 1, UpdatedAtMS: 1}
	fixture.store.users[member.ID] = member
	if err := fixture.accounts.SetAccountPassword(context.Background(), member.ID, "alice-password"); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, password string
		status         int
	}{{"alice", "alice-password", 200}, {"owner", "alice-password", 401}, {"alice", "wrong-password", 401}, {"", "alice-password", 400}} {
		body := string(mustJSON(t, map[string]string{"username": test.name, "password": test.password}))
		response := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/login/session", body))
		if response.Code != test.status {
			t.Fatalf("account password sign-in=%d want %d: %s", response.Code, test.status, response.Body.String())
		}
		if response.Code == http.StatusOK {
			request := jsonRequest(http.MethodGet, "/api/v1/account/session", "")
			request.AddCookie(sessionCookieFrom(response))
			account := decodeDataMap(t, performRequest(fixture.engine, request))["account"].(map[string]any)
			if account["user_id"] != member.ID {
				t.Fatal("member password signed in as another account")
			}
		}
	}
}

func TestPersonalPasswordUpdateRevokesAllAccountAccess(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	cookie, _ := exchangeAccountSession(t, fixture, fixture.token)
	request := jsonRequest(http.MethodPut, "/api/v1/account/password", `{"password":"replacement-password"}`)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, request); response.Code != http.StatusOK {
		t.Fatalf("password update=%d %s", response.Code, response.Body.String())
	}
	expired := jsonRequest(http.MethodGet, "/api/v1/account/session", "")
	expired.AddCookie(cookie)
	if response := performRequest(fixture.engine, expired); response.Code != http.StatusUnauthorized {
		t.Fatal("password update left browser session live")
	}
	if _, err := fixture.accounts.ResolveAPIToken(context.Background(), fixture.token); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatal("password update left an API token live")
	}
}

func TestGlobalCredentialManagementRoutesAreRemoved(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	for _, item := range []struct{ method, path string }{{http.MethodGet, "/api/v1/status"}, {http.MethodPost, "/api/v1/token/rotate"}, {http.MethodPut, "/api/v1/password"}} {
		request := jsonRequest(item.method, item.path, `{}`)
		request.Header.Set("Authorization", "Bearer "+fixture.token)
		if response := performRequest(fixture.engine, request); response.Code != http.StatusNotFound {
			t.Fatalf("retired credential path=%d", response.Code)
		}
	}
}

func TestPersonalTokensCannotRevokeAnotherAccountsCredential(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	member := domain.User{ID: "fedcba9876543210fedcba9876543210", Username: "alice", DisplayName: "Alice", Role: domain.UserRoleMember, Status: domain.AccountActive, CreatedAtMS: 1, UpdatedAtMS: 1}
	fixture.store.users[member.ID] = member
	issued, err := fixture.accounts.CreateAPIToken(context.Background(), member.ID, "member device")
	if err != nil {
		t.Fatal(err)
	}
	primaryAccount, err := fixture.accounts.ResolveAPIToken(context.Background(), fixture.token)
	if err != nil {
		t.Fatal(err)
	}
	request := jsonRequest(http.MethodDelete, "/api/v1/account/tokens/"+primaryAccount.Token.ID, "")
	request.Header.Set("Authorization", "Bearer "+issued.Value)
	response := performRequest(fixture.engine, request)
	if response.Code != http.StatusNotFound {
		t.Fatalf("another account's token revocation = %d %s", response.Code, response.Body.String())
	}
	if _, err := fixture.accounts.ResolveAPIToken(context.Background(), fixture.token); err != nil {
		t.Fatal("another account revoked the token")
	}
	if _, err := fixture.accounts.ResolveAPIToken(context.Background(), issued.Value); err != nil {
		t.Fatal("resource refusal invalidated the caller's credential")
	}
}
