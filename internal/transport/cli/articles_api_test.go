package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"reflect"
	"testing"
)

func TestArticleAPIWholeWorkflowAndDirectReadParity(t *testing.T) {
	fixture := newPostCLIFixture(t)
	t.Setenv("MOMENT_TOKEN", fixture.token)
	application := momentTestApplication()
	bodyPath := writeArticleBodyFile(t, cliArticleBody)
	run := func(args ...string) commandOutput {
		t.Helper()
		args = append(args, "--url", fixture.server.URL, "--json")
		output := execute(application, "", args...)
		if output.exit != ExitSuccess || output.stderr != "" {
			t.Fatalf("%v: %#v", args, output)
		}
		return output
	}
	created := commandData(t, run("article", "new", "--title", "API article", "--body", bodyPath))
	id := created["id"].(string)
	read := commandData(t, run("article", "draft", id))
	direct := commandData(t, execute(application, "", "article", "draft", id, "--data-dir", fixture.dataDir, "--json"))
	if !reflect.DeepEqual(read, direct) {
		t.Fatalf("API/direct differ: %#v / %#v", read, direct)
	}
	listed := decodeOneDocument(t, run("article", "drafts").stdout)["data"].([]any)
	if len(listed) != 1 {
		t.Fatalf("drafts = %#v", listed)
	}
	run("article", "save", id, "--title", "Changed", "--body", bodyPath, "--expected-updated-at", read["updated_at"].(string))
	post := commandData(t, run("article", "publish", id, "--visibility", "only_me", "--idempotency-key", "api-article"))
	replay := commandData(t, run("article", "publish", id, "--visibility", "only_me", "--idempotency-key", "api-article"))
	if post["id"] != replay["id"] || post["visibility"] != "only_me" {
		t.Fatalf("publish/replay = %#v / %#v", post, replay)
	}
	workingCopy := commandData(t, run("article", "edit", post["id"].(string)))
	if workingCopy["published_entry_id"] != post["id"] {
		t.Fatalf("working copy = %#v", workingCopy)
	}
	discarded := commandData(t, run("article", "discard", workingCopy["id"].(string), "--yes"))
	if discarded["discarded"] != true {
		t.Fatalf("discard = %#v", discarded)
	}
	if len(decodeOneDocument(t, run("article", "drafts").stdout)["data"].([]any)) != 0 {
		t.Fatal("draft survived discard")
	}
}

func TestArticleAPIRejectsMalformedDraftsAndFalseDiscardAcknowledgements(t *testing.T) {
	const id = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	base := map[string]any{
		"id": id, "title": "", "body": json.RawMessage(`{"schema_version":1,"type":"doc","content":[]}`), "body_schema_version": 1,
		"cover_media_id": nil, "published_entry_id": nil,
		"created_at": "2026-09-25T02:00:00Z", "updated_at": "2026-09-25T02:00:00Z",
	}
	cases := []struct {
		name   string
		change func(map[string]any)
	}{
		{"missing nullable field", func(d map[string]any) { delete(d, "cover_media_id") }},
		{"null title", func(d map[string]any) { d["title"] = nil }},
		{"invalid body", func(d map[string]any) { d["body"] = map[string]any{"type": "foreign"} }},
		{"wrong version", func(d map[string]any) { d["body_schema_version"] = 2 }},
		{"invalid ID", func(d map[string]any) { d["id"] = "bad" }},
		{"invalid reference", func(d map[string]any) { d["published_entry_id"] = "bad" }},
		{"noncanonical revision", func(d map[string]any) { d["updated_at"] = "2026-09-25T02:00:00+00:00" }},
		{"unknown field", func(d map[string]any) { d["extra"] = true }},
	}
	t.Setenv("MOMENT_TOKEN", "test-token")
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			data := make(map[string]any)
			for k, v := range base {
				data[k] = v
			}
			one.change(data)
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "meta": map[string]any{"request_id": "r"}})
			}))
			defer server.Close()
			output := execute(momentTestApplication(), "", "article", "draft", id, "--url", server.URL, "--json")
			if output.exit != ExitUnavailable || output.stderr != "" {
				t.Fatalf("accepted malformed response: %#v", output)
			}
		})
	}
	for _, data := range []map[string]any{{"id": id, "discarded": false}, {"id": "01ARZ3NDEKTSV4RRFFQ69G5FAW", "discarded": true}, {"id": id}} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"data": data, "meta": map[string]any{"request_id": "r"}})
		}))
		output := execute(momentTestApplication(), "", "article", "discard", id, "--yes", "--url", server.URL, "--json")
		server.Close()
		if output.exit != ExitUnavailable {
			t.Fatalf("accepted false discard: %#v", output)
		}
	}
}
