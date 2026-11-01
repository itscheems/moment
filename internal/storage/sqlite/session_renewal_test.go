package sqlite

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestSessionRenewalSlidingAndAbsoluteDeadlines(t *testing.T) {
	repo, initial := newAccountFixture(t)
	user := activeCredentialAccount(t, initial, "renewal")
	now := time.Now().UTC().Truncate(time.Millisecond)
	start := now
	service, err := app.NewAccountService(repo, app.WithAccountClock(func() time.Time { return now }))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	id, other := strings.Repeat("a", 32), strings.Repeat("b", 32)
	ceiling := start.Add(domain.SessionAbsoluteTTL).UnixMilli()
	record, err := service.StartSession(ctx, user.ID, id, "web", ceiling)
	if err != nil || record.ExpiresAtMS != start.Add(domain.SessionIdleTTL).UnixMilli() {
		t.Fatalf("initial deadline: %+v %v", record, err)
	}
	if _, err := service.StartSession(ctx, user.ID, other, "web", ceiling); err != nil {
		t.Fatal(err)
	}
	secret := strings.Repeat("s", 43)
	set, err := service.BootstrapBrowserAccounts(ctx, secret, strings.Repeat("c", 43), 1, id)
	if err != nil || set.ExpiresAtMS != ceiling {
		t.Fatalf("collection deadline: %+v %v", set, err)
	}
	for day := 29; day <= 174; day += 29 {
		now = start.Add(time.Duration(day) * 24 * time.Hour)
		record, err = service.RenewSession(ctx, id, ceiling)
		want := min(now.Add(domain.SessionIdleTTL).UnixMilli(), ceiling)
		if err != nil || record.ExpiresAtMS != want || record.IssuedAtMS != start.UnixMilli() {
			t.Fatalf("day %d: %+v %v", day, record, err)
		}
	}
	// The retained collection survives while its unused member does not.
	_, entries, err := service.BrowserAccounts(ctx, secret, 1)
	if err != nil || len(entries) != 1 || !entries[0].Available {
		t.Fatalf("collection expired with active sign-in: %+v %v", entries, err)
	}
	if _, err := service.ResolveSession(ctx, other); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("unused session kept alive: %v", err)
	}
	now = start.Add(domain.SessionAbsoluteTTL)
	if _, err := service.RenewSession(ctx, id, ceiling); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("absolute boundary renewed: %v", err)
	}
}

func TestSessionRenewalNeverRevivesUnavailableCredentials(t *testing.T) {
	for _, reason := range []string{"idle", "logout", "all-sessions", "suspended", "source-token", "password"} {
		t.Run(reason, func(t *testing.T) {
			repo, initial := newAccountFixture(t)
			user := activeCredentialAccount(t, initial, "renewblocked")
			now := time.Now().UTC().Truncate(time.Millisecond)
			service, err := app.NewAccountService(repo, app.WithAccountClock(func() time.Time { return now }))
			if err != nil {
				t.Fatal(err)
			}
			ctx := context.Background()
			token, err := service.CreateAPIToken(ctx, user.ID, "source")
			if err != nil {
				t.Fatal(err)
			}
			id := strings.Repeat("a", 32)
			ceiling := now.Add(domain.SessionAbsoluteTTL).UnixMilli()
			if _, err := service.StartTokenSession(ctx, user.ID, id, "web", ceiling, token.Token.ID); err != nil {
				t.Fatal(err)
			}
			switch reason {
			case "idle":
				now = now.Add(domain.SessionIdleTTL)
			case "logout":
				err = service.EndSession(ctx, id)
			case "all-sessions":
				_, err = service.EndAllSessions(ctx, user.ID)
			case "suspended":
				_, err = service.SetStatus(ctx, user.ID, domain.AccountSuspended)
			case "source-token":
				err = service.RevokeAPIToken(ctx, user.ID, token.Token.ID)
			case "password":
				err = service.SetAccountPassword(ctx, user.ID, "changed-password")
			}
			if err != nil {
				t.Fatal(err)
			}
			if _, err := service.RenewSession(ctx, id, ceiling); err == nil {
				t.Fatal("unavailable sign-in renewed")
			}
			if _, err := service.ResolveSession(ctx, id); err == nil {
				t.Fatal("unavailable sign-in restored")
			}
		})
	}
}

func TestConcurrentSessionRenewalCannotUndoLogout(t *testing.T) {
	_, service := newAccountFixture(t)
	user := activeCredentialAccount(t, service, "renewrace")
	ctx := context.Background()
	id := strings.Repeat("a", 32)
	ceiling := time.Now().Add(domain.SessionAbsoluteTTL).UnixMilli()
	if _, err := service.StartSession(ctx, user.ID, id, "web", ceiling); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() { defer wg.Done(); _, _ = service.RenewSession(ctx, id, ceiling) }()
	go func() {
		defer wg.Done()
		if err := service.EndSession(ctx, id); err != nil {
			t.Error(err)
		}
	}()
	wg.Wait()
	if _, err := service.RenewSession(ctx, id, ceiling); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("revoked session restored: %v", err)
	}
}
