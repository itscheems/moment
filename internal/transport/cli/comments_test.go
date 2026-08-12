package cli

import (
	"bytes"
	"context"
	"errors"
	"net/http/httptest"
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
)

type commentCLIFixture struct {
	dataDir string
	token   string
	postID  string
	runtime *runtimefs.Runtime
	server  *httptest.Server
}

func newCommentCLIFixture(t *testing.T) commentCLIFixture {
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
	postRepository, _ := sqlitestore.NewPostRepository(database, runtimeState.Locks)
	postID := domain.ID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	posts, err := app.NewPostService(postRepository, app.WithPostIDGenerator(func(time.Time) (domain.ID, error) { return postID, nil }))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := posts.Create(context.Background(), app.CreatePostInput{ContentMD: "comment host"}); err != nil {
		t.Fatal(err)
	}
	siteRepository, _ := sqlitestore.NewSiteRepository(database, runtimeState.Locks)
	site, _ := app.NewSiteService(siteRepository, app.SiteOptions{AllowDevelopmentLocalHTTP: true})
	commentRepository, _ := sqlitestore.NewCommentRepository(database, runtimeState.Locks)
	comments, _ := app.NewCommentService(commentRepository)
	mediaStore, err := media.NewStore(runtimeState, database, media.Options{})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := ginhttp.New(ginhttp.Config{Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Moment")}}, OwnerAuth: manager, SiteSettings: site, Posts: posts, Comments: comments, Media: mediaStore})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	t.Cleanup(func() { server.Close(); _ = database.Close() })
	return commentCLIFixture{dataDir: dataDir, token: credential.Token, postID: postID.String(), runtime: runtimeState, server: server}
}

func TestCommentCLIDirectAPIParityOwnerIdentityIdempotencyAndSafety(t *testing.T) {
	fixture := newCommentCLIFixture(t)
	application := momentTestApplication()
	createdOutput := execute(application, "", "comment", "create", fixture.postID, "--data-dir", fixture.dataDir, "--content", "owner **root**", "--idempotency-key", "comment-cli-1", "--json")
	created := commandData(t, createdOutput)
	if created["author_kind"] != "owner" || created["author_name"] != "Owner" || created["parent_id"] != nil || created["depth"] != float64(0) || createdOutput.stderr != "" {
		t.Fatalf("created = %#v output=%#v", created, createdOutput)
	}
	replayedOutput := execute(application, "", "comment", "create", fixture.postID, "--data-dir", fixture.dataDir, "--content", "owner **root**", "--idempotency-key", "comment-cli-1", "--json")
	if replayed := commandData(t, replayedOutput); !reflect.DeepEqual(created, replayed) || decodeOneDocument(t, replayedOutput.stdout)["meta"].(map[string]any)["replayed"] != true {
		t.Fatalf("replay = %#v", replayedOutput)
	}
	conflict := execute(application, "", "comment", "create", fixture.postID, "--data-dir", fixture.dataDir, "--content", "changed", "--idempotency-key", "comment-cli-1", "--json")
	if conflict.exit != ExitConflict || decodeOneDocument(t, conflict.stdout)["error"].(map[string]any)["code"] != "IDEMPOTENCY_KEY_REUSED" {
		t.Fatalf("conflict = %#v", conflict)
	}

	t.Setenv("MOMENT_TOKEN", fixture.token)
	apiList := commandData(t, execute(application, "", "--url", fixture.server.URL, "comment", "list", fixture.postID, "--json"))
	directList := commandData(t, execute(application, "", "comment", "list", fixture.postID, "--data-dir", fixture.dataDir, "--json"))
	if !reflect.DeepEqual(apiList, directList) {
		t.Fatalf("api=%#v direct=%#v", apiList, directList)
	}
	reply := commandData(t, execute(application, "", "--url", fixture.server.URL, "comment", "reply", fixture.postID, created["id"].(string), "--content", "API owner reply", "--json"))
	if reply["author_kind"] != "owner" || reply["parent_id"] != created["id"] || reply["depth"] != float64(1) {
		t.Fatalf("reply = %#v", reply)
	}
	if after := commandData(t, execute(application, "", "comment", "list", fixture.postID, "--data-dir", fixture.dataDir, "--json")); len(after["items"].([]any)) != 2 {
		t.Fatalf("after = %#v", after)
	}

	dryRun := execute(application, "", "--url", fixture.server.URL, "comment", "delete", created["id"].(string), "--dry-run", "--json")
	dryMeta := decodeOneDocument(t, dryRun.stdout)["meta"].(map[string]any)
	if dryRun.exit != ExitSuccess || dryMeta["dry_run"] != true || dryMeta["deleted"] != false || dryMeta["replies_affected"] != float64(1) {
		t.Fatalf("dry-run = %#v", dryRun)
	}
	refused := execute(application, "", "comment", "delete", created["id"].(string), "--data-dir", fixture.dataDir, "--no-input", "--json")
	if refused.exit != ExitInvalidInput {
		t.Fatalf("refused = %#v", refused)
	}
	deleted := execute(application, "", "comment", "delete", created["id"].(string), "--data-dir", fixture.dataDir, "--yes", "--json")
	if deleted.exit != ExitSuccess || decodeOneDocument(t, deleted.stdout)["meta"].(map[string]any)["replies_affected"] != float64(1) {
		t.Fatalf("deleted = %#v", deleted)
	}
	if empty := commandData(t, execute(application, "", "comment", "list", fixture.postID, "--data-dir", fixture.dataDir, "--json")); len(empty["items"].([]any)) != 0 {
		t.Fatalf("empty = %#v", empty)
	}
	missing := execute(application, "", "--url", fixture.server.URL, "comment", "delete", created["id"].(string), "--yes", "--json")
	if missing.exit != ExitNotFound || decodeOneDocument(t, missing.stdout)["error"].(map[string]any)["code"] != "COMMENT_NOT_FOUND" {
		t.Fatalf("missing = %#v", missing)
	}
}

func TestCommentMediaCLIManifestAndAPIUploadParity(t *testing.T) {
	fixture := newCommentCLIFixture(t)
	application := momentTestApplication()
	imagePath := writeCLIImage(t)
	manifest := writePostManifest(t, `[
  {"path":`+jsonString(imagePath)+`,"alt_text":"night sky"},
  {"path":`+jsonString(imagePath)+`,"alt_text":""}
]`)

	directOutput := execute(application, "", "comment", "create", fixture.postID,
		"--data-dir", fixture.dataDir, "--content", "direct media comment",
		"--media-manifest", manifest, "--json")
	if directOutput.exit != ExitSuccess || directOutput.stderr != "" {
		t.Fatalf("direct media comment = %#v", directOutput)
	}
	direct := commandData(t, directOutput)
	assertCLICommentMedia(t, direct, []any{"night sky", ""})

	t.Setenv("MOMENT_TOKEN", fixture.token)
	apiOutput := execute(application, "", "--url", fixture.server.URL, "comment", "reply",
		fixture.postID, direct["id"].(string), "--content", "API media reply",
		"--media", imagePath, "--media-decorative", "--json")
	if apiOutput.exit != ExitSuccess || apiOutput.stderr != "" {
		t.Fatalf("API media reply = %#v", apiOutput)
	}
	apiReply := commandData(t, apiOutput)
	assertCLICommentMedia(t, apiReply, []any{""})

	apiList := commandData(t, execute(application, "", "--url", fixture.server.URL,
		"comment", "list", fixture.postID, "--json"))
	directList := commandData(t, execute(application, "", "comment", "list", fixture.postID,
		"--data-dir", fixture.dataDir, "--json"))
	if !reflect.DeepEqual(apiList, directList) {
		t.Fatalf("comment media parity: api=%#v direct=%#v", apiList, directList)
	}

	overLimit := make([]string, domain.MaxCommentMediaUsages+1)
	for index := range overLimit {
		overLimit[index] = `{"path":` + jsonString(imagePath) + `,"alt_text":null}`
	}
	invalid := execute(application, "["+strings.Join(overLimit, ",")+"]",
		"comment", "create", fixture.postID, "--data-dir", fixture.dataDir,
		"--content", "too many", "--media-manifest", "-", "--json")
	if invalid.exit != ExitInvalidInput || decodeOneDocument(t, invalid.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("over-limit media = %#v", invalid)
	}
}

func assertCLICommentMedia(t *testing.T, comment map[string]any, expectedAlts []any) {
	t.Helper()
	raw, ok := comment["media"].([]any)
	if !ok || raw == nil || len(raw) != len(expectedAlts) {
		t.Fatalf("comment media = %#v", comment["media"])
	}
	for index, value := range raw {
		item, ok := value.(map[string]any)
		if !ok || len(item) != 3 || item["position"] != float64(index) || !reflect.DeepEqual(item["alt_text"], expectedAlts[index]) {
			t.Fatalf("comment media[%d] = %#v", index, value)
		}
		if _, err := domain.ParseID(item["media_id"].(string)); err != nil {
			t.Fatalf("comment media[%d] id = %v", index, err)
		}
	}
}

func TestCommentCLIIdentityBoundaryErrorsCancellationHelpAndMaintenanceLease(t *testing.T) {
	fixture := newCommentCLIFixture(t)
	application := momentTestApplication()
	for _, flag := range []string{"--guest-name", "--client-ip", "--honeypot"} {
		output := execute(application, "", "comment", "create", fixture.postID, "--data-dir", fixture.dataDir, "--content", "x", flag, "spoof", "--json")
		if output.exit != ExitInvalidInput || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
			t.Fatalf("flag %s = %#v", flag, output)
		}
	}
	t.Setenv("MOMENT_TOKEN", "wrong-token")
	unauthorized := execute(application, "", "--url", fixture.server.URL, "comment", "create", fixture.postID, "--content", "blocked", "--json")
	if unauthorized.exit != ExitUnauthorized || decodeOneDocument(t, unauthorized.stdout)["error"].(map[string]any)["code"] != "UNAUTHORIZED" {
		t.Fatalf("unauthorized = %#v", unauthorized)
	}
	publicList := execute(application, "", "--url", fixture.server.URL, "comment", "list", fixture.postID, "--json")
	if publicList.exit != ExitSuccess || publicList.stderr != "" {
		t.Fatalf("public list used token = %#v", publicList)
	}

	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout, stderr bytes.Buffer
	exit := application.Execute(cancelled, []string{"comment", "list", fixture.postID, "--data-dir", fixture.dataDir, "--json"}, Streams{Out: &stdout, Err: &stderr})
	if exit != ExitTimeout || decodeOneDocument(t, stdout.String())["error"].(map[string]any)["code"] != "CANCELLED" || stderr.String() != "" {
		t.Fatalf("cancelled exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	stdout.Reset()
	stderr.Reset()
	exit = application.Execute(cancelled, []string{"comment", "create", fixture.postID, "--data-dir", fixture.dataDir, "--content", "cancelled", "--json"}, Streams{Out: &stdout, Err: &stderr})
	if exit != ExitTimeout || decodeOneDocument(t, stdout.String())["error"].(map[string]any)["code"] != "CANCELLED" || stderr.String() != "" {
		t.Fatalf("cancelled create exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}
	if thread := commandData(t, execute(application, "", "comment", "list", fixture.postID, "--data-dir", fixture.dataDir, "--json")); len(thread["items"].([]any)) != 0 {
		t.Fatalf("cancelled create mutated = %#v", thread)
	}
	for _, verb := range []string{"list", "create", "reply", "delete"} {
		output := execute(application, "", "comment", verb, "--help")
		if output.exit != ExitSuccess || output.stderr != "" {
			t.Fatalf("help %s = %#v", verb, output)
		}
		for _, required := range []string{"INPUTS", "OUTPUT", "ERRORS", "Examples:", "--json"} {
			if !strings.Contains(output.stdout, required) {
				t.Fatalf("help %s missing %s", verb, required)
			}
		}
	}

	direct, err := openDirectCommentWrite(context.Background(), Invocation{target: directTarget{dataDir: fixture.dataDir}})
	if err != nil {
		t.Fatal(err)
	}
	if !direct.maintenance.Holds(runtimefs.MaintenanceLock, runtimefs.Shared) {
		t.Fatal("direct comment write lacks maintenance shared")
	}
	blocked, stop := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer stop()
	if _, err := fixture.runtime.Locks.Acquire(blocked, runtimefs.MaintenanceLock, runtimefs.Exclusive); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("exclusive lock was not blocked: %v", err)
	}
	if err := direct.Close(); err != nil {
		t.Fatal(err)
	}
}
