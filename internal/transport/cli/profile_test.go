package cli

import "testing"

func TestOwnProfileUpdateHasDirectAPIParityAndDoesNotWriteSiteConfiguration(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", fixture.token)
	direct := execute(application, "", "user", "update-profile", "--as", "writer", "--data-dir", fixture.dataDir, "--username", "renamed", "--display-name", "Actual Writer", "--bio", "Account biography", "--json")
	if direct.exit != ExitSuccess {
		t.Fatalf("direct profile=%+v", direct)
	}
	api := execute(application, "", "user", "update-profile", "--url", fixture.server.URL, "--username", "renamed", "--display-name", "Actual Writer", "--bio", "Account biography", "--json")
	assertCanonicalParity(t, direct, api)
	site := commandData(t, execute(application, "", "site", "get", "--data-dir", fixture.dataDir, "--json"))
	if site["title"] != "Moment" || site["username"] != "renamed" || site["bio"] != "Account biography" {
		t.Fatalf("account/site separation=%+v", site)
	}
	update := execute(application, "", "site", "update", "--data-dir", fixture.dataDir, "--title", "Site title", "--json")
	if update.exit != ExitSuccess {
		t.Fatalf("site update=%+v", update)
	}
	cleared := execute(application, "", "user", "update-profile", "--as", "renamed", "--data-dir", fixture.dataDir, "--clear-avatar", "--clear-cover", "--json")
	if cleared.exit != ExitSuccess {
		t.Fatalf("picture clear=%+v", cleared)
	}
	assertCanonicalParity(t, cleared, execute(application, "", "user", "update-profile", "--url", fixture.server.URL, "--clear-avatar", "--clear-cover", "--json"))
	missing := execute(application, "", "user", "update-profile", "--data-dir", fixture.dataDir, "--display-name", "missing identity", "--json")
	if missing.exit != ExitUnauthorized {
		t.Fatalf("direct identity required=%+v", missing)
	}
	stale := execute(application, "", "site", "update", "--data-dir", fixture.dataDir, "--display-name", "another person", "--json")
	if stale.exit != ExitInvalidInput {
		t.Fatalf("personal field was accepted as site configuration=%+v", stale)
	}
}
