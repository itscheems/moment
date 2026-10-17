package sqlite

import (
	"context"
	"sync"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

func TestNewSchemaRequiresExplicitAccountInitialization(t *testing.T) {
	ctx := context.Background()
	state, err := runtimefs.Initialize(ctx, runtimefs.Config{DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	db, err := Open(ctx, state, Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	for _, table := range []string{"users", "site_settings"} {
		assertCount(t, db.read, "SELECT count(*) FROM "+table, 0)
	}
	repository, err := NewAccountRepository(db, state.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(repository)
	if err != nil {
		t.Fatal(err)
	}
	first, err := accounts.InitializeSiteAccount(ctx, "alice")
	if err != nil || !first.IsAdmin() {
		t.Fatalf("explicit bootstrap=%+v,%v", first, err)
	}
	if _, err := accounts.InitializeSiteAccount(ctx, "another"); !domain.HasCode(err, domain.CodeInstanceAlreadyInitialized) {
		t.Fatalf("second bootstrap=%v", err)
	}
	profile, err := accounts.ProfileAccount(ctx)
	if err != nil || profile.ID != first.ID {
		t.Fatal("site profile does not reference the explicitly initialized account")
	}
	var kinds int
	if err := db.read.QueryRowContext(ctx, `SELECT count(*) FROM pragma_table_info('comments') WHERE name='author_kind'`).Scan(&kinds); err != nil || kinds != 0 {
		t.Fatal("retired comment category remains in schema")
	}
}

func TestAdministratorRoleChangesDoNotTransferProfileOrCredentials(t *testing.T) {
	_, accounts := newAccountFixture(t)
	ctx := context.Background()
	initial, err := accounts.ProfileAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	bob := activeCredentialAccount(t, accounts, "bob")
	token, err := accounts.CreateAPIToken(ctx, initial.ID, "alice device")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SetRole(ctx, bob.ID, domain.UserRoleAdmin); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SetRole(ctx, initial.ID, domain.UserRoleMember); err != nil {
		t.Fatal(err)
	}
	profile, err := accounts.ProfileAccount(ctx)
	if err != nil || profile.ID != initial.ID || profile.IsAdmin() {
		t.Fatalf("profile followed administrator=%+v,%v", profile, err)
	}
	resolved, err := accounts.ResolveAPIToken(ctx, token.Value)
	if err != nil || resolved.User.ID != initial.ID || resolved.User.IsAdmin() {
		t.Fatal("credential followed administrator instead of its account")
	}
	for _, operation := range []func() error{
		func() error { _, err := accounts.SetRole(ctx, bob.ID, domain.UserRoleMember); return err },
		func() error { _, err := accounts.SetStatus(ctx, bob.ID, domain.AccountSuspended); return err },
		func() error { _, err := accounts.Delete(ctx, bob.ID); return err },
	} {
		if err := operation(); !domain.HasCode(err, domain.CodeLastAdministrator) {
			t.Fatalf("last administrator protection=%v", err)
		}
	}
}

func TestConcurrentRoleChangesRetainAnActiveAdministrator(t *testing.T) {
	_, accounts := newAccountFixture(t)
	ctx := context.Background()
	first, err := accounts.ProfileAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	second := activeCredentialAccount(t, accounts, "second")
	if _, err := accounts.SetRole(ctx, second.ID, domain.UserRoleAdmin); err != nil {
		t.Fatal(err)
	}
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, id := range []string{first.ID, second.ID} {
		wait.Add(1)
		go func(id string) {
			defer wait.Done()
			_, err := accounts.SetRole(ctx, id, domain.UserRoleMember)
			results <- err
		}(id)
	}
	wait.Wait()
	close(results)
	succeeded, refused := 0, 0
	for err := range results {
		if err == nil {
			succeeded++
		} else if domain.HasCode(err, domain.CodeLastAdministrator) {
			refused++
		} else {
			t.Fatal(err)
		}
	}
	if succeeded != 1 || refused != 1 {
		t.Fatalf("concurrent demotions: success=%d refused=%d", succeeded, refused)
	}
	users, err := accounts.List(ctx)
	if err != nil {
		t.Fatal(err)
	}
	active := 0
	for _, user := range users {
		if user.IsAdmin() && user.Status == domain.AccountActive {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("active administrators=%d", active)
	}
}

func TestDisplayAccountSelectionDoesNotTransferAdministration(t *testing.T) {
	_, accounts := newAccountFixture(t)
	ctx := context.Background()
	administrator, err := accounts.ProfileAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	member := activeCredentialAccount(t, accounts, "profilemember")
	selected, err := accounts.SelectSiteProfile(ctx, member.ID)
	if err != nil || selected.ID != member.ID || selected.IsAdmin() {
		t.Fatalf("display selection=%+v,%v", selected, err)
	}
	initial, err := accounts.Account(ctx, administrator.ID)
	if err != nil || !initial.IsAdmin() {
		t.Fatal("selecting a display account changed administration")
	}
	if _, err := accounts.Delete(ctx, member.ID); !domain.HasCode(err, domain.CodeAccountInUse) {
		t.Fatalf("selected account deletion=%v", err)
	}
	if _, err := accounts.SetStatus(ctx, member.ID, domain.AccountSuspended); !domain.HasCode(err, domain.CodeAccountInUse) {
		t.Fatalf("selected account suspension=%v", err)
	}
	if _, err := accounts.SelectSiteProfile(ctx, administrator.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SetStatus(ctx, member.ID, domain.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SelectSiteProfile(ctx, member.ID); !domain.HasCode(err, domain.CodeAccountNotApproved) {
		t.Fatalf("inactive display account=%v", err)
	}
}
