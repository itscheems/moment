package cli

import (
	"reflect"
	"testing"
)

func TestArticleWithdrawalCLIDirectAPIAndDryRun(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	body := writeArticleBodyFile(t, cliArticleBody)
	created := execute(application, "", "article", "new", "--data-dir", fixture.dataDir, "--as", "writer", "--title", "withdraw", "--body", body, "--idempotency-key", "once", "--json")
	if created.exit != ExitSuccess {
		t.Fatalf("create=%+v", created)
	}
	draftID := commandData(t, created)["id"].(string)
	replay := execute(application, "", "article", "new", "--data-dir", fixture.dataDir, "--as", "writer", "--title", "withdraw", "--body", body, "--idempotency-key", "once", "--json")
	if replay.exit != ExitSuccess || commandData(t, replay)["id"] != draftID {
		t.Fatal("retry created another draft")
	}
	published := execute(application, "", "article", "publish", draftID, "--data-dir", fixture.dataDir, "--as", "writer", "--json")
	if published.exit != ExitSuccess {
		t.Fatalf("publish=%+v", published)
	}
	id := commandData(t, published)["id"].(string)
	inspect := execute(application, "", "article", "unpublish", id, "--data-dir", fixture.dataDir, "--as", "writer", "--dry-run", "--json")
	if inspect.exit != ExitSuccess {
		t.Fatalf("dry run=%+v", inspect)
	}
	still := execute(application, "", "post", "get", id, "--url", fixture.server.URL, "--json")
	if still.exit != ExitSuccess {
		t.Fatal("dry run hid article")
	}
	// API commands use the fixture's authenticated token just like other parity tests.
	t.Setenv("MOMENT_TOKEN", fixture.token)
	withdrawn := execute(application, "", "article", "unpublish", id, "--url", fixture.server.URL, "--yes", "--json")
	if withdrawn.exit != ExitSuccess {
		t.Fatalf("withdraw=%+v", withdrawn)
	}
	api := execute(application, "", "article", "draft", commandData(t, withdrawn)["id"].(string), "--url", fixture.server.URL, "--json")
	direct := execute(application, "", "article", "draft", commandData(t, withdrawn)["id"].(string), "--data-dir", fixture.dataDir, "--as", "writer", "--json")
	if api.exit != ExitSuccess || direct.exit != ExitSuccess || !reflect.DeepEqual(commandData(t, api), commandData(t, direct)) {
		t.Fatalf("draft parity=%+v / %+v", api, direct)
	}
	deleted := execute(application, "", "article", "delete", id, "--data-dir", fixture.dataDir, "--as", "writer", "--dry-run", "--json")
	if deleted.exit != ExitSuccess {
		t.Fatalf("delete dry run=%+v", deleted)
	}
}
