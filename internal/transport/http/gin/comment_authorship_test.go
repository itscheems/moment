package ginhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moment/moment/internal/domain"
)

func TestAnApprovedMemberCannotModerateTheConversation(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	member := approvedMemberSession(t, fixture)

	for name, probe := range map[string]struct {
		method string
		path   string
	}{
		"pinning a conversation":   {http.MethodPut, "/api/v1/posts/01ARZ3NDEKTSV4RRFFQ69G5FAV/comments/01ARZ3NDEKTSV4RRFFQ69G5FB0/pin"},
		"unpinning a conversation": {http.MethodDelete, "/api/v1/posts/01ARZ3NDEKTSV4RRFFQ69G5FAV/comments/01ARZ3NDEKTSV4RRFFQ69G5FB0/pin"},
	} {
		t.Run(name, func(t *testing.T) {
			response := performRequest(fixture.engine, memberCommentRequest(member, probe.method, probe.path, ""))
			if response.Code != http.StatusUnauthorized && response.Code != http.StatusForbidden {
				t.Fatalf("%s = %d, want a refusal: %s", probe.path, response.Code, response.Body.String())
			}
		})
	}
}

// Removing a comment removes every reply under it, which is the other half of
// the same split. Taking your own words back is yours; taking back the answer
// somebody else wrote underneath them is moderation, and the SQL cascade cannot
// tell the two apart on its own.
func TestAnApprovedMemberCannotDeleteAnotherAccountsReply(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	member := approvedMemberSession(t, fixture)

	created := performRequest(fixture.engine, memberCommentRequest(member, http.MethodPost, commentResourcePath(fixture), `{"content_md":"a member writes first"}`))
	if created.Code != http.StatusCreated {
		t.Fatalf("member create = %d %s", created.Code, created.Body.String())
	}
	written := commentData(t, created)
	rootID, ok := written["id"].(string)
	if !ok {
		t.Fatalf("member comment = %#v", written)
	}
	if author, resolved := written["author"].(map[string]any); !resolved || author["user_id"] != member.user.ID {
		t.Fatalf("member comment author = %#v, want %s", written["author"], member.user.ID)
	}
	reply := postAccountComment(t, fixture, `{"content_md":"and the owner answers","parent_id":"`+rootID+`"}`)

	for name, path := range map[string]string{
		"removal": "/api/v1/comments/" + rootID,
		"preview": "/api/v1/comments/" + rootID + "?dry_run=true",
	} {
		t.Run(name, func(t *testing.T) {
			response := performRequest(fixture.engine, memberCommentRequest(member, http.MethodDelete, path, ""))
			if response.Code != http.StatusForbidden || decodeError(t, response.Body).Error.Code != "UNAUTHORIZED" {
				t.Fatalf("%s = %d, want a refusal: %s", name, response.Code, response.Body.String())
			}
		})
	}

	items := decodeCommentItems(t, performRequest(fixture.engine, commentRequest(http.MethodGet, publicCommentPath(fixture), "")))
	if len(items) != 2 || items[0]["id"] != rootID || items[1]["id"] != reply["id"] {
		t.Fatalf("thread after the refusal = %#v", items)
	}
}

// And the rule the cascade guard stands on top of: a comment somebody else
// wrote is not this account's to remove at all, replies or no replies. It
// arrived with the route group being stepped down and had nothing holding it.
func TestAnApprovedMemberCannotDeleteSomebodyElsesComment(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	member := approvedMemberSession(t, fixture)
	owned := postAccountComment(t, fixture, `{"content_md":"the owner's own words"}`)
	ownedID, ok := owned["id"].(string)
	if !ok {
		t.Fatalf("owner comment = %#v", owned)
	}

	response := performRequest(fixture.engine, memberCommentRequest(member, http.MethodDelete, "/api/v1/comments/"+ownedID, ""))
	if response.Code != http.StatusForbidden || decodeError(t, response.Body).Error.Code != "UNAUTHORIZED" {
		t.Fatalf("member delete of the owner's comment = %d: %s", response.Code, response.Body.String())
	}

	items := decodeCommentItems(t, performRequest(fixture.engine, commentRequest(http.MethodGet, publicCommentPath(fixture), "")))
	if len(items) != 1 || items[0]["id"] != ownedID {
		t.Fatalf("thread after the refusal = %#v", items)
	}
}

type commentMember struct {
	user    domain.User
	session *http.Cookie
	csrf    string
}

func approvedMemberSession(t *testing.T, fixture commentHTTPFixture) commentMember {
	t.Helper()
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
	// Approved, so nothing here is the pending boundary being tested again.
	member, err = fixture.accountService.SetStatus(context.Background(), member.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}

	current := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	current.AddCookie(session)
	csrf := decodeCSRF(t, performRequest(fixture.engine, current))
	if csrf == "" {
		t.Fatal("an approved member holds no CSRF token")
	}
	return commentMember{user: member, session: session, csrf: csrf}
}

// memberCommentRequest authenticates as a member the way a browser does: the
// session cookie, and the CSRF header that cookie authentication requires.
func memberCommentRequest(member commentMember, method, path, body string) *http.Request {
	request := jsonRequest(method, path, body)
	request.RemoteAddr = "192.0.2.25:4000"
	request.AddCookie(member.session)
	request.Header.Set(CSRFHeaderName, member.csrf)
	return request
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
