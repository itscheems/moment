package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// deviceLoginServer answers the two device endpoints and the one account
// endpoint the login command uses.
//
// It is a stand-in rather than a real engine because a device-capable provider
// cannot be pointed at a fake from outside the oauth package, and the server
// side of this flow is verified against a real engine there. What is under test
// here is the command: whether it waits, whether it stores what it was given,
// and whether what it stores is presented in the one way the server accepts.
type deviceLoginServer struct {
	server *httptest.Server

	mu sync.Mutex
	// answers is the queue of poll outcomes, oldest first. Each entry is either
	// a status the flow may report or an error code it ends on.
	answers   []string
	pollTimes []time.Time
	// sessionValue is what an approved poll hands back, and the only cookie the
	// account endpoint will answer to.
	sessionValue string
	csrfToken    string
	// slowDownSeconds is the interval a slow_down names, or zero for none.
	slowDownSeconds int
	intervalSeconds int
	// accountCookies records what the login command presented afterwards.
	accountCookies []string
}

func newDeviceLoginServer(t *testing.T) *deviceLoginServer {
	t.Helper()
	fake := &deviceLoginServer{
		answers:         []string{"authorized"},
		sessionValue:    "signed.session.value",
		csrfToken:       "csrf-token-bound-into-the-session",
		intervalSeconds: 1,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/login/device/github/start", func(writer http.ResponseWriter, _ *http.Request) {
		fake.success(writer, map[string]any{
			"device_code":        "the-device-code",
			"user_code":          "WDJB-MJHT",
			"verification_uri":   "https://provider.test/device",
			"interval_seconds":   fake.intervalSeconds,
			"expires_in_seconds": 900,
		})
	})
	mux.HandleFunc("/api/v1/login/device/github/poll", func(writer http.ResponseWriter, request *http.Request) {
		var body struct {
			DeviceCode string `json:"device_code"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil || body.DeviceCode != "the-device-code" {
			fake.failure(writer, http.StatusBadRequest, "OAUTH_STATE_INVALID", "that device code is not one this sign-in can use")
			return
		}
		fake.mu.Lock()
		fake.pollTimes = append(fake.pollTimes, time.Now())
		next := "authorized"
		if len(fake.answers) > 0 {
			next = fake.answers[0]
			fake.answers = fake.answers[1:]
		}
		fake.mu.Unlock()

		switch next {
		case "pending":
			fake.success(writer, map[string]any{"status": "pending", "interval_seconds": 0, "session": nil})
		case "slow_down":
			fake.success(writer, map[string]any{
				"status": "slow_down", "interval_seconds": fake.slowDownSeconds, "session": nil,
			})
		case "authorized":
			fake.success(writer, map[string]any{
				"status": "authorized", "interval_seconds": 0,
				"session": map[string]any{
					"value":      fake.sessionValue,
					"csrf_token": fake.csrfToken,
					"expires_at": time.Now().UTC().Add(12 * time.Hour).Truncate(time.Second).Format(time.RFC3339),
				},
			})
		case "OAUTH_DEVICE_EXPIRED":
			fake.failure(writer, http.StatusBadRequest, next, "the device code expired before it was approved")
		case "OAUTH_DEVICE_DENIED":
			fake.failure(writer, http.StatusForbidden, next, "the sign-in was refused at the provider")
		case "REGISTRATION_CLOSED":
			fake.failure(writer, http.StatusForbidden, next, "this site is not accepting new accounts")
		default:
			fake.failure(writer, http.StatusBadGateway, "OAUTH_EXCHANGE_FAILED", "the identity provider could not complete the device sign-in")
		}
	})
	mux.HandleFunc("/api/v1/account/session", func(writer http.ResponseWriter, request *http.Request) {
		fake.mu.Lock()
		presented := ""
		if cookie, err := request.Cookie("moment_session"); err == nil {
			presented = cookie.Value
		}
		fake.accountCookies = append(fake.accountCookies, presented)
		expected := fake.sessionValue
		fake.mu.Unlock()

		// The whole point of storing a credential is being able to present it.
		// Answering without checking would let a command that never sends the
		// cookie pass this test.
		if presented != expected {
			fake.failure(writer, http.StatusUnauthorized, "UNAUTHORIZED", "owner authentication is required")
			return
		}
		fake.success(writer, map[string]any{
			"expires_at": time.Now().UTC().Add(12 * time.Hour).Format(time.RFC3339),
			"csrf_token": fake.csrfToken,
			"account": map[string]any{
				"user_id": "0123456789abcdef0123456789abcdef", "username": "octocat",
				"display_name": "The Octocat", "bio": "", "avatar_media_id": nil, "cover_media_id": nil,
				"role": "member", "profile_url": "/octocat", "status": "pending", "username_chosen": false,
			},
			"limits": map[string]any{
				"profile_picture_bytes": 8 << 20, "display_name_length": 80, "bio_length": 400,
			},
		})
	})
	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (fake *deviceLoginServer) success(writer http.ResponseWriter, data map[string]any) {
	writer.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"data": data, "meta": map[string]any{"request_id": "req_device"},
	})
}

func (fake *deviceLoginServer) failure(writer http.ResponseWriter, status int, code, message string) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(map[string]any{
		"error": map[string]any{
			"code": code, "message": message, "details": map[string]any{}, "request_id": "req_device",
		},
	})
}

func (fake *deviceLoginServer) polls() int {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return len(fake.pollTimes)
}

// pollGaps reports how long the command waited before each poll, which is the
// only place a backoff is observable from outside it.
func (fake *deviceLoginServer) pollGaps(startedAt time.Time) []time.Duration {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	gaps := make([]time.Duration, 0, len(fake.pollTimes))
	previous := startedAt
	for _, at := range fake.pollTimes {
		gaps = append(gaps, at.Sub(previous))
		previous = at
	}
	return gaps
}

func (fake *deviceLoginServer) presentedCookies() []string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	return append([]string(nil), fake.accountCookies...)
}

func TestLoginWaitsForApprovalThenStoresASessionItCanPresent(t *testing.T) {
	t.Parallel()
	fake := newDeviceLoginServer(t)
	fake.answers = []string{"pending", "authorized"}
	sessionFile := filepath.Join(t.TempDir(), ".moment-local", "session.json")

	started := time.Now()
	output := execute(momentTestApplication(), "",
		"--url", fake.server.URL, "login", "github", "--session-file", sessionFile, "--json")
	if output.exit != ExitSuccess {
		t.Fatalf("login = %#v", output)
	}
	document := decodeOneDocument(t, output.stdout)
	data := document["data"].(map[string]any)
	if data["username"] != "octocat" || data["status"] != "pending" || data["provider"] != "github" {
		t.Fatalf("result = %#v", data)
	}
	if data["session_file"] != sessionFile {
		t.Fatalf("session_file = %v", data["session_file"])
	}

	// The code a person has to type reaches them, and on stderr: stdout carries
	// one machine document and nothing else.
	if !strings.Contains(output.stderr, "WDJB-MJHT") || !strings.Contains(output.stderr, "https://provider.test/device") {
		t.Fatalf("stderr never showed the code to enter:\n%s", output.stderr)
	}
	// And the credential itself never reaches either stream.
	if strings.Contains(output.stdout, fake.sessionValue) || strings.Contains(output.stderr, fake.sessionValue) {
		t.Fatal("the session value was written to the command output")
	}
	if strings.Contains(output.stdout, fake.csrfToken) || strings.Contains(output.stderr, fake.csrfToken) {
		t.Fatal("the CSRF token was written to the command output")
	}

	if fake.polls() != 2 {
		t.Fatalf("polls = %d, want one pending and one approval", fake.polls())
	}
	// The interval was honoured rather than hammered. Two polls at one second
	// cannot have finished sooner than that.
	if elapsed := time.Since(started); elapsed < time.Second {
		t.Fatalf("two polls took %s; the reported interval was not waited out", elapsed)
	}

	stored, err := readStoredSession(sessionFile, mustParseURL(t, fake.server.URL))
	if err != nil {
		t.Fatal(err)
	}
	if stored.Session != fake.sessionValue || stored.CSRFToken != fake.csrfToken {
		t.Fatalf("stored = %+v", stored)
	}
	if stored.BaseURL != fake.server.URL {
		t.Fatalf("base_url = %q; a session is only meaningful to the instance that signed it", stored.BaseURL)
	}
	info, err := os.Stat(sessionFile)
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Fatalf("session file mode = %v, want 0600", info.Mode().Perm())
	}
	directory, err := os.Stat(filepath.Dir(sessionFile))
	if err != nil {
		t.Fatal(err)
	}
	if directory.Mode().Perm() != 0o700 {
		t.Fatalf("session directory mode = %v, want 0700", directory.Mode().Perm())
	}

	// The credential was actually presented, in the one way the server reads
	// one. The account endpoint refuses anything else, so reaching it at all is
	// the assertion.
	presented := fake.presentedCookies()
	if len(presented) != 1 || presented[0] != fake.sessionValue {
		t.Fatalf("presented cookies = %#v", presented)
	}
}

func TestLoginReportsWhyASignInEndedWithoutASession(t *testing.T) {
	t.Parallel()
	for name, probe := range map[string]struct {
		answer string
		code   string
		exit   ExitCode
	}{
		"nobody approved the code in time": {answer: "OAUTH_DEVICE_EXPIRED", code: "OAUTH_DEVICE_EXPIRED", exit: ExitTimeout},
		"a person refused it":              {answer: "OAUTH_DEVICE_DENIED", code: "OAUTH_DEVICE_DENIED", exit: ExitUnauthorized},
		"the site admits nobody new":       {answer: "REGISTRATION_CLOSED", code: "REGISTRATION_CLOSED", exit: ExitConflict},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newDeviceLoginServer(t)
			fake.answers = []string{probe.answer}
			sessionFile := filepath.Join(t.TempDir(), "session.json")

			output := execute(momentTestApplication(), "",
				"--url", fake.server.URL, "login", "github", "--session-file", sessionFile, "--json")
			if output.exit != probe.exit {
				t.Fatalf("exit = %d, want %d: %s", output.exit, probe.exit, output.stdout)
			}
			failure := decodeOneDocument(t, output.stdout)["error"].(map[string]any)
			if failure["code"] != probe.code {
				t.Fatalf("code = %v, want %q", failure["code"], probe.code)
			}
			if failure["suggestion"] == "" {
				t.Fatal("a refused sign-in offered nothing to do next")
			}
			// Nothing was stored, so a failed login cannot leave a file behind
			// that a later command mistakes for being signed in.
			if _, err := os.Stat(sessionFile); !os.IsNotExist(err) {
				t.Fatalf("a failed login left a session file behind: %v", err)
			}
		})
	}
}

func TestLoginRequiresExactlyOneProvider(t *testing.T) {
	// Three commands once shipped without declaring their positional argument,
	// and cobra read the argument as an unknown subcommand. Declaring it is what
	// makes `moment login github` reach the handler at all.
	for name, args := range map[string][]string{
		"no provider":  {"login"},
		"two of them":  {"login", "github", "google"},
		"an empty one": {"login", ""},
	} {
		t.Run(name, func(t *testing.T) {
			output := execute(momentTestApplication(), "", append([]string{"--url", "http://127.0.0.1:1"}, append(args, "--json")...)...)
			if output.exit != ExitInvalidInput {
				t.Fatalf("exit = %d, want %d: %s", output.exit, ExitInvalidInput, output.stdout)
			}
			failure := decodeOneDocument(t, output.stdout)["error"].(map[string]any)
			if failure["code"] != "INVALID_ARGUMENT" {
				t.Fatalf("code = %v", failure["code"])
			}
		})
	}

	// A name that is not a bare provider key never becomes part of a path.
	for _, hostile := range []string{"../owner/session", "github/../..", "GitHub!"} {
		output := execute(momentTestApplication(), "", "--url", "http://127.0.0.1:1", "login", hostile, "--json")
		if output.exit != ExitInvalidInput {
			t.Fatalf("provider %q = %#v, want a refusal", hostile, output)
		}
	}
}

// Being told to slow down has to slow this command down in particular, not
// merely be representable. The loop treats an unrecognised status as a broken
// server, so a slow_down that fell through would end the sign-in rather than
// pace it -- and the person waiting would never know why.
func TestLoginBacksOffWhenTheServerAsksItTo(t *testing.T) {
	t.Parallel()
	fake := newDeviceLoginServer(t)
	fake.answers = []string{"slow_down", "authorized"}
	sessionFile := filepath.Join(t.TempDir(), "session.json")

	startedAt := time.Now()
	output := execute(momentTestApplication(), "",
		"--url", fake.server.URL, "login", "github", "--session-file", sessionFile, "--json")
	if output.exit != ExitSuccess {
		t.Fatalf("login = %#v", output)
	}
	gaps := fake.pollGaps(startedAt)
	if len(gaps) != 2 {
		t.Fatalf("polls = %d, want one slow_down and one approval", len(gaps))
	}
	// The step the device flow specifies is five seconds, so the second wait is
	// the reported interval plus that, and strictly longer than the first.
	want := time.Duration(fake.intervalSeconds)*time.Second + deviceSlowDownStep
	if gaps[1] < want || gaps[1] <= gaps[0] {
		t.Fatalf("waits = %v; the second poll did not back off to %s", gaps, want)
	}
}

// Being told to slow down has to actually slow the caller down. A provider that
// says so and is polled at the same rate is one that stops answering this
// deployment at all, so the interval is required to rise whatever was reported.
func TestSlowDownAlwaysLengthensTheInterval(t *testing.T) {
	for name, probe := range map[string]struct {
		current  time.Duration
		reported int
		want     time.Duration
	}{
		"the provider names a longer interval":  {current: 5 * time.Second, reported: 30, want: 30 * time.Second},
		"the provider names the same one again": {current: 5 * time.Second, reported: 5, want: 10 * time.Second},
		"the provider names a shorter one":      {current: 10 * time.Second, reported: 2, want: 15 * time.Second},
		"the provider names none at all":        {current: 5 * time.Second, reported: 0, want: 10 * time.Second},
	} {
		t.Run(name, func(t *testing.T) {
			next := nextPollInterval(probe.current, probe.reported)
			if next != probe.want {
				t.Fatalf("next interval = %s, want %s", next, probe.want)
			}
			if next <= probe.current {
				t.Fatalf("interval %s did not rise above %s", next, probe.current)
			}
		})
	}
}

// A stored session is only useful if it can be presented, and the server takes
// it in exactly one shape: the cookie it issued, plus the CSRF token echoed on
// anything that writes.
func TestAStoredSessionIsPresentedAsACookieAndACSRFHeader(t *testing.T) {
	credential := storedSession{
		BaseURL:   "http://127.0.0.1:8080",
		Session:   "signed.session.value",
		CSRFToken: "csrf-token",
	}
	for method, wantCSRF := range map[string]bool{
		http.MethodGet:    false,
		http.MethodHead:   false,
		http.MethodPost:   true,
		http.MethodPatch:  true,
		http.MethodDelete: true,
	} {
		t.Run(method, func(t *testing.T) {
			request := httptest.NewRequest(method, "http://127.0.0.1:8080/api/v1/account/profile", nil)
			presentSession(request, credential)

			cookie, err := request.Cookie("moment_session")
			if err != nil || cookie.Value != credential.Session {
				t.Fatalf("cookie = %v (err %v)", cookie, err)
			}
			got := request.Header.Get("X-CSRF-Token")
			if wantCSRF && got != credential.CSRFToken {
				t.Fatalf("%s sent CSRF header %q; the gate refuses an unsafe cookie request without it", method, got)
			}
			if !wantCSRF && got != "" {
				t.Fatalf("%s sent a CSRF header it does not need: %q", method, got)
			}
		})
	}
}

func TestLoginRefusesAStoredSessionFileThatIsNotOne(t *testing.T) {
	directory := t.TempDir()
	instance := mustParseURL(t, "http://x")
	for name, contents := range map[string]string{
		"not JSON at all":        "signed.session.value\n",
		"an empty session":       `{"base_url":"http://x","session":"","csrf_token":"c","expires_at":"2026-01-01T00:00:00Z"}`,
		"a field we do not know": `{"session":"v","csrf_token":"c","base_url":"http://x","expires_at":"2026-01-01T00:00:00Z","role":"owner"}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(directory, strings.ReplaceAll(name, " ", "-")+".json")
			if err := os.WriteFile(path, []byte(contents), 0o600); err != nil {
				t.Fatal(err)
			}
			if _, err := readStoredSession(path, instance); err == nil {
				t.Fatal("a file that is not a stored session was accepted as one")
			}
		})
	}
	if _, err := readStoredSession(filepath.Join(directory, "absent.json"), instance); err == nil {
		t.Fatal("a missing session file was accepted")
	}
}

// The instance is recorded beside the credential so that presenting a session
// to a server that never issued it is refused here rather than discovered from
// that server's answer. It was recorded and never read, which made the field a
// note to nobody.
func TestAStoredSessionIsRefusedByEveryOtherInstance(t *testing.T) {
	directory := t.TempDir()
	write := func(t *testing.T, name, baseURL string) string {
		t.Helper()
		path := filepath.Join(directory, name+".json")
		if err := writeStoredSession(path, storedSession{
			BaseURL:   baseURL,
			Session:   "signed.session.value",
			CSRFToken: "csrf-token",
			ExpiresAt: time.Now().UTC().Add(time.Hour).Truncate(time.Second),
		}); err != nil {
			t.Fatal(err)
		}
		return path
	}

	issued := write(t, "issued", "http://127.0.0.1:8080")
	for name, presentedTo := range map[string]string{
		"another port":      "http://127.0.0.1:9090",
		"another host":      "http://moments.example",
		"another scheme":    "https://127.0.0.1:8080",
		"another base path": "http://127.0.0.1:8080/journal",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := readStoredSession(issued, mustParseURL(t, presentedTo)); err == nil {
				t.Fatalf("a session issued elsewhere was accepted for %s", presentedTo)
			}
		})
	}

	// The same server spelt the two ways --url accepts is one server. --url is
	// kept exactly as typed, so a guard comparing text would refuse the
	// credential it had just written.
	for _, spelling := range []string{"http://127.0.0.1:8080", "http://127.0.0.1:8080/"} {
		if _, err := readStoredSession(issued, mustParseURL(t, spelling)); err != nil {
			t.Fatalf("the issuing server spelt %q was refused its own session: %v", spelling, err)
		}
	}

	// A file naming no instance at all is exactly the file this field exists to
	// prevent, so it is not admitted by default.
	anonymous := write(t, "anonymous", "")
	if _, err := readStoredSession(anonymous, mustParseURL(t, "http://127.0.0.1:8080")); err == nil {
		t.Fatal("a session naming no instance was accepted")
	}
}

func mustParseURL(t *testing.T, value string) *url.URL {
	t.Helper()
	parsed, err := url.Parse(value)
	if err != nil {
		t.Fatal(err)
	}
	return parsed
}
