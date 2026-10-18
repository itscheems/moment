package cli

import (
	"context"
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestArticlePreviewIsConsistentAcrossDirectAndAPIReadSurfaces(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", fixture.token)
	direct := []string{"--data-dir", fixture.dataDir, "--as", "writer"}
	api := []string{"--url", fixture.server.URL}
	read := func(mode []string, args ...string) any {
		t.Helper()
		command := append(append([]string{}, mode...), args...)
		output := execute(application, "", append(command, "--json")...)
		if output.exit != ExitSuccess || output.stderr != "" {
			t.Fatalf("read %v: %#v", args, output)
		}
		return decodeOneDocument(t, output.stdout)["data"]
	}
	previewOf := func(post map[string]any, full bool) {
		t.Helper()
		article, ok := post["article"].(map[string]any)
		if !ok || article["excerpt"] != "previewneedle "+strings.Repeat("字", 145)+"\nlast\nsecond..." || (article["body"] != nil) != full {
			t.Fatalf("unexpected article projection: %#v", article)
		}
		if !full && (article["body_schema_version"] != nil || post["content_html"] != "") {
			t.Fatalf("a list shipped the preview's source body: %#v", article)
		}
	}
	prose := "previewneedle " + strings.Repeat("字", 145)
	quotedProse, err := json.Marshal(prose)
	if err != nil {
		t.Fatal(err)
	}
	body := `{"schema_version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":` + string(quotedProse) + `},{"type":"hardBreak"},{"type":"text","text":"last"}]},{"type":"paragraph","content":[{"type":"text","text":"second"}]}]}`
	bodyPath := writeArticleBodyFile(t, body)
	draft := read(direct, "article", "new", "--title", "preview article", "--body", bodyPath).(map[string]any)
	draftID := draft["id"].(string)
	want := prose + "\nlast\nsecond..."
	if draft["excerpt"] != want {
		t.Fatalf("create did not return the reading preview: %#v", draft)
	}
	if one, two := read(direct, "article", "draft", draftID), read(api, "article", "draft", draftID); !reflect.DeepEqual(one, two) {
		t.Fatalf("draft direct/API mismatch: %#v, %#v", one, two)
	}
	one, two := read(direct, "article", "drafts"), read(api, "article", "drafts")
	if !reflect.DeepEqual(one, two) {
		t.Fatalf("draft summary direct/API mismatch: %#v, %#v", one, two)
	}
	summaries := one.([]any)
	if len(summaries) != 1 || summaries[0].(map[string]any)["excerpt"] != want || summaries[0].(map[string]any)["body"] != nil {
		t.Fatalf("draft summary = %#v", summaries)
	}
	post := read(direct, "article", "publish", draftID).(map[string]any)
	id := post["id"].(string)
	previewOf(post, true)
	quote := read(direct, "post", "create", "--content", "quote wrapper", "--quote", id).(map[string]any)
	quoteID := quote["id"].(string)
	before := readArticleState(t, fixture, id)
	if len([]rune(before.excerpt)) != 140 || strings.Contains(before.excerpt, "\n") {
		t.Fatalf("legacy cache changed: %q", before.excerpt)
	}

	for _, request := range [][]string{
		{"post", "get", id}, {"post", "list"}, {"search", "query", "previewneedle"}, {"post", "get", quoteID},
	} {
		one, two := read(direct, request...), read(api, request...)
		if !reflect.DeepEqual(one, two) {
			t.Fatalf("%v direct/API mismatch: %#v, %#v", request, one, two)
		}
		data := one.(map[string]any)
		switch request[1] {
		case "get":
			if request[2] == id {
				previewOf(data, true)
			} else {
				previewOf(data["quoted_post"].(map[string]any), false)
			}
		case "list":
			var articles int
			for _, item := range data["items"].([]any) {
				row := item.(map[string]any)
				if row["kind"] == "article" {
					previewOf(row, false)
					articles++
				}
			}
			if articles != 1 {
				t.Fatalf("list lost its article: %#v", data)
			}
		case "query":
			hits := data["items"].([]any)
			if len(hits) != 1 {
				t.Fatalf("search lost its article: %#v", data)
			}
			previewOf(hits[0].(map[string]any), false)
		}
	}
	if after := readArticleState(t, fixture, id); after != before {
		t.Fatalf("reads rewrote cache, timestamp or media: before=%+v after=%+v", before, after)
	}

	// Visibility is resolved before a private article's body is parsed. A bad
	// private body remains absent from public detail, list, search and quote.
	if err := fixture.database.WriteTx(context.Background(), func(q sqlitestore.Queries) error {
		if _, err := q.ExecContext(context.Background(), `UPDATE entries SET visibility='only_me' WHERE id=?`, id); err != nil {
			return err
		}
		_, err := q.ExecContext(context.Background(), `UPDATE entry_article SET body_json='{"schema_version":1,"type":"doc","content":[{"type":"unknown"}]}' WHERE entry_id=?`, id)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOMENT_TOKEN", "")
	output := execute(application, "", "--url", fixture.server.URL, "post", "get", id, "--json")
	if output.exit != ExitNotFound {
		t.Fatalf("private damaged article leaked through detail: %#v", output)
	}
	list := read(api, "post", "list").(map[string]any)["items"].([]any)
	if len(list) != 1 || list[0].(map[string]any)["id"] != quoteID {
		t.Fatalf("private article leaked through list: %#v", list)
	}
	quoted := read(api, "post", "get", quoteID).(map[string]any)["quoted_post"].(map[string]any)
	if quoted["unavailable"] != true || quoted["article"] != nil {
		t.Fatalf("private article leaked through quote: %#v", quoted)
	}
	if hits := read(api, "search", "query", "previewneedle").(map[string]any)["items"].([]any); len(hits) != 0 {
		t.Fatalf("private article leaked through search: %#v", hits)
	}
}
