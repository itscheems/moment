package ginhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/doctor"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// unreadableAccounts is what a half-applied migration or a corrupted page looks
// like from above the storage layer: the file opens, and nothing in it answers.
type unreadableAccounts struct{ failure error }

var errAccountsUnreadable = errors.New("database disk image is malformed")

func (store unreadableAccounts) ReadAccounts(context.Context, func(app.AccountStore, app.SessionStore) error) error {
	return store.failure
}

func (store unreadableAccounts) WriteAccounts(context.Context, func(app.AccountStore, app.SessionStore) error) error {
	return store.failure
}

// brokenAccountsFixture serves doctor from a real runtime while every account
// read fails.
func brokenAccountsFixture(t *testing.T) (http.Handler, string) {
	t.Helper()
	return accountFailureFixture(t, errAccountsUnreadable)
}

func accountFailureFixture(t *testing.T, failure error) (http.Handler, string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "data")
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: root, Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	testAccounts(t, database, runtimeState)
	t.Cleanup(func() { _ = database.Close() })
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
	reporter, err := doctor.New(doctor.Options{DataDir: root, ActiveDatabase: database})
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(unreadableAccounts{failure: failure})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{
		Assets:        testAssets(),
		SessionSigner: manager,
		Accounts:      accounts,
		Doctor:        reporter,
		Readiness:     func(context.Context) error { return nil },
		RequestID:     func() string { return "req_broken" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine, issueTestAPIToken(t, testAccounts(t, database, runtimeState))
}

// A valid credential grants nothing when the account database cannot answer.
func TestBearerRejectsProtectedRoutesWhileTheAccountDatabaseIsUnreadable(t *testing.T) {
	engine, token := brokenAccountsFixture(t)

	for _, path := range []string{"/api/v1/owner/doctor", "/api/v1/account/security", "/api/v1/account/session"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := performRequest(engine, request)
		if response.Code != http.StatusServiceUnavailable {
			t.Fatalf("%s = %d, body = %s", path, response.Code, response.Body.String())
		}
		if code := decodeError(t, response.Body).Error.Code; code != "AUTH_UNAVAILABLE" {
			t.Fatalf("%s = %s, want AUTH_UNAVAILABLE", path, code)
		}
	}

	// Invalid credentials remain an authentication refusal, independently of storage.
	request := httptest.NewRequest(http.MethodGet, "/api/v1/owner/doctor", nil)
	request.Header.Set("Authorization", "Bearer not-the-owner-token")
	if response := performRequest(engine, request); response.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong token = %d, want 401: %s", response.Code, response.Body.String())
	}
}

func TestBearerCannotAuthorizeAMissingAccount(t *testing.T) {
	engine, token := accountFailureFixture(t, domain.NewError(domain.CodeAccountNotFound, "account does not exist", nil))

	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := performRequest(engine, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("account session = %d, want 401: %s", response.Code, response.Body.String())
	}
}
