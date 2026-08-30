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
	// native verifies an assertion a mobile client obtained for itself. It is
	// a second verifier rather than a second audience on the first because the
	// two must not be interchangeable: a token minted for the web client is
	// one this server asked for and received over its own TLS connection,
	// while a token arriving from a device is whatever the device sent. Only
	// the latter has to name the platform client id it could only have got by
	// running the flow as that client.
	//
	// Nil when no native client id is registered, which is what
	// acceptsAssertions reports.
	native *oidc.IDTokenVerifier
}

// NewGoogle builds the Google provider against a discovery document.
//
// Discovery happens once at startup rather than per sign-in, so a provider that
// cannot be reached fails while the operator is watching instead of on someone
// else's first login attempt.
func NewGoogle(ctx context.Context, credentials Credentials, redirectURL, nativeClientID string) (*Google, error) {
	return newGoogleForIssuer(ctx, googleIssuer, credentials, redirectURL, nativeClientID)
}

// newGoogleForIssuer is NewGoogle with the issuer left open, so the adapter can
// be exercised against a stand-in that speaks the same protocol. Production has
// exactly one issuer and NewGoogle is the only way to reach it.
func newGoogleForIssuer(ctx context.Context, issuer string, credentials Credentials, redirectURL, nativeClientID string) (*Google, error) {
	if !credentials.Configured() || redirectURL == "" {
		return nil, nil
	}
	provider, err := oidc.NewProvider(withClient(ctx), issuer)
	if err != nil {
		return nil, unavailable("discover google endpoints", err)
	}
	google := &Google{
		config: &oauth2.Config{
			ClientID:     credentials.ClientID,
			ClientSecret: credentials.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  redirectURL,
			Scopes:       []string{oidc.ScopeOpenID, "email", "profile"},
		},
		verifier: provider.Verifier(&oidc.Config{ClientID: credentials.ClientID}),
	}
	if native := strings.TrimSpace(nativeClientID); native != "" {
		google.native = provider.Verifier(&oidc.Config{ClientID: native})
	}
	return google, nil
}

// AcceptsAssertions reports whether a native client id was registered.
//
// Without one there is no audience a device's token could name, so every
// assertion would be refused. Saying so here keeps the provider list honest:
// a client is told it may sign in this way only where it can.
func (google *Google) AcceptsAssertions() bool { return google != nil && google.native != nil }

// VerifyAssertion accepts an ID token a Google client on a device obtained for
// itself.
//
// No nonce is checked, and there is nothing here that could check one: the
// value would have to be compared against something this server minted, and
// this server did not start this flow -- the app did, holding a client id of
// its own. What stands in its place is the audience. A token naming the native
// client id was issued to an application proving possession of that client id
// through PKCE, and it expires within the hour. That is the same evidence every
// mainstream identity backend accepts from a native client, and it is strictly
// more than the browser callback verifies about the connection it arrived on.
func (google *Google) VerifyAssertion(ctx context.Context, assertion string) (app.ExternalIdentity, error) {
	if google.native == nil {
		return app.ExternalIdentity{}, unavailable("google has no native client registered here", nil)
	}
	if strings.TrimSpace(assertion) == "" {
		return app.ExternalIdentity{}, unavailable("google assertion is empty", nil)
	}
	idToken, err := google.native.Verify(withClient(ctx), assertion)
	if err != nil {
		return app.ExternalIdentity{}, unavailable("verify google assertion", err)
	}
	return google.identityFrom(idToken)
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
	return google.identityFrom(idToken)
}

// identityFrom reads the person out of a token that has already been verified.
//
// Shared by the two ways one arrives, so a claim is read the same way whichever
// client obtained it. Everything that differs between the flows -- who
// exchanged the code, which audience the token names, whether a nonce can be
// checked at all -- has been settled before this is called.
func (google *Google) identityFrom(idToken *oidc.IDToken) (app.ExternalIdentity, error) {
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
