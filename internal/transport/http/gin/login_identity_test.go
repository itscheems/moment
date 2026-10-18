package ginhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
)

// exchangeWith posts a sign-in body and reports what came back.
func exchangeWith(t *testing.T, fixture authenticationTestFixture, body map[string]string) (int, string) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response := performRequest(fixture.engine,
		jsonRequest(http.MethodPost, "/api/v1/login/session", string(encoded)))
	return response.Code, response.Body.String()
}

// Token identity comes from its account row; an optional handle cannot
// attribute a credential to another account.
func TestSignInCredentialsCannotBeAttributedToAnotherAccount(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	account, err := fixture.accounts.ProfileAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name   string
		status int
	}{{account.Username, 200}, {"  " + strings.ToUpper(account.Username) + "  ", 200}, {"someone", 401}, {"nobodyhere", 401}, {"Not A Username", 401}, {"", 200}} {
		request := map[string]string{"token": fixture.token}
		if test.name != "" {
			request["username"] = test.name
		}
		status, body := exchangeWith(t, fixture, request)
		if status != test.status {
			t.Fatalf("credential attribution=%d want %d: %s", status, test.status, body)
		}
	}
	member := domain.User{ID: "fedcba9876543210fedcba9876543210", Username: "someone", DisplayName: "Someone", Role: domain.UserRoleMember, Status: domain.AccountActive, CreatedAtMS: 1, UpdatedAtMS: 1}
	fixture.store.users[member.ID] = member
	issued, err := fixture.accounts.CreateAPIToken(context.Background(), member.ID, "member device")
	if err != nil {
		t.Fatal(err)
	}
	response := performRequest(fixture.engine, jsonRequest(http.MethodPost, "/api/v1/login/session", string(mustJSON(t, map[string]string{"token": issued.Value}))))
	if response.Code != http.StatusOK {
		t.Fatalf("member token sign-in=%d %s", response.Code, response.Body.String())
	}
	current := jsonRequest(http.MethodGet, "/api/v1/account/session", "")
	current.AddCookie(sessionCookieFrom(response))
	data := decodeDataMap(t, performRequest(fixture.engine, current))["account"].(map[string]any)
	if data["user_id"] != member.ID {
		t.Fatal("a personal token signed in as the site profile account")
	}
}

// The list calls itself active sessions. Expired rows outlive their expiry by
// the retention grace that keeps "when did this device last sign in"
// answerable, and that grace is for the janitor -- a credential that can no
// longer authenticate anything has no business being offered as one that can.
func TestActiveSessionsExcludeTheOnesThatNoLongerWork(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	cookie, _ := exchangeAccountSession(t, fixture, fixture.token)

	primaryAccount, err := fixture.accounts.ProfileAccount(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	expired, err := fixture.accounts.StartSession(t.Context(), primaryAccount.ID,
		"11111111111111111111111111111111", "web",
		time.Now().UTC().Add(-time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}

	request := jsonRequest(http.MethodGet, "/api/v1/account/sessions", "")
	request.AddCookie(cookie)
	response := performRequest(fixture.engine, request)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d: %s", response.Code, response.Body.String())
	}
	body := response.Body.String()
	if strings.Contains(body, expired.ID) {
		t.Fatalf("an expired session was offered as active: %s", body)
	}
	// The live one is still there, which is what proves the filter removed a
	// dead row rather than the whole list.
	if !strings.Contains(body, `"current":true`) {
		t.Fatalf("the signed-in session went missing: %s", body)
	}
}
