package cli

import (
	"bytes"
	"context"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	ginhttp "github.com/moment/moment/internal/transport/http/gin"
)

func initializeSiteCLIData(t *testing.T) (*runtimefs.Runtime, string) {
	t.Helper()
	dataDir := t.TempDir()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{
		DataDir:     dataDir,
		Environment: runtimefs.EnvironmentDevelopment,
	})
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := database.Close(); err != nil {
		t.Fatal(err)
	}
	return runtimeState, dataDir
}

func TestSiteDirectCommandsJSONFlagsAndStableErrors(t *testing.T) {
	_, dataDir := initializeSiteCLIData(t)
	application := momentTestApplication()

	initialOutput := execute(application, "", "site", "get", "--data-dir", dataDir, "--json")
	initial := commandData(t, initialOutput)
	if initial["title"] != "Moment" || initial["timezone"] == "" || initial["comments_enabled"] != true || initial["public_base_url"] != nil || initial["avatar_media_id"] != nil {
		t.Fatalf("initial = %#v", initial)
	}
	if initialOutput.stderr != "" {
		t.Fatalf("JSON get stderr = %q", initialOutput.stderr)
	}

	updatedOutput := execute(application, "", "site", "update", "--data-dir", dataDir,
		"--title", "Jojo moments",
		"--bio", "portable notes",
		"--timezone", "Asia/Shanghai",
		"--public-base-url", "HTTP://LOCALHOST:80/journal/",
		"--comments-enabled=false",
		"--json",
	)
	updated := commandData(t, updatedOutput)
	if updated["title"] != "Jojo moments" || updated["bio"] != "portable notes" || updated["timezone"] != "Asia/Shanghai" ||
		updated["public_base_url"] != "http://localhost/journal" || updated["comments_enabled"] != false {
		t.Fatalf("updated = %#v", updated)
	}
	if updatedOutput.stderr != "" {
		t.Fatalf("JSON update stderr = %q", updatedOutput.stderr)
	}

	cleared := commandData(t, execute(application, "", "site", "update", "--data-dir", dataDir, "--clear-public-base-url", "--json"))
	if cleared["public_base_url"] != nil || cleared["title"] != "Jojo moments" || cleared["comments_enabled"] != false {
		t.Fatalf("cleared = %#v", cleared)
	}

	for name, args := range map[string][]string{
		"no flags":        {"site", "update", "--data-dir", dataDir, "--json"},
		"URL conflict":    {"site", "update", "--data-dir", dataDir, "--public-base-url", "https://example.test", "--clear-public-base-url", "--json"},
		"avatar conflict": {"site", "update", "--data-dir", dataDir, "--avatar-media-id", "01ARZ3NDEKTSV4RRFFQ69G5FAY", "--clear-avatar", "--json"},
		"bad timezone":    {"site", "update", "--data-dir", dataDir, "--timezone", "UTC+08:00", "--json"},
	} {
		t.Run(name, func(t *testing.T) {
			output := execute(application, "", args...)
			if output.exit != ExitInvalidInput || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
				t.Fatalf("output = %#v", output)
			}
		})
	}

	missingAvatar := execute(application, "", "site", "update", "--data-dir", dataDir, "--avatar-media-id", "01ARZ3NDEKTSV4RRFFQ69G5FAY", "--json")
	if missingAvatar.exit != ExitNotFound || decodeOneDocument(t, missingAvatar.stdout)["error"].(map[string]any)["code"] != "MEDIA_NOT_FOUND" {
		t.Fatalf("missing avatar = %#v", missingAvatar)
	}
}

func TestSiteDirectAPIParityPublicGetAndAuthenticatedUpdate(t *testing.T) {
	runtimeState, dataDir := initializeSiteCLIData(t)
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
	defer func() { _ = database.Close() }()
	repository, err := sqlitestore.NewSiteRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSiteService(repository, app.SiteOptions{AllowDevelopmentLocalHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := ginhttp.New(ginhttp.Config{
		Assets: fstest.MapFS{
			"index.html": &fstest.MapFile{Data: []byte("Moment")},
		},
		OwnerAuth:    manager,
		SiteSettings: service,
		Readiness: func(ctx context.Context) error {
			if err := database.Ready(ctx); err != nil {
				return err
			}
			return service.Ready(ctx)
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	application := momentTestApplication()

	t.Setenv("MOMENT_TOKEN", "")
	direct := commandData(t, execute(application, "", "site", "get", "--data-dir", dataDir, "--json"))
	apiGet := execute(application, "", "--url", server.URL, "site", "get", "--json")
	api := commandData(t, apiGet)
	if !reflect.DeepEqual(direct, api) {
		t.Fatalf("direct = %#v, api = %#v", direct, api)
	}
	if apiGet.stderr != "" {
		t.Fatalf("public API get stderr = %q", apiGet.stderr)
	}

	unauthorized := execute(application, "", "--url", server.URL, "site", "update", "--bio", "blocked", "--json")
	if unauthorized.exit != ExitUnauthorized || decodeOneDocument(t, unauthorized.stdout)["error"].(map[string]any)["code"] != "OWNER_TOKEN_REQUIRED" {
		t.Fatalf("unauthorized = %#v", unauthorized)
	}

	t.Setenv("MOMENT_TOKEN", credential.Token)
	apiUpdated := commandData(t, execute(application, "", "--url", server.URL, "site", "update",
		"--title", "API identity", "--timezone", "Europe/Paris", "--comments-enabled=false", "--json"))
	directAfter := commandData(t, execute(application, "", "site", "get", "--data-dir", dataDir, "--json"))
	if !reflect.DeepEqual(apiUpdated, directAfter) {
		t.Fatalf("API update = %#v, direct read = %#v", apiUpdated, directAfter)
	}

	tokenFile := filepath.Join(t.TempDir(), "owner.token")
	if err := os.WriteFile(tokenFile, []byte(credential.Token), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOMENT_TOKEN", "wrong")
	fromFile := commandData(t, execute(application, "", "--url", server.URL, "--token-file", tokenFile, "site", "update", "--bio", "from token file", "--json"))
	if fromFile["bio"] != "from token file" {
		t.Fatalf("token-file update = %#v", fromFile)
	}
}

func TestSiteCommandCancellationAndHelpContract(t *testing.T) {
	_, dataDir := initializeSiteCLIData(t)
	application := momentTestApplication()
	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	exit := application.Execute(cancelledContext, []string{"site", "get", "--data-dir", dataDir, "--json"}, Streams{Out: &stdout, Err: &stderr})
	if exit != ExitTimeout || decodeOneDocument(t, stdout.String())["error"].(map[string]any)["code"] != "CANCELLED" || stderr.String() != "" {
		t.Fatalf("cancelled exit=%d stdout=%q stderr=%q", exit, stdout.String(), stderr.String())
	}

	for _, args := range [][]string{{"site", "get", "--help"}, {"site", "update", "--help"}} {
		output := execute(application, "", args...)
		if output.exit != ExitSuccess || output.stderr != "" {
			t.Fatalf("help output = %#v", output)
		}
		for _, required := range []string{"INPUTS", "OUTPUT", "ERRORS", "Examples:", "--json"} {
			if !strings.Contains(output.stdout, required) {
				t.Fatalf("args %v missing %q:\n%s", args, required, output.stdout)
			}
		}
	}
}
