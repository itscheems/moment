package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
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
	credential, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{})
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
		Assets:    fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Moment")}},
		OwnerAuth: manager, SiteSettings: site, Posts: posts, Search: search,
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
	if createdOutput.exit != ExitSuccess || createdOutput.stderr != "" || created["kind"] != nil || created["content_html"] == "" {
		t.Fatalf("create = %#v", createdOutput)
	}
	createdEnvelope := decodeOneDocument(t, createdOutput.stdout)
	if createdEnvelope["meta"].(map[string]any)["replayed"] != false {
		t.Fatalf("create meta = %#v", createdEnvelope["meta"])
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

	t.Setenv("MOMENT_TOKEN", "wrong-token-that-must-not-be-read")
	publicGet := execute(application, "", "--url", fixture.server.URL, "post", "get", id, "--json")
	publicList := execute(application, "", "--url", fixture.server.URL, "post", "list", "--json")
	if publicGet.exit != ExitSuccess || publicList.exit != ExitSuccess || publicGet.stderr != "" || publicList.stderr != "" {
		t.Fatalf("public reads used token: get=%#v list=%#v", publicGet, publicList)
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

	for _, args := range [][]string{
		{"post", "list", "--data-dir", fixture.dataDir, "--cursor", cursor, "--json"},
		{"post", "list", "--data-dir", fixture.dataDir, "--cursor", "invalid", "--json"},
		{"post", "list", "--data-dir", fixture.dataDir, "--limit", "101", "--json"},
		{"post", "list", "--data-dir", fixture.dataDir, "--from", "2026-08-11", "--to", "2026-08-10", "--json"},
	} {
		output := execute(application, "", args...)
		if output.exit != ExitInvalidInput || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
			t.Fatalf("args %v = %#v", args, output)
		}
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
	validData := `{"id":"01ARZ3NDEKTSV4RRFFQ69G5FAV","content_md":"valid","content_html":"<p>valid</p>\n","created_at":"2026-08-10T12:00:00Z","updated_at":"2026-08-10T12:00:00Z","published_at":"2026-08-10T12:00:00Z"}`
	responses := map[string]string{
		"null data":            `{"data":null,"meta":{"request_id":"req_fake"}}`,
		"zero post":            `{"data":{"id":"","content_md":"","content_html":"","created_at":"0001-01-01T00:00:00Z","updated_at":"0001-01-01T00:00:00Z","published_at":"0001-01-01T00:00:00Z"},"meta":{}}`,
		"null items":           `{"data":{"items":null},"meta":{"next_cursor":null}}`,
		"missing cursor":       `{"data":{"items":[]},"meta":{}}`,
		"empty next cursor":    `{"data":{"items":[]},"meta":{"next_cursor":""}}`,
		"missing meta":         `{"data":` + validData + `}`,
		"unknown top field":    `{"data":` + validData + `,"meta":{},"extra":true}`,
		"duplicate data":       `{"data":` + validData + `,"data":` + validData + `,"meta":{}}`,
		"trailing document":    `{"data":` + validData + `,"meta":{}} {}`,
		"null envelope meta":   `{"data":` + validData + `,"meta":null}`,
		"scalar envelope meta": `{"data":` + validData + `,"meta":true}`,
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
			_, _ = writer.Write([]byte(`{"data":` + validData + `,"meta":{}}`))
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
		ContentMD:   "valid",
		ContentHTML: "<p>valid</p>\n",
		CreatedAt:   instant,
		UpdatedAt:   instant,
		PublishedAt: instant,
	}
	tests := []struct {
		name   string
		status int
		meta   map[string]any
		args   []string
	}{
		{name: "create missing replayed", status: http.StatusCreated, meta: map[string]any{}, args: []string{"post", "create", "--content", "valid"}},
		{name: "create wrong replayed type", status: http.StatusCreated, meta: map[string]any{"replayed": "false"}, args: []string{"post", "create", "--content", "valid"}},
		{name: "create status conflicts false", status: http.StatusOK, meta: map[string]any{"replayed": false}, args: []string{"post", "create", "--content", "valid"}},
		{name: "create status conflicts true", status: http.StatusCreated, meta: map[string]any{"replayed": true}, args: []string{"post", "create", "--content", "valid"}},
		{name: "delete missing deleted", status: http.StatusOK, meta: map[string]any{}, args: []string{"post", "delete", canonical.ID, "--yes"}},
		{name: "delete false", status: http.StatusOK, meta: map[string]any{"deleted": false}, args: []string{"post", "delete", canonical.ID, "--yes"}},
		{name: "delete wrong type", status: http.StatusOK, meta: map[string]any{"deleted": "true"}, args: []string{"post", "delete", canonical.ID, "--yes"}},
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
