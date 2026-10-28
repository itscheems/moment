package cli

import (
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"testing"
	"testing/fstest"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	ginhttp "github.com/moment/moment/internal/transport/http/gin"
)

func TestPersonalImportCLIDirectAndAPISharePreviewAndRetryBehavior(t *testing.T) {
	state, dataDir := initializeSiteCLIData(t)
	ctx := context.Background()
	credential, err := initializeTestAccountCredential(ctx, state, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(ctx, state, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	accounts := testAccountService(t, db, state)
	account, err := accounts.ProfileAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	repo, err := sqlitestore.NewPostRepository(db, state.Locks)
	if err != nil {
		t.Fatal(err)
	}
	posts, err := app.NewPostService(repo)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = posts.Create(app.WithContentActor(ctx, account), app.CreatePostInput{ContentMD: "portable note"}); err != nil {
		t.Fatal(err)
	}
	exporter, err := archive.NewExporter(archive.Options{DataDir: dataDir, AppVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(dataDir, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err = manager.Load(); err != nil {
		t.Fatal(err)
	}
	engine, err := ginhttp.New(ginhttp.Config{Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Moment")}}, SessionSigner: manager, Accounts: accounts, Archive: exporter})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	t.Setenv("MOMENT_TOKEN", credential.Token)
	application := momentTestApplication()
	path := filepath.Join(t.TempDir(), "personal.zip")
	if got := execute(application, "", "user", "export", account.Username, "--data-dir", dataDir, "--output", path, "--json"); got.exit != ExitSuccess {
		t.Fatalf("export=%+v", got)
	}
	decode := func(args ...string) archive.AccountImportResult {
		t.Helper()
		got := execute(application, "", args...)
		if got.exit != ExitSuccess || got.stderr != "" {
			t.Fatalf("command=%+v", got)
		}
		var envelope struct {
			Data archive.AccountImportResult `json:"data"`
		}
		if err := json.Unmarshal([]byte(got.stdout), &envelope); err != nil {
			t.Fatal(err)
		}
		return envelope.Data
	}
	direct := decode("--as", account.Username, "user", "import", path, "--data-dir", dataDir, "--dry-run", "--json")
	remote := decode("--url", server.URL, "user", "import", path, "--dry-run", "--json")
	if !reflect.DeepEqual(direct, remote) || direct.Counts.Entries != 1 {
		t.Fatalf("preview drift: direct=%+v api=%+v", direct, remote)
	}
	applied := decode("--url", server.URL, "user", "import", path, "--yes", "--json")
	if applied.Added.Entries != 0 || applied.Skipped.Entries != 1 {
		t.Fatalf("import=%+v", applied)
	}
	repeated := decode("--as", account.Username, "user", "import", path, "--data-dir", dataDir, "--yes", "--json")
	if repeated.Added.Entries != 0 || repeated.Skipped.Entries != 1 {
		t.Fatalf("retry=%+v", repeated)
	}
	refused := execute(application, "", "user", "import", path, "--data-dir", dataDir, "--dry-run", "--json")
	if refused.exit == ExitSuccess {
		t.Fatal("direct import selected an account implicitly")
	}
}
