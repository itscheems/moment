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
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// unreadableAccounts is what a half-applied migration or a corrupted page looks
// like from above the storage layer: the file opens, and nothing in it answers.
type unreadableAccounts struct{}

var errAccountsUnreadable = errors.New("database disk image is malformed")

func (unreadableAccounts) ReadAccounts(context.Context, func(app.AccountStore, app.SessionStore) error) error {
	return errAccountsUnreadable
}

func (unreadableAccounts) WriteAccounts(context.Context, func(app.AccountStore, app.SessionStore) error) error {
	return errAccountsUnreadable
}

// brokenAccountsFixture serves doctor from a real runtime while every account
// read fails.
func brokenAccountsFixture(t *testing.T) (http.Handler, string) {
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
	t.Cleanup(func() { _ = database.Close() })
	credential, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{})
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
	accounts, err := app.NewAccountService(unreadableAccounts{})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{
		Assets:    testAssets(),
		OwnerAuth: manager,
		Accounts:  accounts,
		Doctor:    reporter,
		Readiness: func(context.Context) error { return nil },
		RequestID: func() string { return "req_broken" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return engine, credential.Token
}

// The owner token has to keep working when the database is what is broken.
//
// That is the reason it lives in secrets.json rather than in a row, and doctor
// and credential repair are the two things it exists to reach. Looking the
// owner account up before honouring the token made both unreachable in exactly
// the state they were built for, and reported the outage as a 500 from a route
// whose whole job is explaining outages.
func TestBearerReachesDoctorWhileTheAccountsTableIsUnreadable(t *testing.T) {
	engine, token := brokenAccountsFixture(t)

	for _, path := range []string{"/api/v1/owner/doctor", "/api/v1/owner/status"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.Header.Set("Authorization", "Bearer "+token)
		response := performRequest(engine, request)
		if response.Code != http.StatusOK {
			t.Fatalf("%s = %d, body = %s", path, response.Code, response.Body.String())
		}
	}

	// A token that is not the owner's is still refused, so the degraded path
	// admits nobody the working one would not.
	request := httptest.NewRequest(http.MethodGet, "/api/v1/owner/doctor", nil)
	request.Header.Set("Authorization", "Bearer not-the-owner-token")
	if response := performRequest(engine, request); response.Code != http.StatusUnauthorized {
		t.Fatalf("a wrong token = %d, want 401: %s", response.Code, response.Body.String())
	}
}

// What the degraded grant does not carry is a name.
//
// Every route in the account tiers either edits one account or signs something
// with it, and neither is expressible without knowing which. The refusal is the
// point: an admitted caller with no id would otherwise author content signed by
// nobody.
func TestBearerWithNoReadableOwnerRowCannotActAsAnAccount(t *testing.T) {
	engine, token := brokenAccountsFixture(t)

	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	response := performRequest(engine, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("account session = %d, want 401: %s", response.Code, response.Body.String())
	}
}
