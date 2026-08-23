package oauth

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"
)

// fakeIssuer speaks enough OpenID Connect to exercise the real Google adapter.
//
// This exists because the parts of that adapter worth being sure about --
// signature verification, issuer and audience checks, the nonce, PKCE reaching
// the token endpoint -- are exactly the parts that never run in a unit test
// that substitutes the provider. Standing up something that answers the same
// protocol is the cheapest way to run them.
type fakeIssuer struct {
	server   *httptest.Server
	key      *rsa.PrivateKey
	keyID    string
	clientID string

	// claims lets a test bend what the issuer asserts.
	subject       string
	email         string
	emailVerified bool
	name          string
	picture       string
	nonce         string
	audience      string
	issuerClaim   string
	omitIDToken   bool

	// lastForm records what the adapter actually posted.
	lastForm url.Values
}

func newFakeIssuer(t *testing.T, clientID string) *fakeIssuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	issuer := &fakeIssuer{
		key:           key,
		keyID:         "test-key",
		clientID:      clientID,
		subject:       "108214906739283746501",
		email:         "person@example.test",
		emailVerified: true,
		name:          "Person Example",
		picture:       "https://lh3.googleusercontent.com/a/portrait",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/.well-known/openid-configuration", func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"issuer":                                issuer.base(),
			"authorization_endpoint":                issuer.base() + "/authorize",
			"token_endpoint":                        issuer.base() + "/token",
			"jwks_uri":                              issuer.base() + "/jwks",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(writer http.ResponseWriter, _ *http.Request) {
		set := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{
			Key: key.Public(), KeyID: issuer.keyID, Algorithm: string(jose.RS256), Use: "sig",
		}}}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(set)
	})
	mux.HandleFunc("/token", func(writer http.ResponseWriter, request *http.Request) {
		_ = request.ParseForm()
		issuer.lastForm = request.PostForm
		body := map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600}
		if !issuer.omitIDToken {
			body["id_token"] = issuer.signIDToken(t)
		}
		writer.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(writer).Encode(body)
	})
	issuer.server = httptest.NewServer(mux)
	t.Cleanup(issuer.server.Close)
	return issuer
}

func (issuer *fakeIssuer) base() string { return issuer.server.URL }

func (issuer *fakeIssuer) signIDToken(t *testing.T) string {
	t.Helper()
	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: issuer.key},
		(&jose.SignerOptions{}).WithType("JWT").WithHeader(jose.HeaderKey("kid"), issuer.keyID),
	)
	if err != nil {
		t.Fatal(err)
	}
	audience := issuer.audience
	if audience == "" {
		audience = issuer.clientID
	}
	issuerClaim := issuer.issuerClaim
	if issuerClaim == "" {
		issuerClaim = issuer.base()
	}
	now := time.Now()
	claims := map[string]any{
		"iss":            issuerClaim,
		"aud":            audience,
		"sub":            issuer.subject,
		"exp":            now.Add(time.Hour).Unix(),
		"iat":            now.Unix(),
		"email":          issuer.email,
		"email_verified": issuer.emailVerified,
		"name":           issuer.name,
		"picture":        issuer.picture,
	}
	if issuer.nonce != "" {
		claims["nonce"] = issuer.nonce
	}
	raw, err := jwt.Signed(signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func (issuer *fakeIssuer) provider(t *testing.T) *Google {
	t.Helper()
	google, err := newGoogleForIssuer(context.Background(), issuer.base(),
		Credentials{ClientID: issuer.clientID, ClientSecret: "secret"},
		"http://127.0.0.1:8080/api/v1/login/oauth/google/callback")
	if err != nil {
		t.Fatal(err)
	}
	if google == nil {
		t.Fatal("provider was not configured")
	}
	return google
}

func TestGoogleExchangeVerifiesTheTokenItIsHandedAndReportsTheAccount(t *testing.T) {
	issuer := newFakeIssuer(t, "client-123.apps.googleusercontent.com")
	issuer.nonce = "the-nonce"
	google := issuer.provider(t)

	identity, err := google.Exchange(context.Background(), "the-code", "the-verifier", "the-nonce")
	if err != nil {
		t.Fatalf("exchange: %v", err)
	}
	if identity.Provider != GoogleName || identity.ProviderUserID != issuer.subject {
		t.Fatalf("identity = %+v", identity)
	}
	if identity.Email != issuer.email || !identity.EmailVerified || !identity.EmailDeliverable {
		t.Fatalf("email fields = %+v", identity)
	}
	if identity.DisplayName != "Person Example" || identity.AvatarURL != issuer.picture {
		t.Fatalf("profile fields = %+v", identity)
	}

	// PKCE actually reached the token endpoint. Without this assertion the
	// verifier could be silently dropped and every test would still pass.
	if got := issuer.lastForm.Get("code_verifier"); got != "the-verifier" {
		t.Fatalf("code_verifier posted = %q", got)
	}
	if got := issuer.lastForm.Get("grant_type"); got != "authorization_code" {
		t.Fatalf("grant_type = %q", got)
	}
}

func TestGoogleAuthCodeURLCarriesPKCENonceAndAccountChooser(t *testing.T) {
	issuer := newFakeIssuer(t, "client-123.apps.googleusercontent.com")
	google := issuer.provider(t)

	parsed, err := url.Parse(google.AuthCodeURL("the-state", "the-verifier", "the-nonce"))
	if err != nil {
		t.Fatal(err)
	}
	query := parsed.Query()
	if query.Get("state") != "the-state" || query.Get("nonce") != "the-nonce" {
		t.Fatalf("query = %v", query)
	}
	if query.Get("code_challenge_method") != "S256" {
		t.Fatalf("challenge method = %q, want S256", query.Get("code_challenge_method"))
	}
	digest := sha256.Sum256([]byte("the-verifier"))
	if want := base64.RawURLEncoding.EncodeToString(digest[:]); query.Get("code_challenge") != want {
		t.Fatalf("code_challenge = %q, want %q", query.Get("code_challenge"), want)
	}
	if query.Get("prompt") != "select_account" {
		t.Fatalf("prompt = %q", query.Get("prompt"))
	}
	// Offline access is what would pull this app into the test-user cap and the
	// seven day expiry. Asserting its absence is asserting a product decision.
	if query.Get("access_type") == "offline" {
		t.Fatal("offline access was requested")
	}
	scopes := strings.Fields(query.Get("scope"))
	for _, scope := range scopes {
		switch scope {
		case "openid", "email", "profile":
		default:
			t.Fatalf("unexpected scope %q", scope)
		}
	}
}

func TestGoogleExchangeRefusesTokensItCannotTrust(t *testing.T) {
	for name, bend := range map[string]func(*fakeIssuer){
		"a token signed for a different audience": func(issuer *fakeIssuer) {
			issuer.audience = "someone-else.apps.googleusercontent.com"
		},
		"a token claiming a different issuer": func(issuer *fakeIssuer) {
			issuer.issuerClaim = "https://accounts.google.com"
		},
		"a token replayed from another sign-in": func(issuer *fakeIssuer) {
			issuer.nonce = "a-different-nonce"
		},
		"a response carrying no id token at all": func(issuer *fakeIssuer) {
			issuer.omitIDToken = true
		},
		"a token with no subject": func(issuer *fakeIssuer) {
			issuer.subject = ""
		},
	} {
		t.Run(name, func(t *testing.T) {
			issuer := newFakeIssuer(t, "client-123.apps.googleusercontent.com")
			issuer.nonce = "the-nonce"
			bend(issuer)
			google := issuer.provider(t)
			if _, err := google.Exchange(context.Background(), "code", "verifier", "the-nonce"); err == nil {
				t.Fatal("an untrustworthy token was accepted")
			}
		})
	}
}

func TestGoogleFallsBackToTheAddressLocalPartWhenNoNameIsGiven(t *testing.T) {
	issuer := newFakeIssuer(t, "client-123.apps.googleusercontent.com")
	issuer.nonce = "n"
	issuer.name = ""
	google := issuer.provider(t)
	identity, err := google.Exchange(context.Background(), "code", "verifier", "n")
	if err != nil {
		t.Fatal(err)
	}
	if identity.DisplayName != "person" {
		t.Fatalf("display name = %q", identity.DisplayName)
	}
}

func TestUnverifiedGoogleAddressIsNotTreatedAsDeliverable(t *testing.T) {
	issuer := newFakeIssuer(t, "client-123.apps.googleusercontent.com")
	issuer.nonce = "n"
	issuer.emailVerified = false
	google := issuer.provider(t)
	identity, err := google.Exchange(context.Background(), "code", "verifier", "n")
	if err != nil {
		t.Fatal(err)
	}
	if identity.EmailVerified || identity.EmailDeliverable {
		t.Fatalf("unverified address was trusted: %+v", identity)
	}
}

func TestProviderCredentialsMustBeCompleteToRegister(t *testing.T) {
	for name, credentials := range map[string]Credentials{
		"no id":     {ClientSecret: "secret"},
		"no secret": {ClientID: "client"},
		"neither":   {},
		"blank":     {ClientID: "   ", ClientSecret: "  "},
	} {
		t.Run(name, func(t *testing.T) {
			if credentials.Configured() {
				t.Fatal("a half-configured provider reported itself ready")
			}
			if github := NewGitHub(credentials, "http://127.0.0.1:8080/cb"); github != nil {
				t.Fatal("a half-configured provider was built anyway")
			}
		})
	}
}
