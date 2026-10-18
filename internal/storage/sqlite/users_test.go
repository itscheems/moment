package sqlite

import (
	"errors"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func newAccountFixture(t *testing.T) (*AccountRepository, *app.AccountService) {
	t.Helper()
	database, runtimeState := openTestDatabase(t, Options{})
	repository, err := NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(repository)
	if err != nil {
		t.Fatal(err)
	}
	return repository, accounts
}

// attach writes an identity row straight through the store.
//
// The service refuses a second identity from a provider an account already
// signs in with, so a row that predates that rule can only be produced from
// below it -- which is exactly the state this asserts is still removable.
func attach(t *testing.T, repository *AccountRepository, identity domain.Identity) {
	t.Helper()
	ctx := testAccountContext()
	if err := repository.WriteAccounts(ctx, func(accounts app.AccountStore, _ app.SessionStore) error {
		return accounts.LinkIdentity(ctx, identity)
	}); err != nil {
		t.Fatal(err)
	}
}

func githubIdentity(userID, providerUserID string) domain.Identity {
	return domain.Identity{
		Provider:       "github",
		ProviderUserID: providerUserID,
		UserID:         userID,
		CreatedAtMS:    1,
		LastLoginAtMS:  1,
	}
}

func providerNames(t *testing.T, accounts *app.AccountService, userID string) []string {
	t.Helper()
	identities, err := accounts.Identities(testAccountContext(), userID)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(identities))
	for _, identity := range identities {
		names = append(names, identity.Provider+":"+identity.ProviderUserID)
	}
	return names
}

// Removing one credential removes one credential.
//
// The primary key is (provider, provider_user_id) and nothing makes
// (user_id, provider) unique, so an account can hold two identities from the
// same provider -- and a delete written against the account and the provider
// name took both of them for one click, while the guard in front of it counted
// providers and read the pair as a single way in.
func TestUnlinkRemovesOneIdentityRatherThanEveryOneFromAProvider(t *testing.T) {
	repository, accounts := newAccountFixture(t)
	ctx := testAccountContext()

	outcome, err := accounts.SignIn(ctx, app.ExternalIdentity{
		Provider: "google", ProviderUserID: "107021282825217378365", DisplayName: "Ada Lovelace",
	})
	if err != nil {
		t.Fatal(err)
	}
	member := outcome.User
	// Written in the order the ordering has to override, so that a listing
	// which merely returned rows as they were stored would answer differently.
	attach(t, repository, githubIdentity(member.ID, "999999999"))
	attach(t, repository, githubIdentity(member.ID, "216108319"))

	// Which of the two goes first is decided by the listing order, and a
	// remove keyed by a provider name can express nothing finer -- so the
	// answer has to be the same one every time rather than whatever the table
	// happens to return.
	if err := accounts.UnlinkIdentity(ctx, member, "github"); err != nil {
		t.Fatalf("first unlink: %v", err)
	}
	if got := providerNames(t, accounts, member.ID); len(got) != 2 || got[0] != "github:999999999" {
		t.Fatalf("identities = %v, want google and the second github still attached", got)
	}

	if err := accounts.UnlinkIdentity(ctx, member, "github"); err != nil {
		t.Fatalf("second unlink: %v", err)
	}
	if got := providerNames(t, accounts, member.ID); len(got) != 1 || got[0] != "google:107021282825217378365" {
		t.Fatalf("identities = %v, want google alone", got)
	}

	// And the last one is still the last one.
	err = accounts.UnlinkIdentity(ctx, member, "google")
	var refusal *domain.Error
	if !errors.As(err, &refusal) || refusal.Code != domain.CodeLastCredential {
		t.Fatalf("removing the only credential = %v, want %q", err, domain.CodeLastCredential)
	}
}

// The guard counts credentials, not provider names.
//
// Two GitHub identities are two ways in. Counting providers said "this is the
// only way to sign in to this account" about a pair and refused to remove
// either, which left an account holding a credential nothing could detach.
func TestLastCredentialGuardCountsIdentities(t *testing.T) {
	repository, accounts := newAccountFixture(t)
	ctx := testAccountContext()

	outcome, err := accounts.SignIn(ctx, app.ExternalIdentity{
		Provider: "github", ProviderUserID: "216108319", DisplayName: "Ada Lovelace",
	})
	if err != nil {
		t.Fatal(err)
	}
	member := outcome.User
	attach(t, repository, githubIdentity(member.ID, "999999999"))

	if err := accounts.UnlinkIdentity(ctx, member, "github"); err != nil {
		t.Fatalf("unlink with a spare credential: %v", err)
	}
	if got := providerNames(t, accounts, member.ID); len(got) != 1 || got[0] != "github:999999999" {
		t.Fatalf("identities = %v, want the second github still attached", got)
	}
}

// A provider an account does not hold is reported as absent rather than
// removing whatever the account does hold.
func TestUnlinkingAProviderThatIsNotLinkedRemovesNothing(t *testing.T) {
	_, accounts := newAccountFixture(t)
	ctx := testAccountContext()

	outcome, err := accounts.SignIn(ctx, app.ExternalIdentity{
		Provider: "google", ProviderUserID: "107021282825217378365", DisplayName: "Ada Lovelace",
	})
	if err != nil {
		t.Fatal(err)
	}
	err = accounts.UnlinkIdentity(ctx, outcome.User, "github")
	var refusal *domain.Error
	// Not ACCOUNT_NOT_FOUND, which is what this asserted: the account exists
	// and is right here. Only the identity is absent, and the transport turns
	// the old code into a 401 telling the caller to sign in again.
	if !errors.As(err, &refusal) || refusal.Code != domain.CodeIdentityNotLinked {
		t.Fatalf("unlinking an absent provider = %v, want %q", err, domain.CodeIdentityNotLinked)
	}
	if got := providerNames(t, accounts, outcome.User.ID); len(got) != 1 {
		t.Fatalf("identities = %v, want the google identity untouched", got)
	}
}
