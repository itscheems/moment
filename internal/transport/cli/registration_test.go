package cli

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	ginhttp "github.com/moment/moment/internal/transport/http/gin"
)

func registrationSite(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	commandData(t, execute(momentTestApplication(), "", "init", "--data-dir", root, "--username", "writer", "--json"))
	return root
}

func registrationAccounts(t *testing.T, root string) (*runtimefs.Runtime, *sqlitestore.Database) {
	t.Helper()
	state, err := runtimefs.OpenContext(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(context.Background(), state, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return state, db
}

func registrationMode(t *testing.T, db *sqlitestore.Database, mode domain.RegistrationMode) {
	t.Helper()
	ctx := context.Background()
	if err := db.WriteTx(ctx, func(queries sqlitestore.Queries) error {
		_, err := queries.ExecContext(ctx, `UPDATE site_settings SET registration_mode=? WHERE id=1`, string(mode))
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func registrationServer(t *testing.T, root string) (*sqlitestore.Database, *httptest.Server) {
	t.Helper()
	state, db := registrationAccounts(t, root)
	accounts := testAccountService(t, db, state)
	manager, err := auth.NewManager(root, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	engine, err := ginhttp.New(ginhttp.Config{
		Assets:        fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Moment")}},
		SessionSigner: manager, Accounts: accounts,
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/api/v1/register" && request.Header.Get("Authorization") != "" {
			t.Error("anonymous registration forwarded an account token")
		}
		engine.ServeHTTP(writer, request)
	}))
	t.Cleanup(server.Close)
	return db, server
}

func registrationCommandData(t *testing.T, output commandOutput, password string) map[string]any {
	t.Helper()
	if strings.Contains(output.stdout+output.stderr, password) || strings.Contains(output.stdout, "csrf_token") || strings.Contains(output.stdout, "expires_at") {
		t.Fatalf("registration leaked credentials: %#v", output)
	}
	if output.stderr != "" {
		t.Fatalf("registration wrote stderr: %#v", output)
	}
	data := commandData(t, output)
	if len(data) != 3 || data["account_created"] != true {
		t.Fatalf("registration data = %+v", data)
	}
	return data
}

func TestAccountRegistrationDirectAndAnonymousAPIParity(t *testing.T) {
	application := momentTestApplication()
	directRoot := registrationSite(t)
	apiRoot := registrationSite(t)
	apiDB, server := registrationServer(t, apiRoot)
	_, directDB := registrationAccounts(t, directRoot)
	t.Setenv("MOMENT_TOKEN", "ambient-token-that-must-not-be-sent")
	secret := "password123"
	file := filepath.Join(t.TempDir(), "password.txt")
	if err := os.WriteFile(file, []byte(secret+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	direct := registrationCommandData(t, execute(application, secret+"\n", "auth", "register", "--data-dir", directRoot, "--username", "Alice", "--password-file", "-", "--json"), secret)
	api := registrationCommandData(t, execute(application, "", "auth", "register", "--url", server.URL, "--username", "Alice", "--password-file", file, "--verification-token-file", registrationProofFile(t, apiRoot, "alice@example.test"), "--json"), secret)
	if !reflect.DeepEqual(direct, api) || direct["username"] != "alice" || direct["account_status"] != "pending" {
		t.Fatalf("approval parity: direct=%v api=%v", direct, api)
	}
	for _, args := range [][]string{{"auth", "register", "--data-dir", directRoot}, {"auth", "register", "--url", server.URL}} {
		duplicateArgs := append(append([]string(nil), args...), "--username", "ALICE", "--password-file", "-", "--json")
		duplicate := execute(application, secret+"\n", duplicateArgs...)
		if duplicate.exit != ExitConflict || duplicate.stderr != "" || decodeOneDocument(t, duplicate.stdout)["error"].(map[string]any)["code"] != "USERNAME_TAKEN" {
			t.Fatalf("duplicate registration = %#v", duplicate)
		}
		invalidArgs := append(append([]string(nil), args...), "--username", "charlie", "--password-file", "-", "--json")
		invalid := execute(application, "short\n", invalidArgs...)
		if invalid.exit != ExitInvalidInput || invalid.stderr != "" || decodeOneDocument(t, invalid.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
			t.Fatalf("invalid password parity = %#v", invalid)
		}
	}
	for _, pair := range []struct {
		root string
		db   *sqlitestore.Database
	}{{directRoot, directDB}, {apiRoot, apiDB}} {
		state, err := runtimefs.OpenContext(context.Background(), pair.root)
		if err != nil {
			t.Fatal(err)
		}
		accounts := testAccountService(t, pair.db, state)
		if _, err := accounts.VerifyPassword(context.Background(), "alice", secret); err != nil {
			t.Fatalf("registered password did not sign in: %v", err)
		}
	}
	registrationMode(t, directDB, domain.RegistrationOpen)
	registrationMode(t, apiDB, domain.RegistrationOpen)
	direct = registrationCommandData(t, execute(application, secret+"\n", "auth", "register", "--data-dir", directRoot, "--username", "Bob", "--password-file", "-", "--json"), secret)
	api = registrationCommandData(t, execute(application, secret+"\n", "auth", "register", "--url", server.URL, "--username", "Bob", "--password-file", "-", "--verification-token-file", registrationProofFile(t, apiRoot, "bob@example.test"), "--json"), secret)
	if !reflect.DeepEqual(direct, api) || direct["account_status"] != "active" {
		t.Fatalf("open parity: direct=%v api=%v", direct, api)
	}
	registrationMode(t, directDB, domain.RegistrationClosed)
	registrationMode(t, apiDB, domain.RegistrationClosed)
	for _, args := range [][]string{{"auth", "register", "--data-dir", directRoot}, {"auth", "register", "--url", server.URL}} {
		args = append(args, "--username", "charlie", "--password-file", "-", "--json")
		output := execute(application, secret+"\n", args...)
		if output.exit != ExitConflict || output.stderr != "" || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "REGISTRATION_CLOSED" {
			t.Fatalf("closed registration = %#v", output)
		}
	}
}

func TestAccountRegistrationInputAndRunningInstanceBoundaries(t *testing.T) {
	application := momentTestApplication()
	root := registrationSite(t)
	missing := execute(application, "", "auth", "register", "--data-dir", root, "--username", "alice", "--json")
	if missing.exit != ExitInvalidInput || decodeOneDocument(t, missing.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("missing password source = %#v", missing)
	}
	wrongActor := execute(application, "password123\n", "auth", "register", "--data-dir", root, "--as", "writer", "--username", "alice", "--password-file", "-", "--json")
	if wrongActor.exit != ExitInvalidInput || decodeOneDocument(t, wrongActor.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("registration accepted an actor selector: %#v", wrongActor)
	}
	withTokenFile := execute(application, "password123\n", "auth", "register", "--url", "http://127.0.0.1:1", "--token-file", "unused.token", "--username", "alice", "--password-file", "-", "--json")
	if withTokenFile.exit != ExitInvalidInput || decodeOneDocument(t, withTokenFile.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" {
		t.Fatalf("anonymous registration accepted token-file: %#v", withTokenFile)
	}
	bad := execute(application, "short\n", "auth", "register", "--data-dir", root, "--username", "alice", "--password-file", "-", "--json")
	if bad.exit != ExitInvalidInput || decodeOneDocument(t, bad.stdout)["error"].(map[string]any)["code"] != "INVALID_ARGUMENT" || strings.Contains(bad.stdout+bad.stderr, "short") {
		t.Fatalf("invalid password = %#v", bad)
	}
	state, db := registrationAccounts(t, root)
	lease, err := state.Locks.Acquire(context.Background(), runtimefs.ServeLock, runtimefs.Shared)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	output := execute(application, "password123\n", "auth", "register", "--data-dir", root, "--username", "alice", "--password-file", "-", "--json")
	if output.exit != ExitConflict || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "INSTANCE_IN_USE" {
		t.Fatalf("running server registration = %#v", output)
	}
	accounts := testAccountService(t, db, state)
	if _, err := accounts.AccountByUsername(context.Background(), "alice"); !domain.HasCode(err, domain.CodeAccountNotFound) {
		t.Fatalf("refused registration still wrote account: %v", err)
	}
}

func TestAccountRegistrationAPIFailurePreservesCreatedOutcome(t *testing.T) {
	const password = "password123"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "" || request.Method != http.MethodPost || request.URL.Path != "/api/v1/register" {
			t.Errorf("unexpected registration request: %s %s", request.Method, request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusServiceUnavailable)
		_, _ = writer.Write([]byte(`{"error":{"code":"REGISTRATION_SESSION_UNAVAILABLE","message":"account created but session unavailable","details":{"account_created":true,"username":"alice","recovery":"login"},"request_id":"req_reg"}}`))
	}))
	defer server.Close()
	t.Setenv("MOMENT_TOKEN", "ambient-token")
	output := execute(momentTestApplication(), password+"\n", "auth", "register", "--url", server.URL, "--username", "Alice", "--password-file", "-", "--json")
	if output.exit != ExitUnavailable || output.stderr != "" || strings.Contains(output.stdout, password) {
		t.Fatalf("session failure output = %#v", output)
	}
	document := decodeOneDocument(t, output.stdout)
	failure := document["error"].(map[string]any)
	if failure["code"] != "REGISTRATION_SESSION_UNAVAILABLE" || !strings.Contains(failure["suggestion"].(string), "instead of registering again") || failure["details"].(map[string]any)["account_created"] != true {
		t.Fatalf("session failure lost committed account status: %#v", failure)
	}
}

func TestAccountRegistrationUncertainAPIResponseDoesNotInviteRetry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"data":{},"meta":{"request_id":"req_uncertain"}}`))
	}))
	defer server.Close()
	output := execute(momentTestApplication(), "password123\n", "auth", "register", "--url", server.URL, "--username", "alice", "--password-file", "-", "--json")
	if output.exit != ExitUnavailable {
		t.Fatalf("uncertain registration = %#v", output)
	}
	failure := decodeOneDocument(t, output.stdout)["error"].(map[string]any)
	if failure["code"] != "REGISTRATION_OUTCOME_UNKNOWN" || !strings.Contains(failure["suggestion"].(string), "before another registration attempt") {
		t.Fatalf("uncertain registration encouraged a duplicate: %#v", failure)
	}
}

func TestAccountRegistrationDoesNotFollowPasswordBearingRedirect(t *testing.T) {
	hits := 0
	destination := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { hits++ }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Location", destination.URL)
		writer.WriteHeader(http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	output := execute(momentTestApplication(), "password123\n", "auth", "register", "--url", server.URL, "--username", "alice", "--password-file", "-", "--json")
	if output.exit != ExitUnavailable || hits != 0 || decodeOneDocument(t, output.stdout)["error"].(map[string]any)["code"] != "REGISTRATION_OUTCOME_UNKNOWN" {
		t.Fatalf("password-bearing redirect followed: hits=%d output=%#v", hits, output)
	}
}

func TestAccountRegistrationHelpDocumentsSecretInput(t *testing.T) {
	output := execute(momentTestApplication(), "", "auth", "register", "--help")
	if output.exit != ExitSuccess || output.stderr != "" || !strings.Contains(output.stdout, "--password-file") || !strings.Contains(output.stdout, "--username") || !strings.Contains(output.stdout, "moment auth register --data-dir") || strings.Contains(output.stdout, "--password ") {
		t.Fatalf("registration help = %#v", output)
	}
}

type registrationTestSender struct{ code string }

func (sender *registrationTestSender) SendVerification(_ context.Context, _, code, _ string) error {
	sender.code = code
	return nil
}
func registrationProofFile(t *testing.T, root, email string) string {
	t.Helper()
	state, db := registrationAccounts(t, root)
	repository, err := sqlitestore.NewAccountRepository(db, state.Locks)
	if err != nil {
		t.Fatal(err)
	}
	sender := &registrationTestSender{}
	accounts, err := app.NewAccountService(repository, app.WithVerificationSender(sender))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := accounts.RequestRegistrationEmail(context.Background(), email, "cli-fixture")
	if err != nil {
		t.Fatal(err)
	}
	proof, err := accounts.VerifyRegistrationEmail(context.Background(), receipt.ChallengeID, sender.code)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "email-proof.txt")
	if err = os.WriteFile(path, []byte(proof.Token), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}
