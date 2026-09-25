package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/runtimefs"
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
	for _, invalidFlag := range []string{"--limit=0", "--from=", "--to=", "--cursor="} {
		for _, mode := range [][]string{{"--data-dir", fixture.dataDir}, {"--url", fixture.server.URL}} {
			args := append([]string{}, mode...)
			args = append(args, "search", "query", "--query", "旧城雨", invalidFlag, "--json")
			invalid := execute(application, "", args...)
			if invalid.exit != ExitInvalidInput || decodeOneDocument(t, invalid.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
				t.Fatalf("%v %s = %#v", mode, invalidFlag, invalid)
			}
		}
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

func TestSearchRebuildRuntimeErrorAndLegacyDryRunAreNonMutating(t *testing.T) {
	application := momentTestApplication()
	fresh := execute(application, "", "search", "rebuild", "--data-dir", t.TempDir(), "--dry-run", "--json")
	if fresh.exit != ExitIO || decodeOneDocument(t, fresh.stdout)["error"].(map[string]any)["code"] != "INSTANCE_NOT_READY" {
		t.Fatalf("fresh data directory = %#v", fresh)
	}

	dataDir := t.TempDir()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: dataDir, Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	createLegacySearchSchema(t, runtimeState.Current.DatabasePath)
	before, err := os.ReadFile(runtimeState.Current.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	legacy := execute(application, "", "search", "rebuild", "--data-dir", dataDir, "--dry-run", "--json")
	if legacy.exit != ExitIntegrity || decodeOneDocument(t, legacy.stdout)["error"].(map[string]any)["code"] != "SEARCH_SCHEMA_UPGRADE_REQUIRED" {
		t.Fatalf("legacy dry-run = %#v", legacy)
	}
	after, err := os.ReadFile(runtimeState.Current.DatabasePath)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Fatal("legacy dry-run changed the SQLite database bytes")
	}
	database, err := sql.Open("sqlite", "file:"+filepath.ToSlash(runtimeState.Current.DatabasePath)+"?mode=ro")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	var version, indexed int
	if err := database.QueryRow(`SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if err := database.QueryRow(`SELECT count(*) FROM entry_fts`).Scan(&indexed); err != nil {
		t.Fatal(err)
	}
	if version != 3 || indexed != 0 {
		t.Fatalf("legacy dry-run state version=%d indexed=%d", version, indexed)
	}
}

func createLegacySearchSchema(t *testing.T, databasePath string) {
	t.Helper()
	database, err := sql.Open("sqlite", databasePath)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	_, currentFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve search test source path")
	}
	migrationDirectory := filepath.Join(filepath.Dir(currentFile), "..", "..", "storage", "sqlite", "migrations")
	for version, filename := range []string{"0001_core.sql", "0002_media_storage_path.sql", "0003_site_settings_defaults.sql"} {
		contents, err := os.ReadFile(filepath.Join(migrationDirectory, filename))
		if err != nil {
			t.Fatal(err)
		}
		canonical := bytes.ReplaceAll(contents, []byte("\r\n"), []byte("\n"))
		canonical = bytes.ReplaceAll(canonical, []byte("\r"), []byte("\n"))
		if _, err := database.Exec(string(canonical)); err != nil {
			t.Fatalf("apply %s: %v", filename, err)
		}
		digest := sha256.Sum256(canonical)
		name := strings.TrimSuffix(strings.SplitN(filename, "_", 2)[1], ".sql")
		if _, err := database.Exec(`
INSERT INTO schema_migrations (version, name, applied_at_ms, checksum)
VALUES (?, ?, ?, ?)`, version+1, name, time.Now().UnixMilli(), hex.EncodeToString(digest[:])); err != nil {
			t.Fatalf("record %s: %v", filename, err)
		}
	}
}

// The search text is what this command is about, so it reads as an argument
// the way every other single-subject command does. Before it did not, and the
// error for "moment search query 荔枝" was "unknown command 荔枝" -- which
// points at the wrong thing entirely.
func TestSearchQueryTakesItsTextAsAnArgument(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	if created := execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "旧城雨 searchable", "--json"); created.exit != ExitSuccess {
		t.Fatalf("create = %#v", created)
	}

	positional := execute(application, "", "search", "query", "旧城", "--data-dir", fixture.dataDir, "--json")
	flagged := execute(application, "", "search", "query", "--query", "旧城", "--data-dir", fixture.dataDir, "--json")
	if positional.exit != ExitSuccess || flagged.exit != ExitSuccess {
		t.Fatalf("positional = %#v\nflagged = %#v", positional, flagged)
	}
	// Everything but the per-invocation metadata: the two spellings are one
	// request said two ways.
	positionalData := decodeOneDocument(t, positional.stdout)["data"]
	flaggedData := decodeOneDocument(t, flagged.stdout)["data"]
	if !reflect.DeepEqual(positionalData, flaggedData) {
		t.Fatalf("the two spellings must answer identically:\n%v\n%v", positionalData, flaggedData)
	}

	// Saying it twice is a mistake worth pointing at rather than resolving.
	both := execute(application, "", "search", "query", "旧城", "--query", "别的", "--data-dir", fixture.dataDir, "--json")
	if both.exit == ExitSuccess {
		t.Fatalf("naming the text twice must fail: %#v", both)
	}
	none := execute(application, "", "search", "query", "--data-dir", fixture.dataDir, "--json")
	if none.exit == ExitSuccess {
		t.Fatalf("no text at all must fail: %#v", none)
	}
}
