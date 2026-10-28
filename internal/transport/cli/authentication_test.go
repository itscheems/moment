package cli

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/buildinfo"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
	ginhttp "github.com/moment/moment/internal/transport/http/gin"
)

func momentTestApplication() *Application {
	return NewMoment(buildinfo.Info{Version: "test", Commit: "test", BuildDate: "test"})
}

func commandData(t *testing.T, output commandOutput) map[string]any {
	t.Helper()
	if output.exit != ExitSuccess {
		t.Fatalf("exit = %d, stdout = %q, stderr = %q", output.exit, output.stdout, output.stderr)
	}
	decoded := decodeOneDocument(t, output.stdout)
	return decoded["data"].(map[string]any)
}

func fingerprintInstance(t *testing.T, root string) map[string]string {
	t.Helper()
	fingerprint := make(map[string]string)
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if entry.IsDir() {
			fingerprint[relative] = "directory"
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			target, err := os.Readlink(path)
			if err != nil {
				return err
			}
			fingerprint[relative] = "symlink:" + target
			return nil
		}
		contents, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		fingerprint[relative] = fmt.Sprintf("file:%x", sha256.Sum256(contents))
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return fingerprint
}

func TestTokenFileSourceIsBoundedAndExplicitlyPreferred(t *testing.T) {
	t.Setenv("MOMENT_TOKEN", "environment-token")
	directory := t.TempDir()
	path := filepath.Join(directory, "account.token")
	if err := os.WriteFile(path, []byte("file-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	token, err := readAPIToken(apiTarget{tokenFile: path})
	if err != nil || token != "file-token" {
		t.Fatalf("explicit file token=%q err=%v", token, err)
	}

	assertCode := func(name string, err error, code string, exit ExitCode) {
		t.Helper()
		var commandErr *CommandError
		if !errors.As(err, &commandErr) || commandErr.Code != code || commandErr.ExitCode != exit {
			t.Fatalf("%s err=%#v", name, err)
		}
	}
	if err := os.WriteFile(path, []byte("two tokens"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = readAPIToken(apiTarget{tokenFile: path})
	assertCode("whitespace", err, "INVALID_ARGUMENT", ExitInvalidInput)

	oversized := filepath.Join(directory, "oversized.token")
	if err := os.WriteFile(oversized, bytes.Repeat([]byte("x"), maxTokenFileBytes+1), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = readAPIToken(apiTarget{tokenFile: oversized})
	assertCode("oversized", err, "INVALID_ARGUMENT", ExitInvalidInput)

	_, err = readAPIToken(apiTarget{tokenFile: directory})
	assertCode("non-regular", err, "TOKEN_FILE_UNREADABLE", ExitIO)

	t.Setenv("MOMENT_TOKEN", "   ")
	_, err = readAPIToken(apiTarget{})
	assertCode("missing", err, "ACCOUNT_TOKEN_REQUIRED", ExitUnauthorized)
}

type testAccountCredential struct{ Token string }

func initializeTestAccountCredential(ctx context.Context, state *runtimefs.Runtime, options auth.Options) (credential testAccountCredential, resultErr error) {
	if _, err := auth.InitializeOffline(ctx, state, options); err != nil {
		return credential, err
	}
	database, err := sqlitestore.Open(ctx, state, sqlitestore.Options{})
	if err != nil {
		return credential, err
	}
	defer func() { resultErr = errors.Join(resultErr, database.Close()) }()
	repository, err := sqlitestore.NewAccountRepository(database, state.Locks)
	if err != nil {
		return credential, err
	}
	accounts, err := app.NewAccountService(repository, app.WithAccountClock(options.Now))
	if err != nil {
		return credential, err
	}
	users, err := accounts.List(ctx)
	if err != nil {
		return credential, err
	}
	if len(users) == 0 {
		if _, err := accounts.InitializeSiteAccount(ctx, "writer"); err != nil {
			return credential, err
		}
	}
	account, err := accounts.ProfileAccount(ctx)
	if err != nil {
		return credential, err
	}
	issued, err := accounts.CreateAPIToken(ctx, account.ID, "CLI transport test")
	return testAccountCredential{Token: issued.Value}, err
}

func TestAccountInitializationRequiresUsernameAndStoresCredentialsInAccounts(t *testing.T) {
	application := momentTestApplication()
	root := filepath.Join(t.TempDir(), "site")
	missing := execute(application, "", "init", "--data-dir", root, "--json")
	if missing.exit != ExitInvalidInput {
		t.Fatalf("missing username: %#v", missing)
	}
	if _, err := os.Stat(root); !os.IsNotExist(err) {
		t.Fatal("invalid initialization changed the data directory")
	}
	initial := commandData(t, execute(application, "", "init", "--data-dir", root, "--username", "writer", "--json"))
	token := initial["token"].(string)
	if len(token) != 43 {
		t.Fatal("initial account token is missing")
	}
	contents, err := os.ReadFile(filepath.Join(root, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	var signing map[string]any
	if err := json.Unmarshal(contents, &signing); err != nil {
		t.Fatal(err)
	}
	if len(signing) != 4 || bytes.Contains(contents, []byte(token)) || signing["token_hash"] != nil || signing["password_hash"] != nil {
		t.Fatal("session signing material stores account credentials")
	}
	state, err := runtimefs.OpenContext(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), state, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = database.Close() }()
	accounts := testAccountService(t, database, state)
	verified, err := accounts.ResolveAPIToken(context.Background(), token)
	if err != nil || verified.User.Username != "writer" {
		t.Fatalf("initial credential identity: %+v,%v", verified.User, err)
	}
	if retired := execute(application, "", "owner", "status", "--data-dir", root, "--json"); retired.exit == ExitSuccess {
		t.Fatal("retired owner command remains registered")
	}
}

func TestPersonalCredentialsDirectLifecycleAndSecretInput(t *testing.T) {
	application := momentTestApplication()
	root := t.TempDir()
	initial := commandData(t, execute(application, "", "init", "--data-dir", root, "--username", "writer", "--json"))
	signingBefore, err := os.ReadFile(filepath.Join(root, "secrets.json"))
	if err != nil {
		t.Fatal(err)
	}
	password := "account-password-example"
	changed := execute(application, password+"\n", "auth", "set-password", "--data-dir", root, "--username", "writer", "--password-file", "-", "--json")
	commandData(t, changed)
	if strings.Contains(changed.stdout+changed.stderr, password) {
		t.Fatal("password leaked into command output")
	}
	status := commandData(t, execute(application, "", "auth", "status", "--data-dir", root, "--username", "writer", "--json"))
	if status["password_enabled"] != true {
		t.Fatal("account password was not persisted")
	}
	issued := commandData(t, execute(application, "", "auth", "create-token", "--data-dir", root, "--username", "writer", "--label", "phone", "--json"))
	id := issued["credential"].(map[string]any)["id"].(string)
	preview := commandData(t, execute(application, "", "auth", "revoke-token", id, "--data-dir", root, "--username", "writer", "--dry-run", "--json"))
	if preview["dry_run"] != true || preview["revoked"] != false {
		t.Fatal("credential dry-run changed authority")
	}
	commandData(t, execute(application, "", "auth", "revoke-token", id, "--data-dir", root, "--username", "writer", "--yes", "--json"))
	state, err := runtimefs.OpenContext(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(context.Background(), state, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	accounts := testAccountService(t, db, state)
	if _, err := accounts.VerifyPassword(context.Background(), "writer", password); err != nil {
		t.Fatalf("stdin password was not stored exactly: %v", err)
	}
	if _, err := accounts.ResolveAPIToken(context.Background(), issued["token"].(string)); err == nil {
		t.Fatal("revoked API token remained valid")
	}
	if _, err := accounts.ResolveAPIToken(context.Background(), initial["token"].(string)); err == nil {
		t.Fatal("password change left the initial API credential valid")
	}
	signingAfter, err := os.ReadFile(filepath.Join(root, "secrets.json"))
	if err != nil || !bytes.Equal(signingBefore, signingAfter) {
		t.Fatal("personal credential management changed the instance signing key")
	}
	listing := execute(application, "", "auth", "list-tokens", "--data-dir", root, "--username", "writer", "--json")
	if listing.exit != ExitSuccess || strings.Contains(listing.stdout, "hash") || strings.Contains(listing.stdout, issued["token"].(string)) {
		t.Fatal("credential listing leaks token material")
	}
}

func TestAccountCredentialAPIModeUsesOnlyAuthenticatedAccount(t *testing.T) {
	application := momentTestApplication()
	root := t.TempDir()
	initial := commandData(t, execute(application, "", "init", "--data-dir", root, "--username", "writer", "--json"))
	state, err := runtimefs.OpenContext(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(context.Background(), state, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	accounts := testAccountService(t, db, state)
	manager, err := auth.NewManager(root, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	engine, err := ginhttp.New(ginhttp.Config{Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Moment")}}, SessionSigner: manager, Accounts: accounts})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	t.Setenv("MOMENT_TOKEN", initial["token"].(string))
	direct := commandData(t, execute(application, "", "auth", "status", "--data-dir", root, "--username", "writer", "--json"))
	api := commandData(t, execute(application, "", "auth", "status", "--url", server.URL, "--json"))
	if !reflect.DeepEqual(direct, api) {
		t.Fatalf("credential status parity: direct=%v api=%v", direct, api)
	}
	rejected := execute(application, "", "auth", "status", "--url", server.URL, "--username", "writer", "--json")
	if rejected.exit != ExitInvalidInput {
		t.Fatal("API mode accepts an impersonation selector")
	}
	issued := commandData(t, execute(application, "", "auth", "create-token", "--url", server.URL, "--label", "remote", "--json"))
	id := issued["credential"].(map[string]any)["id"].(string)
	commandData(t, execute(application, "", "auth", "revoke-token", id, "--url", server.URL, "--yes", "--json"))
	if _, err := accounts.ResolveAPIToken(context.Background(), issued["token"].(string)); err == nil {
		t.Fatal("API revocation did not change account storage")
	}
}

func TestOfflineAccountCredentialChangesRefuseRunningInstances(t *testing.T) {
	application := momentTestApplication()
	root := t.TempDir()
	commandData(t, execute(application, "", "init", "--data-dir", root, "--username", "writer", "--json"))
	state, err := runtimefs.OpenContext(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	lease, err := state.Locks.Acquire(context.Background(), runtimefs.ServeLock, runtimefs.Shared)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lease.Release() }()
	before := fingerprintInstance(t, root)
	out := execute(application, "", "auth", "create-token", "--data-dir", root, "--username", "writer", "--json")
	if out.exit != ExitConflict || decodeOneDocument(t, out.stdout)["error"].(map[string]any)["code"] != "INSTANCE_IN_USE" {
		t.Fatalf("active instance mutation: %#v", out)
	}
	if after := fingerprintInstance(t, root); !reflect.DeepEqual(before, after) {
		t.Fatal("refused offline operation changed instance files")
	}
}

func TestAccountTokenCreationRejectsAnEmptyAPIReceipt(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusCreated)
		_, _ = writer.Write([]byte(`{"data":{"credential":{},"token":""},"meta":{"request_id":"req_empty_token"}}`))
	}))
	defer server.Close()
	t.Setenv("MOMENT_TOKEN", "caller-token")
	output := execute(momentTestApplication(), "", "auth", "create-token", "--url", server.URL, "--json")
	if output.exit != ExitUnavailable {
		t.Fatalf("empty creation receipt reported success: %#v", output)
	}
}

func TestSensitiveAPIConfirmationAndEmergencyRevocation(t *testing.T) {
	application := momentTestApplication()
	root := t.TempDir()
	commandData(t, execute(application, "", "init", "--data-dir", root, "--username", "writer", "--json"))
	state, err := runtimefs.OpenContext(context.Background(), root)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sqlitestore.Open(context.Background(), state, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	accounts := testAccountService(t, db, state)
	user, err := accounts.AccountByUsername(context.Background(), "writer")
	if err != nil {
		t.Fatal(err)
	}
	password := "sensitive-current-password"
	if err := accounts.SetAccountPassword(context.Background(), user.ID, password); err != nil {
		t.Fatal(err)
	}
	token, err := accounts.CreateAPIToken(context.Background(), user.ID, "operator")
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(root, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	engine, err := ginhttp.New(ginhttp.Config{Assets: fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Moment")}}, SessionSigner: manager, Accounts: accounts})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()
	t.Setenv("MOMENT_TOKEN", token.Value)
	rejected := execute(application, "", "auth", "create-token", "--url", server.URL, "--json")
	if rejected.exit != ExitUnauthorized || decodeOneDocument(t, rejected.stdout)["error"].(map[string]any)["code"] != "REAUTHENTICATION_REQUIRED" {
		t.Fatalf("missing confirmation: %#v", rejected)
	}
	proof := filepath.Join(t.TempDir(), "current.password")
	if err := os.WriteFile(proof, []byte(password+"\n"), 0600); err != nil {
		t.Fatal(err)
	}
	issued := commandData(t, execute(application, "", "auth", "create-token", "--url", server.URL, "--reauth-password-file", proof, "--json"))
	directPreview := commandData(t, execute(application, "", "auth", "revoke-all", "--data-dir", root, "--username", "writer", "--dry-run", "--json"))
	apiPreview := commandData(t, execute(application, "", "auth", "revoke-all", "--url", server.URL, "--dry-run", "--json"))
	if !reflect.DeepEqual(directPreview, apiPreview) {
		t.Fatalf("revoke-all preview parity: %v %v", directPreview, apiPreview)
	}
	if _, err := accounts.ResolveAPIToken(context.Background(), issued["token"].(string)); err != nil {
		t.Fatal("dry-run mutated token")
	}
	output := execute(application, "", "auth", "revoke-all", "--url", server.URL, "--reauth-password-file", proof, "--yes", "--json")
	commandData(t, output)
	if strings.Contains(output.stdout+output.stderr, password) {
		t.Fatal("confirmation password leaked")
	}
	for _, value := range []string{token.Value, issued["token"].(string)} {
		if _, err := accounts.ResolveAPIToken(context.Background(), value); err == nil {
			t.Fatal("token survived emergency revocation")
		}
	}
	if _, err := accounts.VerifyPassword(context.Background(), user.Username, password); err != nil {
		t.Fatal("emergency operation removed recovery password")
	}
}

func TestSensitivePasswordProofDoesNotFollowRedirects(t *testing.T) {
	var reached, confirmed atomic.Bool
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { reached.Store(true); w.WriteHeader(http.StatusNoContent) }))
	defer destination.Close()
	password := "redirect-confirmation-password"
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		confirmed.Store(r.Header.Get("X-Moment-Password") == base64.RawURLEncoding.EncodeToString([]byte(password)))
		http.Redirect(w, r, destination.URL, http.StatusTemporaryRedirect)
	}))
	defer origin.Close()
	path := filepath.Join(t.TempDir(), "confirmation.password")
	if err := os.WriteFile(path, []byte(password), 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOMENT_TOKEN", "fixture-token")
	output := execute(momentTestApplication(), "", "auth", "create-token", "--url", origin.URL, "--reauth-password-file", path, "--json")
	if output.exit == ExitSuccess || !confirmed.Load() || reached.Load() {
		t.Fatalf("redirect crossed credential boundary: exit=%d confirmed=%t forwarded=%t", output.exit, confirmed.Load(), reached.Load())
	}
	if strings.Contains(output.stdout+output.stderr, password) {
		t.Fatal("confirmation password leaked into output")
	}
}
