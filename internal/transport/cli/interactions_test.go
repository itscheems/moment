package cli

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestInteractionCLIDirectAPIParityAndIncrementalReads(t *testing.T) {
	f := newCommentCLIFixture(t)
	application := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", f.token)
	direct := func(args ...string) commandOutput {
		return execute(application, "", append([]string{"--data-dir", f.dataDir, "--as", "writer", "--json"}, args...)...)
	}
	api := func(args ...string) commandOutput {
		return execute(application, "", append([]string{"--url", f.server.URL, "--json"}, args...)...)
	}
	for _, verb := range []string{"like", "like-status", "unlike", "reply-context"} {
		a, b := commandData(t, direct("post", verb, f.postID)), commandData(t, api("post", verb, f.postID))
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("%s direct=%+v api=%+v", verb, a, b)
		}
	}
	root := commandData(t, direct("comment", "create", f.postID, "--content", "Root"))["id"].(string)
	for _, verb := range []string{"like", "like-status", "unlike", "reply-context"} {
		a, b := commandData(t, direct("comment", verb, root)), commandData(t, api("comment", verb, root))
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("comment %s parity", verb)
		}
	}
	for _, verb := range []string{"list", "state"} {
		a, b := commandData(t, direct("notification", verb)), commandData(t, api("notification", verb))
		if !reflect.DeepEqual(a, b) {
			t.Fatalf("notification %s direct=%+v api=%+v", verb, a, b)
		}
	}
	page := commandData(t, direct("notification", "list"))
	cursor := page["through_cursor"].(string)
	for _, run := range []func(...string) commandOutput{direct, api} {
		commandData(t, run("notification", "list", "--after", cursor))
		commandData(t, run("notification", "seen", "--through", cursor))
	}
	missing := api("notification", "get", "01ARZ3NDEKTSV4RRFFQ69G5FAY")
	if missing.exit != ExitNotFound {
		t.Fatalf("missing: %+v", missing)
	}
	invalid := direct("notification", "list", "--limit", "0")
	if invalid.exit != ExitInvalidInput {
		t.Fatalf("invalid: %+v", invalid)
	}
	unauth := execute(application, "", "--data-dir", f.dataDir, "notification", "list", "--json")
	if unauth.exit != ExitUnauthorized {
		t.Fatalf("identity: %+v", unauth)
	}
}

// The child executes the production command kernel in a separate OS process.
func TestInteractionDirectProcessHelper(_ *testing.T) {
	if os.Getenv("MOMENT_TEST_INTERACTION_CHILD") == "" {
		return
	}
	application := momentTestApplication()
	code := application.Execute(context.Background(), strings.Split(os.Getenv("MOMENT_TEST_INTERACTION_ARGS"), "\n"), Streams{In: strings.NewReader(""), Out: os.Stdout, Err: os.Stderr})
	os.Exit(int(code))
}

func TestInteractionSSESeesIndependentDirectCLIProcess(t *testing.T) {
	f := newCommentCLIFixture(t)
	db, err := sqlitestore.Open(context.Background(), f.runtime, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	repo, _ := sqlitestore.NewAccountRepository(db, f.runtime.Locks)
	accounts, _ := app.NewAccountService(repo)
	owner, _ := accounts.ProfileAccount(context.Background())
	outcome, err := accounts.SignIn(app.WithContentActor(context.Background(), owner), app.ExternalIdentity{Provider: "google", ProviderUserID: "cedar", DisplayName: "Cedar"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := accounts.SetStatus(app.WithContentActor(context.Background(), owner), outcome.User.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	username := "cedar"
	member, err = accounts.UpdateProfile(app.WithContentActor(context.Background(), member), member.ID, app.ProfileUpdate{Username: &username})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", f.server.URL+"/api/v1/account/notifications/stream", nil)
	request.Header.Set("Authorization", "Bearer "+f.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	reader := bufio.NewReader(response.Body)
	read := func() map[string]any {
		t.Helper()
		var state map[string]any
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				t.Fatal(err)
			}
			if strings.HasPrefix(line, "data: ") {
				if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &state); err != nil {
					t.Fatal(err)
				}
			}
			if line == "\n" && state != nil {
				return state
			}
		}
	}
	if state := read(); state["new_count"] != float64(0) {
		t.Fatalf("initial: %+v", state)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(ctx, executable, "-test.run=^TestInteractionDirectProcessHelper$")
	command.Dir = filepath.Dir(f.dataDir)
	args := []string{"--data-dir", f.dataDir, "--as", member.Username, "post", "like", f.postID, "--json"}
	command.Env = append(os.Environ(), "MOMENT_TEST_INTERACTION_CHILD=1", "MOMENT_TEST_INTERACTION_ARGS="+strings.Join(args, "\n"))
	started := time.Now()
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("child: %v %s", err, output)
	}
	if state := read(); state["new_count"] != float64(1) || time.Since(started) > 3*time.Second {
		t.Fatalf("cross-process update: %+v in %v", state, time.Since(started))
	}
	t.Setenv("MOMENT_TOKEN", f.token)
	application := momentTestApplication()
	direct := func(args ...string) commandOutput {
		return execute(application, "", append([]string{"--data-dir", f.dataDir, "--as", "writer", "--json"}, args...)...)
	}
	api := func(args ...string) commandOutput {
		return execute(application, "", append([]string{"--url", f.server.URL, "--json"}, args...)...)
	}
	a, b := commandData(t, direct("notification", "list")), commandData(t, api("notification", "list"))
	if !reflect.DeepEqual(a, b) {
		t.Fatal("nonempty inbox parity")
	}
	id := a["items"].([]any)[0].(map[string]any)["id"].(string)
	a, b = commandData(t, direct("notification", "get", id)), commandData(t, api("notification", "get", id))
	if !reflect.DeepEqual(a, b) || a["post"] == nil {
		t.Fatalf("current source parity: %+v %+v", a, b)
	}

}
