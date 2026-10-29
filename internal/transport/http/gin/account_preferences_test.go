package ginhttp

import (
	"net/http"
	"testing"
)

func TestOwnAccountPreferencesRequireCSRFAndRemainPrivate(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	login := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/login/session", `{"token":"`+fixture.token+`"}`))
	if login.Code != http.StatusOK {
		t.Fatalf("login: %d %s", login.Code, login.Body.String())
	}
	cookie := sessionCookieFrom(login)
	csrf := decodeDataMap(t, login)["csrf_token"].(string)
	for _, item := range []struct {
		body   string
		csrf   bool
		status int
	}{
		{`{"ui_locale":"en"}`, false, http.StatusForbidden},
		{`{}`, true, http.StatusBadRequest},
		{`{"ui_locale":"fr"}`, true, http.StatusBadRequest},
		{`{"ui_locale":"en","user_id":"another"}`, true, http.StatusBadRequest},
		{`{"ui_locale":"en"}`, true, http.StatusOK},
	} {
		request := jsonRequest(http.MethodPatch, "/api/v1/account/preferences", item.body)
		request.AddCookie(cookie)
		if item.csrf {
			request.Header.Set(CSRFHeaderName, csrf)
		}
		result := performRequest(fixture.engine, request)
		if result.Code != item.status {
			t.Fatalf("%s: %d %s", item.body, result.Code, result.Body.String())
		}
	}
	read := jsonRequest(http.MethodGet, "/api/v1/account/session", "")
	read.AddCookie(cookie)
	data := decodeDataMap(t, performRequest(fixture.engine, read))
	if data["preferences"].(map[string]any)["ui_locale"] != "en" {
		t.Fatalf("restored session: %+v", data)
	}
	profile := data["account"].(map[string]any)
	public := performRequest(fixture.engine, jsonRequest(http.MethodGet, "/api/v1/users/"+profile["username"].(string), ""))
	if _, leaked := decodeDataMap(t, public)["ui_locale"]; leaked {
		t.Fatal("public profile leaked preference")
	}
	if result := performRequest(fixture.engine, jsonRequest(http.MethodGet, "/api/v1/account/preferences", "")); result.Code != http.StatusUnauthorized {
		t.Fatal("anonymous private preferences read accepted")
	}
}
