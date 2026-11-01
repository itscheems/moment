package ginhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func (store *memoryAccounts) CreateBrowserAccountSet(_ context.Context, set app.BrowserAccountSet) error {
	store.browserSets[set.ID] = set
	return nil
}
func (store *memoryAccounts) BrowserAccountSetByHash(_ context.Context, hash []byte) (app.BrowserAccountSet, bool, error) {
	for _, set := range store.browserSets {
		if bytes.Equal(hash, set.CredentialHash) {
			return set, true, nil
		}
	}
	return app.BrowserAccountSet{}, false, nil
}
func (store *memoryAccounts) BrowserAccountMembers(_ context.Context, id string) ([]app.BrowserAccountMember, error) {
	members := []app.BrowserAccountMember{}
	for _, m := range store.browserMembers[id] {
		members = append(members, m)
	}
	return members, nil
}
func (store *memoryAccounts) PutBrowserAccountMember(_ context.Context, m app.BrowserAccountMember, expires int64) error {
	if store.browserMembers[m.SetID] == nil {
		store.browserMembers[m.SetID] = map[string]app.BrowserAccountMember{}
	}
	store.browserMembers[m.SetID][m.UserID] = m
	s := store.browserSets[m.SetID]
	if expires > s.ExpiresAtMS {
		s.ExpiresAtMS = expires
	}
	store.browserSets[m.SetID] = s
	return nil
}
func (store *memoryAccounts) DeleteBrowserAccountMember(_ context.Context, setID, userID string) error {
	delete(store.browserMembers[setID], userID)
	return nil
}
func (store *memoryAccounts) RevokeBrowserAccountSet(_ context.Context, id string, at int64) error {
	s := store.browserSets[id]
	s.RevokedAtMS = &at
	store.browserSets[id] = s
	return nil
}

func (store *memoryAccounts) RotateBrowserAccountSet(_ context.Context, set app.BrowserAccountSet) error {
	store.browserSets[set.ID] = set
	return nil
}

func browserRotation(t *testing.T, f authenticationTestFixture, response *httptest.ResponseRecorder, old *http.Cookie, oldCSRF string) (*http.Cookie, string) {
	t.Helper()
	var next *http.Cookie
	for _, c := range response.Result().Cookies() {
		if c.Name == old.Name {
			next = c
		}
	}
	if next == nil || next.Value == old.Value {
		t.Fatal("browser credential did not rotate")
	}
	request := jsonRequest("GET", "/api/v1/browser/accounts", "")
	request.AddCookie(old)
	if r := performRequest(f.engine, request); r.Code != 401 {
		t.Fatalf("old browser credential accepted: %d", r.Code)
	}
	request = jsonRequest("GET", "/api/v1/browser/accounts", "")
	request.AddCookie(next)
	r := performRequest(f.engine, request)
	if r.Code != 200 {
		t.Fatal(r.Body)
	}
	csrf := decodeDataMap(t, r)["csrf_token"].(string)
	if csrf == oldCSRF {
		t.Fatal("browser CSRF did not rotate")
	}
	request = jsonRequest("DELETE", "/api/v1/browser/accounts", "")
	request.AddCookie(next)
	request.Header.Set(CSRFHeaderName, oldCSRF)
	if r := performRequest(f.engine, request); r.Code != 403 {
		t.Fatalf("old CSRF accepted with fresh browser credential: %d", r.Code)
	}
	return next, csrf
}

func browserBootstrap(t *testing.T, fixture authenticationTestFixture, cookie *http.Cookie, csrf string) (*http.Cookie, string) {
	t.Helper()
	request := jsonRequest("POST", "/api/v1/browser/accounts/bootstrap", "")
	request.AddCookie(cookie)
	request.Header.Set(CSRFHeaderName, csrf)
	response := performRequest(fixture.engine, request)
	if response.Code != 200 {
		t.Fatalf("bootstrap %d: %s", response.Code, response.Body)
	}
	var group *http.Cookie
	for _, c := range response.Result().Cookies() {
		if c.Name == browserAccountsCookieName {
			group = c
		}
	}
	if group == nil {
		t.Fatal("missing browser cookie")
	}
	return group, decodeDataMap(t, response)["csrf_token"].(string)
}

func TestBrowserAccountSwitchingAndScopedLogout(t *testing.T) {
	f := newAuthenticationTestFixture(t, false)
	first, firstData := exchangeAccountSession(t, f, f.token)
	group, csrf := browserBootstrap(t, f, first, firstData["csrf_token"].(string))
	if !group.HttpOnly || group.SameSite != http.SameSiteLaxMode {
		t.Fatal("browser credential protections missing")
	}
	other := domain.User{ID: "fedcba9876543210fedcba9876543210", Username: "member", DisplayName: "Member", Role: domain.UserRoleMember, Status: domain.AccountActive, CreatedAtMS: 1, UpdatedAtMS: 1}
	f.store.users[other.ID] = other
	token, err := f.accounts.CreateAPIToken(context.Background(), other.ID, "test")
	if err != nil {
		t.Fatal(err)
	}
	device, _ := exchangeAccountSession(t, f, token.Value)
	call := func(method, path, body, csrfValue string, cookies ...*http.Cookie) *httptest.ResponseRecorder {
		request := jsonRequest(method, path, body)
		request.Header.Set(CSRFHeaderName, csrfValue)
		for _, c := range cookies {
			request.AddCookie(c)
		}
		return performRequest(f.engine, request)
	}
	if r := call("POST", "/api/v1/browser/accounts", string(mustJSON(t, map[string]string{"token": token.Value})), "", group, first); r.Code != 403 {
		t.Fatalf("missing collection CSRF: %d", r.Code)
	}
	added := call("POST", "/api/v1/browser/accounts", string(mustJSON(t, map[string]string{"token": token.Value})), csrf, group, first)
	if added.Code != 200 {
		t.Fatalf("add %d %s", added.Code, added.Body)
	}
	group, csrf = browserRotation(t, f, added, group, csrf)
	var current *http.Cookie
	for _, c := range added.Result().Cookies() {
		if c.Name == SessionCookieName {
			current = c
		}
	}
	if current == nil {
		t.Fatal("add did not select member")
	}
	for i := 0; i < 2; i++ {
		r := call("POST", "/api/v1/browser/accounts", string(mustJSON(t, map[string]string{"token": token.Value})), csrf, group, current)
		if r.Code != 200 {
			t.Fatal(r.Body)
		}
		group, csrf = browserRotation(t, f, r, group, csrf)
		for _, c := range r.Result().Cookies() {
			if c.Name == SessionCookieName {
				current = c
			}
		}
	}
	issued, _ := f.manager.ValidateSession(current.Value)
	if _, err := f.accounts.ResolveSession(context.Background(), issued.SessionID); err != nil {
		t.Fatal("latest member session was invalid before exit", err)
	}
	listed := call("GET", "/api/v1/browser/accounts", "", "", group)
	if listed.Code != 200 {
		t.Fatal(listed.Body)
	}
	if len(decodeDataMap(t, listed)["accounts"].([]any)) != 2 {
		t.Fatal("duplicate member added")
	}
	if r := call("POST", "/api/v1/browser/accounts/11111111111111111111111111111111/activate", "", csrf, group); r.Code != 401 {
		t.Fatalf("nonmember accepted %d", r.Code)
	}
	owner, _ := f.accounts.ProfileAccount(context.Background())
	switched := call("POST", "/api/v1/browser/accounts/"+owner.ID+"/activate", "", csrf, group, current)
	if switched.Code != 200 {
		t.Fatal(switched.Body)
	}
	for _, c := range switched.Result().Cookies() {
		if c.Name == SessionCookieName {
			first = c
		}
	}
	old, _ := f.manager.ValidateSession(first.Value)
	if old.UserID != owner.ID {
		t.Fatal("wrong switch identity")
	}
	if r := call("DELETE", "/api/v1/browser/accounts/"+other.ID, "", csrf, group, first); r.Code != 200 {
		t.Fatal(r.Body)
	}
	if _, err := f.accounts.ResolveSession(context.Background(), issued.SessionID); err == nil {
		t.Fatal("removed browser session remained valid")
	}
	deviceSession, _ := f.manager.ValidateSession(device.Value)
	if _, err := f.accounts.ResolveSession(context.Background(), deviceSession.SessionID); err != nil {
		t.Fatal("another device was revoked", err)
	}
	if r := call("DELETE", "/api/v1/browser/accounts", "", csrf, group, first); r.Code != 200 {
		t.Fatal(r.Body)
	}
	if r := call("GET", "/api/v1/browser/accounts", "", "", group); r.Code != 401 {
		t.Fatal("revoked collection accepted")
	}
	if r := call("POST", "/api/v1/browser/accounts", `{"token":"invalid"}`, csrf, group); r.Code != 401 {
		t.Fatal("logged out browser was resurrected")
	}
	if strings.Contains(listed.Body.String(), token.Value) || strings.Contains(listed.Body.String(), group.Value) {
		t.Fatal("credential leaked")
	}
}

func TestOAuthAddOnlyUsesExistingIdentity(t *testing.T) {
	f := newOAuthFixture(t)
	f.authenticationTestFixture.engine = f.engine
	first, data := exchangeAccountSession(t, f.authenticationTestFixture, f.token)
	group, csrf := browserBootstrap(t, f.authenticationTestFixture, first, data["csrf_token"].(string))
	start := func() *httptest.ResponseRecorder {
		r := jsonRequest("POST", "/api/v1/login/oauth/google/add", "")
		r.AddCookie(first)
		r.AddCookie(group)
		r.Header.Set(CSRFHeaderName, csrf)
		return f.do(t, r)
	}
	finish := func(started *httptest.ResponseRecorder) *httptest.ResponseRecorder {
		var flow *http.Cookie
		for _, c := range started.Result().Cookies() {
			if c.Name == oauthCookieName {
				flow = c
			}
		}
		r := callbackRequest(f.provider.lastCall.state, "accepted", flow)
		r.AddCookie(group)
		r.AddCookie(first)
		return f.do(t, r)
	}
	started := start()
	if started.Code != 200 {
		t.Fatal(started.Body)
	}
	var result struct {
		Data map[string]string `json:"data"`
	}
	_ = json.Unmarshal(started.Body.Bytes(), &result)
	if result.Data["authorization_url"] == "" {
		t.Fatal("missing provider redirect")
	}
	rejected := finish(started)
	if rejected.Code != 303 || !strings.HasPrefix(rejected.Header().Get("Location"), "/account/add?auth_error=") {
		t.Fatalf("unknown identity add %d %s", rejected.Code, rejected.Body)
	}
	if len(f.store.users) != 1 {
		t.Fatal("add registered an account")
	}
	outcome, err := f.accounts.SignIn(context.Background(), f.provider.identity)
	if err != nil {
		t.Fatal(err)
	}
	started = start()
	finished := finish(started)
	if finished.Code != 303 {
		t.Fatal(finished.Body)
	}
	group, csrf = browserRotation(t, f.authenticationTestFixture, finished, group, csrf)
	var signed *http.Cookie
	for _, c := range finished.Result().Cookies() {
		if c.Name == SessionCookieName {
			signed = c
		}
	}
	if signed == nil {
		t.Fatal("missing added account session")
	}
	member, _ := f.manager.ValidateSession(signed.Value)
	if member.UserID != outcome.User.ID {
		t.Fatal("OAuth add selected wrong identity")
	}
	owner, _ := f.accounts.ProfileAccount(context.Background())
	ids, _ := f.accounts.Identities(context.Background(), owner.ID)
	if len(ids) != 0 {
		t.Fatal("OAuth add linked the current account")
	}
}

func TestStandaloneSignInReplacesRetainedSessionBeforeLogout(t *testing.T) {
	f := newAuthenticationTestFixture(t, false)
	first, data := exchangeAccountSession(t, f, f.token)
	group, oldCSRF := browserBootstrap(t, f, first, data["csrf_token"].(string))
	old, _ := f.manager.ValidateSession(first.Value)
	request := jsonRequest("POST", "/api/v1/login/session", string(mustJSON(t, map[string]string{"token": f.token})))
	request.AddCookie(group)
	response := performRequest(f.engine, request)
	if response.Code != 200 {
		t.Fatal(response.Body)
	}
	var rotated *http.Cookie
	for _, c := range response.Result().Cookies() {
		if c.Name == browserAccountsCookieName {
			rotated = c
		}
	}
	if rotated == nil || rotated.Value == group.Value {
		t.Fatal("fresh authentication reused the browser credential")
	}
	stale := jsonRequest("POST", "/api/v1/browser/accounts/"+old.UserID+"/activate", "")
	stale.AddCookie(group)
	stale.Header.Set(CSRFHeaderName, oldCSRF)
	if r := performRequest(f.engine, stale); r.Code != 401 {
		t.Fatalf("old browser credential restored newly authenticated account: %d", r.Code)
	}
	listRotated := jsonRequest("GET", "/api/v1/browser/accounts", "")
	listRotated.AddCookie(rotated)
	listed := performRequest(f.engine, listRotated)
	if listed.Code != 200 || decodeDataMap(t, listed)["csrf_token"] == oldCSRF {
		t.Fatal("fresh authentication did not rotate browser CSRF")
	}
	group = rotated
	var fresh *http.Cookie
	for _, c := range response.Result().Cookies() {
		if c.Name == SessionCookieName {
			fresh = c
		}
	}
	if fresh == nil {
		t.Fatal("missing fresh session")
	}
	if _, err := f.accounts.ResolveSession(context.Background(), old.SessionID); err == nil {
		t.Fatal("superseded browser session remained valid")
	}
	logout := jsonRequest("DELETE", "/api/v1/login/session", "")
	logout.AddCookie(fresh)
	logout.AddCookie(group)
	logout.Header.Set(CSRFHeaderName, decodeDataMap(t, response)["csrf_token"].(string))
	if r := performRequest(f.engine, logout); r.Code != 200 {
		t.Fatal(r.Body)
	}
	list := jsonRequest("GET", "/api/v1/browser/accounts", "")
	list.AddCookie(group)
	if r := performRequest(f.engine, list); r.Code != 401 {
		t.Fatal("explicitly exited account could be restored from old membership")
	}
}
