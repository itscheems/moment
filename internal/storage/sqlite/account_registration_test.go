package sqlite

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func registrationFixture(t *testing.T) (*Database, *app.AccountService) {
	t.Helper()
	db, runtime := openTestDatabase(t, Options{})
	repository, err := NewAccountRepository(db, runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(repository)
	if err != nil {
		t.Fatal(err)
	}
	return db, accounts
}

func setRegistrationMode(t *testing.T, db *Database, mode domain.RegistrationMode) {
	t.Helper()
	mustWrite(t, db, `UPDATE site_settings SET registration_mode=? WHERE id=1`, string(mode))
}

func TestPasswordRegistrationFollowsPolicyAndCreatesOwnCredential(t *testing.T) {
	db, accounts := registrationFixture(t)
	ctx := context.Background()
	for _, item := range []struct {
		mode   domain.RegistrationMode
		name   string
		status domain.AccountStatus
	}{{domain.RegistrationApproval, "Alice", domain.AccountPending}, {domain.RegistrationOpen, "Bob", domain.AccountActive}} {
		setRegistrationMode(t, db, item.mode)
		created, err := accounts.RegisterPasswordAccount(ctx, item.name, "password123")
		if err != nil {
			t.Fatal(err)
		}
		if created.Username != strings.ToLower(item.name) || created.Status != item.status || created.Role != domain.UserRoleMember || created.UsernameSetAtMS == nil {
			t.Fatalf("registered account = %+v", created)
		}
		if created.CanWritePublicContent() != (item.status == domain.AccountActive) {
			t.Fatalf("wrong publication permission: %+v", created)
		}
		verified, err := accounts.VerifyPassword(ctx, created.Username, "password123")
		if err != nil || verified.ID != created.ID {
			t.Fatalf("password sign-in = %+v, %v", verified, err)
		}
		identities, err := accounts.Identities(ctx, created.ID)
		if err != nil || len(identities) != 0 {
			t.Fatalf("password account has OAuth identities: %+v, %v", identities, err)
		}
	}
	setRegistrationMode(t, db, domain.RegistrationClosed)
	if _, err := accounts.RegisterPasswordAccount(ctx, "charlie", "password123"); !domain.HasCode(err, domain.CodeRegistrationClosed) {
		t.Fatalf("closed registration: %v", err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM users WHERE username='charlie'`, 0)
}

func TestPasswordRegistrationRejectsInvalidInputsAndConcurrentDuplicates(t *testing.T) {
	db, accounts := registrationFixture(t)
	ctx := context.Background()
	for _, item := range []struct {
		username, password string
		code               domain.Code
	}{
		{"register", "password123", domain.CodeUsernameReserved},
		{"1invalid", "password123", domain.CodeInvalidArgument},
		{"shortpw", "short", domain.CodeInvalidArgument},
		{"longpw", string(make([]byte, 73)), domain.CodeInvalidArgument},
		{"spacepw", " password123", domain.CodeInvalidArgument},
		{"badutf", string([]byte{0xff, 'a', 'a', 'a', 'a', 'a', 'a', 'a'}), domain.CodeInvalidArgument},
	} {
		if _, err := accounts.RegisterPasswordAccount(ctx, item.username, item.password); !domain.HasCode(err, item.code) {
			t.Fatalf("%s: %v, want %s", item.username, err, item.code)
		}
	}
	assertCount(t, db.read, `SELECT count(*) FROM users`, 1)
	var wait sync.WaitGroup
	results := make(chan error, 2)
	for _, name := range []string{"SameName", "samename"} {
		wait.Add(1)
		go func(name string) {
			defer wait.Done()
			_, err := accounts.RegisterPasswordAccount(ctx, name, "password123")
			results <- err
		}(name)
	}
	wait.Wait()
	close(results)
	success, conflict := 0, 0
	for err := range results {
		switch {
		case err == nil:
			success++
		case domain.HasCode(err, domain.CodeUsernameTaken):
			conflict++
		default:
			t.Fatalf("unexpected concurrent result: %v", err)
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent duplicate: success=%d conflict=%d", success, conflict)
	}
	assertCount(t, db.read, `SELECT count(*) FROM users WHERE username='samename'`, 1)
	assertCount(t, db.read, `SELECT count(*) FROM account_passwords JOIN users ON users.id=account_passwords.user_id WHERE users.username='samename'`, 1)
}

func TestPasswordRegistrationCountsUTF8BytesAtBothBounds(t *testing.T) {
	_, accounts := registrationFixture(t)
	ctx := context.Background()
	for _, item := range []struct{ name, password string }{
		{"eightascii", "abcdefgh"},
		{"eightutf", strings.Repeat("é", 4)},
		{"seventytwoascii", strings.Repeat("a", 72)},
		{"seventytwoutf", strings.Repeat("é", 36)},
	} {
		created, err := accounts.RegisterPasswordAccount(ctx, item.name, item.password)
		if err != nil {
			t.Fatalf("%s (%d bytes): %v", item.name, len(item.password), err)
		}
		if verified, err := accounts.VerifyPassword(ctx, created.Username, item.password); err != nil || verified.ID != created.ID {
			t.Fatalf("%s password could not sign in: %v", item.name, err)
		}
	}
	for _, password := range []string{"1234567", strings.Repeat("a", 73), strings.Repeat("é", 37), " password123", "password123\t"} {
		if _, err := accounts.RegisterPasswordAccount(ctx, "invalidbound", password); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("accepted %d-byte password: %v", len(password), err)
		}
	}
}

func TestPasswordRegistrationRollsBackWhenCredentialWriteFails(t *testing.T) {
	db, accounts := registrationFixture(t)
	ctx := context.Background()
	mustWrite(t, db, `CREATE TRIGGER fail_registered_password BEFORE INSERT ON account_passwords BEGIN SELECT RAISE(ABORT, 'test password failure'); END`)
	if _, err := accounts.RegisterPasswordAccount(ctx, "failedpw", "password123"); err == nil {
		t.Fatal("password write failure was swallowed")
	}
	assertCount(t, db.read, `SELECT count(*) FROM users WHERE username='failedpw'`, 0)
	assertCount(t, db.read, `SELECT count(*) FROM account_passwords`, 0)
}
