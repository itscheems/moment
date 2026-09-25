package cli

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

const cliArticleBody = `{"schema_version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"命令行写的正文"}]}]}`

func writeArticleBodyFile(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "body.json")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// The command line is how an article is written before there is any interface,
// so the whole arc has to work here first.
func TestArticleCLIWritesPublishesAndEditsAnArticle(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	bodyPath := writeArticleBodyFile(t, cliArticleBody)

	createOutput := execute(application, "", "article", "new", "--data-dir", fixture.dataDir,
		"--title", "命令行文章", "--body", bodyPath, "--json")
	if createOutput.exit != ExitSuccess || createOutput.stderr != "" {
		t.Fatalf("create draft = %#v", createOutput)
	}
	draft := commandData(t, createOutput)
	draftID, _ := draft["id"].(string)
	if draftID == "" || draft["title"] != "命令行文章" || draft["body"] == nil {
		t.Fatalf("draft = %#v", draft)
	}
	revision, _ := draft["updated_at"].(string)

	listOutput := execute(application, "", "article", "drafts", "--data-dir", fixture.dataDir, "--json")
	var listed struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal([]byte(listOutput.stdout), &listed); err != nil {
		t.Fatalf("list = %#v", listOutput)
	}
	if len(listed.Data) != 1 || listed.Data[0]["id"] != draftID {
		t.Fatalf("listed = %#v", listed.Data)
	}

	// A save that does not name the revision it was based on is refused.
	missing := execute(application, "", "article", "save", draftID, "--data-dir", fixture.dataDir,
		"--title", "改名", "--body", bodyPath, "--json")
	if missing.exit == ExitSuccess {
		t.Fatalf("a replacement without a revision must fail: %#v", missing)
	}
	updated := execute(application, "", "article", "save", draftID, "--data-dir", fixture.dataDir,
		"--title", "改过的标题", "--body", bodyPath, "--expected-updated-at", revision, "--json")
	if updated.exit != ExitSuccess {
		t.Fatalf("update = %#v", updated)
	}

	published := execute(application, "", "article", "publish", draftID, "--data-dir", fixture.dataDir,
		"--idempotency-key", "cli-article-001", "--json")
	if published.exit != ExitSuccess || published.stderr != "" {
		t.Fatalf("publish = %#v", published)
	}
	post := commandData(t, published)
	postID, _ := post["id"].(string)
	if post["kind"] != "article" || post["content_md"] != nil || post["content_html"] == "" {
		t.Fatalf("published = %#v", post)
	}

	// A retry replays rather than publishing twice.
	replay := execute(application, "", "article", "publish", draftID, "--data-dir", fixture.dataDir,
		"--idempotency-key", "cli-article-001", "--json")
	if replay.exit != ExitSuccess || commandData(t, replay)["id"] != postID {
		t.Fatalf("replay = %#v", replay)
	}

	// It reads back through the ordinary post surface.
	got := execute(application, "", "post", "get", postID, "--data-dir", fixture.dataDir, "--json")
	if got.exit != ExitSuccess || commandData(t, got)["kind"] != "article" {
		t.Fatalf("post get = %#v", got)
	}

	// Editing opens a working copy that names the article.
	editing := execute(application, "", "article", "edit", postID, "--data-dir", fixture.dataDir, "--json")
	if editing.exit != ExitSuccess {
		t.Fatalf("edit = %#v", editing)
	}
	if commandData(t, editing)["published_entry_id"] != postID {
		t.Fatalf("working copy = %#v", commandData(t, editing))
	}
}

func TestArticleCLIRefusesAnEmptyDraftAndReportsItOnStderr(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	bodyPath := writeArticleBodyFile(t, `{"schema_version":1,"type":"doc","content":[]}`)

	created := execute(application, "", "article", "new", "--data-dir", fixture.dataDir,
		"--body", bodyPath, "--json")
	if created.exit != ExitSuccess {
		t.Fatalf("an empty draft must be saveable: %#v", created)
	}
	draftID, _ := commandData(t, created)["id"].(string)

	refused := execute(application, "", "article", "publish", draftID, "--data-dir", fixture.dataDir, "--json")
	if refused.exit == ExitSuccess {
		t.Fatalf("an empty draft must not publish: %#v", refused)
	}
	// In JSON mode the refusal is an error envelope on stdout, the way every
	// other command answers, with a machine-readable code.
	envelope := decodeOneDocument(t, refused.stdout)
	if envelope["ok"] != false {
		t.Fatalf("stdout must not carry a success envelope: %s", refused.stdout)
	}
	failure, _ := envelope["error"].(map[string]any)
	if failure == nil || failure["code"] == "" {
		t.Fatalf("a refusal must name a code: %s", refused.stdout)
	}
}

// The body is a document, so it can come from stdin like any other stream.
func TestArticleCLIReadsABodyFromStandardInput(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	created := execute(application, cliArticleBody, "article", "new", "--data-dir", fixture.dataDir,
		"--title", "管道进来的", "--body", "-", "--json")
	if created.exit != ExitSuccess {
		t.Fatalf("stdin body = %#v", created)
	}
	if commandData(t, created)["title"] != "管道进来的" {
		t.Fatalf("draft = %#v", commandData(t, created))
	}
}

type articleStoredState struct {
	excerpt   string
	updatedAt int64
	editedAt  sql.NullInt64
	projected int
}

func readArticleState(t *testing.T, fixture postCLIFixture, entryID string) articleStoredState {
	t.Helper()
	var state articleStoredState
	if err := fixture.database.ReadTx(context.Background(), func(queries sqlitestore.Queries) error {
		return queries.QueryRowContext(context.Background(), `
SELECT article.excerpt, entries.updated_at_ms, entries.edited_at_ms,
       (SELECT count(*) FROM entry_media WHERE entry_media.entry_id = article.entry_id)
FROM entry_article AS article
JOIN entries ON entries.id = article.entry_id
WHERE article.entry_id = ?`, entryID).
			Scan(&state.excerpt, &state.updatedAt, &state.editedAt, &state.projected)
	}); err != nil {
		t.Fatal(err)
	}
	return state
}

// Repairing a value the system derived is not an edit by the author.
//
// This is the whole reason the repair is its own command rather than the
// doctor's old advice to open each affected article and publish it again:
// publishing moves updated_at and sets edited_at, so every reader would be told
// the author revised an article they never touched.
func TestArticleRebuildRepairsDerivedValuesWithoutMarkingAnArticleEdited(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	bodyPath := writeArticleBodyFile(t, cliArticleBody)

	created := execute(application, "", "article", "new", "--data-dir", fixture.dataDir,
		"--title", "要修的文章", "--body", bodyPath, "--json")
	if created.exit != ExitSuccess {
		t.Fatalf("create = %#v", created)
	}
	draftID, _ := commandData(t, created)["id"].(string)
	published := execute(application, "", "article", "publish", draftID, "--data-dir", fixture.dataDir, "--json")
	if published.exit != ExitSuccess {
		t.Fatalf("publish = %#v", published)
	}
	postID, _ := commandData(t, published)["id"].(string)

	// A site that was just written has nothing to repair.
	clean := execute(application, "", "article", "rebuild", "--data-dir", fixture.dataDir, "--dry-run", "--json")
	if clean.exit != ExitSuccess || clean.stderr != "" {
		t.Fatalf("clean inspection = %#v", clean)
	}
	if data := commandData(t, clean); data["total"] != float64(1) || data["drifted"] != float64(0) || data["dry_run"] != true {
		t.Fatalf("clean inspection = %#v", data)
	}

	before := readArticleState(t, fixture, postID)
	// Age the excerpt the way a change to how an excerpt is derived does.
	if err := fixture.database.WriteTx(context.Background(), func(queries sqlitestore.Queries) error {
		_, err := queries.ExecContext(context.Background(),
			`UPDATE entry_article SET excerpt = '上一版的摘要' WHERE entry_id = ?`, postID)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// A dry run counts it and changes nothing.
	dry := execute(application, "", "article", "rebuild", "--data-dir", fixture.dataDir, "--dry-run", "--json")
	if data := commandData(t, dry); dry.exit != ExitSuccess || data["drifted"] != float64(1) || data["rebuilt"] != float64(0) {
		t.Fatalf("dry run = %#v", dry)
	}
	if got := readArticleState(t, fixture, postID); got.excerpt != "上一版的摘要" {
		t.Fatalf("a dry run must not repair anything, excerpt = %q", got.excerpt)
	}
	// And without a confirmation there is no repair either.
	refused := execute(application, "", "article", "rebuild", "--data-dir", fixture.dataDir, "--no-input", "--json")
	if refused.exit != ExitInvalidInput {
		t.Fatalf("an unconfirmed repair must be refused: %#v", refused)
	}

	repaired := execute(application, "", "article", "rebuild", "--data-dir", fixture.dataDir, "--yes", "--json")
	if data := commandData(t, repaired); repaired.exit != ExitSuccess ||
		data["drifted"] != float64(1) || data["rebuilt"] != float64(1) || data["dry_run"] != false {
		t.Fatalf("repair = %#v", repaired)
	}

	after := readArticleState(t, fixture, postID)
	if after.excerpt != before.excerpt {
		t.Fatalf("the excerpt must be derived again from the body: want %q got %q", before.excerpt, after.excerpt)
	}
	if after.projected != before.projected {
		t.Fatalf("projection = %d, want %d", after.projected, before.projected)
	}
	if after.updatedAt != before.updatedAt || after.editedAt != before.editedAt {
		t.Fatalf("a repair must not touch the timeline row: before=%#v after=%#v", before, after)
	}
	if after.editedAt.Valid {
		t.Fatalf("an article nobody edited must not carry an edited mark: %#v", after.editedAt)
	}

	// Once repaired it stays repaired.
	again := execute(application, "", "article", "rebuild", "--data-dir", fixture.dataDir, "--dry-run", "--json")
	if data := commandData(t, again); again.exit != ExitSuccess || data["drifted"] != float64(0) {
		t.Fatalf("second inspection = %#v", again)
	}
}
