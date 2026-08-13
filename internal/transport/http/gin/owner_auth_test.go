package ginhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/runtimefs"
)

type ownerTestFixture struct {
	manager               *auth.Manager
	token                 string
	engine                http.Handler
	now                   *time.Time
	instanceID            string
	instanceInitializedAt string
}

func newOwnerTestFixture(t *testing.T, secure bool) ownerTestFixture {
	t.Helper()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	credential, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{Now: func() time.Time { return now }})
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
	engine, err := New(Config{
		Assets:        testAssets(),
		OwnerAuth:     manager,
		SecureCookies: secure,
		Readiness:     func(context.Context) error { return nil },
		RequestID:     func() string { return "req_owner" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return ownerTestFixture{manager: manager, token: credential.Token, engine: engine, now: &now, instanceID: runtimeState.Instance.ID}
}

func ownerRequest(method, path string, body string) *http.Request {
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

func exchangeOwnerSession(t *testing.T, fixture ownerTestFixture, token string) (*http.Cookie, map[string]any) {
	t.Helper()
	body, err := json.Marshal(map[string]string{"token": token})
	if err != nil {
		t.Fatal(err)
	}
	response := performRequest(fixture.engine, ownerRequest(http.MethodPost, "/api/v1/owner/session", string(body)))
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

func TestOwnerSessionCookieCSRFLogoutAndRotationContract(t *testing.T) {
	fixture := newOwnerTestFixture(t, true)
	cookie, sessionData := exchangeOwnerSession(t, fixture, fixture.token)
	if !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode || cookie.Path != "/" || cookie.Expires.IsZero() || cookie.MaxAge <= 0 {
		t.Fatalf("cookie = %#v", cookie)
	}
	if strings.Contains(string(mustJSON(t, sessionData)), fixture.token) {
		t.Fatal("session response leaked bearer token")
	}
	csrf, _ := sessionData["csrf_token"].(string)
	if csrf == "" {
		t.Fatalf("session data = %#v", sessionData)
	}

	statusRequest := ownerRequest(http.MethodGet, "/api/v1/owner/status", "")
	statusRequest.AddCookie(cookie)
	status := performRequest(fixture.engine, statusRequest)
	if status.Code != http.StatusOK || decodeDataMap(t, status)["session_epoch"] != float64(1) {
		t.Fatalf("status = %d, body = %s", status.Code, status.Body.String())
	}

	missingCSRF := ownerRequest(http.MethodPost, "/api/v1/owner/token/rotate", "")
	missingCSRF.AddCookie(cookie)
	missingResponse := performRequest(fixture.engine, missingCSRF)
	if missingResponse.Code != http.StatusForbidden || decodeError(t, missingResponse.Body).Error.Code != "CSRF_INVALID" {
		t.Fatalf("missing CSRF status = %d", missingResponse.Code)
	}

	rotateRequest := ownerRequest(http.MethodPost, "/api/v1/owner/token/rotate", "")
	rotateRequest.AddCookie(cookie)
	rotateRequest.Header.Set(CSRFHeaderName, csrf)
	rotatedResponse := performRequest(fixture.engine, rotateRequest)
	if rotatedResponse.Code != http.StatusOK {
		t.Fatalf("rotate status = %d, body = %s", rotatedResponse.Code, rotatedResponse.Body.String())
	}
	rotated := decodeDataMap(t, rotatedResponse)
	newToken, _ := rotated["token"].(string)
	if newToken == "" || newToken == fixture.token || rotated["session_epoch"] != float64(2) {
		t.Fatalf("rotated = %#v", rotated)
	}

	oldCookieRequest := ownerRequest(http.MethodGet, "/api/v1/owner/status", "")
	oldCookieRequest.AddCookie(cookie)
	if response := performRequest(fixture.engine, oldCookieRequest); response.Code != http.StatusUnauthorized {
		t.Fatalf("old cookie status = %d", response.Code)
	}
	oldBearerRequest := ownerRequest(http.MethodGet, "/api/v1/owner/status", "")
	oldBearerRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, oldBearerRequest); response.Code != http.StatusUnauthorized {
		t.Fatalf("old bearer status = %d", response.Code)
	}
	newBearerRequest := ownerRequest(http.MethodGet, "/api/v1/owner/status", "")
	newBearerRequest.Header.Set("Authorization", "Bearer "+newToken)
	if response := performRequest(fixture.engine, newBearerRequest); response.Code != http.StatusOK {
		t.Fatalf("new bearer status = %d, body = %s", response.Code, response.Body.String())
	}

	newCookie, newSession := exchangeOwnerSession(t, fixture, newToken)
	logoutRequest := ownerRequest(http.MethodDelete, "/api/v1/owner/session", "")
	logoutRequest.AddCookie(newCookie)
	logoutRequest.Header.Set(CSRFHeaderName, newSession["csrf_token"].(string))
	logout := performRequest(fixture.engine, logoutRequest)
	if logout.Code != http.StatusOK || decodeDataMap(t, logout)["logged_out"] != true {
		t.Fatalf("logout status = %d, body = %s", logout.Code, logout.Body.String())
	}
	cleared := logout.Result().Cookies()
	if len(cleared) != 1 || cleared[0].Name != SessionCookieName || cleared[0].MaxAge != -1 || !cleared[0].HttpOnly || !cleared[0].Secure || cleared[0].SameSite != http.SameSiteLaxMode {
		t.Fatalf("cleared cookies = %#v", cleared)
	}
}

func TestBearerPrecedenceMalformedInputAndSessionExpiryFailClosed(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	cookie, _ := exchangeOwnerSession(t, fixture, fixture.token)

	malformed := ownerRequest(http.MethodGet, "/api/v1/owner/status", "")
	malformed.AddCookie(cookie)
	malformed.Header.Set("Authorization", "Bearer wrong")
	if response := performRequest(fixture.engine, malformed); response.Code != http.StatusUnauthorized {
		t.Fatalf("malformed bearer fell back to cookie: %d", response.Code)
	}
	blank := ownerRequest(http.MethodGet, "/api/v1/owner/status", "")
	blank.AddCookie(cookie)
	blank.Header.Set("Authorization", " ")
	if response := performRequest(fixture.engine, blank); response.Code != http.StatusUnauthorized {
		t.Fatalf("blank authorization header fell back to cookie: %d", response.Code)
	}

	duplicate := ownerRequest(http.MethodGet, "/api/v1/owner/status", "")
	duplicate.Header.Add("Authorization", "Bearer "+fixture.token)
	duplicate.Header.Add("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, duplicate); response.Code != http.StatusUnauthorized {
		t.Fatalf("duplicate auth headers status = %d", response.Code)
	}

	*fixture.now = fixture.now.Add(31 * time.Minute)
	expired := ownerRequest(http.MethodGet, "/api/v1/owner/status", "")
	expired.AddCookie(cookie)
	if response := performRequest(fixture.engine, expired); response.Code != http.StatusUnauthorized {
		t.Fatalf("expired cookie status = %d", response.Code)
	}

	for _, body := range []string{"", `{}`, `{"token":"x","extra":true}`, `{not-json}`} {
		response := performRequest(fixture.engine, ownerRequest(http.MethodPost, "/api/v1/owner/session", body))
		if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("body %q status = %d, response = %s", body, response.Code, response.Body.String())
		}
	}
}

func TestBearerAuthenticatedWriteDoesNotRequireCSRF(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	request := ownerRequest(http.MethodPost, "/api/v1/owner/token/rotate", "")
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	response := performRequest(fixture.engine, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
}

func TestPasswordLoginOptionsSetupAndExchange(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	options := performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/owner/login-options", ""))
	if options.Code != http.StatusOK || decodeDataMap(t, options)["password_enabled"] != false {
		t.Fatalf("initial login options = %d, body = %s", options.Code, options.Body.String())
	}
	set := ownerRequest(http.MethodPut, "/api/v1/owner/password", `{"password":"correct horse battery staple"}`)
	set.Header.Set("Authorization", "Bearer "+fixture.token)
	setResponse := performRequest(fixture.engine, set)
	if setResponse.Code != http.StatusOK || decodeDataMap(t, setResponse)["password_enabled"] != true {
		t.Fatalf("set password = %d, body = %s", setResponse.Code, setResponse.Body.String())
	}
	wrong := performRequest(fixture.engine, ownerRequest(http.MethodPost, "/api/v1/owner/session", `{"password":"incorrect password"}`))
	if wrong.Code != http.StatusUnauthorized {
		t.Fatalf("wrong password = %d, body = %s", wrong.Code, wrong.Body.String())
	}
	passwordSession := performRequest(fixture.engine, ownerRequest(http.MethodPost, "/api/v1/owner/session", `{"password":"correct horse battery staple"}`))
	if passwordSession.Code != http.StatusOK || decodeDataMap(t, passwordSession)["csrf_token"] == "" {
		t.Fatalf("password session = %d, body = %s", passwordSession.Code, passwordSession.Body.String())
	}
	options = performRequest(fixture.engine, ownerRequest(http.MethodGet, "/api/v1/owner/login-options", ""))
	if decodeDataMap(t, options)["password_enabled"] != true {
		t.Fatalf("updated login options = %s", options.Body.String())
	}
	for _, body := range []string{`{"token":"x","password":"y"}`, `{"password":"short","extra":true}`, `{"password":""}`} {
		response := performRequest(fixture.engine, ownerRequest(http.MethodPost, "/api/v1/owner/session", body))
		if response.Code != http.StatusBadRequest {
			t.Fatalf("invalid exchange %s = %d, body = %s", body, response.Code, response.Body.String())
		}
	}
}

func TestConcurrentlyPreauthorizedOldBearerCanRotateOnlyOnce(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	engine := fixture.engine.(*gin.Engine)
	arrived := make(chan struct{}, 2)
	release := make(chan struct{})
	engine.POST("/api/v1/owner/test-rotate", RequireOwner(fixture.manager), func(ctx *gin.Context) {
		arrived <- struct{}{}
		<-release
		grant, ok := ownerGrant(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		credential, err := fixture.manager.RotateAuthorized(grant)
		if errors.Is(err, auth.ErrUnauthorized) {
			unauthorized(ctx)
			return
		}
		if err != nil {
			Failure(ctx, http.StatusInternalServerError, "INTERNAL", "owner token rotation failed", nil)
			return
		}
		Success(ctx, http.StatusOK, credential, nil)
	})

	responses := make(chan *httptest.ResponseRecorder, 2)
	for range 2 {
		go func() {
			request := ownerRequest(http.MethodPost, "/api/v1/owner/test-rotate", "")
			request.Header.Set("Authorization", "Bearer "+fixture.token)
			responses <- performRequest(engine, request)
		}()
	}
	for range 2 {
		select {
		case <-arrived:
		case <-time.After(time.Second):
			t.Fatal("requests did not both reach the handler with old grants")
		}
	}
	close(release)

	statusCounts := map[int]int{}
	var newToken string
	for range 2 {
		response := <-responses
		statusCounts[response.Code]++
		if response.Code == http.StatusOK {
			newToken, _ = decodeDataMap(t, response)["token"].(string)
		}
	}
	if statusCounts[http.StatusOK] != 1 || statusCounts[http.StatusUnauthorized] != 1 {
		t.Fatalf("response status counts = %#v", statusCounts)
	}
	status, err := fixture.manager.Status()
	if err != nil || status.SessionEpoch != 2 || newToken == "" || !fixture.manager.AuthenticateBearer(newToken) {
		t.Fatalf("status = %#v, new token live = %t, err = %v", status, fixture.manager.AuthenticateBearer(newToken), err)
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return encoded
}
