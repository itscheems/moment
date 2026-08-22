package ginhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moment/moment/internal/domain"
)

// Moderating the conversation is a different authority from taking part in it.
//
// Both lived in one owner-only route group until a second account could exist.
// Stepping that group down to "any approved account" -- which is what lets a
// member comment at all -- would otherwise hand every approved member the
// power to pin and unpin anyone's conversation. This is the test that keeps the
// two authorities apart.
func TestAnApprovedMemberCannotModerateTheConversation(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)

	flow, state := startOAuthFlowOn(t, fixture.engine)
	session := sessionCookieFrom(performRequest(fixture.engine, callbackRequest(state, "code", flow)))
	if session == nil {
		t.Fatal("sign-in issued no session")
	}

	accounts, err := fixture.accountService.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var member domain.User
	for _, account := range accounts {
		if account.Role == domain.UserRoleMember {
			member = account
		}
	}
	if member.ID == "" {
		t.Fatal("sign-in created no member account")
	}
	// Approved, so this is not the pending boundary being tested again.
	if _, err := fixture.accountService.SetStatus(context.Background(), member.ID, domain.AccountActive); err != nil {
		t.Fatal(err)
	}

	current := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	current.AddCookie(session)
	csrf := decodeCSRF(t, performRequest(fixture.engine, current))
	if csrf == "" {
		t.Fatal("an approved member holds no CSRF token")
	}

	for name, probe := range map[string]struct {
		method string
		path   string
	}{
		"pinning a conversation":   {http.MethodPut, "/api/v1/owner/posts/01ARZ3NDEKTSV4RRFFQ69G5FAV/comments/01ARZ3NDEKTSV4RRFFQ69G5FB0/pin"},
		"unpinning a conversation": {http.MethodDelete, "/api/v1/owner/posts/01ARZ3NDEKTSV4RRFFQ69G5FAV/comments/01ARZ3NDEKTSV4RRFFQ69G5FB0/pin"},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(probe.method, probe.path, nil)
			request.AddCookie(session)
			request.Header.Set(CSRFHeaderName, csrf)
			response := performRequest(fixture.engine, request)
			if response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
				t.Fatalf("%s = %d, want a refusal: %s", probe.path, response.Code, response.Body.String())
			}
		})
	}
}

func decodeCSRF(t *testing.T, response *httptest.ResponseRecorder) string {
	t.Helper()
	body := response.Body.String()
	marker := `"csrf_token":"`
	start := strings.Index(body, marker)
	if start < 0 {
		t.Fatalf("no CSRF token in %s", body)
	}
	rest := body[start+len(marker):]
	end := strings.Index(rest, `"`)
	if end < 0 {
		t.Fatalf("unterminated CSRF token in %s", body)
	}
	return rest[:end]
}
