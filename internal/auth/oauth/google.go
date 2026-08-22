package oauth

import (
	"context"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/moment/moment/internal/app"
)

// GoogleName is the stable key stored in identities.provider.
const GoogleName = "google"

// googleIssuer is the value an ID token must claim. Verifying it is what makes
// a token from some other Google-shaped issuer unusable here.
const googleIssuer = "https://accounts.google.com"

// Google signs people in with their Google Account.
//
// Only identity scopes are requested, and offline access is never asked for.
// That is not merely minimal, it is what keeps this outside the rules that
// catch everything else: Google exempts an app requesting no more than name,
// email and profile from the test-user list, from the unverified-app warning,
// and from the seven-day expiry that applies to authorizations granted while an
// app is still in testing.
type Google struct {
	config   *oauth2.Config
	verifier *oidc.IDTokenVerifier
}

// NewGoogle builds the Google provider against a discovery document.
//
// Discovery happens once at startup rather than per sign-in, so a provider that
// cannot be reached fails while the operator is watching instead of on someone
// else's first login attempt.
func NewGoogle(ctx context.Context, credentials Credentials, redirectURL string) (*Google, error) {
	if !credentials.Configured() || redirectURL == "" {
		return nil, nil
	}
	provider, err := oidc.NewProvider(withClient(ctx), googleIssuer)
	if err != nil {
		return nil, unavailable("discover google endpoints", err)
	}
	return &Google{
		config: &oauth2.Config{
			ClientID:     credentials.ClientID,
			ClientSecret: credentials.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  redirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: credentials.ClientID}),
	}, nil
}

// Name reports the provider key.
func (google *Google) Name() string { return GoogleName }

// Label reports the sign-in button text.
func (google *Google) Label() string { return "Google" }

// AuthCodeURL builds the address the browser is sent to.
func (google *Google) AuthCodeURL(state, verifier, nonce string) string {
	return google.config.AuthCodeURL(state,
		oauth2.S256ChallengeOption(verifier),
		oidc.Nonce(nonce),
		// Without this, a person already signed in to exactly one Google
		// account is never shown which one they are about to use.
		oauth2.SetAuthURLParam("prompt", "select_account"),
	)
}

// googleClaims are the fields this product reads from an ID token.
type googleClaims struct {
	Subject       string `json:"sub"`
	Email         string `json:"email"`
	EmailVerified bool   `json:"email_verified"`
	Name          string `json:"name"`
	GivenName     string `json:"given_name"`
	Picture       string `json:"picture"`
}

// Exchange completes the flow and reports who signed in.
func (google *Google) Exchange(ctx context.Context, code, verifier, nonce string) (app.ExternalIdentity, error) {
	token, err := google.config.Exchange(withClient(ctx), code, oauth2.VerifierOption(verifier))
	if err != nil {
		return app.ExternalIdentity{}, unavailable("exchange google code", err)
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok || rawIDToken == "" {
		return app.ExternalIdentity{}, unavailable("google returned no id token", nil)
	}
	// Signature, issuer, audience and expiry are all checked here. The token
	// arrived over a direct TLS connection to Google, which OpenID Connect
	// allows as a substitute for checking the signature -- but a library that
	// already caches the key set makes the stronger check free, and it is the
	// difference between trusting the transport and verifying the claim.
	idToken, err := google.verifier.Verify(withClient(ctx), rawIDToken)
	if err != nil {
		return app.ExternalIdentity{}, unavailable("verify google id token", err)
	}
	// A replayed token from a different sign-in attempt fails here.
	if idToken.Nonce != nonce {
		return app.ExternalIdentity{}, unavailable("google id token nonce mismatch", nil)
	}
	var claims googleClaims
	if err := idToken.Claims(&claims); err != nil {
		return app.ExternalIdentity{}, unavailable("read google claims", err)
	}
	if strings.TrimSpace(claims.Subject) == "" {
		return app.ExternalIdentity{}, unavailable("google returned no subject", nil)
	}

	displayName := strings.TrimSpace(claims.Name)
	if displayName == "" {
		displayName = strings.TrimSpace(claims.GivenName)
	}
	if displayName == "" && claims.Email != "" {
		displayName, _, _ = strings.Cut(claims.Email, "@")
	}

	return app.ExternalIdentity{
		Provider:       GoogleName,
		ProviderUserID: claims.Subject,
		Email:          strings.TrimSpace(claims.Email),
		EmailVerified:  claims.EmailVerified,
		// Google only reports addresses it delivers to.
		EmailDeliverable: claims.Email != "" && claims.EmailVerified,
		DisplayName:      displayName,
		AvatarURL:        strings.TrimSpace(claims.Picture),
	}, nil
}
