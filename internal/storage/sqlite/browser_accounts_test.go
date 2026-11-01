package sqlite

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestBrowserAccountsPersistIsolationAndRevocation(t *testing.T) {
	repo, service := newAccountFixture(t)
	ctx := context.Background()
	secret := strings.Repeat("a", 43)
	users := []domain.User{}
	sessions := []domain.SessionRecord{}
	for i := 0; i < 7; i++ {
		user := activeCredentialAccount(t, service, fmt.Sprintf("member%d", i))
		users = append(users, user)
		session, err := service.StartSession(ctx, user.ID, fmt.Sprintf("%032x", i+100), "web", time.Now().Add(time.Hour).UnixMilli())
		if err != nil {
			t.Fatal(err)
		}
		sessions = append(sessions, session)
	}
	set, err := service.BootstrapBrowserAccounts(ctx, secret, "csrf", 1, sessions[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	for i, session := range sessions[1:] {
		next := fmt.Sprintf("%043d", i+1)
		set, err = service.AddBrowserAccount(ctx, secret, next, fmt.Sprintf("csrf%039d", i), 1, session.ID)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := service.BrowserAccounts(ctx, secret, 1); !domain.HasCode(err, domain.CodeUnauthorized) {
			t.Fatal("old credential remained valid", err)
		}
		secret = next
	}
	reopened, err := app.NewAccountService(repo)
	if err != nil {
		t.Fatal(err)
	}
	_, entries, err := reopened.BrowserAccounts(ctx, secret, 1)
	if err != nil || len(entries) != 7 {
		t.Fatalf("persistent list %d %v", len(entries), err)
	}
	if _, _, err := service.BrowserAccounts(ctx, strings.Repeat("b", 43), 1); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatal("other browser read membership", err)
	}
	if _, _, err := service.BrowserAccounts(ctx, secret, 2); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatal("old epoch revived collection", err)
	}
	selected, err := service.ActivateBrowserAccount(ctx, secret, 1, users[1].ID)
	if err != nil || selected.Session.ExpiresAtMS != sessions[1].ExpiresAtMS {
		t.Fatal("switch changed expiration", err)
	}
	if err := service.EndSession(ctx, sessions[2].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.ActivateBrowserAccount(ctx, secret, 1, users[2].ID); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatal("revoked session revived", err)
	}
	_, entries, _ = service.BrowserAccounts(ctx, secret, 1)
	for _, entry := range entries {
		if entry.User.ID == users[2].ID && entry.Available {
			t.Fatal("revoked member marked available")
		}
	}
	next, empty, err := service.RemoveBrowserAccounts(ctx, secret, 1, users[0].ID, users[0].ID)
	if err != nil || empty || next == nil || next.User.ID != users[1].ID {
		t.Fatalf("replacement %v %v %v", next, empty, err)
	}
	if _, _, err := service.RemoveBrowserAccounts(ctx, secret, 1, "", users[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := service.BrowserAccounts(ctx, secret, 1); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatal("collection survived logout all")
	}
	if err := repo.ReadAccounts(ctx, func(a app.AccountStore, _ app.SessionStore) error {
		s, ok, err := a.(app.BrowserAccountStore).BrowserAccountSetByHash(ctx, set.CredentialHash)
		if err != nil || !ok || s.RevokedAtMS == nil {
			t.Fatal("revocation was not durable", err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
