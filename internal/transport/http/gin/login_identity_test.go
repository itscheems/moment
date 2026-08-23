package ginhttp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
)

// exchangeWith posts a sign-in body and reports what came back.
func exchangeWith(t *testing.T, fixture ownerTestFixture, body map[string]string) (int, string) {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatal(err)
	}
	response := performRequest(fixture.engine,
		ownerRequest(http.MethodPost, "/api/v1/owner/session", string(encoded)))
	return response.Code, response.Body.String()
}

// The sign-in form collects a handle, and a form that collects one and then
// ignores it is worse than one that never asked: every correct password would
// answer with the owner's session no matter whose name was typed above it.
func TestSignInRefusesACredentialTypedUnderAnotherName(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	member := domain.User{
		ID:          "fedcba9876543210fedcba9876543210",
		Username:    "someone",
		DisplayName: "Someone",
		Role:        domain.UserRoleMember,
		Status:      domain.AccountActive,
		CreatedAtMS: 1,
		UpdatedAtMS: 1,
	}
	fixture.store.users[member.ID] = member

	t.Run("the owner's own handle is accepted", func(t *testing.T) {
		status, body := exchangeWith(t, fixture, map[string]string{
			"token": fixture.token, "username": "owner",
		})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", status, body)
		}
	})

	// The column is COLLATE NOCASE and handles are lowercased on the way in.
	// A form that refuses a name the database considers the same name would be
	// rejecting people for their shift key.
	t.Run("case and surrounding space do not change who a handle names", func(t *testing.T) {
		status, body := exchangeWith(t, fixture, map[string]string{
			"token": fixture.token, "username": "  OWNER  ",
		})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", status, body)
		}
	})

	// Only the owner has a password or a token; everyone else reaches this
	// instance through a provider. Saying so is more use than a wrong-password
	// message, which sends people to reset a credential that was never at fault.
	t.Run("an account with no password is sent to its provider", func(t *testing.T) {
		status, body := exchangeWith(t, fixture, map[string]string{
			"token": fixture.token, "username": "someone",
		})
		if status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401: %s", status, body)
		}
		if !strings.Contains(body, "PASSWORD_NOT_AVAILABLE") {
			t.Fatalf("body = %s", body)
		}
	})

	t.Run("a handle nobody holds is named as the problem", func(t *testing.T) {
		status, body := exchangeWith(t, fixture, map[string]string{
			"token": fixture.token, "username": "nobodyhere",
		})
		if status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401: %s", status, body)
		}
		if !strings.Contains(body, "ACCOUNT_NOT_FOUND") {
			t.Fatalf("body = %s", body)
		}
	})

	// A handle that could never be one gets the same answer as one nobody
	// holds. It is the same fact, and splitting it would only describe the
	// validator.
	t.Run("a handle that is not even shaped like one is refused the same way", func(t *testing.T) {
		status, body := exchangeWith(t, fixture, map[string]string{
			"token": fixture.token, "username": "Not A Username",
		})
		if status != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401: %s", status, body)
		}
		if !strings.Contains(body, "ACCOUNT_NOT_FOUND") {
			t.Fatalf("body = %s", body)
		}
	})

	// The token is the break-glass credential. It has to keep working when
	// nothing else does, including when nobody remembers the handle.
	t.Run("omitting the handle still signs the owner in", func(t *testing.T) {
		status, body := exchangeWith(t, fixture, map[string]string{"token": fixture.token})
		if status != http.StatusOK {
			t.Fatalf("status = %d, want 200: %s", status, body)
		}
	})
}

// The list calls itself active sessions. Expired rows outlive their expiry by
// the retention grace that keeps "when did this device last sign in"
// answerable, and that grace is for the janitor -- a credential that can no
// longer authenticate anything has no business being offered as one that can.
func TestActiveSessionsExcludeTheOnesThatNoLongerWork(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	cookie, _ := exchangeOwnerSession(t, fixture, fixture.token)

	owner, err := fixture.accounts.OwnerAccount(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	expired, err := fixture.accounts.StartSession(t.Context(), owner.ID,
		"11111111111111111111111111111111", "web",
		time.Now().UTC().Add(-time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}

	request := ownerRequest(http.MethodGet, "/api/v1/account/sessions", "")
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
