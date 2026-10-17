package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	ginhttp "github.com/moment/moment/internal/transport/http/gin"
	"github.com/moment/moment/internal/transport/postdto"
)

type postCLIFixture struct {
	runtime  *runtimefs.Runtime
	database *sqlitestore.Database
	posts    *app.PostService
	search   *app.SearchService
	site     *app.SiteService
	dataDir  string
	token    string
	server   *httptest.Server
}

func newPostCLIFixture(t *testing.T) postCLIFixture {
	t.Helper()
	dataDir := t.TempDir()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: dataDir, Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	credential, err := initializeTestAccountCredential(context.Background(), runtimeState, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(runtimeState.Root, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	mediaStore, err := media.NewStore(runtimeState, database, media.Options{})
	if err != nil {
		t.Fatal(err)
	}
	postRepository, err := sqlitestore.NewPostRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	posts, err := app.NewPostService(postRepository)
	if err != nil {
		t.Fatal(err)
	}
	siteRepository, err := sqlitestore.NewSiteRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	site, err := app.NewSiteService(siteRepository, app.SiteOptions{AllowDevelopmentLocalHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	timezone := "Asia/Shanghai"
	if _, err := site.Update(context.Background(), app.SiteUpdate{Timezone: &timezone}); err != nil {
		t.Fatal(err)
	}
	searchRepository, err := sqlitestore.NewSearchRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	search, err := app.NewSearchService(searchRepository)
	if err != nil {
		t.Fatal(err)
	}
	engine, err := ginhttp.New(ginhttp.Config{
		Assets:        fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Moment")}},
		SessionSigner: manager, Accounts: testAccountService(t, database, runtimeState), SiteSettings: site, Posts: posts, Search: search, Media: mediaStore,
		Readiness: func(ctx context.Context) error { return database.Ready(ctx) },
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	t.Cleanup(func() {
		server.Close()
		_ = database.Close()
	})
	return postCLIFixture{runtime: runtimeState, database: database, posts: posts, search: search, site: site, dataDir: dataDir, token: credential.Token, server: server}
}

func TestPostDirectAgentContractIdempotencyAndDeleteSafety(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()

	createdOutput := execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "first **moment**", "--idempotency-key", "cli-post-001", "--json")
	created := commandData(t, createdOutput)
	id := created["id"].(string)
	// kind is on the wire now that a post can be a moment or an article.
	if createdOutput.exit != ExitSuccess || createdOutput.stderr != "" || created["kind"] != "moment" || created["content_html"] == "" {
		t.Fatalf("create = %#v", createdOutput)
	}
	createdEnvelope := decodeOneDocument(t, createdOutput.stdout)
	if createdEnvelope["meta"].(map[string]any)["replayed"] != false {
		t.Fatalf("create meta = %#v", createdEnvelope["meta"])
	}
	quotedOutput := execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--quote", id, "--content", "my take", "--idempotency-key", "cli-post-quote-001", "--json")
	quoted := commandData(t, quotedOutput)
	quotedPost, ok := quoted["quoted_post"].(map[string]any)
	if quotedOutput.exit != ExitSuccess || !ok || quoted["content_md"] != "my take" || quotedPost["id"] != id || quotedPost["content_md"] != "first **moment**" {
		t.Fatalf("quoted create = %#v data=%#v", quotedOutput, quoted)
	}

	replayOutput := execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "first **moment**", "--idempotency-key", "cli-post-001", "--json")
	if replay := commandData(t, replayOutput); !reflect.DeepEqual(created, replay) || decodeOneDocument(t, replayOutput.stdout)["meta"].(map[string]any)["replayed"] != true {
		t.Fatalf("replay data = %#v, output = %#v", replay, replayOutput)
	}
	conflict := execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "different", "--idempotency-key", "cli-post-001", "--json")
	if conflict.exit != ExitConflict || decodeOneDocument(t, conflict.stdout)["error"].(map[string]any)["code"] != "IDEMPOTENCY_KEY_REUSED" || conflict.stderr != "" {
		t.Fatalf("conflict = %#v", conflict)
	}

	updated := commandData(t, execute(application, "", "post", "update", id, "--data-dir", fixture.dataDir, "--content", "edited", "--json"))
	if updated["content_md"] != "edited" || updated["published_at"] != created["published_at"] {
		t.Fatalf("updated = %#v", updated)
	}
	dryRunOutput := execute(application, "", "post", "delete", id, "--data-dir", fixture.dataDir, "--dry-run", "--json")
	if commandData(t, dryRunOutput)["content_md"] != "edited" || decodeOneDocument(t, dryRunOutput.stdout)["meta"].(map[string]any)["dry_run"] != true {
		t.Fatalf("dry-run = %#v", dryRunOutput)
	}
	if current := commandData(t, execute(application, "", "post", "get", id, "--data-dir", fixture.dataDir, "--json")); current["content_md"] != "edited" {
		t.Fatalf("dry-run mutated = %#v", current)
	}

	refused := execute(application, "", "post", "delete", id, "--data-dir", fixture.dataDir, "--no-input", "--json")
	if refused.exit != ExitInvalidInput || decodeOneDocument(t, refused.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("non-interactive refusal = %#v", refused)
	}
	deleted := execute(application, "", "post", "delete", id, "--data-dir", fixture.dataDir, "--yes", "--json")
	if deleted.exit != ExitSuccess || commandData(t, deleted)["content_md"] != "edited" || decodeOneDocument(t, deleted.stdout)["meta"].(map[string]any)["deleted"] != true {
		t.Fatalf("deleted = %#v", deleted)
	}
	missing := execute(application, "", "post", "get", id, "--data-dir", fixture.dataDir, "--json")
	if missing.exit != ExitNotFound || decodeOneDocument(t, missing.stdout)["error"].(map[string]any)["code"] != "POST_NOT_FOUND" {
		t.Fatalf("missing = %#v", missing)
	}
	replayedAfterDelete := commandData(t, execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "first **moment**", "--idempotency-key", "cli-post-001", "--json"))
	if !reflect.DeepEqual(created, replayedAfterDelete) {
		t.Fatalf("replay after delete = %#v, want %#v", replayedAfterDelete, created)
	}
}

func TestPostDirectAPIParityAndPublicReadsDoNotRequireToken(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", fixture.token)

	directCreated := commandData(t, execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "parity", "--json"))
	id := directCreated["id"].(string)
	apiGet := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "get", id, "--json"))
	if !reflect.DeepEqual(directCreated, apiGet) {
		t.Fatalf("direct create = %#v, API get = %#v", directCreated, apiGet)
	}

	apiUpdated := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "update", id, "--content", "updated through API", "--json"))
	directGet := commandData(t, execute(application, "", "post", "get", id, "--data-dir", fixture.dataDir, "--json"))
	if !reflect.DeepEqual(apiUpdated, directGet) {
		t.Fatalf("API update = %#v, direct get = %#v", apiUpdated, directGet)
	}
	pinned := commandData(t, execute(application, "", "post", "pin", id, "--data-dir", fixture.dataDir, "--yes", "--json"))
	if pinned["pinned_post_id"] != id {
		t.Fatalf("direct pin = %#v", pinned)
	}
	apiSite := commandData(t, execute(application, "", "--url", fixture.server.URL, "site", "get", "--json"))
	if !reflect.DeepEqual(pinned, apiSite) {
		t.Fatalf("direct pin = %#v, API site = %#v", pinned, apiSite)
	}
	unpinned := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "unpin", id, "--yes", "--json"))
	if unpinned["pinned_post_id"] != nil {
		t.Fatalf("API unpin = %#v", unpinned)
	}
	directSite := commandData(t, execute(application, "", "site", "get", "--data-dir", fixture.dataDir, "--json"))
	if !reflect.DeepEqual(unpinned, directSite) {
		t.Fatalf("API unpin = %#v, direct site = %#v", unpinned, directSite)
	}
	directList := commandData(t, execute(application, "", "post", "list", "--data-dir", fixture.dataDir, "--limit", "1", "--json"))
	apiList := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "list", "--limit", "1", "--json"))
	if !reflect.DeepEqual(directList, apiList) {
		t.Fatalf("direct list = %#v, API list = %#v", directList, apiList)
	}
	apiCreatedOutput := execute(application, "", "--url", fixture.server.URL, "post", "create", "--content", "API retry", "--idempotency-key", "api-post-001", "--json")
	apiCreated := commandData(t, apiCreatedOutput)
	apiReplayOutput := execute(application, "", "--url", fixture.server.URL, "post", "create", "--content", "API retry", "--idempotency-key", "api-post-001", "--json")
	if !reflect.DeepEqual(apiCreated, commandData(t, apiReplayOutput)) || decodeOneDocument(t, apiReplayOutput.stdout)["meta"].(map[string]any)["replayed"] != true {
		t.Fatalf("API replay create=%#v replay=%#v", apiCreatedOutput, apiReplayOutput)
	}
	apiConflict := execute(application, "", "--url", fixture.server.URL, "post", "create", "--content", "API changed", "--idempotency-key", "api-post-001", "--json")
	if apiConflict.exit != ExitConflict || decodeOneDocument(t, apiConflict.stdout)["error"].(map[string]any)["code"] != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("API idempotency conflict = %#v", apiConflict)
	}

	// Reads never need a token: none at all, or one the server does not
	// honour, both read the public site.
	for _, token := range []string{"", "wrong-token-that-must-not-be-accepted"} {
		t.Setenv("MOMENT_TOKEN", token)
		publicGet := execute(application, "", "--url", fixture.server.URL, "post", "get", id, "--json")
		publicList := execute(application, "", "--url", fixture.server.URL, "post", "list", "--json")
		if publicGet.exit != ExitSuccess || publicList.exit != ExitSuccess || publicGet.stderr != "" || publicList.stderr != "" {
			t.Fatalf("public reads with token %q: get=%#v list=%#v", token, publicGet, publicList)
		}
	}
	unauthorized := execute(application, "", "--url", fixture.server.URL, "post", "update", id, "--content", "blocked", "--json")
	if unauthorized.exit != ExitUnauthorized || decodeOneDocument(t, unauthorized.stdout)["error"].(map[string]any)["code"] != "UNAUTHORIZED" {
		t.Fatalf("unauthorized = %#v", unauthorized)
	}

	t.Setenv("MOMENT_TOKEN", fixture.token)
	dryRun := execute(application, "", "--url", fixture.server.URL, "post", "delete", id, "--dry-run", "--json")
	if dryRun.exit != ExitSuccess || commandData(t, dryRun)["content_md"] != "updated through API" {
		t.Fatalf("API dry-run = %#v", dryRun)
	}
	deleted := execute(application, "", "--url", fixture.server.URL, "post", "delete", id, "--yes", "--json")
	if deleted.exit != ExitSuccess || commandData(t, deleted)["id"] != id {
		t.Fatalf("API delete = %#v", deleted)
	}
}

func TestPostLocationCLIDirectAPIParityAndStrictInputs(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", fixture.token)
	location := `{"name":"静安寺","display_name":"上海市静安区南京西路1686号","country_code":"CN","country":"中国","region":"上海市","city":"上海市","district":"静安区","category":"风景名胜","provider":"amap","provider_place_id":"B0FFF","latitude":31.223186,"longitude":121.44526,"coordinate_system":"gcj02"}`
	manifest := filepath.Join(t.TempDir(), "location.json")
	if err := os.WriteFile(manifest, []byte(location), 0o600); err != nil {
		t.Fatal(err)
	}

	created := commandData(t, execute(application, "", "post", "create", "--data-dir", fixture.dataDir,
		"--content", "带位置", "--location-json", manifest, "--json"))
	id := created["id"].(string)
	if got := created["location"].(map[string]any); got["name"] != "静安寺" || len(got) != 2 {
		t.Fatalf("public location = %#v", got)
	}
	apiGet := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "get", id, "--json"))
	if !reflect.DeepEqual(created, apiGet) {
		t.Fatalf("direct/API location drift: direct=%#v api=%#v", created, apiGet)
	}
	manual := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "update", id,
		"--location-name", "书房", "--json"))
	if manual["location"].(map[string]any)["name"] != "书房" {
		t.Fatalf("manual location = %#v", manual)
	}
	cleared := commandData(t, execute(application, "", "post", "update", id, "--data-dir", fixture.dataDir,
		"--clear-location", "--json"))
	if cleared["location"] != nil {
		t.Fatalf("location not cleared: %#v", cleared)
	}

	for _, output := range []commandOutput{
		execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--location-name", "书房", "--location-json", manifest, "--json"),
		execute(application, location, "post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--location-json", "-", "--media-manifest", "-", "--json"),
		execute(application, "", "post", "update", id, "--data-dir", fixture.dataDir, "--clear-location", "--location-name", "书房", "--json"),
	} {
		if output.exit != ExitInvalidInput || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
			t.Fatalf("invalid location flags = %#v", output)
		}
	}
}

func TestPostMediaCLISingleManifestAndTransportParity(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	imagePath := writeCLIImage(t)
	t.Setenv("MOMENT_TOKEN", fixture.token)

	createdOutput := execute(application, "", "post", "create", "--data-dir", fixture.dataDir,
		"--content", "single image", "--media", imagePath, "--json")
	if createdOutput.exit != ExitSuccess || createdOutput.stderr != "" {
		t.Fatalf("single create = %#v", createdOutput)
	}
	created := commandData(t, createdOutput)
	mediaItems := assertCLIPostMedia(t, created, []any{nil})
	mediaID := mediaItems[0]["media_id"].(string)
	id := created["id"].(string)

	manifest := writePostManifest(t, `[
  {"path":`+jsonString(imagePath)+`,"alt_text":"same bytes, provided"},
  {"media_id":"`+mediaID+`","alt_text":""},
  {"media_id":"`+mediaID+`","alt_text":null}
]`)
	directUpdate := execute(application, "", "post", "update", id, "--data-dir", fixture.dataDir,
		"--media-manifest", manifest, "--json")
	if directUpdate.exit != ExitSuccess || commandData(t, directUpdate)["content_md"] != "single image" {
		t.Fatalf("direct manifest update = %#v", directUpdate)
	}
	assertCLIPostMedia(t, commandData(t, directUpdate), []any{"same bytes, provided", "", nil})
	apiGet := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "get", id, "--json"))
	if !reflect.DeepEqual(commandData(t, directUpdate), apiGet) {
		t.Fatalf("direct update/API get drift: direct=%#v api=%#v", commandData(t, directUpdate), apiGet)
	}

	apiDecorative := execute(application, "", "--url", fixture.server.URL, "post", "update", id,
		"--media", imagePath, "--media-decorative", "--json")
	if apiDecorative.exit != ExitSuccess {
		t.Fatalf("API decorative update = %#v", apiDecorative)
	}
	assertCLIPostMedia(t, commandData(t, apiDecorative), []any{""})
	directGet := commandData(t, execute(application, "", "post", "get", id, "--data-dir", fixture.dataDir, "--json"))
	if !reflect.DeepEqual(commandData(t, apiDecorative), directGet) {
		t.Fatalf("API update/direct get drift: api=%#v direct=%#v", commandData(t, apiDecorative), directGet)
	}

	cleared := execute(application, "[]", "post", "update", id, "--data-dir", fixture.dataDir,
		"--media-manifest", "-", "--json")
	if cleared.exit != ExitSuccess {
		t.Fatalf("stdin clear = %#v", cleared)
	}
	if values := assertCLIPostMedia(t, commandData(t, cleared), nil); len(values) != 0 {
		t.Fatalf("cleared media = %#v", values)
	}
	apiAfterClear := commandData(t, execute(application, "", "--url", fixture.server.URL, "post", "get", id, "--json"))
	if !reflect.DeepEqual(commandData(t, cleared), apiAfterClear) {
		t.Fatalf("stdin clear parity drift: direct=%#v api=%#v", commandData(t, cleared), apiAfterClear)
	}
}

func TestPostMediaCLIAPIPathReplayAndFailureLeavesOnlyGCCandidate(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	imagePath := writeCLIImage(t)
	t.Setenv("MOMENT_TOKEN", fixture.token)

	args := []string{"--url", fixture.server.URL, "post", "create", "--media", imagePath, "--idempotency-key", "api-media-replay", "--json"}
	createdOutput := execute(application, "", args...)
	created := commandData(t, createdOutput)
	if createdOutput.exit != ExitSuccess {
		t.Fatalf("API media create = %#v", createdOutput)
	}
	replayOutput := execute(application, "", args...)
	if replayOutput.exit != ExitSuccess || !reflect.DeepEqual(created, commandData(t, replayOutput)) || decodeOneDocument(t, replayOutput.stdout)["meta"].(map[string]any)["replayed"] != true {
		t.Fatalf("API media replay = %#v", replayOutput)
	}
	if postCLITableCount(t, fixture.database, "media") != 1 || postCLITableCount(t, fixture.database, "entry_media") != 1 {
		t.Fatal("content-addressed replay duplicated media or usages")
	}
	id := created["id"].(string)
	if output := execute(application, "[]", "--url", fixture.server.URL, "post", "update", id, "--content", "kept before delete", "--media-manifest", "-", "--json"); output.exit != ExitSuccess {
		t.Fatalf("API clear before delete = %#v", output)
	}
	if output := execute(application, "", "--url", fixture.server.URL, "post", "delete", id, "--yes", "--json"); output.exit != ExitSuccess {
		t.Fatalf("API delete = %#v", output)
	}
	replayAfterDelete := execute(application, "", args...)
	if replayAfterDelete.exit != ExitSuccess || !reflect.DeepEqual(created, commandData(t, replayAfterDelete)) {
		t.Fatalf("API replay after delete = %#v", replayAfterDelete)
	}

	secondFixture := newPostCLIFixture(t)
	t.Setenv("MOMENT_TOKEN", secondFixture.token)
	postFailed := execute(application, "", "--url", secondFixture.server.URL, "post", "create",
		"--content", "![remote](https://example.com/image.png)", "--media", imagePath, "--json")
	if postFailed.exit != ExitConflict || postCLITableCount(t, secondFixture.database, "entries") != 0 ||
		postCLITableCount(t, secondFixture.database, "entry_media") != 0 || postCLITableCount(t, secondFixture.database, "media") != 1 {
		t.Fatalf("post failure after upload = %#v", postFailed)
	}
	var postFailureClock *int64
	if err := secondFixture.database.ReadTx(context.Background(), func(queries sqlitestore.Queries) error {
		return queries.QueryRowContext(context.Background(), `SELECT unreferenced_since_ms FROM media`).Scan(&postFailureClock)
	}); err != nil || postFailureClock == nil {
		t.Fatalf("post failure upload was not a GC candidate: %v, %v", postFailureClock, err)
	}

	badImage := filepath.Join(t.TempDir(), "not-image.txt")
	if err := os.WriteFile(badImage, []byte("not an image"), 0o600); err != nil {
		t.Fatal(err)
	}
	manifest := writePostManifest(t, `[
  {"path":`+jsonString(imagePath)+`,"alt_text":null},
  {"path":`+jsonString(badImage)+`,"alt_text":"invalid second upload"}
]`)
	failed := execute(application, "", "--url", secondFixture.server.URL, "post", "create",
		"--content", "must not partially attach", "--media-manifest", manifest, "--json")
	if failed.exit == ExitSuccess {
		t.Fatalf("failed upload unexpectedly succeeded: %#v", failed)
	}
	if postCLITableCount(t, secondFixture.database, "entries") != 0 || postCLITableCount(t, secondFixture.database, "entry_media") != 0 || postCLITableCount(t, secondFixture.database, "media") != 1 {
		t.Fatal("upload failure left a partial post/usage or lost the successful GC candidate")
	}
	var unreferenced *int64
	if err := secondFixture.database.ReadTx(context.Background(), func(queries sqlitestore.Queries) error {
		return queries.QueryRowContext(context.Background(), `SELECT unreferenced_since_ms FROM media`).Scan(&unreferenced)
	}); err != nil || unreferenced == nil {
		t.Fatalf("successful pre-post upload was not an unreferenced GC candidate: %v, %v", unreferenced, err)
	}
}

func TestPostMediaCLIStrictManifestFlagsAndPathSafety(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	imagePath := writeCLIImage(t)
	mediaID := commandData(t, execute(application, "", "media", "add", imagePath, "--data-dir", fixture.dataDir, "--json"))["media"].(map[string]any)["id"].(string)
	overLimit := make([]string, 0, 10)
	for range 10 {
		overLimit = append(overLimit, `{"media_id":"`+mediaID+`","alt_text":null}`)
	}
	tests := []struct {
		name  string
		stdin string
		args  []string
	}{
		{name: "empty convenience alt", args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media", imagePath, "--media-alt", "", "--json"}},
		{name: "both alt modes", args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media", imagePath, "--media-alt", "x", "--media-decorative", "--json"}},
		{name: "repeated convenience path", args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media", imagePath, "--media", imagePath, "--json"}},
		{name: "manifest and convenience", stdin: "[]", args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media", imagePath, "--media-manifest", "-", "--json"}},
		{name: "repeated manifest", stdin: "[]", args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", "-", "--media-manifest", "-", "--json"}},
		{name: "manifest EOF", stdin: "", args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", "-", "--json"}},
		{name: "manifest malformed", stdin: "[", args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", "-", "--json"}},
		{name: "manifest null", stdin: "null", args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", "-", "--json"}},
		{name: "manifest trailing", stdin: "[] {}", args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", "-", "--json"}},
		{name: "missing alt", stdin: `[{"media_id":"` + mediaID + `"}]`, args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", "-", "--json"}},
		{name: "both source fields", stdin: `[{"path":` + jsonString(imagePath) + `,"media_id":"` + mediaID + `","alt_text":null}]`, args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", "-", "--json"}},
		{name: "unknown field", stdin: `[{"media_id":"` + mediaID + `","alt_text":null,"decorative":true}]`, args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", "-", "--json"}},
		{name: "duplicate field", stdin: `[{"media_id":"` + mediaID + `","media_id":"` + mediaID + `","alt_text":null}]`, args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", "-", "--json"}},
		{name: "blank provided alt", stdin: `[{"media_id":"` + mediaID + `","alt_text":"   "}]`, args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", "-", "--json"}},
		{name: "over limit", stdin: `[` + strings.Join(overLimit, ",") + `]`, args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", "-", "--json"}},
		{name: "over byte limit", stdin: strings.Repeat(" ", maxPostMediaManifestBytes+1), args: []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", "-", "--json"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			output := execute(application, test.stdin, test.args...)
			if output.exit != ExitInvalidInput || output.stderr != "" || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
				t.Fatalf("output = %#v", output)
			}
		})
	}

	manifestDirectory := t.TempDir()
	if output := execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", manifestDirectory, "--json"); output.exit != ExitInvalidInput {
		t.Fatalf("manifest directory = %#v", output)
	}
	manifestTarget := writePostManifest(t, "[]")
	manifestLink := filepath.Join(t.TempDir(), "linked-manifest.json")
	if err := os.Symlink(manifestTarget, manifestLink); err != nil {
		t.Fatal(err)
	}
	if output := execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", manifestLink, "--json"); output.exit != ExitInvalidInput {
		t.Fatalf("manifest symlink = %#v", output)
	}
	mediaLink := filepath.Join(t.TempDir(), "linked.png")
	if err := os.Symlink(imagePath, mediaLink); err != nil {
		t.Fatal(err)
	}
	if output := execute(application, "", "post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media", mediaLink, "--json"); output.exit != ExitInvalidInput {
		t.Fatalf("media symlink = %#v", output)
	}
	if postCLITableCount(t, fixture.database, "entries") != 0 || postCLITableCount(t, fixture.database, "entry_media") != 0 {
		t.Fatal("invalid flags or manifests mutated posts")
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	exit := application.Execute(cancelled, []string{"post", "create", "--data-dir", fixture.dataDir, "--content", "x", "--media-manifest", "-", "--json"}, Streams{
		In: strings.NewReader(`[]`), Out: &stdout, Err: &stderr,
	})
	if exit != ExitTimeout || stderr.String() != "" || decodeOneDocument(t, stdout.String())["error"].(map[string]any)["code"] != "CANCELLED" ||
		postCLITableCount(t, fixture.database, "entries") != 0 || postCLITableCount(t, fixture.database, "entry_media") != 0 {
		t.Fatalf("cancelled manifest exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
}

func TestPostListDateCursorErrorsCancellationAndHelp(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	repository, err := sqlitestore.NewPostRepository(fixture.database, fixture.runtime.Locks)
	if err != nil {
		t.Fatal(err)
	}
	instants := []time.Time{
		time.Date(2026, time.August, 9, 15, 59, 59, 0, time.UTC),
		time.Date(2026, time.August, 9, 16, 0, 0, 0, time.UTC),
		time.Date(2026, time.August, 10, 2, 0, 0, 0, time.UTC),
	}
	ids := []domain.ID{"01ARZ3NDEKTSV4RRFFQ69G5FAV", "01ARZ3NDEKTSV4RRFFQ69G5FAW", "01ARZ3NDEKTSV4RRFFQ69G5FAX"}
	index := 0
	seed, err := app.NewPostService(repository,
		app.WithPostClock(func() time.Time { return instants[index] }),
		app.WithPostIDGenerator(func(time.Time) (domain.ID, error) { id := ids[index]; index++; return id, nil }),
	)
	if err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{"before", "inside one", "inside two"} {
		if _, err := seed.Create(context.Background(), app.CreatePostInput{ContentMD: content}); err != nil {
			t.Fatal(err)
		}
	}

	firstOutput := execute(application, "", "post", "list", "--data-dir", fixture.dataDir, "--from", "2026-08-10", "--to", "2026-08-11", "--limit", "1", "--json")
	first := commandData(t, firstOutput)
	items := first["items"].([]any)
	firstMeta := decodeOneDocument(t, firstOutput.stdout)["meta"].(map[string]any)
	if len(items) != 1 || items[0].(map[string]any)["content_md"] != "inside two" || firstMeta["next_cursor"] == nil {
		t.Fatalf("first = %#v, meta = %#v", first, firstMeta)
	}
	cursor := firstMeta["next_cursor"].(string)
	continued := commandData(t, execute(application, "", "post", "list", "--data-dir", fixture.dataDir, "--from", "2026-08-10", "--to", "2026-08-11", "--limit", "1", "--cursor", cursor, "--json"))
	if continued["items"].([]any)[0].(map[string]any)["content_md"] != "inside one" {
		t.Fatalf("continued = %#v", continued)
	}

	// An anchored day reads back from its end and prev_cursor walks forward.
	anchoredOutput := execute(application, "", "post", "list", "--data-dir", fixture.dataDir, "--anchor", "2026-08-09", "--limit", "5", "--json")
	anchored := commandData(t, anchoredOutput)
	anchoredMeta := decodeOneDocument(t, anchoredOutput.stdout)["meta"].(map[string]any)
	if anchoredItems := anchored["items"].([]any); len(anchoredItems) != 1 || anchoredItems[0].(map[string]any)["content_md"] != "before" || anchoredMeta["next_cursor"] != nil || anchoredMeta["prev_cursor"] == nil {
		t.Fatalf("anchored = %#v, meta = %#v", anchored, anchoredMeta)
	}
	newerOutput := execute(application, "", "post", "list", "--data-dir", fixture.dataDir, "--limit", "1", "--cursor", anchoredMeta["prev_cursor"].(string), "--json")
	newer := commandData(t, newerOutput)
	newerMeta := decodeOneDocument(t, newerOutput.stdout)["meta"].(map[string]any)
	if newer["items"].([]any)[0].(map[string]any)["content_md"] != "inside one" || newerMeta["prev_cursor"] == nil {
		t.Fatalf("newer = %#v, meta = %#v", newer, newerMeta)
	}

	for _, args := range [][]string{
		{"post", "list", "--data-dir", fixture.dataDir, "--cursor", cursor, "--json"},
		{"post", "list", "--data-dir", fixture.dataDir, "--cursor", "invalid", "--json"},
		{"post", "list", "--data-dir", fixture.dataDir, "--limit", "101", "--json"},
		{"post", "list", "--data-dir", fixture.dataDir, "--from", "2026-08-11", "--to", "2026-08-10", "--json"},
		{"post", "list", "--data-dir", fixture.dataDir, "--anchor", "2026-08-09", "--from", "2026-08-01", "--json"},
		{"post", "list", "--data-dir", fixture.dataDir, "--anchor", "2026-08-09", "--cursor", cursor, "--json"},
		{"post", "list", "--data-dir", fixture.dataDir, "--anchor", "08-09-2026", "--json"},
		{"post", "list", "--data-dir", fixture.dataDir, "--anchor", "", "--json"},
	} {
		output := execute(application, "", args...)
		if output.exit != ExitInvalidInput || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
			t.Fatalf("args %v = %#v", args, output)
		}
	}

	repostOutput := execute(application, "", "post", "repost", ids[1].String(), "--data-dir", fixture.dataDir, "--json")
	if repostOutput.exit != ExitSuccess || commandData(t, repostOutput)["timeline_item_kind"] != "original" {
		t.Fatalf("repost = %#v", repostOutput)
	}
	linked := commandData(t, execute(application, "", "post", "list", "--data-dir", fixture.dataDir, "--from", "2026-08-10", "--to", "2026-08-11", "--json"))["items"].([]any)
	linkedKinds := map[string]bool{}
	for _, raw := range linked {
		item := raw.(map[string]any)
		if item["id"] == ids[1].String() {
			linkedKinds[item["timeline_item_kind"].(string)] = true
		}
	}
	if len(linkedKinds) != 2 || !linkedKinds["original"] || !linkedKinds["repost"] {
		t.Fatalf("linked CLI timeline = %#v", linked)
	}
	unrepostOutput := execute(application, "", "post", "unrepost", ids[1].String(), "--data-dir", fixture.dataDir, "--json")
	if unrepostOutput.exit != ExitSuccess || commandData(t, unrepostOutput)["reposted_at"] != nil {
		t.Fatalf("unrepost = %#v", unrepostOutput)
	}

	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	exit := application.Execute(cancelledContext, []string{"post", "list", "--data-dir", fixture.dataDir, "--json"}, Streams{Out: &stdout, Err: &stderr})
	if exit != ExitTimeout || decodeOneDocument(t, stdout.String())["error"].(map[string]any)["code"] != "CANCELLED" || stderr.String() != "" {
		t.Fatalf("cancelled exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}

	for _, verb := range []string{"list", "get", "create", "update", "delete"} {
		output := execute(application, "", "post", verb, "--help")
		if output.exit != ExitSuccess || output.stderr != "" {
			t.Fatalf("%s help = %#v", verb, output)
		}
		for _, required := range []string{"INPUTS", "OUTPUT", "ERRORS", "Examples:", "--json"} {
			if !strings.Contains(output.stdout, required) {
				t.Fatalf("%s help missing %q:\n%s", verb, required, output.stdout)
			}
		}
		if (verb == "create" || verb == "update") && (!strings.Contains(output.stdout, "--media-manifest") || !strings.Contains(output.stdout, "--media-decorative")) {
			t.Fatalf("%s help omitted ordered media inputs:\n%s", verb, output.stdout)
		}
	}
}

func TestDirectPostWriteHoldsAuthoritativeMaintenanceLease(t *testing.T) {
	fixture := newPostCLIFixture(t)
	invocation := Invocation{target: directTarget{dataDir: fixture.dataDir}}
	direct, err := openDirectPostWrite(context.Background(), invocation)
	if err != nil {
		t.Fatal(err)
	}
	if !direct.maintenance.Holds(runtimefs.MaintenanceLock, runtimefs.Shared) {
		t.Fatal("direct post write did not retain maintenance shared")
	}
	if direct.media == nil {
		t.Fatal("direct post write did not bind media ingestion to the retained maintenance lease")
	}
	blockedContext, cancel := context.WithTimeout(context.Background(), 25*time.Millisecond)
	defer cancel()
	if _, err := fixture.runtime.Locks.Acquire(blockedContext, runtimefs.MaintenanceLock, runtimefs.Exclusive); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("exclusive maintenance was not blocked: %v", err)
	}
	if err := direct.Close(); err != nil {
		t.Fatal(err)
	}
	lease, err := fixture.runtime.Locks.Acquire(context.Background(), runtimefs.MaintenanceLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
}

func TestPostAPIRejectsNonCanonicalSuccessEnvelopes(t *testing.T) {
	validData := `{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","content_md":"valid","content_html":"<p>valid</p>\n","created_at":"2026-08-10T12:00:00Z","updated_at":"2026-08-10T12:00:00Z","published_at":"2026-08-10T12:00:00Z","comment_count":0,"media":[]}`
	responses := map[string]string{
		"null data":            `{"data":null,"meta":{"request_id":"req_fake"}}`,
		"zero post":            `{"data":{"id":"","content_md":"","content_html":"","created_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z","published_at":"0001-01-01T00:00:00Z"},"meta":{"request_id":"req_fake"}}`,
		"null items":           `{"data":{"items":null},"meta":{"request_id":"req_fake","next_cursor":null}}`,
		"missing cursor":       `{"data":{"items":[]},"meta":{"request_id":"req_fake"}}`,
		"empty next cursor":    `{"data":{"items":[]},"meta":{"request_id":"req_fake","next_cursor":""}}`,
		"missing meta":         `{"data":` + validData + `}`,
		"empty meta":           `{"data":` + validData + `,"meta":{}}`,
		"blank request id":     `{"data":` + validData + `,"meta":{"request_id":"  "}}`,
		"wrong request id":     `{"data":` + validData + `,"meta":{"request_id":7}}`,
		"unknown top field":    `{"data":` + validData + `,"meta":{"request_id":"req_fake"},"extra":true}`,
		"duplicate data":       `{"data":` + validData + `,"data":` + validData + `,"meta":{"request_id":"req_fake"}}`,
		"trailing document":    `{"data":` + validData + `,"meta":{"request_id":"req_fake"}} {}`,
		"null envelope meta":   `{"data":` + validData + `,"meta":null}`,
		"scalar envelope meta": `{"data":` + validData + `,"meta":true}`,
		"null post media":      `{"data":` + strings.Replace(validData, `"media":[]`, `"media":null`, 1) + `,"meta":{"request_id":"req_fake"}}`,
		"usage missing alt":    `{"data":` + strings.Replace(validData, `"media":[]`, `"media":[{"media_id":"01ARZ3NDEKTSV4RRFFQ69G5FAW","position":0}]`, 1) + `,"meta":{"request_id":"req_fake"}}`,
		"usage missing width":  `{"data":` + strings.Replace(validData, `"media":[]`, `"media":[{"media_id":"01ARZ3NDEKTSV4RRFFQ69G5FAW","position":0,"alt_text":null,"height":600}]`, 1) + `,"meta":{"request_id":"req_fake"}}`,
		"usage zero height":    `{"data":` + strings.Replace(validData, `"media":[]`, `"media":[{"media_id":"01ARZ3NDEKTSV4RRFFQ69G5FAW","position":0,"alt_text":null,"width":800,"height":0}]`, 1) + `,"meta":{"request_id":"req_fake"}}`,
	}
	for name, body := range responses {
		t.Run(name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				_, _ = writer.Write([]byte(body))
			}))
			defer server.Close()
			application := momentTestApplication()
			args := []string{"--url", server.URL, "post", "get", "01ARZ3NDEKTSV4RRFFQ69G5FAV", "--json"}
			if strings.Contains(name, "items") || strings.Contains(name, "cursor") {
				args = []string{"--url", server.URL, "post", "list", "--json"}
			}
			output := execute(application, "", args...)
			if output.exit != ExitUnavailable || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" {
				t.Fatalf("output = %#v", output)
			}
		})
	}
	t.Run("non JSON content type", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
			writer.Header().Set("Content-Type", "text/plain")
			_, _ = writer.Write([]byte(`{"data":` + validData + `,"meta":{"request_id":"req_fake"}}`))
		}))
		defer server.Close()
		output := execute(momentTestApplication(), "", "--url", server.URL, "post", "get", "01ARZ3NDEKTSV4RRFFQ69G5FAV", "--json")
		if output.exit != ExitUnavailable || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" {
			t.Fatalf("output = %#v", output)
		}
	})
}

func TestPostAPIRequiresMutationMetadataAndStatusPairing(t *testing.T) {
	instant := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	canonical := postdto.Post{
		ID:          "01ARZ3NDEKTSV4RRFFQ69G5FAV",
		Kind:        string(domain.KindMoment),
		ContentMD:   domain.MarkdownBody("valid"),
		ContentHTML: "<p>valid</p>\n",
		CreatedAt:   instant,
		UpdatedAt:   instant,
		PublishedAt: instant,
		Media:       []postdto.MediaProjection{},
	}
	tests := []struct {
		name   string
		status int
		meta   map[string]any
		args   []string
	}{
		{name: "create missing replayed", status: http.StatusCreated, meta: map[string]any{"request_id": "req"}, args: []string{"post", "create", "--content", "valid"}},
		{name: "create wrong replayed type", status: http.StatusCreated, meta: map[string]any{"request_id": "req", "replayed": "false"}, args: []string{"post", "create", "--content", "valid"}},
		{name: "create status conflicts false", status: http.StatusOK, meta: map[string]any{"request_id": "req", "replayed": false}, args: []string{"post", "create", "--content", "valid"}},
		{name: "create status conflicts true", status: http.StatusCreated, meta: map[string]any{"request_id": "req", "replayed": true}, args: []string{"post", "create", "--content", "valid"}},
		{name: "delete missing deleted", status: http.StatusOK, meta: map[string]any{"request_id": "req"}, args: []string{"post", "delete", canonical.ID, "--yes"}},
		{name: "delete false", status: http.StatusOK, meta: map[string]any{"request_id": "req", "deleted": false}, args: []string{"post", "delete", canonical.ID, "--yes"}},
		{name: "delete wrong type", status: http.StatusOK, meta: map[string]any{"request_id": "req", "deleted": "true"}, args: []string{"post", "delete", canonical.ID, "--yes"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.Header().Set("Content-Type", "application/json")
				writer.WriteHeader(test.status)
				_ = json.NewEncoder(writer).Encode(map[string]any{"data": canonical, "meta": test.meta})
			}))
			defer server.Close()
			t.Setenv("MOMENT_TOKEN", "fixture-token")
			args := append([]string{"--url", server.URL}, test.args...)
			args = append(args, "--json")
			output := execute(momentTestApplication(), "", args...)
			if output.exit != ExitUnavailable || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "DEPENDENCY_INVALID_RESPONSE" {
				t.Fatalf("output = %#v", output)
			}
		})
	}
}

func writePostManifest(t *testing.T, contents string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "post-media.json")
	if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func jsonString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}

func assertCLIPostMedia(t *testing.T, post map[string]any, expectedAlts []any) []map[string]any {
	t.Helper()
	raw, present := post["media"]
	items, ok := raw.([]any)
	if !present || !ok || items == nil || len(items) != len(expectedAlts) {
		t.Fatalf("post media = %#v", raw)
	}
	result := make([]map[string]any, 0, len(items))
	for index, rawItem := range items {
		item, ok := rawItem.(map[string]any)
		width, widthOK := item["width"].(float64)
		height, heightOK := item["height"].(float64)
		if !ok || len(item) != 6 || item["position"] != float64(index) || item["mime_type"] == "" || !reflect.DeepEqual(item["alt_text"], expectedAlts[index]) || !widthOK || width <= 0 || !heightOK || height <= 0 {
			t.Fatalf("media[%d] = %#v", index, rawItem)
		}
		if _, err := domain.ParseID(item["media_id"].(string)); err != nil {
			t.Fatalf("media[%d] ID = %v", index, err)
		}
		for _, field := range []string{"media_id", "position", "alt_text", "width", "height"} {
			if _, present := item[field]; !present {
				t.Fatalf("media[%d] omitted %s", index, field)
			}
		}
		result = append(result, item)
	}
	return result
}

func postCLITableCount(t *testing.T, database *sqlitestore.Database, table string) int {
	t.Helper()
	var count int
	if err := database.ReadTx(context.Background(), func(queries sqlitestore.Queries) error {
		return queries.QueryRowContext(context.Background(), `SELECT count(*) FROM `+table).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	return count
}

func TestPostListRefreshCursorDirectAndAPIParity(t *testing.T) {
	fixture := newPostCLIFixture(t)
	application := momentTestApplication()
	direct := execute(application, "", "post", "list", "--data-dir", fixture.dataDir, "--json")
	envelope := decodeOneDocument(t, direct.stdout)
	cursor, ok := envelope["meta"].(map[string]any)["refresh_cursor"].(string)
	if direct.exit != ExitSuccess || !ok || cursor == "" {
		t.Fatalf("direct: %+v", direct)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/v1/posts" || request.URL.Query().Get("cursor") != cursor {
			t.Errorf("unexpected request: %s", request.URL)
		}
		writer.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(writer).Encode(map[string]any{"data": envelope["data"], "meta": map[string]any{"request_id": "req_refresh", "next_cursor": nil, "prev_cursor": nil, "refresh_cursor": cursor}}); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()
	api := execute(application, "", "post", "list", "--url", server.URL, "--cursor", cursor, "--json")
	actual := decodeOneDocument(t, api.stdout)
	if api.exit != ExitSuccess || !reflect.DeepEqual(actual["data"], envelope["data"]) || actual["meta"].(map[string]any)["refresh_cursor"] != cursor {
		t.Fatalf("API: %+v", api)
	}
}
