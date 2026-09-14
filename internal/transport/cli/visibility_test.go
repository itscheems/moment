package cli

import (
	"reflect"
	"testing"
)

// Direct mode is the owner by construction; API mode is the owner when its
// token is configured. Either way the CLI writes and reads the audience it
// was asked for, and a visitor's read of the same server sees none of it.
func TestPostVisibilityFlagDirectAPIParityAndVisitorReads(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", fixture.token)

	created := commandData(t, execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "just me", "--visibility", "only_me", "--json"))
	id := created["id"].(string)
	if created["visibility"] != "only_me" {
		t.Fatalf("direct create = %#v", created)
	}
	public := commandData(t, execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "for all", "--json"))
	if public["visibility"] != "everyone" {
		t.Fatalf("default create = %#v", public)
	}
	invalid := execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--visibility", "friends", "--json")
	if invalid.exit != ExitInvalidInput || decodeOneDocument(t, invalid.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("invalid visibility = %#v", invalid)
	}

	apiGet := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "get", id, "--json"))
	if !reflect.DeepEqual(created, apiGet) {
		t.Fatalf("direct create = %#v, API get with token = %#v", created, apiGet)
	}
	apiList := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "list", "--json"))
	if items := apiList["items"].([]any); len(items) != 2 {
		t.Fatalf("owner API list = %#v", apiList)
	}

	t.Setenv("MOMENT_TOKEN", "")
	visitorGet := execute(application, "", "--url", fixture.server.URL, "post", "get", id, "--json")
	if visitorGet.exit != ExitNotFound || decodeOneDocument(t, visitorGet.stdout)["error"].(map[string]any)["code"] != "POST_NOT_FOUND" {
		t.Fatalf("visitor get = %#v", visitorGet)
	}
	visitorList := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "list", "--json"))
	if items := visitorList["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != public["id"] {
		t.Fatalf("visitor list = %#v", visitorList)
	}

	t.Setenv("MOMENT_TOKEN", fixture.token)
	revealed := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "update", id, "--visibility", "everyone", "--json"))
	if revealed["visibility"] != "everyone" || revealed["edited_at"] != nil {
		t.Fatalf("API reveal = %#v", revealed)
	}
	directGet := commandData(t, execute(application, "", "post", "get", id, "--data-dir", fixture.dataDir, "--json"))
	if !reflect.DeepEqual(revealed, directGet) {
		t.Fatalf("API reveal = %#v, direct get = %#v", revealed, directGet)
	}
	empty := execute(application, "", "post", "update", id, "--data-dir", fixture.dataDir, "--json")
	if empty.exit != ExitInvalidInput {
		t.Fatalf("empty update = %#v", empty)
	}
}
