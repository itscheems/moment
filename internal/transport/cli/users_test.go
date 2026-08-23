package cli

import (
	"context"
	"net/http/httptest"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	ginhttp "github.com/moment/moment/internal/transport/http/gin"
)

func TestUserSuggestDirectAPIParity(t *testing.T) {
	runtimeState, dataDir := initializeSiteCLIData(t)
	credential, err := auth.InitializeOffline(context.Background(), runtimeState, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	application := momentTestApplication()
	updated := commandData(t, execute(application, "", "site", "update", "--data-dir", dataDir,
		"--username", "cheems", "--display-name", "Cheems", "--json"))
	if updated["username"] != "cheems" || updated["display_name"] != "Cheems" {
		t.Fatalf("updated identity = %#v", updated)
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
		Assets:       fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Moment")}},
		OwnerAuth:    manager,
		Accounts:     testAccountService(t, database, runtimeState),
		SiteSettings: service,
		Readiness:    service.Ready,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()

	t.Setenv("MOMENT_TOKEN", credential.Token)
	directDocument := decodeOneDocument(t, execute(application, "", "user", "suggest", "--data-dir", dataDir, "--query", "chee", "--json").stdout)
	apiOutput := execute(application, "", "--url", server.URL, "user", "suggest", "--query", "chee", "--json")
	if apiOutput.exit != ExitSuccess || apiOutput.stderr != "" {
		t.Fatalf("API output = %#v", apiOutput)
	}
	apiDocument := decodeOneDocument(t, apiOutput.stdout)
	if !reflect.DeepEqual(directDocument["data"], apiDocument["data"]) {
		t.Fatalf("direct = %#v, api = %#v", directDocument["data"], apiDocument["data"])
	}
	items := apiDocument["data"].([]any)
	if len(items) != 1 {
		t.Fatalf("suggestions = %#v", items)
	}
	item := items[0].(map[string]any)
	if item["username"] != "cheems" || item["display_name"] != "Cheems" || item["profile_url"] != "/cheems" {
		t.Fatalf("suggestion = %#v", item)
	}

	empty := decodeOneDocument(t, execute(application, "", "user", "suggest", "--data-dir", dataDir, "--query", "nobody", "--json").stdout)["data"].([]any)
	if len(empty) != 0 {
		t.Fatalf("non-match = %#v", empty)
	}
	invalid := execute(application, "", "user", "suggest", "--data-dir", dataDir, "--limit", "21", "--json")
	if invalid.exit != ExitInvalidInput || decodeOneDocument(t, invalid.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("invalid limit = %#v", invalid)
	}
}
