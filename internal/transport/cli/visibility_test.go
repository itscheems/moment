package cli

import (
	"reflect"
	"testing"
)

func TestPostVisibilityFlagDirectAPIParityAndVisitorReads(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", fixture.token)

	created := commandData(t, execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--as", "writer", "--content", "just me", "--visibility", "only_me", "--json"))
	id := created["id"].(string)
	if created["visibility"] != "only_me" {
		t.Fatalf("direct create = %#v", created)
	}
	public := commandData(t, execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--as", "writer", "--content", "for all", "--json"))
	if public["visibility"] != "everyone" {
		t.Fatalf("default create = %#v", public)
	}
	invalid := execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--as", "writer", "--content", "x", "--visibility", "friends", "--json")
	if invalid.exit != ExitInvalidInput || decodeOneDocument(t, invalid.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("invalid visibility = %#v", invalid)
	}

	apiGet := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "get", id, "--json"))
	if !reflect.DeepEqual(created, apiGet) {
		t.Fatalf("direct create = %#v, API get with token = %#v", created, apiGet)
	}
	apiList := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "list", "--json"))
	directList := commandData(t, execute(application, "", "post", "list", "--data-dir", fixture.dataDir, "--as", "writer", "--json"))
	if !reflect.DeepEqual(directList, apiList) {
		t.Fatalf("public timeline direct = %#v, API = %#v", directList, apiList)
	}
	if items := apiList["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != public["id"] {
		t.Fatalf("public timeline = %#v", apiList)
	}

	authorID := created["author"].(map[string]any)["user_id"].(string)
	apiProfile := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "list", "--author-id", authorID, "--json"))
	directProfile := commandData(t, execute(application, "", "post", "list", "--data-dir", fixture.dataDir, "--as", "writer", "--author-id", authorID, "--json"))
	if !reflect.DeepEqual(directProfile, apiProfile) {
		t.Fatalf("author timeline direct = %#v, API = %#v", directProfile, apiProfile)
	}
	items := apiProfile["items"].([]any)
	if len(items) != 2 || apiProfile["author_post_count"] != float64(2) {
		t.Fatalf("author timeline = %#v", apiProfile)
	}
	foundPrivate := false
	for _, item := range items {
		post := item.(map[string]any)
		if post["id"] == id && post["visibility"] == "only_me" {
			foundPrivate = true
		}
	}
	if !foundPrivate {
		t.Fatalf("author timeline omitted private post: %#v", apiProfile)
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
	visitorProfile := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "list", "--author-id", authorID, "--json"))
	if items := visitorProfile["items"].([]any); len(items) != 1 || items[0].(map[string]any)["id"] != public["id"] || visitorProfile["author_post_count"] != float64(1) {
		t.Fatalf("visitor author timeline = %#v", visitorProfile)
	}

	t.Setenv("MOMENT_TOKEN", fixture.token)
	revealed := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "update", id, "--visibility", "everyone", "--json"))
	if revealed["visibility"] != "everyone" || revealed["edited_at"] != nil {
		t.Fatalf("API reveal = %#v", revealed)
	}
	directGet := commandData(t, execute(application, "", "post", "get", id, "--data-dir", fixture.dataDir, "--as", "writer", "--json"))
	if !reflect.DeepEqual(revealed, directGet) {
		t.Fatalf("API reveal = %#v, direct get = %#v", revealed, directGet)
	}
	empty := execute(application, "", "post", "update", id, "--data-dir", fixture.dataDir, "--as", "writer", "--json")
	if empty.exit != ExitInvalidInput {
		t.Fatalf("empty update = %#v", empty)
	}
}
