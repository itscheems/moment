package ginhttp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/url"
	"strings"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/auth/oauth"
	"github.com/moment/moment/internal/domain"
)

const (
	// oauthCookieName carries one in-flight sign-in.
	oauthCookieName = "moment_oauth"
	// oauthCookiePath keeps the cookie off every request that is not part of
	// a sign-in.
	oauthCookiePath = "/api/v1/login/oauth"
	// oauthFlowTTL bounds how long a started sign-in may be completed.
	oauthFlowTTL = 10 * time.Minute
	// oauthExchangeBudget bounds one completed callback.
	//
	// A provider exchange is several sequential calls -- the token endpoint,
	// then a profile, then addresses -- each with its own client timeout, so
	// without a budget over the whole thing a slow provider can hold somebody's
	// sign-in open for the sum of them. The person waiting is better served by
	// being told to try again.
	oauthExchangeBudget = 20 * time.Second
	// signInPath is where a failed sign-in returns the person to.
	signInPath = "/owner/login"
)

// oauthFlow is the state one sign-in carries between the two requests.
//
// It travels in a signed, HttpOnly cookie rather than a database row. The
// signature makes it unforgeable, the cookie makes it browser-bound, and
// deleting it on arrival makes it single-use -- which is the whole job of the
// state parameter.
type oauthFlow struct {
	Provider    string `json:"p"`
	State       string `json:"s"`
	Verifier    string `json:"v"`
	Nonce       string `json:"n"`
	ReturnPath  string `json:"r"`
	LinkUserID  string `json:"l,omitempty"`
	ExpiresAtMS int64  `json:"e"`
}

// OAuthConfig binds the federated sign-in transport to its dependencies.
type OAuthConfig struct {
	Registry *oauth.Registry
	Cookies  CookieOptions
	Log      SurfaceLogFunc
}

// record writes one authentication event.
//
// Sign-in produced no event of its own before accounts existed -- the access
// log recorded a POST to a path, with no subject and no outcome. That was
// tolerable while there was one possible answer to "who"; it stops being
// tolerable the moment there is more than one.
//
// Field names matter here: the log redacts any key containing "token" or
// "secret", so a client_secret is masked automatically while a client_id and a
// provider name, which are not secrets, stay readable.
func (config OAuthConfig) record(ctx *gin.Context, level, event string, fields map[string]any) {
	if config.Log == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["request_id"] = RequestID(ctx.Request.Context())
	config.Log(requestSurface(ctx), level, event, fields)
}

// RegisterOAuthRoutes installs federated sign-in.
//
// The path prefix is /api/v1/login/oauth because a redirect URI is matched
// exactly by the providers, so it is the registered address that decides the
// route rather than the other way round.
func RegisterOAuthRoutes(engine *gin.Engine, gate *Gate, config OAuthConfig) {
	group := engine.Group(oauthCookiePath)
	group.GET("/providers", listProviders(config.Registry))
	group.GET("/:provider/start", startOAuth(gate, config))
	group.GET("/:provider/callback", completeOAuth(gate, config))
}

func listProviders(registry *oauth.Registry) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		described := registry.Describe()
		if described == nil {
			described = []oauth.Descriptor{}
		}
		Success(ctx, http.StatusOK, map[string]any{"providers": described}, nil)
	}
}

// startOAuth mints one sign-in and sends the browser to the provider.
func startOAuth(gate *Gate, config OAuthConfig) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		provider, found := config.Registry.Lookup(ctx.Param("provider"))
		if !found {
			redirectToSignIn(ctx, domain.CodeOAuthProviderUnavailable)
			return
		}
		state, stateErr := gate.manager.RandomToken(32)
		verifier, verifierErr := gate.manager.RandomToken(32)
		nonce, nonceErr := gate.manager.RandomToken(32)
		if stateErr != nil || verifierErr != nil || nonceErr != nil {
			redirectToSignIn(ctx, domain.CodeOAuthProviderUnavailable)
			return
		}

		flow := oauthFlow{
			Provider:    provider.Name(),
			State:       state,
			Verifier:    verifier,
			Nonce:       nonce,
			ReturnPath:  safeReturnPath(ctx.Query("return")),
			ExpiresAtMS: time.Now().UTC().Add(oauthFlowTTL).UnixMilli(),
		}
		// Starting a sign-in while already signed in means "attach this
		// provider to the account I am using", not "become someone else".
		// Deciding that here, and sealing it into the flow, is what stops the
		// callback from having to guess.
		if session, err := ctx.Request.Cookie(SessionCookieName); err == nil {
			if issued, _, authErr := gate.manager.AuthorizeSession(session.Value); authErr == nil {
				flow.LinkUserID = issued.UserID
			}
		}

		payload, err := json.Marshal(flow)
		if err != nil {
			redirectToSignIn(ctx, domain.CodeOAuthProviderUnavailable)
			return
		}
		sealed, err := gate.manager.SealState(payload)
		if err != nil {
			redirectToSignIn(ctx, domain.CodeOAuthProviderUnavailable)
			return
		}
		setOAuthCookie(ctx, sealed, config.Cookies)
		redirect(ctx, provider.AuthCodeURL(state, verifier, nonce))
	}
}

// completeOAuth finishes a sign-in and hands back a session.
func completeOAuth(gate *Gate, config OAuthConfig) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		// The cookie is cleared before anything else can fail, so a flow is
		// spent by being answered rather than by succeeding.
		cookie, err := ctx.Request.Cookie(oauthCookieName)
		clearOAuthCookie(ctx, config.Cookies)
		if err != nil {
			redirectToSignIn(ctx, domain.CodeOAuthStateInvalid)
			return
		}
		payload, err := gate.manager.OpenState(cookie.Value)
		if err != nil {
			redirectToSignIn(ctx, domain.CodeOAuthStateInvalid)
			return
		}
		var flow oauthFlow
		if err := json.Unmarshal(payload, &flow); err != nil {
			redirectToSignIn(ctx, domain.CodeOAuthStateInvalid)
			return
		}

		// Four checks, none redundant: the flow has not expired, it belongs to
		// the provider whose callback this is, the browser presented the same
		// state the flow was minted with, and the comparison is not a prefix.
		if time.Now().UTC().UnixMilli() > flow.ExpiresAtMS ||
			flow.Provider != strings.ToLower(strings.TrimSpace(ctx.Param("provider"))) ||
			flow.State == "" || !constantTimeEqual(ctx.Query("state"), flow.State) {
			redirectToSignIn(ctx, domain.CodeOAuthStateInvalid)
			return
		}
		if declined := ctx.Query("error"); declined != "" {
			redirectToSignIn(ctx, domain.CodeOAuthStateInvalid)
			return
		}
		code := ctx.Query("code")
		if code == "" {
			redirectToSignIn(ctx, domain.CodeOAuthIdentityUnusable)
			return
		}
		provider, found := config.Registry.Lookup(flow.Provider)
		if !found {
			redirectToSignIn(ctx, domain.CodeOAuthProviderUnavailable)
			return
		}

		exchangeCtx, cancelExchange := context.WithTimeout(ctx.Request.Context(), oauthExchangeBudget)
		defer cancelExchange()
		external, err := provider.Exchange(exchangeCtx, code, flow.Verifier, flow.Nonce)
		if err != nil {
			config.record(ctx, "warn", "auth_login_failed", map[string]any{"provider": flow.Provider, "reason": "exchange"})
			redirectToSignIn(ctx, domain.CodeOAuthExchangeFailed)
			return
		}

		if flow.LinkUserID != "" {
			if err := gate.accounts.LinkIdentity(ctx.Request.Context(), flow.LinkUserID, external); err != nil {
				config.record(ctx, "warn", "auth_identity_link_failed", map[string]any{"user_id": flow.LinkUserID, "provider": flow.Provider})
				redirectToSignIn(ctx, oauthFailureCode(err))
				return
			}
			config.record(ctx, "info", "auth_identity_linked", map[string]any{"user_id": flow.LinkUserID, "provider": flow.Provider})
			redirectToReturn(ctx, flow.ReturnPath, "linked", flow.Provider)
			return
		}

		outcome, err := gate.accounts.SignIn(ctx.Request.Context(), external)
		if err != nil {
			config.record(ctx, "warn", "auth_login_failed", map[string]any{
				"provider": flow.Provider, "reason": string(oauthFailureCode(err)),
			})
			redirectToSignIn(ctx, oauthFailureCode(err))
			return
		}
		if err := issueSessionFor(ctx, gate, outcome.User, config.Cookies); err != nil {
			config.record(ctx, "error", "auth_login_failed", map[string]any{"provider": flow.Provider, "reason": "session"})
			redirectToSignIn(ctx, domain.CodeOAuthExchangeFailed)
			return
		}
		if outcome.Created {
			config.record(ctx, "info", "account_created", map[string]any{
				"user_id": outcome.User.ID, "provider": flow.Provider, "status": string(outcome.User.Status),
			})
			// Best effort, on a three second budget. A picture that cannot be
			// fetched costs an initial-letter avatar, never a failed sign-in.
			if err := gate.accounts.CaptureAvatar(ctx.Request.Context(), outcome.User.ID, external.AvatarURL); err != nil {
				config.record(ctx, "debug", "account_avatar_skipped", map[string]any{"user_id": outcome.User.ID, "provider": flow.Provider})
			}
		}
		config.record(ctx, "info", "auth_session_issued", map[string]any{
			"user_id": outcome.User.ID, "method": "oauth", "provider": flow.Provider,
		})
		redirectToReturn(ctx, flow.ReturnPath, "ok", "")
	}
}

// issueSessionFor mints a browser session for an account a provider vouched
// for. A fresh session id is always minted, so a sign-in can never adopt a
// session identifier that was in play before it.
func issueSessionFor(ctx *gin.Context, gate *Gate, user domain.User, cookies CookieOptions) error {
	sessionID, err := app.NewSessionID()
	if err != nil {
		return err
	}
	value, session, err := gate.manager.IssueSessionForVerifiedSubject(auth.Subject{UserID: user.ID, SessionID: sessionID})
	if err != nil {
		return err
	}
	if _, err := gate.accounts.StartSession(ctx.Request.Context(), user.ID, sessionID,
		string(requestSurface(ctx)), session.ExpiresAt.UnixMilli()); err != nil {
		return err
	}
	setSessionCookie(ctx, value, session.ExpiresAt, cookies)
	return nil
}

// oauthFailureCode maps an account decision onto the code the sign-in page
// knows how to explain.
func oauthFailureCode(err error) domain.Code {
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		switch domainErr.Code {
		case domain.CodeAccountSuspended, domain.CodeRegistrationClosed,
			domain.CodeIdentityAlreadyLinked, domain.CodeOAuthIdentityUnusable:
			return domainErr.Code
		}
	}
	return domain.CodeOAuthExchangeFailed
}

// safeReturnPath admits only same-site absolute paths.
//
// It is the server-side twin of the browser's own check. A sign-in that can be
// pointed at an arbitrary address is an open redirect, and one that arrives
// carrying a session cookie is a good one to hand an attacker.
func safeReturnPath(candidate string) string {
	if candidate == "" || !strings.HasPrefix(candidate, "/") || strings.HasPrefix(candidate, "//") {
		return "/"
	}
	if strings.ContainsAny(candidate, "\\\r\n") {
		return "/"
	}
	parsed, err := url.Parse(candidate)
	if err != nil || parsed.Host != "" || parsed.Scheme != "" {
		return "/"
	}
	return candidate
}

// redirect is the one place this transport answers with a 3xx.
//
// Everywhere else a failure is a JSON envelope, and that is right, because
// everywhere else the caller asked for data. Here the browser is in the middle
// of a top-level navigation: an envelope would be rendered to the person as
// raw text on a blank page. 303 rather than 302 because both hops are GET and
// 303 says so without relying on how a client chooses to interpret 302.
func redirect(ctx *gin.Context, location string) {
	ctx.Header("Cache-Control", "no-store")
	ctx.Redirect(http.StatusSeeOther, location)
	ctx.Abort()
}

func redirectToSignIn(ctx *gin.Context, code domain.Code) {
	redirect(ctx, signInPath+"?auth_error="+url.QueryEscape(string(code)))
}

// redirectToReturn sends the browser back where it came from with the outcome
// attached.
//
// The provider name rides along on a link so the page can say what was bound.
// Without it the browser has to remember across a full navigation what it was
// in the middle of, which is a moving part that exists only because the answer
// was omitted here.
func redirectToReturn(ctx *gin.Context, returnPath, outcome, provider string) {
	target := safeReturnPath(returnPath)
	separator := "?"
	if strings.Contains(target, "?") {
		separator = "&"
	}
	location := target + separator + "auth=" + url.QueryEscape(outcome)
	if provider != "" {
		location += "&provider=" + url.QueryEscape(provider)
	}
	redirect(ctx, location)
}

func setOAuthCookie(ctx *gin.Context, value string, options CookieOptions) {
	http.SetCookie(ctx.Writer, &http.Cookie{
		Name:     oauthCookieName,
		Value:    value,
		Path:     oauthCookiePath,
		MaxAge:   int(oauthFlowTTL.Seconds()),
		HttpOnly: true,
		Secure:   options.Secure,
		// A provider's return trip is a cross-site top-level GET, which Lax
		// permits. Strict would drop the cookie on exactly the request that
		// needs it.
		SameSite: http.SameSiteLaxMode,
	})
}

func clearOAuthCookie(ctx *gin.Context, options CookieOptions) {
	http.SetCookie(ctx.Writer, &http.Cookie{
		Name:     oauthCookieName,
		Value:    "",
		Path:     oauthCookiePath,
		MaxAge:   -1,
		Expires:  time.Unix(1, 0).UTC(),
		HttpOnly: true,
		Secure:   options.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}
