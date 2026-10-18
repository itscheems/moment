package sqlite

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func activeCredentialAccount(t *testing.T, service *app.AccountService, name string) domain.User {
	t.Helper()
	outcome, err := service.SignIn(testAccountContext(), app.ExternalIdentity{Provider: "github", ProviderUserID: name, DisplayName: name})
	if err != nil {
		t.Fatal(err)
	}
	user, err := service.SetStatus(testAccountContext(), outcome.User.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	return user
}

func TestAccountCredentialsSeparatePasswordsAndTokens(t *testing.T) {
	repository, accounts := newAccountFixture(t)
	ctx := testAccountContext()
	alice := activeCredentialAccount(t, accounts, "alice")
	bob := activeCredentialAccount(t, accounts, "bob")
	for _, account := range []domain.User{alice, bob} {
		if err := accounts.SetAccountPassword(ctx, account.ID, account.Username+"-password"); err != nil {
			t.Fatal(err)
		}
		verified, err := accounts.VerifyPassword(ctx, account.Username, account.Username+"-password")
		if err != nil || verified.ID != account.ID {
			t.Fatalf("password identity = %+v, %v", verified, err)
		}
	}
	if _, err := accounts.VerifyPassword(ctx, bob.Username, alice.Username+"-password"); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("another account's password was accepted: %v", err)
	}
	first, err := accounts.CreateAPIToken(ctx, alice.ID, "desktop")
	if err != nil {
		t.Fatal(err)
	}
	second, err := accounts.CreateAPIToken(ctx, bob.ID, "terminal")
	if err != nil {
		t.Fatal(err)
	}
	if first.Value == second.Value || first.Token.ID == second.Token.ID {
		t.Fatal("independent accounts share a credential")
	}
	for _, issued := range []app.IssuedAPIToken{first, second} {
		resolved, err := accounts.ResolveAPIToken(ctx, issued.Value)
		if err != nil || resolved.User.ID != issued.Token.UserID {
			t.Fatalf("token identity = %+v, %v", resolved.User, err)
		}
	}
	tokens, err := accounts.APITokens(ctx, alice.ID)
	if err != nil || len(tokens) != 1 || tokens[0].ID != first.Token.ID {
		t.Fatalf("account token listing: %+v, %v", tokens, err)
	}
	encoded, err := json.Marshal(tokens)
	if err != nil || bytes.Contains(encoded, []byte(first.Value)) || bytes.Contains(encoded, []byte("hash")) {
		t.Fatal("token listing exposed secret material")
	}
	if err := repository.ReadAccounts(ctx, func(store app.AccountStore, _ app.SessionStore) error {
		credentials := store.(app.AccountCredentialStore)
		hash, found, err := credentials.PasswordHash(ctx, alice.ID)
		if err != nil {
			return err
		}
		if !found || bytes.Equal(hash, []byte(alice.Username+"-password")) || len(tokens[0].Hash) != 32 || bytes.Equal(tokens[0].Hash, []byte(first.Value)) {
			t.Fatal("credentials are not stored as hashes")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	methods, err := accounts.AccountSignInMethods(ctx, alice.ID)
	if err != nil || len(methods) != 2 {
		t.Fatalf("a member's credential methods = %v, %v", methods, err)
	}
}

func TestRevokingOneAccountTokenDoesNotAffectOtherCredentials(t *testing.T) {
	_, accounts := newAccountFixture(t)
	ctx := testAccountContext()
	alice := activeCredentialAccount(t, accounts, "alice")
	bob := activeCredentialAccount(t, accounts, "bob")
	first, err := accounts.CreateAPIToken(ctx, alice.ID, "first")
	if err != nil {
		t.Fatal(err)
	}
	other, err := accounts.CreateAPIToken(ctx, alice.ID, "other device")
	if err != nil {
		t.Fatal(err)
	}
	if err := accounts.RevokeAPIToken(ctx, bob.ID, first.Token.ID); !domain.HasCode(err, domain.CodeAPITokenNotFound) {
		t.Fatalf("another account revoked a token: %v", err)
	}
	if _, err := accounts.ResolveAPIToken(ctx, first.Value); err != nil {
		t.Fatal(err)
	}
	if err := accounts.RevokeAPIToken(ctx, alice.ID, first.Token.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.ResolveAPIToken(ctx, first.Value); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("revoked token still works: %v", err)
	}
	if _, err := accounts.ResolveAPIToken(ctx, other.Value); err != nil {
		t.Fatalf("unrelated token revoked: %v", err)
	}
	if err := accounts.RevokeAPIToken(ctx, alice.ID, first.Token.ID); err != nil {
		t.Fatalf("repeated revocation: %v", err)
	}
}

func TestPasswordReplacementRevokesOnlyThatAccountsSessions(t *testing.T) {
	_, accounts := newAccountFixture(t)
	ctx := testAccountContext()
	alice := activeCredentialAccount(t, accounts, "alice")
	bob := activeCredentialAccount(t, accounts, "bob")
	aliceSession := "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	bobSession := "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
	for _, item := range []struct{ user, session string }{{alice.ID, aliceSession}, {bob.ID, bobSession}} {
		if _, err := accounts.StartSession(ctx, item.user, item.session, "web", time.Now().Add(time.Hour).UnixMilli()); err != nil {
			t.Fatal(err)
		}
	}
	if err := accounts.SetAccountPassword(ctx, alice.ID, "first-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.ResolveSession(ctx, aliceSession); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("password did not revoke the account session: %v", err)
	}
	if _, err := accounts.ResolveSession(ctx, bobSession); err != nil {
		t.Fatalf("another account's session revoked: %v", err)
	}
	if err := accounts.SetAccountPassword(ctx, alice.ID, "replacement-password"); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.VerifyPassword(ctx, alice.Username, "first-password"); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("old password still works: %v", err)
	}
	if _, err := accounts.VerifyPassword(ctx, alice.Username, "replacement-password"); err != nil {
		t.Fatal(err)
	}
}

func TestSuspensionPermanentlyRevokesAccountTokens(t *testing.T) {
	_, accounts := newAccountFixture(t)
	ctx := testAccountContext()
	alice := activeCredentialAccount(t, accounts, "alice")
	token, err := accounts.CreateAPIToken(ctx, alice.ID, "device")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SetStatus(ctx, alice.ID, domain.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.ResolveAPIToken(ctx, token.Value); err == nil {
		t.Fatal("suspended account token accepted")
	}
	if _, err := accounts.SetStatus(ctx, alice.ID, domain.AccountActive); err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.ResolveAPIToken(ctx, token.Value); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("reactivation revived a revoked token: %v", err)
	}
	if _, err := accounts.CreateAPIToken(ctx, alice.ID, "new device"); err != nil {
		t.Fatal(err)
	}
}

type failedCredentialTransactions struct{ failure error }

func (tx failedCredentialTransactions) ReadAccounts(context.Context, func(app.AccountStore, app.SessionStore) error) error {
	return tx.failure
}
func (tx failedCredentialTransactions) WriteAccounts(context.Context, func(app.AccountStore, app.SessionStore) error) error {
	return tx.failure
}

func TestCredentialVerificationRefusesUnreadableDatabase(t *testing.T) {
	failure := errors.New("unreadable account database")
	accounts, err := app.NewAccountService(failedCredentialTransactions{failure: failure})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.VerifyPassword(testAccountContext(), "alice", "a-password"); !errors.Is(err, failure) {
		t.Fatalf("password swallowed storage failure: %v", err)
	}
	value := base64.RawURLEncoding.EncodeToString(make([]byte, 32))
	if _, err := accounts.ResolveAPIToken(testAccountContext(), value); !errors.Is(err, failure) {
		t.Fatalf("token swallowed storage failure: %v", err)
	}
}

func TestPendingAccountsCannotMintTokensAndInvalidPasswordsDoNotReplaceCredentials(t *testing.T) {
	_, accounts := newAccountFixture(t)
	ctx := testAccountContext()
	pending, err := accounts.SignIn(ctx, app.ExternalIdentity{Provider: "github", ProviderUserID: "pending", DisplayName: "pending"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.CreateAPIToken(ctx, pending.User.ID, "device"); !domain.HasCode(err, domain.CodeAccountNotApproved) {
		t.Fatalf("pending token issuance: %v", err)
	}
	if err := accounts.SetAccountPassword(ctx, pending.User.ID, "valid-password"); err != nil {
		t.Fatal(err)
	}
	for _, password := range []string{"short", strings.Repeat("x", 73), " trailing-password "} {
		if err := accounts.SetAccountPassword(ctx, pending.User.ID, password); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("invalid password replaced credential: %v", err)
		}
	}
	if _, err := accounts.VerifyPassword(ctx, pending.User.Username, "valid-password"); err != nil {
		t.Fatal(err)
	}
}
