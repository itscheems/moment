package cli

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http/httptest"
	"path/filepath"
	"testing"
	"testing/fstest"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	ginhttp "github.com/moment/moment/internal/transport/http/gin"
)

func TestAccountExportCLIFileParityAndIdentityBoundary(t *testing.T) {
	runtimeState, dataDir := initializeSiteCLIData(t)
	ctx := context.Background()
	credential, err := initializeTestAccountCredential(ctx, runtimeState, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	manager, _ := auth.NewManager(dataDir, auth.Options{})
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(ctx, runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	accounts := testAccountService(t, db, runtimeState)
	primaryAccount, err := accounts.ProfileAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	exporter, err := archive.NewExporter(archive.Options{DataDir: dataDir, AppVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := ginhttp.New(ginhttp.Config{Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Moment")}}, SessionSigner: manager, Accounts: accounts, Archive: exporter})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	app := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", credential.Token)
	directPath := filepath.Join(t.TempDir(), "direct.zip")
	apiPath := filepath.Join(t.TempDir(), "api.zip")
	direct := execute(app, "", "user", "export", primaryAccount.Username, "--data-dir", dataDir, "--output", directPath, "--json")
	api := execute(app, "", "--url", server.URL, "user", "export", "--output", apiPath, "--json")
	if direct.exit != ExitSuccess || api.exit != ExitSuccess || direct.stderr != "" || api.stderr != "" {
		t.Fatalf("direct=%#v api=%#v", direct, api)
	}
	for _, result := range []string{direct.stdout, api.stdout} {
		var envelope struct {
			Data archive.Summary `json:"data"`
		}
		if err := json.Unmarshal([]byte(result), &envelope); err != nil {
			t.Fatal(err)
		}
		if envelope.Data.FormatVersion != archive.AccountFormatVersion {
			t.Fatalf("personal format version=%d", envelope.Data.FormatVersion)
		}
	}
	if err := archive.ValidateAccountFileContext(ctx, directPath); err != nil {
		t.Fatal(err)
	}
	if err := archive.ValidateAccountFileContext(ctx, apiPath); err != nil {
		t.Fatal(err)
	}
	readAccount := func(path string) []byte {
		reader, err := zip.OpenReader(path)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = reader.Close() }()
		for _, file := range reader.File {
			if file.Name == "data/account.json" {
				source, err := file.Open()
				if err != nil {
					t.Fatal(err)
				}
				body, err := io.ReadAll(source)
				_ = source.Close()
				if err != nil {
					t.Fatal(err)
				}
				return body
			}
		}
		t.Fatal("missing account")
		return nil
	}
	if !bytes.Equal(readAccount(directPath), readAccount(apiPath)) {
		t.Fatal("direct/API account drift")
	}
	selected := execute(app, "", "--url", server.URL, "user", "export", primaryAccount.Username, "--output", filepath.Join(t.TempDir(), "bad.zip"), "--json")
	if selected.exit != ExitInvalidInput {
		t.Fatalf("API accepted user selection: %#v", selected)
	}
	repeated := execute(app, "", "user", "export", primaryAccount.Username, "--data-dir", dataDir, "--output", directPath, "--json")
	if repeated.exit == ExitSuccess {
		t.Fatal("overwrote file without consent")
	}
}
