package oauth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/moment/moment/internal/app"
)

// fakeGitHub speaks enough of GitHub's OAuth App flow to exercise the real
// adapter.
//
// GitHub is not OpenID Connect. There is no signed token to inspect, so
// everything this adapter concludes about a person is assembled from two REST
// calls -- and the shapes those calls take for ordinary accounts (a profile
// with a null email, a noreply primary address, a token the addresses endpoint
// refuses) are exactly the ones a developer never sees while testing against
// their own account. Answering the same protocol is the only way to run them.
type fakeGitHub struct {
	server *httptest.Server

	// user is the GET /user body, held as a map so a test can omit a field
	// rather than only blank it, and so email can be an explicit null.
	user map[string]any
	// emails is the GET /user/emails body.
	emails []map[string]any

	// Each of these bends one answer out of shape.
	userStatus   int
	emailsStatus int
	userBody     string
	emailsBody   string
	errorBody    string
	// formEncodedToken answers the token endpoint the way GitHub does by
	// default: x-www-form-urlencoded rather than JSON.
	formEncodedToken bool
	// deadEmailsURL points the addresses call at a port nothing listens on.
	deadEmailsURL bool
	// tokenError is the code GitHub reports for a code it will not honour --
	// which it does with HTTP 200, not a 4xx.
	tokenError string

	// What the adapter actually sent.
	lastTokenForm url.Values
	userHeader    http.Header
	emailsHeader  http.Header
	emailsCalls   int
}

func newFakeGitHub(t *testing.T) *fakeGitHub {
	t.Helper()
	fake := &fakeGitHub{
		user: map[string]any{
			"id":    583231,
			"login": "octocat",
			"name":  "The Octocat",
			// The default GitHub encourages: "Keep my email address private"
			// makes this null for a large share of real accounts.
			"email":      nil,
			"avatar_url": "https://avatars.githubusercontent.com/u/583231?v=4",
		},
		emails: []map[string]any{
			{"email": "octocat@example.test", "primary": true, "verified": true},
		},
		errorBody: `{"message":"Bad credentials","documentation_url":"https://docs.github.com/rest"}`,
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/login/oauth/access_token", func(writer http.ResponseWriter, request *http.Request) {
		_ = request.ParseForm()
		fake.lastTokenForm = request.PostForm
		if fake.tokenError != "" {
			// GitHub answers a reused or expired code with 200 and an error
			// body, not with a 4xx.
			writer.Header().Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
			_, _ = io.WriteString(writer, url.Values{
				"error":             {fake.tokenError},
				"error_description": {"The code passed is incorrect or expired."},
				"error_uri":         {"https://docs.github.com/apps/troubleshooting"},
			}.Encode())
			return
		}
		if fake.formEncodedToken {
			writer.Header().Set("Content-Type", "application/x-www-form-urlencoded; charset=utf-8")
			_, _ = io.WriteString(writer, url.Values{
				"access_token": {"gho_token"},
				"scope":        {"read:user,user:email"},
				"token_type":   {"bearer"},
			}.Encode())
			return
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"access_token": "gho_token", "token_type": "bearer", "scope": "read:user,user:email",
		})
	})
	mux.HandleFunc("/user", func(writer http.ResponseWriter, request *http.Request) {
		fake.userHeader = request.Header.Clone()
		fake.answer(writer, fake.userStatus, fake.userBody, fake.user)
	})
	mux.HandleFunc("/user/emails", func(writer http.ResponseWriter, request *http.Request) {
		fake.emailsCalls++
		fake.emailsHeader = request.Header.Clone()
		fake.answer(writer, fake.emailsStatus, fake.emailsBody, fake.emails)
	})

	fake.server = httptest.NewServer(mux)
	t.Cleanup(fake.server.Close)
	return fake
}

func (fake *fakeGitHub) answer(writer http.ResponseWriter, status int, raw string, body any) {
	writer.Header().Set("Content-Type", "application/json")
	if status == 0 {
		status = http.StatusOK
	}
	writer.WriteHeader(status)
	switch {
	case status < 200 || status >= 300:
		_, _ = io.WriteString(writer, fake.errorBody)
	case raw != "":
		_, _ = io.WriteString(writer, raw)
	default:
		_ = json.NewEncoder(writer).Encode(body)
	}
}

func (fake *fakeGitHub) provider(t *testing.T) *GitHub {
	t.Helper()
	emails := fake.server.URL + "/user/emails"
	if fake.deadEmailsURL {
		emails = "http://" + unusedAddress(t) + "/user/emails"
	}
	provider := newGitHubAt(
		Credentials{ClientID: "Iv1.0123456789abcdef", ClientSecret: "secret"},
		"http://127.0.0.1:8080/api/v1/login/oauth/github/callback",
		githubEndpoints{
			authorize: fake.server.URL + "/login/oauth/authorize",
			token:     fake.server.URL + "/login/oauth/access_token",
			user:      fake.server.URL + "/user",
			emails:    emails,
		})
	if provider == nil {
		t.Fatal("provider was not configured")
	}
	return provider
}

// exchange runs the whole flow the way a callback handler would.
func (fake *fakeGitHub) exchange(t *testing.T) (app.ExternalIdentity, error) {
	t.Helper()
	return fake.provider(t).Exchange(context.Background(), "the-code", "the-verifier", "")
}

// unusedAddress is a loopback address nothing is listening on, which is how a
// transport-level failure of one call is staged.
func unusedAddress(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func TestGitHubExchangeReportsTheAccountAndCarriesPKCEToTheTokenEndpoint(t *testing.T) {
	fake := newFakeGitHub(t)

	identity, err := fake.exchange(t)
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if identity.Provider != GitHubName || identity.ProviderUserID != "583231" {
		t.Fatalf("identity = %+v", identity)
	}
	if identity.Email != "octocat@example.test" || !identity.EmailVerified || !identity.EmailDeliverable {
		t.Fatalf("email fields = %+v", identity)
	}
	if identity.DisplayName != "The Octocat" {
		t.Fatalf("display name = %q", identity.DisplayName)
	}
	if identity.AvatarURL != "https://avatars.githubusercontent.com/u/583231?v=4" {
		t.Fatalf("avatar = %q", identity.AvatarURL)
	}
	// The profile answered email: null, so that address can only have come
	// from the addresses endpoint.
	if fake.emailsCalls != 1 {
		t.Fatalf("addresses endpoint called %d times", fake.emailsCalls)
	}

	// PKCE actually reached the token endpoint. Without this assertion the
	// verifier could be dropped and every other test in this file stays green.
	if got := fake.lastTokenForm.Get("code_verifier"); got != "the-verifier" {
		t.Fatalf("code_verifier posted = %q", got)
	}
	if got := fake.lastTokenForm.Get("grant_type"); got != "authorization_code" {
		t.Fatalf("grant_type = %q", got)
	}
	if got := fake.lastTokenForm.Get("code"); got != "the-code" {
		t.Fatalf("code = %q", got)
	}
}

func TestGitHubAuthCodeURLCarriesS256PKCEAndOnlyTheIdentityScopes(t *testing.T) {
	fake := newFakeGitHub(t)

	parsed, err := url.Parse(fake.provider(t).AuthCodeURL("the-state", "the-verifier", ""))
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("state") != "the-state" {
		t.Fatalf("state = %q", query.Get("state"))
	}
	if query.Get("response_type") != "code" {
		t.Fatalf("response_type = %q", query.Get("response_type"))
	}
	// GitHub refuses the plain challenge method outright, so getting this
	// wrong breaks sign-in against the real host and nothing else.
	if query.Get("code_challenge_method") != "S256" {
		t.Fatalf("challenge method = %q, want S256", query.Get("code_challenge_method"))
	}
	digest := sha256.Sum256([]byte("the-verifier"))
	if want := base64.RawURLEncoding.EncodeToString(digest[:]); query.Get("code_challenge") != want {
		t.Fatalf("code_challenge = %q, want %q", query.Get("code_challenge"), want)
	}
	for _, scope := range strings.FieldsFunc(query.Get("scope"), func(r rune) bool { return r == ' ' || r == ',' }) {
		switch scope {
		case "read:user", "user:email":
		default:
			t.Fatalf("unexpected scope %q", scope)
		}
	}
	if !strings.Contains(query.Get("scope"), "user:email") {
		t.Fatalf("scope = %q, without user:email no address is ever readable", query.Get("scope"))
	}
}

func TestGitHubPicksThePrimaryVerifiedAddressRatherThanTheFirstOne(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.emails = []map[string]any{
		{"email": "old@example.test", "primary": false, "verified": true},
		{"email": "unconfirmed@example.test", "primary": true, "verified": false},
		{"email": "chosen@example.test", "primary": true, "verified": true},
		{"email": "also@example.test", "primary": false, "verified": true},
	}

	identity, err := fake.exchange(t)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Email != "chosen@example.test" {
		t.Fatalf("email = %q, want the primary verified address", identity.Email)
	}
	if !identity.EmailVerified || !identity.EmailDeliverable {
		t.Fatalf("email fields = %+v", identity)
	}
}

func TestGitHubKeepsANoReplyPrimaryAddressButNeverAsDeliverable(t *testing.T) {
	for name, address := range map[string]string{
		"the numbered form":   "12345+octocat@users.noreply.github.com",
		"the older bare form": "octocat@users.noreply.github.com",
		"in mixed case":       "12345+Octocat@Users.NoReply.GitHub.COM",
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			fake.emails = []map[string]any{{"email": address, "primary": true, "verified": true}}

			identity, err := fake.exchange(t)
			if err != nil {
				t.Fatal(err)
			}
			// Worth keeping: it is the address GitHub will use, and it
			// identifies the account.
			if identity.Email != address {
				t.Fatalf("the address was not kept: %q", identity.Email)
			}
			// GitHub did verify it belongs to this account.
			if !identity.EmailVerified {
				t.Fatalf("a verified noreply address was reported unverified: %+v", identity)
			}
			// Nothing will ever arrive there.
			if identity.EmailDeliverable {
				t.Fatal("a noreply address was reported deliverable")
			}
		})
	}
}

func TestGitHubFallsBackToTheProfileAddressWhenTheAddressesCallCannotBeUsed(t *testing.T) {
	for name, bend := range map[string]func(*fakeGitHub){
		"the token carries no user:email scope": func(fake *fakeGitHub) {
			fake.emailsStatus = http.StatusForbidden
		},
		"the endpoint cannot be reached at all": func(fake *fakeGitHub) {
			fake.deadEmailsURL = true
		},
		"the answer is not the shape we expect": func(fake *fakeGitHub) {
			fake.emailsBody = `{"message":"Requires authentication"}`
		},
		"the answer is larger than we will hold": func(fake *fakeGitHub) {
			fake.emailsBody = `[{"email":"` + strings.Repeat("a", maxGitHubResponse) +
				`@example.test","primary":true,"verified":true}]`
		},
		"the list comes back empty": func(fake *fakeGitHub) {
			fake.emails = nil
		},
		"no address is both primary and verified": func(fake *fakeGitHub) {
			fake.emails = []map[string]any{
				{"email": "secondary@example.test", "primary": false, "verified": true},
				{"email": "unconfirmed@example.test", "primary": true, "verified": false},
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			fake.user["email"] = "public@example.test"
			bend(fake)

			identity, err := fake.exchange(t)
			if err != nil {
				t.Fatalf("a failed addresses call must not fail sign-in: %v", err)
			}
			if identity.Email != "public@example.test" {
				t.Fatalf("email = %q", identity.Email)
			}
			// GitHub published that address; nothing here verified it.
			if identity.EmailVerified {
				t.Fatal("the public profile address was reported as verified")
			}
			if !identity.EmailDeliverable {
				t.Fatal("an ordinary address was reported undeliverable")
			}
		})
	}
}

func TestGitHubProfileNoReplyAddressIsNeitherVerifiedNorDeliverable(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.emailsStatus = http.StatusForbidden
	fake.user["email"] = "583231+octocat@users.noreply.github.com"

	identity, err := fake.exchange(t)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Email != "583231+octocat@users.noreply.github.com" {
		t.Fatalf("email = %q", identity.Email)
	}
	if identity.EmailVerified || identity.EmailDeliverable {
		t.Fatalf("an unverified noreply address was trusted: %+v", identity)
	}
}

func TestGitHubSignsInWithNoAddressAtAll(t *testing.T) {
	fake := newFakeGitHub(t)
	// Private profile address plus a token the addresses endpoint refuses.
	fake.emailsStatus = http.StatusForbidden

	identity, err := fake.exchange(t)
	if err != nil {
		t.Fatalf("no address is a supported outcome, not a failure: %v", err)
	}
	if identity.Email != "" || identity.EmailVerified || identity.EmailDeliverable {
		t.Fatalf("email fields = %+v", identity)
	}
	// The account is still identified, which is what makes this usable.
	if identity.ProviderUserID != "583231" || identity.DisplayName != "The Octocat" {
		t.Fatalf("identity = %+v", identity)
	}
}

func TestGitHubIdentityIsKeyedOnTheNumericIDAndNotTheLogin(t *testing.T) {
	fake := newFakeGitHub(t)
	before, err := fake.exchange(t)
	if err != nil {
		t.Fatal(err)
	}

	// A login can be changed by its owner, and once released anyone may
	// register it. Keying on it is how one account gets served another's.
	fake.user["login"] = "someone-else"
	renamed, err := fake.exchange(t)
	if err != nil {
		t.Fatal(err)
	}
	if renamed.ProviderUserID != before.ProviderUserID {
		t.Fatalf("a rename moved the identity: %q -> %q", before.ProviderUserID, renamed.ProviderUserID)
	}
	if renamed.ProviderUserID == "octocat" || renamed.ProviderUserID == "someone-else" {
		t.Fatalf("the login is being used as the key: %q", renamed.ProviderUserID)
	}

	// A different account is a different identity even under the same login.
	fake.user["id"] = 999001
	other, err := fake.exchange(t)
	if err != nil {
		t.Fatal(err)
	}
	if other.ProviderUserID == before.ProviderUserID {
		t.Fatalf("two accounts share one key: %q", other.ProviderUserID)
	}
	if other.ProviderUserID != "999001" {
		t.Fatalf("provider user id = %q", other.ProviderUserID)
	}
}

func TestGitHubRefusesAnAccountWithoutANumericID(t *testing.T) {
	for name, bend := range map[string]func(*fakeGitHub){
		"the id is zero":    func(fake *fakeGitHub) { fake.user["id"] = 0 },
		"the id is missing": func(fake *fakeGitHub) { delete(fake.user, "id") },
		"the id is null":    func(fake *fakeGitHub) { fake.user["id"] = nil },
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeGitHub(t)
			bend(fake)

			identity, err := fake.exchange(t)
			if err == nil {
				t.Fatalf("an identity was built with provider_user_id %q", identity.ProviderUserID)
			}
			if !errors.Is(err, ErrUnavailable) {
				t.Fatalf("error = %v, want one wrapping ErrUnavailable", err)
			}
		})
	}
}

func TestGitHubDisplayNameFallsBackToTheLogin(t *testing.T) {
	for label, expectation := range map[string]struct {
		name  any
		login any
		want  string
	}{
		"a name is given":          {name: "The Octocat", login: "octocat", want: "The Octocat"},
		"only a login is given":    {name: nil, login: "octocat", want: "octocat"},
		"the name is only spaces":  {name: "   ", login: "octocat", want: "octocat"},
		"neither is given":         {name: nil, login: nil, want: ""},
		"both are only whitespace": {name: " ", login: "\t ", want: ""},
	} {
		t.Run(label, func(t *testing.T) {
			fake := newFakeGitHub(t)
			fake.user["name"] = expectation.name
			fake.user["login"] = expectation.login

			identity, err := fake.exchange(t)
			if err != nil {
				t.Fatal(err)
			}
			if identity.DisplayName != expectation.want {
				t.Fatalf("display name = %q, want %q", identity.DisplayName, expectation.want)
			}
		})
	}
}

func TestGitHubExchangeAcceptsAFormEncodedTokenResponse(t *testing.T) {
	fake := newFakeGitHub(t)
	// GitHub's token endpoint answers in form encoding unless the caller sends
	// Accept: application/json, and this adapter sends no such header -- it
	// relies on x/oauth2 choosing its parser from the response Content-Type.
	// Nothing else here would notice if that stopped being true.
	fake.formEncodedToken = true

	identity, err := fake.exchange(t)
	if err != nil {
		t.Fatalf("a form-encoded token response was rejected: %v", err)
	}
	if identity.ProviderUserID != "583231" || identity.Email != "octocat@example.test" {
		t.Fatalf("identity = %+v", identity)
	}
	// And the access token really was recovered from that body: the API calls
	// went out bearing it.
	if got := fake.userHeader.Get("Authorization"); got != "Bearer gho_token" {
		t.Fatalf("Authorization = %q", got)
	}
}

func TestGitHubAPICallsIdentifyThemselvesToTheAPI(t *testing.T) {
	fake := newFakeGitHub(t)
	if _, err := fake.exchange(t); err != nil {
		t.Fatal(err)
	}

	for label, header := range map[string]http.Header{
		"the profile call":   fake.userHeader,
		"the addresses call": fake.emailsHeader,
	} {
		t.Run(label, func(t *testing.T) {
			if header == nil {
				t.Fatal("the call was never made")
			}
			if got := header.Get("Authorization"); got != "Bearer gho_token" {
				t.Fatalf("Authorization = %q", got)
			}
			if got := header.Get("Accept"); got != "application/vnd.github+json" {
				t.Fatalf("Accept = %q", got)
			}
			// An unpinned version is a silent breaking change waiting to land.
			if header.Get("X-GitHub-Api-Version") == "" {
				t.Fatal("no API version was pinned")
			}
		})
	}
}

func TestGitHubReportsARefusedCallWithoutEchoingTheRemoteBody(t *testing.T) {
	fake := newFakeGitHub(t)
	fake.userStatus = http.StatusUnauthorized
	fake.errorBody = `{"message":"Bad credentials","hint":"gho_secretlookingvalue","documentation_url":"https://docs.github.com/rest"}`

	_, err := fake.exchange(t)
	if err == nil {
		t.Fatal("a refused profile call was treated as a sign-in")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want one wrapping ErrUnavailable", err)
	}
	message := err.Error()
	for _, leaked := range []string{"Bad credentials", "gho_secretlookingvalue", "documentation_url"} {
		if strings.Contains(message, leaked) {
			t.Fatalf("the remote body reached the error message: %q", message)
		}
	}
	// The status is ours to report, and it is what an operator needs.
	if !strings.Contains(message, "401") {
		t.Fatalf("error = %q, without the status there is nothing to act on", message)
	}
}

func TestGitHubRefusesAProfileLargerThanItWillHold(t *testing.T) {
	// The ceiling has to stay a ceiling. Sizing the body off the constant alone
	// would let someone raise it to a gigabyte and keep this test green, so the
	// bound itself is asserted first.
	if maxGitHubResponse > 4<<20 {
		t.Fatalf("maxGitHubResponse = %d, large enough that a hostile answer is worth holding", maxGitHubResponse)
	}

	fake := newFakeGitHub(t)
	// Valid JSON, just past the ceiling. Nothing about this process should be
	// decided by how much a remote host feels like sending.
	fake.userBody = `{"id":583231,"login":"octocat","name":"` + strings.Repeat("A", maxGitHubResponse) + `"}`

	_, err := fake.exchange(t)
	if err == nil {
		t.Fatal("an unbounded response was accepted")
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want one wrapping ErrUnavailable", err)
	}
	if strings.Contains(err.Error(), "AAAA") || len(err.Error()) > 200 {
		t.Fatalf("the oversized body reached the error message (%d bytes)", len(err.Error()))
	}
}

func TestGitHubRefusesATokenEndpointErrorReportedWithA200(t *testing.T) {
	fake := newFakeGitHub(t)
	// A reused or expired code is the most common real failure of this flow,
	// and GitHub reports it with a 200 carrying an error field rather than a
	// 4xx. Nothing downstream should mistake that for a signed-in person.
	fake.tokenError = "bad_verification_code"

	identity, err := fake.exchange(t)
	if err == nil {
		t.Fatalf("a refused code produced an identity: %+v", identity)
	}
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v, want one wrapping ErrUnavailable", err)
	}
	// And no API call went out carrying whatever an empty token would be.
	if fake.userHeader != nil {
		t.Fatalf("the profile was fetched with Authorization %q", fake.userHeader.Get("Authorization"))
	}
	if fake.emailsCalls != 0 {
		t.Fatal("the addresses endpoint was called without a token")
	}
}
