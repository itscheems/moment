package ginhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestRenewedSessionsKeepPostAndMediaAuthority(t *testing.T) {
	f := newPostHTTPFixture(t, 0)
	ctx := context.Background()
	outcome, err := f.accounts.SignIn(ctx, app.ExternalIdentity{Provider: "google", ProviderUserID: "renewal-reader", DisplayName: "Reader"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := f.accounts.SetStatus(ctx, outcome.User.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	key, err := f.accounts.CreateAPIToken(ctx, member.ID, "reader")
	if err != nil {
		t.Fatal(err)
	}
	ownerCookie, ownerSession := exchangeAccountSession(t, f.authenticationTestFixture, f.token)
	memberCookie, memberSession := exchangeAccountSession(t, f.authenticationTestFixture, key.Value)
	call := func(method, path, body string, cookie *http.Cookie, csrf string) *httptest.ResponseRecorder {
		req := jsonRequest(method, path, body)
		if cookie != nil {
			req.AddCookie(cookie)
		}
		if csrf != "" {
			req.Header.Set(CSRFHeaderName, csrf)
		}
		return performRequest(f.engine, req)
	}
	ownerCSRF, memberCSRF := ownerSession["csrf_token"].(string), memberSession["csrf_token"].(string)
	upload := mediaUploadRequest(t, testGIF(t))
	upload.AddCookie(ownerCookie)
	upload.Header.Set(CSRFHeaderName, ownerCSRF)
	response := performRequest(f.engine, upload)
	if response.Code != 201 {
		t.Fatal(response.Body)
	}
	mediaID := decodeDataMap(t, response)["media"].(map[string]any)["id"].(string)
	ownerPost := call("POST", "/api/v1/posts", `{"content_md":"auditsentinel private owner","visibility":"only_me","media":[{"media_id":"`+mediaID+`","position":0,"alt_text":null}]}`, ownerCookie, ownerCSRF)
	memberPost := call("POST", "/api/v1/posts", `{"content_md":"auditsentinel private member","visibility":"only_me"}`, memberCookie, memberCSRF)
	publicPost := call("POST", "/api/v1/posts", `{"content_md":"auditsentinel public"}`, ownerCookie, ownerCSRF)
	for _, r := range []*httptest.ResponseRecorder{ownerPost, memberPost, publicPost} {
		if r.Code != 201 {
			t.Fatal(r.Body)
		}
	}
	ownerID, memberID := decodeDataMap(t, ownerPost)["id"].(string), decodeDataMap(t, memberPost)["id"].(string)
	// Renew both principals through the real HTTP gate, not a manually supplied viewer.
	for _, signed := range []struct {
		cookie *http.Cookie
		csrf   string
	}{{ownerCookie, ownerCSRF}, {memberCookie, memberCSRF}} {
		if r := call("POST", "/api/v1/account/session/renew", "", signed.cookie, signed.csrf); r.Code != 200 {
			t.Fatal(r.Body)
		}
	}
	for _, viewer := range []struct {
		name            string
		cookie          *http.Cookie
		ownID, hiddenID string
		count           int
	}{{"author", ownerCookie, ownerID, memberID, 2}, {"member", memberCookie, memberID, ownerID, 2}, {"visitor", nil, "", ownerID, 1}} {
		t.Run(viewer.name, func(t *testing.T) {
			if viewer.ownID != "" {
				if r := call("GET", "/api/v1/posts/"+viewer.ownID, "", viewer.cookie, ""); r.Code != 200 {
					t.Fatal(r.Body)
				}
			}
			if r := call("GET", "/api/v1/posts/"+viewer.hiddenID, "", viewer.cookie, ""); r.Code != 404 {
				t.Fatalf("private detail=%d", r.Code)
			}
			for _, path := range []string{"/api/v1/posts?limit=10", "/api/v1/search?q=auditsentinel"} {
				r := call("GET", path, "", viewer.cookie, "")
				raw := r.Body.String()
				if r.Code != 200 || len(decodeDataMap(t, r)["items"].([]any)) != viewer.count || strings.Contains(raw, viewer.hiddenID) {
					t.Fatalf("%s leaked private content: %d %s", path, r.Code, r.Body)
				}
				if r.Header().Get("Cache-Control") != "no-store" {
					t.Fatalf("private-capable JSON cached: %s", r.Header().Get("Cache-Control"))
				}
			}
			for _, suffix := range []string{"", "/preview"} {
				want := 404
				if viewer.name == "author" {
					want = 200
				}
				if r := call("GET", "/api/v1/media/"+mediaID+suffix, "", viewer.cookie, ""); r.Code != want {
					t.Fatalf("media%s=%d want %d", suffix, r.Code, want)
				}
			}
		})
	}
	// An explicit bearer always chooses its own principal, even with another account's cookie.
	req := jsonRequest("GET", "/api/v1/posts/"+ownerID, "")
	req.AddCookie(ownerCookie)
	req.Header.Set("Authorization", "Bearer "+key.Value)
	if r := performRequest(f.engine, req); r.Code != 404 {
		t.Fatalf("cookie overrode bearer identity: %d", r.Code)
	}
	if r := call("PATCH", "/api/v1/posts/"+ownerID, `{"content_md":"unauthorized change"}`, memberCookie, memberCSRF); r.Code != 403 {
		t.Fatalf("another account edited private post: %d", r.Code)
	}
	// Ending just this sign-in removes private reads but leaves its independent API key valid.
	signed, err := f.manager.ValidateSession(ownerCookie.Value)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.accounts.EndSession(ctx, signed.SessionID); err != nil {
		t.Fatal(err)
	}
	if r := call("GET", "/api/v1/posts/"+ownerID, "", ownerCookie, ""); r.Code != 404 {
		t.Fatalf("revoked session read private post: %d", r.Code)
	}
	if r := call("POST", "/api/v1/account/session/renew", "", ownerCookie, ownerCSRF); r.Code != 401 {
		t.Fatalf("revoked session renewed: %d", r.Code)
	}
	if _, err := f.accounts.ResolveAPIToken(ctx, f.token); err != nil {
		t.Fatal("session logout revoked independent key", err)
	}
}

func TestRenewalUsesCurrentRoleAndDoesNotExtendConfirmation(t *testing.T) {
	f := newAuthenticationTestFixture(t, false, domain.SessionAbsoluteTTL)
	owner, err := f.accounts.ResolveAPIToken(context.Background(), f.token)
	if err != nil {
		t.Fatal(err)
	}
	cookie, data := exchangeAccountSession(t, f, f.token)
	csrf := data["csrf_token"].(string)
	proof := confirmOverHTTP(t, f, cookie, csrf, string(mustJSON(t, map[string]string{"token": f.token})))
	renew := jsonRequest("POST", "/api/v1/account/session/renew", "")
	renew.AddCookie(cookie)
	renew.Header.Set(CSRFHeaderName, csrf)
	if r := performRequest(f.engine, renew); r.Code != 200 || len(r.Result().Cookies()) != 0 {
		t.Fatalf("renew changed confirmation: %d %s", r.Code, r.Body)
	}
	probe := gin.New()
	probe.GET("/proof", f.gate.RequireAccount(), func(c *gin.Context) {
		_, valid := f.gate.reauthenticationExpiry(c)
		if valid {
			c.Status(200)
		} else {
			c.Status(403)
		}
	})
	req := jsonRequest("GET", "/proof", "")
	req.AddCookie(cookie)
	req.AddCookie(proof)
	if r := performRequest(probe, req); r.Code != 200 {
		t.Fatalf("fresh confirmation rejected: %d", r.Code)
	}
	f.gate.now = func() time.Time { return proof.Expires.Add(time.Second) }
	if r := performRequest(probe, req); r.Code != 403 {
		t.Fatalf("ordinary renewal extended sensitive proof: %d", r.Code)
	}
	req = jsonRequest("GET", "/api/v1/accounts", "")
	req.AddCookie(cookie)
	if r := performRequest(f.engine, req); r.Code != 200 {
		t.Fatalf("initial administrator rejected: %d", r.Code)
	}
	// Change the stored role, keeping the exact same cookie and API key.
	current := f.store.users[owner.User.ID]
	current.Role = domain.UserRoleMember
	f.store.users[current.ID] = current
	if r := performRequest(f.engine, renew); r.Code != 200 {
		t.Fatal(r.Body)
	}
	req = jsonRequest("GET", "/api/v1/accounts", "")
	req.AddCookie(cookie)
	if r := performRequest(f.engine, req); r.Code != 403 {
		t.Fatalf("renewal preserved old admin role: %d", r.Code)
	}
	req = jsonRequest("GET", "/api/v1/accounts", "")
	req.Header.Set("Authorization", "Bearer "+f.token)
	if r := performRequest(f.engine, req); r.Code != 403 {
		t.Fatalf("API key preserved old admin role: %d", r.Code)
	}
}

func TestRenewalRejectsCredentialConfusionAndArbitrarySessionInput(t *testing.T) {
	f := newAuthenticationTestFixture(t, false, domain.SessionAbsoluteTTL)
	cookie, data := exchangeAccountSession(t, f, f.token)
	csrf := data["csrf_token"].(string)
	_, other := exchangeAccountSession(t, f, f.token)
	for _, test := range []struct {
		name, auth, csrf, body string
		tampered               bool
		status                 int
	}{
		{name: "missing CSRF", status: 403},
		{name: "different session CSRF", csrf: other["csrf_token"].(string), status: 403},
		{name: "bearer is not renewable", auth: "Bearer " + f.token, csrf: csrf, status: 401},
		{name: "invalid bearer cannot fall back", auth: "Bearer invalid", csrf: csrf, status: 401},
		{name: "forged cookie", csrf: csrf, tampered: true, status: 401},
		{name: "cannot select session or duration", csrf: csrf, body: `{"session_id":"other","days":1000}`, status: 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			req := jsonRequest("POST", "/api/v1/account/session/renew", test.body)
			c := *cookie
			if test.tampered {
				c.Value += "x"
			}
			req.AddCookie(&c)
			req.Header.Set(CSRFHeaderName, test.csrf)
			if test.auth != "" {
				req.Header.Set("Authorization", test.auth)
			}
			response := performRequest(f.engine, req)
			if response.Code != test.status || len(response.Result().Cookies()) != 0 {
				t.Fatalf("status=%d want=%d body=%s", response.Code, test.status, response.Body)
			}
		})
	}
}
