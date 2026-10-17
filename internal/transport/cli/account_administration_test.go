package cli

import (
	"context"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"testing"
)

func TestRoleAndDisplaySelectionHaveDirectAPIParity(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", fixture.token)
	accounts := testAccountService(t, fixture.database, fixture.runtime)
	outcome, err := accounts.SignIn(context.Background(), app.ExternalIdentity{Provider: "google", ProviderUserID: "bob", DisplayName: "Bob"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := accounts.SetStatus(context.Background(), outcome.User.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	direct := func(args ...string) commandOutput {
		return execute(application, "", append(args, "--data-dir", fixture.dataDir, "--json")...)
	}
	api := func(args ...string) commandOutput {
		return execute(application, "", append(args, "--url", fixture.server.URL, "--json")...)
	}
	first := direct("user", "set-role", member.Username, "--role", "admin")
	if first.exit != ExitSuccess {
		t.Fatalf("direct role=%+v", first)
	}
	assertCanonicalParity(t, first, api("user", "set-role", member.Username, "--role", "admin"))
	if out := api("user", "set-role", member.Username, "--role", "member"); out.exit != ExitSuccess {
		t.Fatalf("API role=%+v", out)
	}
	selected := direct("site", "set-profile", member.Username)
	if selected.exit != ExitSuccess {
		t.Fatalf("direct profile=%+v", selected)
	}
	assertCanonicalParity(t, selected, api("site", "set-profile", member.Username))
	profile := commandData(t, selected)
	if profile["profile_user_id"] != member.ID {
		t.Fatal("selected display profile did not change")
	}
	administrator, err := accounts.AccountByUsername(context.Background(), "writer")
	if err != nil || !administrator.IsAdmin() {
		t.Fatal("display selection transferred administrator rights")
	}
	if user, err := accounts.Account(context.Background(), member.ID); err != nil || user.IsAdmin() {
		t.Fatal("display account was promoted")
	}
	refused := direct("user", "set-role", "writer", "--role", "member")
	if refused.exit != ExitConflict {
		t.Fatalf("last administrator=%+v", refused)
	}
	assertCanonicalParity(t, refused, api("user", "set-role", "writer", "--role", "member"))
	assertCanonicalParity(t, direct("user", "set-role", "missing", "--role", "member"), api("user", "set-role", "missing", "--role", "member"))
	rejected := api("site", "set-profile", member.Username, "--as", "writer")
	if rejected.exit != ExitInvalidInput {
		t.Fatalf("API identity override=%+v", rejected)
	}
}
