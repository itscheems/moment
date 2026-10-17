package ginhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

type mentionFixture struct {
	engine   http.Handler
	manager  *auth.Manager
	accounts *app.AccountService
	token    string
}

func newMentionFixture(t *testing.T) mentionFixture {
	t.Helper()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	_, err = auth.InitializeOffline(context.Background(), runtimeState, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(runtimeState.Root, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	site, err := app.NewSiteService(&httpSiteTransactions{
		settings: app.SiteSettings{Title: "Moment", Timezone: "UTC", CommentsEnabled: true},
		media:    map[string]bool{},
	}, app.SiteOptions{AllowDevelopmentLocalHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	accounts := testAccountsForRuntime(t, runtimeState)
	engine, err := New(Config{
		Assets:       testAssets(),
		SessionSigner:    manager,
		Accounts:     accounts,
		SiteSettings: site,
		Readiness:    func(context.Context) error { return nil },
		RequestID:    func() string { return "req_mentions" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return mentionFixture{engine: engine, manager: manager, accounts: accounts, token: issueTestAPIToken(t, accounts)}
}

// signIn puts one account behind a browser credential, the same way the
// federated callback does.
func (fixture mentionFixture) signIn(t *testing.T, user domain.User) *http.Cookie {
	t.Helper()
	sessionID, err := app.NewSessionID()
	if err != nil {
		t.Fatal(err)
	}
	value, session, err := fixture.manager.IssueSessionForVerifiedSubject(auth.Subject{UserID: user.ID, SessionID: sessionID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.accounts.StartSession(context.Background(), user.ID, sessionID, "browser", session.ExpiresAt.UnixMilli()); err != nil {
		t.Fatal(err)
	}
	return &http.Cookie{Name: SessionCookieName, Value: value}
}

func (fixture mentionFixture) seed(t *testing.T, providerUserID, displayName string, status domain.AccountStatus) domain.User {
	t.Helper()
	outcome, err := fixture.accounts.SignIn(context.Background(), app.ExternalIdentity{
		Provider: "google", ProviderUserID: providerUserID, DisplayName: displayName,
	})
	if err != nil {
		t.Fatal(err)
	}
	if outcome.User.Status == status {
		return outcome.User
	}
	updated, err := fixture.accounts.SetStatus(context.Background(), outcome.User.ID, status)
	if err != nil {
		t.Fatal(err)
	}
	return updated
}

func mentionUsernames(t *testing.T, response *httptest.ResponseRecorder) []string {
	t.Helper()
	var envelope struct {
		Data []app.MentionSuggestion `json:"data"`
		Meta map[string]any          `json:"meta"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Meta["count"] != float64(len(envelope.Data)) {
		t.Fatalf("count = %v, data = %d", envelope.Meta["count"], len(envelope.Data))
	}
	names := make([]string, 0, len(envelope.Data))
	for _, item := range envelope.Data {
		names = append(names, item.Username)
	}
	return names
}

// Who may enumerate the roster.
//
// This endpoint answered for the site's single owner when it was written, which
// made it a public fact about a public profile. It now answers for every
// admitted account, over a leading-wildcard scan, and its only caller is a
// composer -- so it is admitted at the tier that may open one. Approval is the
// line because a pending account is an application, and an autocomplete that
// returned applications would be a way to read the review queue.
func TestMentionSuggestionsAreForApprovedAccountsOnly(t *testing.T) {
	fixture := newMentionFixture(t)
	owner, err := fixture.accounts.OwnerAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	admitted := fixture.seed(t, "1", "Ada Lovelace", domain.AccountActive)
	waiting := fixture.seed(t, "2", "Applicant Waiting", domain.AccountPending)

	const path = "/api/v1/users/mention-suggestions?q=&limit=8"

	t.Run("a visitor with no account cannot page through the membership list", func(t *testing.T) {
		response := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, path, nil))
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous = %d, want 401: %s", response.Code, response.Body.String())
		}
	})

	t.Run("an account still waiting to be approved cannot either", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(fixture.signIn(t, waiting))
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusForbidden {
			t.Fatalf("pending = %d, want 403: %s", response.Code, response.Body.String())
		}
		if code := decodeError(t, response.Body).Error.Code; code != string(domain.CodeAccountNotApproved) {
			t.Fatalf("code = %q, want %q", code, domain.CodeAccountNotApproved)
		}
	})

	t.Run("an approved account gets the admitted roster and nobody else", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(fixture.signIn(t, admitted))
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusOK {
			t.Fatalf("member = %d, body = %s", response.Code, response.Body.String())
		}
		names := mentionUsernames(t, response)
		if len(names) != 2 || names[0] != owner.Username || names[1] != admitted.Username {
			t.Fatalf("suggestions = %v, want the owner first then %q", names, admitted.Username)
		}
		for _, name := range names {
			if name == waiting.Username {
				t.Fatalf("an account nobody has admitted was suggestible: %v", names)
			}
		}
	})

	t.Run("the owner's own token reaches it too", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/users/mention-suggestions?q=ada&limit=8", nil)
		request.Header.Set("Authorization", "Bearer "+fixture.token)
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusOK {
			t.Fatalf("owner = %d, body = %s", response.Code, response.Body.String())
		}
		if names := mentionUsernames(t, response); len(names) != 1 || names[0] != admitted.Username {
			t.Fatalf("suggestions = %v, want %q alone", names, admitted.Username)
		}
	})

	t.Run("a limit outside the bound is still a bad request", func(t *testing.T) {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/users/mention-suggestions?limit=0", nil)
		request.Header.Set("Authorization", "Bearer "+fixture.token)
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("invalid limit = %d, body = %s", response.Code, response.Body.String())
		}
	})
}
