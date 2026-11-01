package ginhttp

import (
	"context"
	"net/http"
	"testing"

	"github.com/moment/moment/internal/domain"
)

func TestBrowserBootstrapAdoptionRotatesCollectionCredentials(t *testing.T) {
	f := newAuthenticationTestFixture(t, false)
	first, data := exchangeAccountSession(t, f, f.token)
	group, csrf := browserBootstrap(t, f, first, data["csrf_token"].(string))
	other := domain.User{ID: "fedcba9876543210fedcba9876543210", Username: "member", DisplayName: "Member", Role: domain.UserRoleMember, Status: domain.AccountActive, CreatedAtMS: 1, UpdatedAtMS: 1}
	f.store.users[other.ID] = other
	token, err := f.accounts.CreateAPIToken(context.Background(), other.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	current, currentData := exchangeAccountSession(t, f, token.Value)
	request := jsonRequest(http.MethodPost, "/api/v1/browser/accounts/bootstrap", "")
	request.AddCookie(group)
	request.AddCookie(current)
	request.Header.Set(CSRFHeaderName, currentData["csrf_token"].(string))
	response := performRequest(f.engine, request)
	if response.Code != http.StatusOK {
		t.Fatalf("adopt current session: %d %s", response.Code, response.Body)
	}
	group, _ = browserRotation(t, f, response, group, csrf)
	entries := decodeDataMap(t, response)["accounts"].([]any)
	if len(entries) != 2 {
		t.Fatalf("adoption did not preserve original membership: %+v", entries)
	}
	seen := map[string]bool{}
	for _, entry := range entries {
		view := entry.(map[string]any)
		seen[view["account"].(map[string]any)["user_id"].(string)] = view["available"].(bool)
	}
	owner, err := f.accounts.ProfileAccount(context.Background())
	if err != nil || !seen[owner.ID] || !seen[other.ID] {
		t.Fatalf("adoption lost an available identity: %+v, %v", seen, err)
	}

	request = jsonRequest(http.MethodPost, "/api/v1/browser/accounts/bootstrap", "")
	request.AddCookie(group)
	request.AddCookie(current)
	request.Header.Set(CSRFHeaderName, currentData["csrf_token"].(string))
	response = performRequest(f.engine, request)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body)
	}
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == group.Name && cookie.Value != group.Value {
			t.Fatal("idempotent bootstrap unnecessarily rotated the collection")
		}
	}
}

func TestSecureBrowserCollectionRejectsLegacyCookieName(t *testing.T) {
	f := newAuthenticationTestFixture(t, true)
	current, data := exchangeAccountSession(t, f, f.token)
	request := jsonRequest(http.MethodPost, "/api/v1/browser/accounts/bootstrap", "")
	request.AddCookie(current)
	request.Header.Set(CSRFHeaderName, data["csrf_token"].(string))
	response := performRequest(f.engine, request)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body)
	}
	var group *http.Cookie
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == "__Host-moment_browser_accounts" {
			group = cookie
		}
	}
	if group == nil || !group.Secure || !group.HttpOnly || group.Path != "/" || group.Domain != "" || group.SameSite != http.SameSiteLaxMode {
		t.Fatalf("host-bound browser credential protections missing: %+v", group)
	}
	csrf := decodeDataMap(t, response)["csrf_token"].(string)
	legacy := *group
	legacy.Name = browserAccountsCookieName
	for _, endpoint := range []struct{ method, path string }{
		{http.MethodGet, "/api/v1/browser/accounts"},
		{http.MethodPost, "/api/v1/browser/accounts/invalid/activate"},
	} {
		request = jsonRequest(endpoint.method, endpoint.path, "")
		request.AddCookie(&legacy)
		request.Header.Set(CSRFHeaderName, csrf)
		if r := performRequest(f.engine, request); r.Code != http.StatusUnauthorized {
			t.Fatalf("legacy collection credential accepted by secure endpoint: %d %s", r.Code, r.Body)
		}
	}
	request = jsonRequest(http.MethodPost, "/api/v1/login/session", string(mustJSON(t, map[string]string{"token": f.token})))
	request.AddCookie(group)
	response = performRequest(f.engine, request)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body)
	}
	group, csrf = browserRotation(t, f, response, group, csrf)
	if !group.Secure || !group.HttpOnly || group.Path != "/" || group.Domain != "" {
		t.Fatalf("rotation weakened host-bound cookie: %+v", group)
	}
	request = jsonRequest(http.MethodDelete, "/api/v1/browser/accounts", "")
	request.AddCookie(group)
	request.Header.Set(CSRFHeaderName, csrf)
	response = performRequest(f.engine, request)
	if response.Code != http.StatusOK {
		t.Fatal(response.Body)
	}
	cleared := false
	for _, cookie := range response.Result().Cookies() {
		if cookie.Name == group.Name {
			cleared = cookie.MaxAge == -1 && cookie.Secure && cookie.HttpOnly && cookie.Path == "/" && cookie.Domain == ""
		}
	}
	if !cleared {
		t.Fatal("exit did not clear the host-bound collection cookie")
	}
}

func TestRegistrationRotatesExistingBrowserCollectionCredentials(t *testing.T) {
	f := newAuthenticationTestFixture(t, false)
	f.store.mode = domain.RegistrationOpen
	current, data := exchangeAccountSession(t, f, f.token)
	group, csrf := browserBootstrap(t, f, current, data["csrf_token"].(string))
	request := jsonRequest(http.MethodPost, "/api/v1/register", verifiedRegistrationBody(t, f, "newmember", "password123"))
	request.AddCookie(group)
	response := performRequest(f.engine, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("register with retained collection: %d %s", response.Code, response.Body)
	}
	registered := sessionCookieFrom(response)
	if registered == nil {
		t.Fatal("registration did not select its new session")
	}
	group, _ = browserRotation(t, f, response, group, csrf)
	request = jsonRequest(http.MethodGet, "/api/v1/browser/accounts", "")
	request.AddCookie(group)
	response = performRequest(f.engine, request)
	if response.Code != http.StatusOK || len(decodeDataMap(t, response)["accounts"].([]any)) != 2 {
		t.Fatalf("registration lost retained membership: %d %s", response.Code, response.Body)
	}
	request = jsonRequest(http.MethodGet, "/api/v1/account/session", "")
	request.AddCookie(registered)
	response = performRequest(f.engine, request)
	if response.Code != http.StatusOK || decodeDataMap(t, response)["account"].(map[string]any)["username"] != "newmember" {
		t.Fatalf("registration selected the wrong account: %d %s", response.Code, response.Body)
	}
}
