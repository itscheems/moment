package archive

import (
	"context"
	"fmt"
	"math"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const fixtureAccountID = "0123456789abcdef0123456789abcdef"

func testAuthorization(n int) Authorization {
	if n == 0 {
		return Authorization{}
	}
	return Authorization{AccountID: fixtureAccountID, CredentialID: fmt.Sprintf("session:%032x", n)}
}
func initializeFixtureSessions(t *testing.T, accounts *app.AccountService, userID string) {
	t.Helper()
	for n := 1; n <= 16; n++ {
		if _, err := accounts.StartSession(context.Background(), userID, fmt.Sprintf("%032x", n), "cli", time.Date(2100, 1, 1, 0, 0, 0, 0, time.UTC).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
}

func revokeFixtureSession(db *sqlitestore.Database, n int, at time.Time) error {
	return db.WriteTx(context.Background(), func(q sqlitestore.Queries) error {
		_, err := q.ExecContext(context.Background(), `UPDATE sessions SET revoked_at_ms=? WHERE id=?`, at.UnixMilli(), fmt.Sprintf("%032x", n))
		return err
	})
}

func TestStagingIsBoundToAnAccountAndLiveCredential(t *testing.T) {
	fixture := newExportFixture(t, false)
	ctx := context.Background()
	now := exportFixtureClock.Add(time.Hour)
	repo, err := sqlitestore.NewAccountRepository(fixture.database, fixture.runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(repo, app.WithAccountClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	other, err := accounts.SignIn(ctx, app.ExternalIdentity{Provider: "google", ProviderUserID: "second-administrator", DisplayName: "Second Admin"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SetStatus(ctx, other.User.ID, domain.AccountActive); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SetRole(ctx, other.User.ID, domain.UserRoleAdmin); err != nil {
		t.Fatal(err)
	}
	firstToken, err := accounts.CreateAPIToken(ctx, fixtureAccountID, "import initiator")
	if err != nil {
		t.Fatal(err)
	}
	otherToken, err := accounts.CreateAPIToken(ctx, other.User.ID, "other administrator")
	if err != nil {
		t.Fatal(err)
	}
	first := Authorization{AccountID: fixtureAccountID, CredentialID: "token:" + firstToken.Token.ID}
	second := Authorization{AccountID: other.User.ID, CredentialID: "token:" + otherToken.Token.ID}
	inspector := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64)
	inspection, err := inspector.InspectFile(ctx, exportFixtureFile(t, fixture), first)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := inspector.Resume(ctx, inspection.StagingToken, second); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
		t.Fatalf("another administrator reused staging: %v", err)
	}
	if _, err := inspector.InspectFile(ctx, exportFixtureFile(t, fixture), second); !domain.HasCode(err, domain.CodeImportInProgress) {
		t.Fatalf("another administrator replaced pending staging: %v", err)
	}
	if _, err := inspector.Resume(ctx, inspection.StagingToken, testAuthorization(1)); !domain.HasCode(err, domain.CodeImportStagingInvalid) {
		t.Fatalf("another credential reused staging: %v", err)
	}
	if err := accounts.RevokeAPIToken(ctx, fixtureAccountID, firstToken.Token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := testCommitter(t, fixture.runtime.Root, &now, math.MaxUint64).Commit(ctx, inspection.StagingToken, first, nil); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("revoked credential committed staging: %v", err)
	}
	current, err := runtimefs.Open(fixture.runtime.Root)
	if err != nil || current.Current.ID != fixture.runtime.Current.ID {
		t.Fatal("rejected credential changed CURRENT")
	}
}

func TestArchiveRestoresDisplayAccountWithoutGrantingAdministration(t *testing.T) {
	fixture := newExportFixture(t, false)
	ctx := context.Background()
	repo, _ := sqlitestore.NewAccountRepository(fixture.database, fixture.runtime.Locks)
	accounts, _ := app.NewAccountService(repo, app.WithAccountClock(func() time.Time { return exportFixtureClock }))
	other, err := accounts.SignIn(ctx, app.ExternalIdentity{Provider: "google", ProviderUserID: "admin", DisplayName: "Another Administrator"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SetStatus(ctx, other.User.ID, domain.AccountActive); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SetRole(ctx, other.User.ID, domain.UserRoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SetRole(ctx, fixtureAccountID, domain.UserRoleMember); err != nil {
		t.Fatal(err)
	}
	now := exportFixtureClock.Add(time.Hour)
	inspection, err := testInspector(t, fixture.runtime.Root, fixture.database, &now, math.MaxUint64).InspectFile(ctx, exportFixtureFile(t, fixture), Authorization{AccountID: other.User.ID, CredentialID: "local"})
	if err != nil {
		t.Fatal(err)
	}
	users, _, site := stagedAccounts(t, fixture, inspection)
	if site.ProfileUserID != fixtureAccountID {
		t.Fatal("restore transferred display account")
	}
	for _, user := range users {
		if user.ID == fixtureAccountID && user.Role != "member" {
			t.Fatal("display account was promoted by restore")
		}
		if user.ID == other.User.ID && user.Role != "admin" {
			t.Fatal("administrator role was lost on restore")
		}
	}
}
