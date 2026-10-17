package sqlite

import (
	"strings"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

// Who a composer may offer as a mention is a question about accounts, and it
// used to be answered by reading the site singleton -- correct while there was
// one account, and a member nobody could mention the moment there were two.
func TestMentionSuggestionsFindAdmittedAccountsAndNobodyElse(t *testing.T) {
	database, runtimeState := openTestDatabase(t, Options{})
	repository, err := NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(repository)
	if err != nil {
		t.Fatal(err)
	}
	ctx := testAccountContext()

	owner, err := accounts.ProfileAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.UpdateProfile(ctx, owner.ID, app.ProfileUpdate{
		Username: strptr("hostess"), DisplayName: strptr("The Hostess"),
	}); err != nil {
		t.Fatal(err)
	}

	seed := func(providerUserID, displayName string, status domain.AccountStatus) domain.User {
		t.Helper()
		outcome, err := accounts.SignIn(ctx, app.ExternalIdentity{
			Provider: "google", ProviderUserID: providerUserID, DisplayName: displayName,
		})
		if err != nil {
			t.Fatal(err)
		}
		if outcome.User.Status != status {
			if _, err := accounts.SetStatus(ctx, outcome.User.ID, status); err != nil {
				t.Fatal(err)
			}
		}
		return outcome.User
	}
	admitted := seed("1", "Ada Lovelace", domain.AccountActive)
	waiting := seed("2", "Applicant Waiting", domain.AccountPending)
	barred := seed("3", "Barred Person", domain.AccountSuspended)

	names := func(query string) []string {
		t.Helper()
		items, err := accounts.SuggestMentions(ctx, query, 8)
		if err != nil {
			t.Fatal(err)
		}
		found := make([]string, 0, len(items))
		for _, item := range items {
			found = append(found, item.Username)
			if item.ProfileURL != domain.ProfileURL(item.Username) {
				t.Fatalf("profile url %q does not match handle %q", item.ProfileURL, item.Username)
			}
		}
		return found
	}

	// The owner sorts first; an application and a barred account are absent.
	all := names("")
	if len(all) != 2 || all[0] != "hostess" || all[1] != admitted.Username {
		t.Fatalf("suggestions = %v, want the owner then the admitted member", all)
	}
	for _, hidden := range []domain.User{waiting, barred} {
		for _, name := range all {
			if name == hidden.Username {
				t.Fatalf("%q (%s) was offered as a mention", hidden.Username, hidden.Status)
			}
		}
	}

	if found := names("ada"); len(found) != 1 || found[0] != admitted.Username {
		t.Fatalf("handle prefix = %v", found)
	}
	if found := names("@lovelace"); len(found) != 1 || found[0] != admitted.Username {
		t.Fatalf("display name fragment = %v", found)
	}
	if found := names("nobody"); len(found) != 0 {
		t.Fatalf("a query nobody matches returned %v", found)
	}

	// A query is text somebody typed, not a pattern. Without escaping, "%"
	// would match every admitted account and "_" any single character.
	for _, wildcard := range []string{"%", "_", "\\", "%%"} {
		if found := names(wildcard); len(found) != 0 {
			t.Fatalf("wildcard %q was read as a pattern and matched %v", wildcard, found)
		}
	}

	if _, err := accounts.SuggestMentions(ctx, "", 0); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("invalid limit error = %v", err)
	}
	if _, err := accounts.SuggestMentions(ctx, strings.Repeat("a", 31), 8); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatal("an over-long query was accepted")
	}
}

func strptr(value string) *string { return &value }
