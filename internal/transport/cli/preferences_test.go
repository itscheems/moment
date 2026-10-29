package cli

import "testing"

func TestAccountPreferencesDirectAPIParity(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", fixture.token)
	for _, locale := range []string{"en", "zh-CN", "auto"} {
		direct := execute(application, "", "user", "update-preferences", "--as", "writer", "--data-dir", fixture.dataDir, "--ui-locale", locale, "--json")
		api := execute(application, "", "user", "update-preferences", "--url", fixture.server.URL, "--ui-locale", locale, "--json")
		assertCanonicalParity(t, direct, api)
		assertCanonicalParity(t,
			execute(application, "", "user", "get-preferences", "--as", "writer", "--data-dir", fixture.dataDir, "--json"),
			execute(application, "", "user", "get-preferences", "--url", fixture.server.URL, "--json"))
	}
	for _, flags := range [][]string{{}, {"--ui-locale", "fr"}} {
		args := append([]string{"user", "update-preferences", "--as", "writer", "--data-dir", fixture.dataDir, "--json"}, flags...)
		if result := execute(application, "", args...); result.exit != ExitInvalidInput {
			t.Fatalf("invalid input: %+v", result)
		}
	}
	if result := execute(application, "", "user", "get-preferences", "--data-dir", fixture.dataDir, "--json"); result.exit != ExitUnauthorized {
		t.Fatalf("missing identity: %+v", result)
	}
}
