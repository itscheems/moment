package cli

import (
	"context"
	"reflect"
	"testing"

	"github.com/moment/moment/internal/storage/sqlite"
)

func TestSearchCLIDirectAPIParityAndRebuildSafety(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	created := execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "旧城雨 searchable", "--json")
	if created.exit != ExitSuccess {
		t.Fatalf("create = %#v", created)
	}

	direct := execute(application, "", "search", "query", "--data-dir", fixture.dataDir, "--query", "旧城雨", "--json")
	api := execute(application, "", "--url", fixture.server.URL, "search", "query", "--query", "旧城雨", "--json")
	if direct.exit != ExitSuccess || api.exit != ExitSuccess || direct.stderr != "" || api.stderr != "" {
		t.Fatalf("direct=%#v API=%#v", direct, api)
	}
	if !reflect.DeepEqual(commandData(t, direct), commandData(t, api)) {
		t.Fatalf("direct data=%#v API data=%#v", commandData(t, direct), commandData(t, api))
	}
	if envelope := decodeOneDocument(t, direct.stdout); envelope["meta"].(map[string]any)["next_cursor"] != nil {
		t.Fatalf("direct meta = %#v", envelope["meta"])
	}

	missing := execute(application, "", "search", "query", "--data-dir", fixture.dataDir, "--json")
	if missing.exit != ExitInvalidInput || decodeOneDocument(t, missing.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("missing query = %#v", missing)
	}
	unsupported := execute(application, "", "--url", fixture.server.URL, "search", "rebuild", "--dry-run", "--json")
	if unsupported.exit != ExitInvalidInput || decodeOneDocument(t, unsupported.stdout)["error"].(map[string]any)["code"] != "MODE_UNSUPPORTED" {
		t.Fatalf("unsupported rebuild = %#v", unsupported)
	}

	if err := fixture.database.WriteTx(context.Background(), func(queries sqlite.Queries) error {
		_, err := queries.ExecContext(context.Background(), `DELETE FROM entry_fts`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	dryRun := execute(application, "", "search", "rebuild", "--data-dir", fixture.dataDir, "--dry-run", "--json")
	if dryRun.exit != ExitSuccess || commandData(t, dryRun)["drifted"].(float64) != 1 || commandData(t, dryRun)["rebuilt"].(float64) != 0 {
		t.Fatalf("dry-run = %#v", dryRun)
	}
	refused := execute(application, "", "search", "rebuild", "--data-dir", fixture.dataDir, "--no-input", "--json")
	if refused.exit != ExitInvalidInput {
		t.Fatalf("refused = %#v", refused)
	}
	rebuilt := execute(application, "", "search", "rebuild", "--data-dir", fixture.dataDir, "--yes", "--json")
	if rebuilt.exit != ExitSuccess || commandData(t, rebuilt)["rebuilt"].(float64) != 1 {
		t.Fatalf("rebuilt = %#v", rebuilt)
	}
	verified := execute(application, "", "search", "query", "--data-dir", fixture.dataDir, "--query", "旧城雨", "--json")
	if verified.exit != ExitSuccess || len(commandData(t, verified)["items"].([]any)) != 1 {
		t.Fatalf("verified = %#v", verified)
	}
}
