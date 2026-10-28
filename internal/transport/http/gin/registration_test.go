package ginhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moment/moment/internal/domain"
)

func TestRegistrationOptionsAndSessionContract(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, true)
	for _, mode := range []domain.RegistrationMode{domain.RegistrationOpen, domain.RegistrationApproval, domain.RegistrationClosed} {
		fixture.store.mode = mode
		response := performRequest(fixture.engine, jsonRequest(http.MethodGet, "/api/v1/register/options", ""))
		if response.Code != http.StatusOK {
			t.Fatalf("options: %d %s", response.Code, response.Body.String())
		}
		data := decodeDataMap(t, response)
		if len(data) != 5 || data["registration_mode"] != string(mode) || data["username_min_length"] != float64(3) || data["username_max_length"] != float64(30) || data["password_min_bytes"] != float64(8) || data["password_max_bytes"] != float64(72) {
			t.Fatalf("options: %+v", data)
		}
	}
	fixture.store.mode = domain.RegistrationApproval
	response := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/register", `{"username":"NewMember","password":"password123"}`))
	if response.Code != http.StatusCreated {
		t.Fatalf("register: %d %s", response.Code, response.Body.String())
	}
	data := decodeDataMap(t, response)
	if len(data) != 5 || data["account_created"] != true || data["username"] != "newmember" || data["account_status"] != "pending" || data["expires_at"] == "" || data["csrf_token"] == "" {
		t.Fatalf("registration response: %+v", data)
	}
	cookie := sessionCookieFrom(response)
	if cookie == nil || !cookie.HttpOnly || !cookie.Secure || cookie.SameSite != http.SameSiteLaxMode {
		t.Fatalf("registration cookie: %+v", cookie)
	}
	read := jsonRequest(http.MethodGet, "/api/v1/account/session", "")
	read.AddCookie(cookie)
	readResponse := performRequest(fixture.engine, read)
	if readResponse.Code != http.StatusOK {
		t.Fatalf("read registered session: %d %s", readResponse.Code, readResponse.Body.String())
	}
	account := decodeDataMap(t, readResponse)["account"].(map[string]any)
	if account["username"] != "newmember" || account["status"] != "pending" || account["role"] != "member" {
		t.Fatalf("registered account: %+v", account)
	}
	pendingWrite := jsonRequest(http.MethodPost, "/api/v1/account/tokens", `{"label":"pending-device"}`)
	pendingWrite.AddCookie(cookie)
	pendingWrite.Header.Set(CSRFHeaderName, data["csrf_token"].(string))
	if result := performRequest(fixture.engine, pendingWrite); result.Code != http.StatusForbidden || decodeError(t, result.Body).Error.Code != "ACCOUNT_NOT_APPROVED" {
		t.Fatalf("pending account escaped active-account gate: %d %s", result.Code, result.Body.String())
	}
	passwordLogin := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/login/session", `{"username":"newmember","password":"password123"}`))
	if passwordLogin.Code != http.StatusOK {
		t.Fatalf("password login: %d %s", passwordLogin.Code, passwordLogin.Body.String())
	}
	protected := jsonRequest(http.MethodPut, "/api/v1/account/password", `{"password":"replacement-password"}`)
	protected.AddCookie(cookie)
	protected.AddCookie(confirmOverHTTP(t, fixture, cookie, data["csrf_token"].(string), `{"password":"password123"}`))
	protected.Header.Set(CSRFHeaderName, data["csrf_token"].(string))
	if result := performRequest(fixture.engine, protected); result.Code != http.StatusOK {
		t.Fatalf("registered CSRF did not authorize own account: %d %s", result.Code, result.Body.String())
	}
}

func TestRegistrationRejectsClosedInvalidAndForgedInput(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	for _, item := range []struct {
		body      string
		code      int
		errorCode string
	}{
		{`{"username":"validname","password":"short"}`, http.StatusBadRequest, "INVALID_ARGUMENT"},
		{`{"username":"register","password":"password123"}`, http.StatusBadRequest, "USERNAME_RESERVED"},
		{`{"username":"validname","password":"password123","role":"admin"}`, http.StatusBadRequest, "INVALID_ARGUMENT"},
		{`{"username":"validname","password":"password123"} {}`, http.StatusBadRequest, "INVALID_ARGUMENT"},
	} {
		response := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/register", item.body))
		if response.Code != item.code || decodeError(t, response.Body).Error.Code != item.errorCode {
			t.Fatalf("invalid registration: %d %s", response.Code, response.Body.String())
		}
	}
	malformedUTF8 := httptest.NewRequest(http.MethodPost, "/api/v1/register", strings.NewReader("{\"username\":\"validname\",\"password\":\"password"+string([]byte{0xff})+"\"}"))
	malformedUTF8.Header.Set("Content-Type", "application/json")
	if result := performRequest(fixture.engine, malformedUTF8); result.Code != http.StatusBadRequest || decodeError(t, result.Body).Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("invalid UTF-8 registration: %d %s", result.Code, result.Body.String())
	}
	fixture.store.mode = domain.RegistrationClosed
	closed := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/register", `{"username":"validname","password":"password123"}`))
	if closed.Code != http.StatusForbidden || decodeError(t, closed.Body).Error.Code != "REGISTRATION_CLOSED" {
		t.Fatalf("closed registration: %d %s", closed.Code, closed.Body.String())
	}
	fixture.store.mode = domain.RegistrationOpen
	created := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/register", `{"username":"validname","password":"password123"}`))
	if created.Code != http.StatusCreated || decodeDataMap(t, created)["account_status"] != "active" {
		t.Fatalf("open registration: %d %s", created.Code, created.Body.String())
	}
	duplicate := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/register", `{"username":"VALIDNAME","password":"password123"}`))
	if duplicate.Code != http.StatusConflict || decodeError(t, duplicate.Body).Error.Code != "USERNAME_TAKEN" {
		t.Fatalf("duplicate registration: %d %s", duplicate.Code, duplicate.Body.String())
	}
}

func TestRegistrationCreationSurvivesSessionStoreFailure(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	fixture.store.failSession = true
	response := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/register", `{"username":"recoverable","password":"password123"}`))
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("session failure: %d %s", response.Code, response.Body.String())
	}
	errorData := decodeError(t, response.Body).Error
	if errorData.Code != "REGISTRATION_SESSION_UNAVAILABLE" || errorData.Details["account_created"] != true || errorData.Details["username"] != "recoverable" || errorData.Details["recovery"] != "login" {
		t.Fatalf("session failure did not say account was created: %+v", errorData)
	}
	if sessionCookieFrom(response) != nil {
		t.Fatal("unpersisted session was put in a cookie")
	}
	fixture.store.failSession = false
	if _, err := fixture.accounts.VerifyPassword(context.Background(), "recoverable", "password123"); err != nil {
		t.Fatalf("created account cannot recover by password: %v", err)
	}
	login := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/login/session", `{"username":"recoverable","password":"password123"}`))
	if login.Code != http.StatusOK {
		t.Fatalf("recovery login: %d %s", login.Code, login.Body.String())
	}
}

func TestSessionIssuingRoutesRejectCrossSiteFormMediaTypes(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	owner, err := fixture.accounts.ProfileAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err := fixture.accounts.SetAccountPassword(context.Background(), owner.ID, "password123="); err != nil {
		t.Fatal(err)
	}
	for _, item := range []struct{ path, body string }{
		{"/api/v1/register", `{"username":"formuser","password":"password123="}`},
		{"/api/v1/login/session", `{"username":"writer","password":"password123="}`},
	} {
		request := httptest.NewRequest(http.MethodPost, item.path, strings.NewReader(item.body))
		request.Header.Set("Content-Type", "text/plain")
		request.Header.Set("Origin", "https://other.example")
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusUnsupportedMediaType || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" || sessionCookieFrom(response) != nil {
			t.Fatalf("cross-site form accepted at %s: %d %s", item.path, response.Code, response.Body.String())
		}
	}
}

func TestRegistrationDoesNotReplaceAnExistingSignedInSession(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	cookie, _ := exchangeAccountSession(t, fixture, fixture.token)
	request := jsonRequest(http.MethodPost, "/api/v1/register", `{"username":"anotheruser","password":"password123"}`)
	request.AddCookie(cookie)
	response := performRequest(fixture.engine, request)
	if response.Code != http.StatusConflict || decodeError(t, response.Body).Error.Code != "ALREADY_SIGNED_IN" || sessionCookieFrom(response) != nil {
		t.Fatalf("registration replaced signed-in account: %d %s", response.Code, response.Body.String())
	}
	if _, err := fixture.accounts.VerifyPassword(context.Background(), "anotheruser", "password123"); !domain.HasCode(err, domain.CodeAccountNotFound) {
		t.Fatalf("rejected registration still created an account: %v", err)
	}
}
