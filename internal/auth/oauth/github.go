package oauth

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	"golang.org/x/oauth2"

	"github.com/moment/moment/internal/app"
)

// GitHubName is the stable key stored in identities.provider.
const GitHubName = "github"

const (
	githubAuthorizeURL = "https://github.com/login/oauth/authorize"
	githubTokenURL     = "https://github.com/login/oauth/access_token"
	githubUserURL      = "https://api.github.com/user"
	// per_page is pinned at GitHub's maximum because the adapter reads one page
	// and never follows Link. The default is 30, so without this an account
	// whose primary address happens to sort onto page two would silently
	// degrade to its profile address, or to none at all.
	githubEmailsURL = "https://api.github.com/user/emails?per_page=100"
	// githubNoReplyDomain marks addresses GitHub issues in place of a real one.
	// They are syntactically valid and permanently undeliverable.
	githubNoReplyDomain = "@users.noreply.github.com"
	// maxGitHubResponse bounds a provider response the same way the location
	// providers bound theirs.
	maxGitHubResponse = 1 << 20
)

// GitHub signs people in with their GitHub account.
//
// An OAuth App rather than a GitHub App, deliberately. A GitHub App's
// advantages -- fine-grained repository permissions, short-lived installation
// tokens, independence from the installing user -- all describe things this
// product never does. What it would add is an install step in front of a person
// who only wants to sign in.
type GitHub struct {
	config *oauth2.Config
	// userURL and emailsURL are held rather than read from the constants above
	// so the adapter can be pointed somewhere else; see newGitHubAt.
	userURL   string
	emailsURL string
}

// NewGitHub builds the GitHub provider.
func NewGitHub(credentials Credentials, redirectURL string) *GitHub {
	return newGitHubAt(credentials, redirectURL, githubEndpoints{
		authorize: githubAuthorizeURL,
		token:     githubTokenURL,
		user:      githubUserURL,
		emails:    githubEmailsURL,
	})
}

// githubEndpoints is the set of addresses one GitHub adapter talks to.
type githubEndpoints struct{ authorize, token, user, emails string }

// newGitHubAt is NewGitHub with the endpoints left open, so the adapter can be
// exercised against a stand-in that speaks the same protocol. What that buys is
// the email resolution below: a private profile address, a noreply primary, a
// token without user:email are all ordinary for real people and none of them
// can be staged against github.com. Production has exactly one set of endpoints
// and NewGitHub is the only way to reach them.
func newGitHubAt(credentials Credentials, redirectURL string, endpoints githubEndpoints) *GitHub {
	if !credentials.Configured() || redirectURL == "" {
		return nil
	}
	return &GitHub{
		config: &oauth2.Config{
			ClientID:     credentials.ClientID,
			ClientSecret: credentials.ClientSecret,
			Endpoint:     oauth2.Endpoint{AuthURL: endpoints.authorize, TokenURL: endpoints.token},
			RedirectURL:  redirectURL,
			// read:user reads the profile and user:email reads addresses. Both are
			// needed: read:user alone never yields an address, and most accounts
			// keep theirs private, which makes /user report a null email.
			Scopes: []string{"read:user", "user:email"},
		},
		userURL:   endpoints.user,
		emailsURL: endpoints.emails,
	}
}

// Name reports the provider key.
func (github *GitHub) Name() string { return GitHubName }

// Label reports the sign-in button text.
func (github *GitHub) Label() string { return "GitHub" }

// AuthCodeURL builds the address the browser is sent to.
//
// GitHub supports PKCE and requires S256; the plain method is refused.
func (github *GitHub) AuthCodeURL(state, verifier, _ string) string {
	return github.config.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier))
}

type githubUser struct {
	ID        int64  `json:"id"`
	Login     string `json:"login"`
	Name      string `json:"name"`
	Email     string `json:"email"`
	AvatarURL string `json:"avatar_url"`
}

type githubEmail struct {
	Email    string `json:"email"`
	Primary  bool   `json:"primary"`
	Verified bool   `json:"verified"`
}

// Exchange completes the flow and reports who signed in.
func (github *GitHub) Exchange(ctx context.Context, code, verifier, _ string) (app.ExternalIdentity, error) {
	// x/oauth2 selects its parser from the response Content-Type, so GitHub's
	// habit of answering the token endpoint in form encoding unless asked for
	// JSON is handled without an Accept header here.
	token, err := github.config.Exchange(withClient(ctx), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return app.ExternalIdentity{}, unavailable("exchange github code", err)
	}

	var profile githubUser
	if err := github.get(ctx, token, github.userURL, &profile); err != nil {
		return app.ExternalIdentity{}, err
	}
	if profile.ID == 0 {
		return app.ExternalIdentity{}, unavailable("github returned no account id", nil)
	}

	email, verified, deliverable := github.resolveEmail(ctx, token, profile)

	displayName := strings.TrimSpace(profile.Name)
	if displayName == "" {
		displayName = strings.TrimSpace(profile.Login)
	}

	return app.ExternalIdentity{
		Provider: GitHubName,
		// The numeric id, never the login. A login can be changed by its owner
		// and re-registered by someone else once released.
		ProviderUserID:   strconv.FormatInt(profile.ID, 10),
		Email:            email,
		EmailVerified:    verified,
		EmailDeliverable: deliverable,
		DisplayName:      displayName,
		AvatarURL:        strings.TrimSpace(profile.AvatarURL),
	}, nil
}

// resolveEmail finds the best address GitHub will admit to.
//
// GET /user reports null for anyone who kept their address private, which is
// both common and the default GitHub encourages, so the addresses endpoint is
// the real source. Only a primary, verified address is accepted: an unverified
// one proves nothing about who controls it.
func (github *GitHub) resolveEmail(ctx context.Context, token *oauth2.Token, profile githubUser) (string, bool, bool) {
	var addresses []githubEmail
	if err := github.get(ctx, token, github.emailsURL, &addresses); err == nil {
		for _, candidate := range addresses {
			address := strings.TrimSpace(candidate.Email)
			if candidate.Primary && candidate.Verified && address != "" {
				return address, true, !strings.HasSuffix(strings.ToLower(address), githubNoReplyDomain)
			}
		}
	}
	// The profile address, when present at all, is one GitHub already shows
	// publicly. It is worth keeping for recognition, but nothing here has
	// verified it.
	if address := strings.TrimSpace(profile.Email); address != "" {
		return address, false, !strings.HasSuffix(strings.ToLower(address), githubNoReplyDomain)
	}
	// No address is a supported outcome, not a failure.
	return "", false, false
}

func (github *GitHub) get(ctx context.Context, token *oauth2.Token, url string, destination any) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return unavailable("build github request", err)
	}
	request.Header.Set("Accept", "application/vnd.github+json")
	request.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	request.Header.Set("Authorization", "Bearer "+token.AccessToken)

	response, err := httpClient().Do(request)
	if err != nil {
		return unavailable("call github", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, io.LimitReader(response.Body, 4<<10))
		_ = response.Body.Close()
	}()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return unavailable(fmt.Sprintf("github responded %d", response.StatusCode), nil)
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, maxGitHubResponse+1))
	if err != nil || len(body) > maxGitHubResponse {
		return unavailable("read github response", err)
	}
	if err := json.Unmarshal(body, destination); err != nil {
		return unavailable("decode github response", err)
	}
	return nil
}
