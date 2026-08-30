package cli

import (
	"context"
	"errors"
	"testing"
	"testing/fstest"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/auth/oauth"
	"github.com/moment/moment/internal/observability"
	"github.com/moment/moment/internal/runtimefs"
	"github.com/moment/moment/internal/server"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

// recordedEvent is one line the server would have written to stderr. Warnings
// are the only place a discarded configuration is visible, so a test that wants
// to assert one has to read them.
type recordedEvent struct {
	surface observability.Surface
	level   string
	event   string
	fields  map[string]any
}

func newLiveHostForTest(t *testing.T, runtimeState *runtimefs.Runtime, listen string) (*liveServerHost, *[]recordedEvent) {
	t.Helper()
	manager, err := auth.NewManager(runtimeState.Root, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	exporter, err := archive.NewExporter(archive.Options{DataDir: runtimeState.Root, AppVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	committer, err := archive.NewCommitter(archive.CommitOptions{DataDir: runtimeState.Root, AppVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	events := make([]recordedEvent, 0, 2)
	host := &liveServerHost{
		board:         server.NewSwitchboard(),
		auth:          manager,
		assets:        fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("Moment")}},
		listenAddress: listen,
		exporter:      exporter,
		log: func(surface observability.Surface, level, event string, fields map[string]any) {
			events = append(events, recordedEvent{surface: surface, level: level, event: event, fields: fields})
		},
	}
	host.committer = &hostedImportCommitter{core: committer, host: host}
	t.Cleanup(func() { _ = host.CloseActive() })
	return host, &events
}

func testSiteService(t *testing.T, runtimeState *runtimefs.Runtime) *app.SiteService {
	t.Helper()
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	repository, err := sqlitestore.NewSiteRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSiteService(repository, app.SiteOptions{AllowDevelopmentLocalHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// clearProviderCredentials keeps a developer's own environment out of the
// answers below. These tests decide what is configured; inheriting a real
// client id would put a live provider on the other end of one of them.
func clearProviderCredentials(t *testing.T) {
	t.Helper()
	for _, name := range []string{
		"MOMENT_GOOGLE_CLIENT_ID", "MOMENT_GOOGLE_CLIENT_SECRET",
		"MOMENT_GITHUB_CLIENT_ID", "MOMENT_GITHUB_CLIENT_SECRET",
	} {
		t.Setenv(name, "")
	}
}

func TestOnlyAnAddressThisMachineAnswersAloneCountsAsLoopback(t *testing.T) {
	for address, want := range map[string]bool{
		"127.0.0.1": true,
		"127.0.0.2": true,
		"localhost": true,
		"::1":       true,
		// `--listen :8080` behind a reverse proxy splits to an empty host, and
		// it is a wildcard bind rather than a loopback one.
		"":             false,
		"0.0.0.0":      false,
		"::":           false,
		"192.168.1.10": false,
		"example.test": false,
	} {
		t.Run(address, func(t *testing.T) {
			if got := isLoopbackHost(address); got != want {
				t.Fatalf("isLoopbackHost(%q) = %t, want %t", address, got, want)
			}
		})
	}
}

// A wildcard bind has no address a provider could have a redirect URI
// registered against, so it offers no sign-in. What it must not do is discard
// an operator's configured credentials in silence: an empty provider list is
// byte-identical to a build nobody configured, and the operator followed
// .env.example exactly.
func TestAWildcardBindOffersNoSignInAndReportsWhatItDiscarded(t *testing.T) {
	runtimeState, _ := initializeSiteCLIData(t)
	site := testSiteService(t, runtimeState)
	clearProviderCredentials(t)
	t.Setenv("MOMENT_GITHUB_CLIENT_ID", "Iv1.0123456789abcdef")
	t.Setenv("MOMENT_GITHUB_CLIENT_SECRET", "github-client-secret")

	for name, listen := range map[string]string{
		"behind a reverse proxy":     ":8080",
		"as the shipped image binds": "0.0.0.0:8080",
		"on every v6 interface":      "[::]:8080",
	} {
		t.Run(name, func(t *testing.T) {
			host, events := newLiveHostForTest(t, runtimeState, listen)
			providers, err := host.identityProviders(context.Background(), site)
			if err != nil {
				t.Fatal(err)
			}
			if !providers.Empty() {
				t.Fatalf("%s registered %v against no canonical address", listen, providers.Names())
			}
			warned := false
			for _, recorded := range *events {
				if recorded.event != "oauth_credentials_unused" {
					continue
				}
				warned = true
				if recorded.level != "warn" || recorded.surface != observability.SurfaceSystem {
					t.Fatalf("discarded credentials reported as %s on %s", recorded.level, recorded.surface)
				}
				configured, _ := recorded.fields["providers"].([]string)
				if len(configured) != 1 || configured[0] != oauth.GitHubName {
					t.Fatalf("warning named %#v", recorded.fields["providers"])
				}
				// The credentials themselves are configuration, not a subject
				// for a log line.
				if recorded.fields["client_id"] != nil || recorded.fields["client_secret"] != nil {
					t.Fatalf("warning carried credential material: %#v", recorded.fields)
				}
			}
			if !warned {
				t.Fatalf("%s discarded configured credentials without a word: %#v", listen, *events)
			}
		})
	}

	// The same credentials on a loopback bind are used, which is what makes the
	// silence above a defect rather than the intended answer.
	host, events := newLiveHostForTest(t, runtimeState, "127.0.0.1:8080")
	providers, err := host.identityProviders(context.Background(), site)
	if err != nil {
		t.Fatal(err)
	}
	if names := providers.Names(); len(names) != 1 || names[0] != oauth.GitHubName {
		t.Fatalf("loopback registered %v", names)
	}
	for _, recorded := range *events {
		if recorded.event == "oauth_credentials_unused" {
			t.Fatalf("credentials that were used were reported as discarded: %#v", recorded)
		}
	}
}

// Restoring the previous generation after a failed import commit is
// host.CloseActive followed by host.Activate, and Activate is where the
// identity providers are built. The second activation below is that rollback,
// with the provider made unreachable first: anything that still went looking
// for it would fail exactly the way an egress-restricted network does, and turn
// one failure into two.
func TestRestoringThePreviousGenerationDoesNotDependOnReachingGoogle(t *testing.T) {
	runtimeState, _ := initializeSiteCLIData(t)
	clearProviderCredentials(t)
	t.Setenv("MOMENT_GOOGLE_CLIENT_ID", "client-123.apps.googleusercontent.com")
	t.Setenv("MOMENT_GOOGLE_CLIENT_SECRET", "google-client-secret")

	discoveries := 0
	previous := newGoogleProvider
	newGoogleProvider = func(context.Context, oauth.Credentials, string, string) (*oauth.Google, error) {
		discoveries++
		if discoveries > 1 {
			return nil, errors.New("accounts.google.com is unreachable")
		}
		// A stand-in for a discovered adapter. Nothing signs in through it;
		// what is under test is whether it gets built a second time.
		return &oauth.Google{}, nil
	}
	t.Cleanup(func() { newGoogleProvider = previous })

	host, _ := newLiveHostForTest(t, runtimeState, "127.0.0.1:8080")
	if err := host.Activate(context.Background(), runtimeState); err != nil {
		t.Fatalf("first activation: %v", err)
	}
	if err := host.CloseActive(); err != nil {
		t.Fatal(err)
	}
	if err := host.Activate(context.Background(), runtimeState); err != nil {
		t.Fatalf("the rollback failed because a third party was unreachable: %v", err)
	}
	if discoveries != 1 {
		t.Fatalf("discovery ran %d times; recovery went back to the network", discoveries)
	}
}

// Reuse is keyed by the callback address rather than unconditional, because an
// imported generation can carry a different public_base_url and a provider
// matches a redirect URI character for character. Handing back an adapter built
// for the old address would register a redirect URI nobody accepts.
func TestAChangedCallbackAddressBuildsTheGoogleAdapterAgain(t *testing.T) {
	built := make([]string, 0, 3)
	previous := newGoogleProvider
	newGoogleProvider = func(_ context.Context, _ oauth.Credentials, callback, _ string) (*oauth.Google, error) {
		built = append(built, callback)
		return &oauth.Google{}, nil
	}
	t.Cleanup(func() { newGoogleProvider = previous })

	host := &liveServerHost{}
	credentials := oauth.Credentials{ClientID: "client", ClientSecret: "secret"}
	first := "https://moments.example/api/v1/login/oauth/google/callback"
	second := "https://elsewhere.example/api/v1/login/oauth/google/callback"
	for _, callback := range []string{first, first, second, second, first} {
		if _, err := host.googleProvider(context.Background(), credentials, callback, ""); err != nil {
			t.Fatal(err)
		}
	}
	if len(built) != 3 || built[0] != first || built[1] != second || built[2] != first {
		t.Fatalf("adapters built for %#v", built)
	}
}
