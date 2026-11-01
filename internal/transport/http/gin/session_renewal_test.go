package ginhttp

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
)

func TestSessionRenewalHTTPPreservesCredentialAndConfirmation(t *testing.T) {
	f := newAuthenticationTestFixture(t, true, domain.SessionAbsoluteTTL)
	cookie, data := exchangeAccountSession(t, f, f.token)
	csrf := data["csrf_token"].(string)
	initial := *f.now
	if got := data["expires_at"]; got != initial.Add(domain.SessionIdleTTL).Format(time.RFC3339Nano) {
		t.Fatalf("idle expiry=%v", got)
	}
	if !cookie.Expires.Equal(initial.Add(domain.SessionAbsoluteTTL).Truncate(time.Second)) || !cookie.Secure || !cookie.HttpOnly {
		t.Fatalf("credential bounds: %+v", cookie)
	}
	renew := func(csrfValue string) *http.Request {
		req := jsonRequest("POST", "/api/v1/account/session/renew", "")
		req.AddCookie(cookie)
		req.Header.Set(CSRFHeaderName, csrfValue)
		return req
	}
	if r := performRequest(f.engine, renew("wrong")); r.Code != 403 {
		t.Fatalf("CSRF=%d", r.Code)
	}
	*f.now = initial.Add(29 * 24 * time.Hour)
	response := performRequest(f.engine, renew(csrf))
	if response.Code != 200 || len(response.Result().Cookies()) != 0 {
		t.Fatalf("renewal replaced credentials: %d %s", response.Code, response.Body)
	}
	want := f.now.Add(domain.SessionIdleTTL).Format(time.RFC3339Nano)
	if got := decodeDataMap(t, response)["expires_at"]; got != want {
		t.Fatalf("renewal=%v want=%s", got, want)
	}
	req := jsonRequest("GET", "/api/v1/account/session", "")
	req.AddCookie(cookie)
	current := performRequest(f.engine, req)
	values := decodeDataMap(t, current)
	if current.Code != 200 || values["expires_at"] != want || values["csrf_token"] != csrf {
		t.Fatalf("inconsistent session: %s", current.Body)
	}
	// Renewed ordinary authority still cannot create a token without separate confirmation.
	req = jsonRequest("POST", "/api/v1/account/tokens", `{"label":"protected"}`)
	req.AddCookie(cookie)
	req.Header.Set(CSRFHeaderName, csrf)
	if r := performRequest(f.engine, req); r.Code != 403 {
		t.Fatalf("renewal bypassed confirmation: %d", r.Code)
	}
	signed, err := f.manager.ValidateSession(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.accounts.EndSession(context.Background(), signed.SessionID); err != nil {
		t.Fatal(err)
	}
	if r := performRequest(f.engine, renew(csrf)); r.Code != 401 {
		t.Fatalf("logout restored: %d", r.Code)
	}
}

func TestSessionRenewalHTTPDoesNotExtendLegacySignedLifetime(t *testing.T) {
	f := newAuthenticationTestFixture(t, false)
	cookie, data := exchangeAccountSession(t, f, f.token)
	*f.now = f.now.Add(20 * time.Minute)
	req := jsonRequest("POST", "/api/v1/account/session/renew", "")
	req.AddCookie(cookie)
	req.Header.Set(CSRFHeaderName, data["csrf_token"].(string))
	response := performRequest(f.engine, req)
	if response.Code != 200 || decodeDataMap(t, response)["expires_at"] != data["expires_at"] {
		t.Fatalf("legacy signed deadline extended: %d %s", response.Code, response.Body)
	}
	*f.now = f.now.Add(10 * time.Minute)
	if r := performRequest(f.engine, req); r.Code != 401 {
		t.Fatalf("expired legacy credential restored: %d", r.Code)
	}
}

func TestNotificationPollingDoesNotRenewRetainedAccounts(t *testing.T) {
	f := newAuthenticationTestFixture(t, false, domain.SessionAbsoluteTTL)
	first, data := exchangeAccountSession(t, f, f.token)
	group, groupCSRF := browserBootstrap(t, f, first, data["csrf_token"].(string))
	other := domain.User{ID: "fedcba9876543210fedcba9876543210", Username: "other", DisplayName: "Other", Role: domain.UserRoleMember, Status: domain.AccountActive, CreatedAtMS: f.now.UnixMilli(), UpdatedAtMS: f.now.UnixMilli()}
	f.store.users[other.ID] = other
	token, err := f.accounts.CreateAPIToken(context.Background(), other.ID, "other")
	if err != nil {
		t.Fatal(err)
	}
	req := jsonRequest(http.MethodPost, "/api/v1/browser/accounts", string(mustJSON(t, map[string]string{"token": token.Value})))
	req.AddCookie(group)
	req.Header.Set(CSRFHeaderName, groupCSRF)
	added := performRequest(f.engine, req)
	if added.Code != 200 {
		t.Fatal(added.Body)
	}
	for _, c := range added.Result().Cookies() {
		if c.Name == group.Name {
			group = c
		}
	}
	var second domain.SessionRecord
	for _, s := range f.store.sessions {
		if s.UserID == other.ID {
			second = s
		}
	}
	original := second.ExpiresAtMS
	*f.now = f.now.Add(29 * 24 * time.Hour)
	req = jsonRequest(http.MethodGet, "/api/v1/browser/accounts/notification-counts", "")
	req.AddCookie(group)
	if response := performRequest(f.engine, req); response.Code != 200 {
		t.Fatalf("counts: %d %s", response.Code, response.Body)
	}
	req = jsonRequest(http.MethodPost, "/api/v1/account/session/renew", "")
	req.AddCookie(first)
	req.Header.Set(CSRFHeaderName, data["csrf_token"].(string))
	if response := performRequest(f.engine, req); response.Code != 200 {
		t.Fatal(response.Body)
	}
	if f.store.sessions[second.ID].ExpiresAtMS != original {
		t.Fatal("another account was renewed by notification polling")
	}
	*f.now = f.now.Add(24 * time.Hour)
	if _, err := f.accounts.ResolveSession(context.Background(), second.ID); err == nil {
		t.Fatal("unused retained account did not expire")
	}
	signed, err := f.manager.ValidateSession(first.Value)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.accounts.ResolveSession(context.Background(), signed.SessionID); err != nil {
		t.Fatal("active account expired", err)
	}
}
