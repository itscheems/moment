package sqlite

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

type failedBrowserRotation struct {
	app.AccountStore
	app.BrowserAccountStore
	failure error
}

func (store failedBrowserRotation) RotateBrowserAccountSet(context.Context, app.BrowserAccountSet) error {
	return store.failure
}

type failedBrowserTransactions struct {
	app.AccountTransactions
	failure error
}

func (tx failedBrowserTransactions) WriteAccounts(ctx context.Context, operation func(app.AccountStore, app.SessionStore) error) error {
	return tx.AccountTransactions.WriteAccounts(ctx, func(accounts app.AccountStore, sessions app.SessionStore) error {
		return operation(failedBrowserRotation{accounts, accounts.(app.BrowserAccountStore), tx.failure}, sessions)
	})
}

func TestBrowserAccountRotationRollsBackMembershipAndRevocation(t *testing.T) {
	repo, accounts := newAccountFixture(t)
	ctx := testAccountContext()
	user := activeCredentialAccount(t, accounts, "rotation")
	start := func(id string) domain.SessionRecord {
		s, err := accounts.StartSession(ctx, user.ID, strings.Repeat(id, 32), "web", time.Now().Add(time.Hour).UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	old, fresh := start("a"), start("b")
	secret, csrf := strings.Repeat("s", 43), strings.Repeat("c", 43)
	if _, err := accounts.BootstrapBrowserAccounts(ctx, secret, csrf, 1, old.ID); err != nil {
		t.Fatal(err)
	}
	failure := errors.New("rotation write failed")
	failing, err := app.NewAccountService(failedBrowserTransactions{repo, failure})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := failing.AddBrowserAccount(ctx, secret, strings.Repeat("n", 43), strings.Repeat("d", 43), 1, fresh.ID); !errors.Is(err, failure) {
		t.Fatalf("failure lost: %v", err)
	}
	set, entries, err := accounts.BrowserAccounts(ctx, secret, 1)
	if err != nil || set.CSRFToken != csrf || len(entries) != 1 || entries[0].Member.SessionID != old.ID {
		t.Fatalf("failed rotation changed membership: %+v %v", entries, err)
	}
	if _, err := accounts.ResolveSession(ctx, old.ID); err != nil {
		t.Fatal("failed rotation revoked original session", err)
	}
	if _, _, err := accounts.BrowserAccounts(ctx, strings.Repeat("n", 43), 1); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatal("failed rotation created credential", err)
	}
}

func TestBrowserAccountConcurrentRotationRejectsOldCredential(t *testing.T) {
	_, accounts := newAccountFixture(t)
	ctx := testAccountContext()
	user := activeCredentialAccount(t, accounts, "concurrent")
	ids := []string{strings.Repeat("a", 32), strings.Repeat("b", 32), strings.Repeat("c", 32)}
	for _, id := range ids {
		if _, err := accounts.StartSession(ctx, user.ID, id, "web", time.Now().Add(time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	secret := strings.Repeat("s", 43)
	if _, err := accounts.BootstrapBrowserAccounts(ctx, secret, strings.Repeat("c", 43), 1, ids[0]); err != nil {
		t.Fatal(err)
	}
	errorsCh := make(chan error, 2)
	var wg sync.WaitGroup
	for i := 1; i <= 2; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := accounts.AddBrowserAccount(ctx, secret, strings.Repeat(ids[i][:1], 43), strings.Repeat(ids[i][:1], 42)+"x", 1, ids[i])
			errorsCh <- err
		}(i)
	}
	wg.Wait()
	close(errorsCh)
	successes := 0
	for err := range errorsCh {
		if err == nil {
			successes++
		} else if !domain.HasCode(err, domain.CodeUnauthorized) {
			t.Fatal(err)
		}
	}
	if successes != 1 {
		t.Fatalf("old credential accepted %d concurrent joins", successes)
	}
}

func TestBrowserAccountPasswordReplacementDoesNotRestoreOldCredential(t *testing.T) {
	_, accounts := newAccountFixture(t)
	ctx := testAccountContext()
	user := activeCredentialAccount(t, accounts, "password_rotation")
	if err := accounts.SetAccountPassword(ctx, user.ID, "original-password"); err != nil {
		t.Fatal(err)
	}
	old, err := accounts.StartSession(ctx, user.ID, strings.Repeat("a", 32), "web", time.Now().Add(time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("s", 43)
	if _, err := accounts.BootstrapBrowserAccounts(ctx, secret, strings.Repeat("c", 43), 1, old.ID); err != nil {
		t.Fatal(err)
	}
	if err := accounts.SetAccountPassword(ctx, user.ID, "replacement-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.ActivateBrowserAccount(ctx, secret, 1, user.ID); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatal("revoked session accepted", err)
	}
	verified, err := accounts.VerifyPassword(ctx, user.Username, "replacement-password")
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := accounts.StartSession(ctx, verified.ID, strings.Repeat("b", 32), "web", time.Now().Add(time.Hour).UnixMilli())
	if err != nil {
		t.Fatal(err)
	}
	next := strings.Repeat("n", 43)
	if _, err := accounts.AddBrowserAccount(ctx, secret, next, strings.Repeat("d", 43), 1, fresh.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.ActivateBrowserAccount(ctx, secret, 1, user.ID); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatal("old credential regained account after password login", err)
	}
	if resolved, err := accounts.ActivateBrowserAccount(ctx, next, 1, user.ID); err != nil || resolved.Session.ID != fresh.ID {
		t.Fatal("new credential failed", err)
	}
}
