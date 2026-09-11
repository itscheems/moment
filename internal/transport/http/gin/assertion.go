package ginhttp

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth/oauth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/observability"
)

// assertionBudget bounds one verification. Verifying an assertion is a local
// signature check against a cached key set, so the only thing this waits on is
// a key set that has to be fetched -- which is the same third party the browser
// callback is bounded against, for the same reason.
const assertionBudget = 20 * time.Second

// maxAssertionBytes bounds the one field this transport accepts. A Google ID
// token is around a kilobyte; anything near this ceiling is not one.
const maxAssertionBytes = 8 << 10

// assertionRequest is what a native client sends instead of an authorization
// code.
//
// The field is named for what it holds rather than for the flow, because the
// two are not the same thing: this is the token a client already obtained,
// where the browser flow's equivalent field would be a code this server still
// has to spend.
type assertionRequest struct {
	IDToken string `json:"id_token"`
}

// registerAssertionRoute installs sign-in for a client that ran the
// authorization itself.
//
// Public, and has to be: the entire premise is a caller holding no credential
// here yet. It is a separate route from the account-scoped link rather than one
// route deciding from whether a session happens to be present, and that is the
// whole point -- see linkOwnIdentity for what deciding by inference would cost.
func registerAssertionRoute(group *gin.RouterGroup, gate *Gate, config OAuthConfig) {
	group.POST("/:provider/assertion", assertionLogin(gate, config))
}

// assertionLogin verifies a client-obtained credential and hands back a session.
//
// Everything after the provider answers is the browser callback's sequence, in
// the same order and for the same reason the device poll repeats it: SignIn
// decides whether this identity may have an account at all, a fresh session id
// is minted, the credential is signed, the row is written, then the event is
// recorded. A front door that skipped any of it would be a front door with its
// own rules, and the first rule it would lose is approval.
func assertionLogin(gate *Gate, config OAuthConfig) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		provider, found := config.Registry.AssertionLookup(ctx.Param("provider"))
		if !found {
			Failure(ctx, http.StatusNotFound, string(domain.CodeOAuthProviderUnavailable),
				"that provider does not accept a credential this client obtained by itself", nil)
			return
		}
		assertion, ok := readAssertion(ctx)
		if !ok {
			return
		}

		verifyCtx, cancelVerify := context.WithTimeout(ctx.Request.Context(), assertionBudget)
		defer cancelVerify()
		external, err := provider.VerifyAssertion(verifyCtx, assertion)
		if err != nil {
			recordAssertion(config, ctx, "warn", "auth_login_failed",
				map[string]any{"provider": provider.Name(), "reason": "assertion"})
			Failure(ctx, http.StatusUnauthorized, string(domain.CodeOAuthIdentityUnusable),
				"that sign-in could not be verified", nil)
			return
		}

		outcome, err := gate.accounts.SignIn(ctx.Request.Context(), external)
		if err != nil {
			recordAssertion(config, ctx, "warn", "auth_login_failed",
				map[string]any{"provider": provider.Name(), "reason": string(oauthFailureCode(err))})
			writeDeviceSignInFailure(ctx, err)
			return
		}
		value, session, err := issueDeviceSession(ctx, gate, outcome.User, assertionSurface(ctx))
		if err != nil {
			recordAssertion(config, ctx, "error", "auth_login_failed",
				map[string]any{"provider": provider.Name(), "reason": "session"})
			Failure(ctx, http.StatusInternalServerError, "INTERNAL", "the session could not be issued", nil)
			return
		}
		if outcome.Created {
			recordAssertion(config, ctx, "info", "account_created", map[string]any{
				"user_id": outcome.User.ID, "provider": provider.Name(), "status": string(outcome.User.Status),
			})
			// Best effort, exactly as both other paths treat it. A picture that
			// cannot be fetched costs an initial-letter avatar, never a sign-in.
			if err := gate.accounts.CaptureAvatar(ctx.Request.Context(), outcome.User.ID, external.AvatarURL); err != nil {
				recordAssertion(config, ctx, "debug", "account_avatar_skipped",
					map[string]any{"user_id": outcome.User.ID, "provider": provider.Name()})
			}
		}
		recordAssertion(config, ctx, "info", "auth_session_issued", map[string]any{
			"user_id": outcome.User.ID, "method": "oauth_assertion", "provider": provider.Name(),
		})
		Success(ctx, http.StatusOK, deviceSessionView{
			Value:     value,
			CSRFToken: session.CSRFToken,
			ExpiresAt: session.ExpiresAt,
		}, nil)
	}
}

// readAssertion decodes the one field this route carries, writing its own
// refusal.
func readAssertion(ctx *gin.Context) (string, bool) {
	body, ok := readBody(ctx)
	if !ok {
		return "", false
	}
	var request assertionRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		strings.TrimSpace(request.IDToken) == "" || len(request.IDToken) > maxAssertionBytes {
		Failure(ctx, http.StatusBadRequest, string(domain.CodeInvalidArgument),
			"a JSON id_token is required", nil)
		return "", false
	}
	return request.IDToken, true
}

// recordAssertion writes one authentication event attributed to the app that
// sent it.
//
// Unlike the generic request fallback, assertion sign-in defaults to api even
// without a cookie, bearer token or declaration. Recognized declarations still
// identify native clients; ClientSurface refuses a caller that tries to claim web.
func recordAssertion(config OAuthConfig, ctx *gin.Context, level, event string, fields map[string]any) {
	if config.Log == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["request_id"] = RequestID(ctx.Request.Context())
	config.Log(assertionSurface(ctx), level, event, fields)
}

// assertionSurface reports which client is signing in.
func assertionSurface(ctx *gin.Context) observability.Surface {
	if declared, ok := observability.ClientSurface(ctx.GetHeader(observability.SurfaceHeader)); ok {
		return declared
	}
	return observability.SurfaceAPI
}

// identityLinkRequest is the one credential a link carries.
//
// Exactly one field, and which one depends on what the provider speaks: an
// assertion provider is handed the token its client already holds, a device
// provider is handed the code whoever is polling was given. They are separate
// fields rather than one named for neither because they are not the same kind
// of thing -- a token is evidence, a device code is a handle that may not have
// been approved yet -- and a route that could not tell them apart would have to
// guess which provider it was talking to.
type identityLinkRequest struct {
	IDToken    string `json:"id_token"`
	DeviceCode string `json:"device_code"`
}

// The answers a link can give. Linked is terminal; the other two are the device
// flow's own "not yet", carried through unchanged because a caller polling to
// link does exactly what a caller polling to sign in does until the last step.
const deviceLinked = "linked"

type identityLinkView struct {
	Status          string `json:"status"`
	IntervalSeconds int    `json:"interval_seconds"`
	Provider        string `json:"provider"`
}

// linkOwnIdentity attaches a provider identity to the account making the
// request.
//
// ## Why this is a route of its own
//
// The browser flow decides between signing in and linking by whether the person
// who started it happened to be signed in (oauth.go, where the gate's answer is
// sealed into the flow). Repeating that here would be a bug rather than a
// symmetry. VerifiedAccount reaches gate.authenticate, which refuses a cookie
// session on an unsafe method that carries no CSRF token -- and then reports
// only that nobody was signed in. A link request missing that header would
// therefore not fail; it would quietly become a sign-in, and the person who
// meant to add a second way into their account would be switched to a different
// one. A browser cannot hit that case because its start is a GET.
//
// So the two live apart, and the gate group decides which is which: this route
// sits behind RequireAccount, where the session and the CSRF token are already
// mandatory, and refusing is what happens when either is missing.
//
// Pending accounts may link, for the reason RequireAccount admits them to
// everything else they do to themselves: a second way to reach your own account
// reaches nobody else's.
func linkOwnIdentity(gate *Gate, config OAuthConfig) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		user, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		name := strings.ToLower(strings.TrimSpace(ctx.Param("provider")))
		request, ok := readIdentityLink(ctx)
		if !ok {
			return
		}

		external, status, err := resolveLinkIdentity(ctx, config, name, request)
		if err != nil {
			writeLinkFailure(ctx, config, name, err)
			return
		}
		if status != nil {
			// The provider has not been asked yet, because nobody has approved
			// the code. Nothing is linked and nothing failed.
			Success(ctx, http.StatusOK, *status, nil)
			return
		}

		if err := gate.accounts.LinkIdentity(ctx.Request.Context(), user.ID, external); err != nil {
			recordAssertion(config, ctx, "warn", "auth_identity_link_failed",
				map[string]any{"user_id": user.ID, "provider": name})
			writeAccountError(ctx, err)
			return
		}
		recordAssertion(config, ctx, "info", "auth_identity_linked",
			map[string]any{"user_id": user.ID, "provider": name})
		Success(ctx, http.StatusOK, identityLinkView{Status: deviceLinked, Provider: name}, nil)
	}
}

// resolveLinkIdentity turns whichever credential arrived into an identity.
//
// Three outcomes rather than two: an identity, a device flow that is still
// waiting, or a failure. The waiting case is a pointer so that "not yet" cannot
// be mistaken for an empty identity by a caller that forgets to check.
func resolveLinkIdentity(
	ctx *gin.Context, config OAuthConfig, name string, request identityLinkRequest,
) (app.ExternalIdentity, *identityLinkView, error) {
	if request.IDToken != "" {
		provider, found := config.Registry.AssertionLookup(name)
		if !found {
			return app.ExternalIdentity{}, nil, errLinkFlowUnsupported
		}
		verifyCtx, cancel := context.WithTimeout(ctx.Request.Context(), assertionBudget)
		defer cancel()
		external, err := provider.VerifyAssertion(verifyCtx, request.IDToken)
		return external, nil, err
	}

	provider, found := config.Registry.DeviceLookup(name)
	if !found {
		return app.ExternalIdentity{}, nil, errLinkFlowUnsupported
	}
	pollCtx, cancel := context.WithTimeout(ctx.Request.Context(), devicePollBudget)
	defer cancel()
	external, err := provider.PollDevice(pollCtx, request.DeviceCode)
	switch {
	case errors.Is(err, oauth.ErrDevicePending):
		return app.ExternalIdentity{}, &identityLinkView{Status: devicePending, Provider: name}, nil
	case errors.Is(err, oauth.ErrDeviceSlowDown):
		view := identityLinkView{Status: deviceSlowDown, Provider: name}
		var slow *oauth.SlowDown
		if errors.As(err, &slow) {
			view.IntervalSeconds = wholeSeconds(slow.Interval)
		}
		return app.ExternalIdentity{}, &view, nil
	}
	return external, nil, err
}

// errLinkFlowUnsupported is a credential this provider cannot be linked with
// here -- an id_token for a provider that issues none, or a device code for one
// that grants none.
var errLinkFlowUnsupported = errors.New("that provider cannot be linked that way here")

// readIdentityLink decodes the body, requiring exactly one credential.
func readIdentityLink(ctx *gin.Context) (identityLinkRequest, bool) {
	body, ok := readBody(ctx)
	if !ok {
		return identityLinkRequest{}, false
	}
	var request identityLinkRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	refuse := func() (identityLinkRequest, bool) {
		Failure(ctx, http.StatusBadRequest, string(domain.CodeInvalidArgument),
			"exactly one of id_token or device_code is required", nil)
		return identityLinkRequest{}, false
	}
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return refuse()
	}
	request.IDToken = strings.TrimSpace(request.IDToken)
	request.DeviceCode = strings.TrimSpace(request.DeviceCode)
	if (request.IDToken == "") == (request.DeviceCode == "") ||
		len(request.IDToken) > maxAssertionBytes || len(request.DeviceCode) > maxDeviceCodeBytes {
		return refuse()
	}
	return request, true
}

// writeLinkFailure reports a provider answer that produced no identity.
//
// The device sentinels keep the codes the poll route already gives them, so a
// client that understands one understands both; anything else is the provider
// being unusable rather than the caller being wrong.
func writeLinkFailure(ctx *gin.Context, config OAuthConfig, provider string, err error) {
	switch {
	case errors.Is(err, errLinkFlowUnsupported):
		Failure(ctx, http.StatusNotFound, string(domain.CodeOAuthProviderUnavailable),
			"that provider cannot be linked this way here", nil)
	case errors.Is(err, oauth.ErrDeviceExpired):
		Failure(ctx, http.StatusBadRequest, string(domain.CodeOAuthDeviceExpired),
			"the device code expired before it was approved", nil)
	case errors.Is(err, oauth.ErrDeviceDenied):
		Failure(ctx, http.StatusForbidden, string(domain.CodeOAuthDeviceDenied),
			"the authorization was refused at the provider", nil)
	case errors.Is(err, oauth.ErrDeviceCodeInvalid):
		Failure(ctx, http.StatusBadRequest, string(domain.CodeOAuthStateInvalid),
			"that device code is not one this flow can use", nil)
	default:
		recordAssertion(config, ctx, "warn", "auth_identity_link_failed",
			map[string]any{"provider": provider, "reason": "verify"})
		Failure(ctx, http.StatusUnauthorized, string(domain.CodeOAuthIdentityUnusable),
			"that sign-in could not be verified", nil)
	}
}
