package cli

import (
	"context"
	"testing"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// The account review commands take a username, and nothing executed them with
// one until this test existed.
//
// That is how all three shipped broken: a leaf that does not declare its
// positional arguments makes cobra read the username as a subcommand it has
// never heard of, so `moment user approve someone` answered "unknown command"
// rather than approving anyone. The capability matrix walks every leaf but
// never passes an argument, and the help golden only renders --help, so both
// stayed green. Running the real command with the real argument is the only
// thing that would have caught it, which is what this does.
func TestAccountReviewCommandsAcceptAUsername(t *testing.T) {
	runtimeState, dataDir := initializeSiteCLIData(t)
	if _, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{}); err != nil {
		t.Fatal(err)
	}

	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := sqlitestore.NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	accounts, err := app.NewAccountService(repository)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.SignIn(context.Background(), app.ExternalIdentity{
		Provider:         "google",
		ProviderUserID:   "108214906739283746501",
		Email:            "applicant@example.test",
		EmailVerified:    true,
		EmailDeliverable: true,
		DisplayName:      "Applicant Example",
	}); err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}

	application := momentTestApplication()

	listed := commandList(t, execute(application, "", "user", "list", "--data-dir", dataDir, "--json"))
	applicant := ""
	for _, item := range listed {
		record, ok := item.(map[string]any)
		if !ok {
			t.Fatalf("account entry = %#v", item)
		}
		if record["role"] == string(domain.UserRoleMember) {
			if record["status"] != string(domain.AccountPending) {
				t.Fatalf("a new account landed as %v, want pending", record["status"])
			}
			applicant, _ = record["username"].(string)
		}
	}
	if applicant == "" {
		t.Fatalf("no applicant in %#v", listed)
	}

	identities := commandList(t, execute(application, "", "user", "identities", applicant, "--data-dir", dataDir, "--json"))
	if len(identities) != 1 {
		t.Fatalf("identities = %#v", identities)
	}
	if record, ok := identities[0].(map[string]any); !ok || record["provider"] != "google" {
		t.Fatalf("identity = %#v", identities[0])
	}

	approved := commandData(t, execute(application, "", "user", "approve", applicant, "--data-dir", dataDir, "--json"))
	if approved["status"] != string(domain.AccountActive) {
		t.Fatalf("approved = %#v", approved)
	}

	suspended := commandData(t, execute(application, "", "user", "suspend", applicant, "--data-dir", dataDir, "--yes", "--json"))
	if suspended["status"] != string(domain.AccountSuspended) {
		t.Fatalf("suspended = %#v", suspended)
	}

	for name, args := range map[string][]string{
		"no username at all":       {"user", "approve", "--data-dir", dataDir, "--json"},
		"more than one username":   {"user", "approve", applicant, "someone-else", "--data-dir", dataDir, "--json"},
		"a username nobody holds":  {"user", "approve", "nobodyhere", "--data-dir", dataDir, "--json"},
		"a username nobody can be": {"user", "identities", "Not A Username", "--data-dir", dataDir, "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			output := execute(application, "", args...)
			if output.exit == ExitSuccess {
				t.Fatalf("expected a refusal, got %#v", output)
			}
		})
	}
}

// commandList decodes a command's JSON list payload.
func commandList(t *testing.T, output commandOutput) []any {
	t.Helper()
	if output.exit != ExitSuccess {
		t.Fatalf("command failed: %#v", output)
	}
	payload := decodeOneDocument(t, output.stdout)
	items, ok := payload["data"].([]any)
	if !ok {
		t.Fatalf("data is not a list: %#v", payload)
	}
	return items
}
