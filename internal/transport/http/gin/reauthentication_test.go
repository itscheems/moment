package ginhttp

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/domain"
)

func TestBrowserConfirmationLastsOneHour(t *testing.T) {
	f := newAuthenticationTestFixture(t, false)
	cookie, session := exchangeAccountSession(t, f, f.token)
	proof := confirmOverHTTP(t, f, cookie, session["csrf_token"].(string), string(mustJSON(t, map[string]string{"token": f.token})))
	if proof.MaxAge != 3600 {
		t.Fatalf("confirmation lifetime=%d, want 3600 seconds", proof.MaxAge)
	}
	probe := gin.New()
	probe.GET("/confirmation", f.gate.RequireAccount(), func(ctx *gin.Context) {
		if _, valid := f.gate.reauthenticationExpiry(ctx); valid {
			ctx.Status(http.StatusOK)
		} else {
			ctx.Status(http.StatusForbidden)
		}
	})
	for _, test := range []struct {
		elapsed time.Duration
		status  int
	}{{5 * time.Minute, 200}, {59 * time.Minute, 200}, {time.Hour + time.Second, 403}} {
		// Advance only confirmation time; keep the independently validated
		// login session alive while exercising the confirmation boundary.
		f.gate.now = func() time.Time { return proof.Expires.Add(test.elapsed - time.Hour) }
		req := httptest.NewRequest(http.MethodGet, "/confirmation", nil)
		req.AddCookie(cookie)
		req.AddCookie(proof)
		if response := performRequest(probe, req); response.Code != test.status {
			t.Fatalf("confirmation after %s=%d want %d", test.elapsed, response.Code, test.status)
		}
	}
}

func TestSensitiveCredentialsRequireIndependentProof(t *testing.T) {
	f := newAuthenticationTestFixture(t, false)
	owner, err := f.accounts.ResolveAPIToken(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.accounts.SetAccountPassword(context.Background(), owner.User.ID, "current-password"); err != nil {
		t.Fatal(err)
	}
	token := issueTestAPIToken(t, f.accounts)
	cookie, session := exchangeAccountSession(t, f, token)
	for _, path := range []string{"/api/v1/account/tokens", "/api/v1/account/password"} {
		method, body := http.MethodPost, `{"label":"device"}`
		if path == "/api/v1/account/password" {
			method, body = http.MethodPut, `{"password":"new-password"}`
		}
		for _, bearer := range []bool{false, true} {
			req := jsonRequest(method, path, body)
			if bearer {
				req.Header.Set("Authorization", "Bearer "+token)
			} else {
				req.AddCookie(cookie)
				req.Header.Set(CSRFHeaderName, session["csrf_token"].(string))
			}
			response := performRequest(f.engine, req)
			if response.Code != http.StatusForbidden {
				t.Fatalf("sensitive operation without proof: %s bearer=%t status=%d", path, bearer, response.Code)
			}
		}
	}
}

// Legacy transport tests supply a signed confirmation so their assertions still
// reach the downstream handler. Confirmation itself is exercised over HTTP below.
func testConfirmationCookie(t *testing.T, f authenticationTestFixture, cookie *http.Cookie) *http.Cookie {
	t.Helper()
	session, _, err := f.manager.AuthorizeSession(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(reauthenticationGrant{Purpose: "sensitive-operation", AccountID: session.UserID, CredentialID: "session:" + session.SessionID, ExpiresAtMS: time.Now().Add(reauthenticationTTL - time.Second).UnixMilli()})
	if err != nil {
		t.Fatal(err)
	}
	value, err := f.manager.SealState(payload)
	if err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: reauthenticationCookieName, Value: value}
}

func confirmOverHTTP(t *testing.T, f authenticationTestFixture, cookie *http.Cookie, csrf, body string) *http.Cookie {
	t.Helper()
	request := jsonRequest(http.MethodPost, "/api/v1/account/reauthentication", body)
	request.AddCookie(cookie)
	request.Header.Set(CSRFHeaderName, csrf)
	response := performRequest(f.engine, request)
	if response.Code != http.StatusOK {
		t.Fatalf("confirmation=%d %s", response.Code, response.Body.String())
	}
	for _, result := range response.Result().Cookies() {
		if result.Name == reauthenticationCookieName {
			return result
		}
	}
	t.Fatal("confirmation cookie missing")
	return nil
}

func TestConfirmationRejectsWrongProofAndBindsToOneSession(t *testing.T) {
	f := newAuthenticationTestFixture(t, true)
	owner, _ := f.accounts.ResolveAPIToken(context.Background(), f.token)
	if err := f.accounts.SetAccountPassword(context.Background(), owner.User.ID, "current-password"); err != nil {
		t.Fatal(err)
	}
	token := issueTestAPIToken(t, f.accounts)
	cookie, session := exchangeAccountSession(t, f, token)
	csrf := session["csrf_token"].(string)
	for _, body := range []string{`{"password":"wrong-password"}`, string(mustJSON(t, map[string]string{"token": token}))} {
		req := jsonRequest(http.MethodPost, "/api/v1/account/reauthentication", body)
		req.AddCookie(cookie)
		req.Header.Set(CSRFHeaderName, csrf)
		if response := performRequest(f.engine, req); response.Code != 403 {
			t.Fatalf("wrong independent proof=%d", response.Code)
		}
	}
	proof := confirmOverHTTP(t, f, cookie, csrf, `{"password":"current-password"}`)
	if !proof.HttpOnly || !proof.Secure || proof.Path != "/api/v1" || proof.MaxAge != 3600 || proof.SameSite != http.SameSiteLaxMode {
		t.Fatalf("unsafe confirmation cookie: %#v", proof)
	}
	otherCookie, otherSession := exchangeAccountSession(t, f, token)
	for _, test := range []struct {
		cookie *http.Cookie
		csrf   string
		status int
	}{{cookie, csrf, 201}, {otherCookie, otherSession["csrf_token"].(string), 403}, {cookie, "", 403}} {
		req := jsonRequest(http.MethodPost, "/api/v1/account/tokens", `{"label":"confirmed"}`)
		req.AddCookie(test.cookie)
		req.AddCookie(proof)
		req.Header.Set(CSRFHeaderName, test.csrf)
		if response := performRequest(f.engine, req); response.Code != test.status {
			t.Fatalf("bound confirmation=%d want %d: %s", response.Code, test.status, response.Body.String())
		}
	}
	for _, expires := range []int64{time.Now().Add(-time.Second).UnixMilli(), time.Now().Add(2 * time.Hour).UnixMilli()} {
		payload := mustJSON(t, reauthenticationGrant{Purpose: "sensitive-operation", AccountID: owner.User.ID, CredentialID: "session:" + mustSessionID(t, f, cookie), ExpiresAtMS: expires})
		value, _ := f.manager.SealState(payload)
		req := jsonRequest(http.MethodPost, "/api/v1/account/tokens", `{"label":"expired"}`)
		req.AddCookie(cookie)
		req.AddCookie(&http.Cookie{Name: reauthenticationCookieName, Value: value})
		req.Header.Set(CSRFHeaderName, csrf)
		if response := performRequest(f.engine, req); response.Code != 403 {
			t.Fatalf("invalid lifetime=%d", response.Code)
		}
	}
}
func mustSessionID(t *testing.T, f authenticationTestFixture, cookie *http.Cookie) string {
	t.Helper()
	session, _, err := f.manager.AuthorizeSession(cookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	return session.SessionID
}

func TestSourceTokenRevocationEndsDerivedSessionsOnly(t *testing.T) {
	f := newAuthenticationTestFixture(t, false)
	cookie, _ := exchangeAccountSession(t, f, f.token)
	sibling := issueTestAPIToken(t, f.accounts)
	siblingCookie, _ := exchangeAccountSession(t, f, sibling)
	owner, err := f.accounts.ResolveAPIToken(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.accounts.RevokeAPIToken(context.Background(), owner.User.ID, owner.Token.ID); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		cookie *http.Cookie
		status int
	}{{cookie, 401}, {siblingCookie, 200}} {
		req := jsonRequest(http.MethodGet, "/api/v1/account/session", "")
		req.AddCookie(test.cookie)
		if response := performRequest(f.engine, req); response.Code != test.status {
			t.Fatalf("source revocation=%d want %d", response.Code, test.status)
		}
	}
	actor := app.WithCredentialActor(context.Background(), "token:"+owner.Token.ID)
	if _, err := f.accounts.CreateAPIToken(actor, owner.User.ID, "late request"); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("revoked actor minted token: %v", err)
	}
}

func TestRevokeAllEndsIndependentTokensAndSessions(t *testing.T) {
	f := newAuthenticationTestFixture(t, false)
	owner, _ := f.accounts.ResolveAPIToken(context.Background(), f.token)
	if err := f.accounts.SetAccountPassword(context.Background(), owner.User.ID, "current-password"); err != nil {
		t.Fatal(err)
	}
	first := issueTestAPIToken(t, f.accounts)
	second := issueTestAPIToken(t, f.accounts)
	cookie, _ := exchangeAccountSession(t, f, first)
	req := jsonRequest(http.MethodDelete, "/api/v1/account/credentials", "")
	req.Header.Set("Authorization", "Bearer "+first)
	req.Header.Set(ReauthenticationHeader, base64.RawURLEncoding.EncodeToString([]byte("current-password")))
	if response := performRequest(f.engine, req); response.Code != 200 {
		t.Fatalf("revoke-all=%d %s", response.Code, response.Body.String())
	}
	for _, token := range []string{first, second} {
		if _, err := f.accounts.ResolveAPIToken(context.Background(), token); !domain.HasCode(err, domain.CodeUnauthorized) {
			t.Fatal("token survived revoke-all")
		}
	}
	read := jsonRequest(http.MethodGet, "/api/v1/account/session", "")
	read.AddCookie(cookie)
	if response := performRequest(f.engine, read); response.Code != 401 {
		t.Fatal("session survived revoke-all")
	}
}

func TestBackupAndAdministrativeActionsRequireConfirmation(t *testing.T) {
	f := newAuthenticationTestFixture(t, false)
	owner, _ := f.accounts.ResolveAPIToken(context.Background(), f.token)
	if err := f.accounts.SetAccountPassword(context.Background(), owner.User.ID, "current-password"); err != nil {
		t.Fatal(err)
	}
	token := issueTestAPIToken(t, f.accounts)
	cookie, session := exchangeAccountSession(t, f, token)
	calls := 0
	exporter := archiveExporterFunc(func(_ context.Context, w io.Writer) (archive.Summary, error) {
		calls++
		_, err := io.WriteString(w, "zip")
		return archive.Summary{}, err
	})
	engine, err := New(Config{Assets: testAssets(), SessionSigner: f.manager, Accounts: f.accounts, Archive: exporter, ImportCommitter: fakeImportCommitter{}})
	if err != nil {
		t.Fatal(err)
	}
	f.engine = engine
	for _, test := range []struct{ method, path, body string }{{"GET", "/api/v1/backups/export", ""}, {"POST", "/api/v1/backups/import/commit", `{}`}, {"PUT", "/api/v1/accounts/" + owner.User.ID + "/role", `{"role":"member"}`}, {"POST", "/api/v1/account/identities/google", `{}`}} {
		for _, bearer := range []bool{false, true} {
			req := jsonRequest(test.method, test.path, test.body)
			if bearer {
				req.Header.Set("Authorization", "Bearer "+token)
			} else {
				req.AddCookie(cookie)
				req.Header.Set(CSRFHeaderName, session["csrf_token"].(string))
			}
			response := performRequest(f.engine, req)
			if response.Code != 403 || decodeError(t, response.Body).Error.Code != "REAUTHENTICATION_REQUIRED" {
				t.Fatalf("unconfirmed %s: %d %s", test.path, response.Code, response.Body.String())
			}
		}
	}
	if calls != 0 {
		t.Fatal("unconfirmed export reached archive service")
	}
	req := jsonRequest("GET", "/api/v1/backups/export", "")
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set(ReauthenticationHeader, base64.RawURLEncoding.EncodeToString([]byte("current-password")))
	if response := performRequest(f.engine, req); response.Code != 200 || calls != 1 {
		t.Fatalf("confirmed export=%d calls=%d", response.Code, calls)
	}
	proof := confirmOverHTTP(t, f, cookie, session["csrf_token"].(string), `{"password":"current-password"}`)
	download := jsonRequest("GET", "/api/v1/backups/export", "")
	download.AddCookie(cookie)
	download.AddCookie(proof)
	if response := performRequest(f.engine, download); response.Code != 200 || calls != 2 {
		t.Fatalf("confirmed cookie export=%d calls=%d", response.Code, calls)
	}
}

func TestOAuthConfirmationCannotAttachAnUnrecognizedIdentity(t *testing.T) {
	f := newLinkFixture(t)
	member, cookie, _ := f.signInAsMember(t)
	for _, recognized := range []bool{true, false} {
		start := jsonRequest("GET", "/api/v1/login/oauth/google/start?confirm=1&return=%2Fsettings%2Faccount", "")
		start.AddCookie(cookie)
		started := performRequest(f.engine, start)
		var flow *http.Cookie
		for _, c := range started.Result().Cookies() {
			if c.Name == oauthCookieName {
				flow = c
			}
		}
		if flow == nil {
			t.Fatalf("confirmation flow not issued: %d", started.Code)
		}
		if !recognized {
			f.google.identity.ProviderUserID = "unrecognized-provider-account"
		}
		callback := jsonRequest("GET", "/api/v1/login/oauth/google/callback?code=code&state="+f.google.lastCall.state, "")
		callback.AddCookie(cookie)
		callback.AddCookie(flow)
		response := performRequest(f.engine, callback)
		confirmed := false
		for _, c := range response.Result().Cookies() {
			if c.Name == reauthenticationCookieName && c.Value != "" {
				confirmed = true
			}
		}
		if confirmed != recognized {
			t.Fatalf("provider confirmation=%t recognized=%t", confirmed, recognized)
		}
		identities, err := f.accounts.Identities(context.Background(), member.ID)
		if err != nil || len(identities) != 1 {
			t.Fatalf("confirmation changed identities: %v %v", identities, err)
		}
	}
}

func TestRevokingSessionSourceRequestsFreshSignIn(t *testing.T) {
	f := newAuthenticationTestFixture(t, false)
	owner, err := f.accounts.ResolveAPIToken(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	cookie, session := exchangeAccountSession(t, f, f.token)
	csrf := session["csrf_token"].(string)
	proof := confirmOverHTTP(t, f, cookie, csrf, string(mustJSON(t, map[string]string{"token": f.token})))
	request := jsonRequest("DELETE", "/api/v1/account/tokens/"+owner.Token.ID, "")
	request.AddCookie(cookie)
	request.AddCookie(proof)
	request.Header.Set(CSRFHeaderName, csrf)
	response := performRequest(f.engine, request)
	if response.Code != 200 || decodeDataMap(t, response)["reauthentication_required"] != true {
		t.Fatalf("source revocation=%d %s", response.Code, response.Body.String())
	}
	cleared := map[string]bool{}
	for _, c := range response.Result().Cookies() {
		if c.MaxAge < 0 {
			cleared[c.Name] = true
		}
	}
	if !cleared[SessionCookieName] || !cleared[reauthenticationCookieName] {
		t.Fatal("revoked session cookies were retained")
	}
	copied := jsonRequest("GET", "/api/v1/account/session", "")
	copied.AddCookie(cookie)
	if response := performRequest(f.engine, copied); response.Code != 401 {
		t.Fatal("copied source session survived")
	}
}
