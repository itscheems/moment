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
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/auth/oauth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/observability"
)

const (
	// devicePath is where a caller with no browser signs in. It is not under
	// the OAuth prefix because that prefix is the registered redirect URI, and
	// nothing in this flow is ever redirected to.
	devicePath = "/api/v1/login/device"
	// deviceStartBudget and devicePollBudget bound one call to the provider,
	// for the same reason the browser callback is bounded: a slow provider must
	// not be able to hold a request open for the sum of its own timeouts.
	deviceStartBudget = 15 * time.Second
	devicePollBudget  = 20 * time.Second
	// maxDeviceCodeBytes bounds the one field this transport accepts. Provider
	// device codes are tens of bytes; anything near this is not one.
	maxDeviceCodeBytes = 512
)

// The four things a poll can answer with that are not a refusal. A caller reads
// status before anything else, and the session material is a nested object
// precisely so that a caller which does not cannot mistake "still waiting" for
// a sign-in: there is no empty string to hand on, only a null to fall over.
const (
	devicePending    = "pending"
	deviceSlowDown   = "slow_down"
	deviceAuthorized = "authorized"
)

// deviceStartView is what a person has to be shown and what the caller has to
// send back.
//
// The device code goes to the caller because nothing here remembers it. This
// deployment has no ephemeral server-side store -- the browser flow carries its
// own in-flight state in a signed cookie for the same reason -- and a device
// flow needs none as long as whoever polls carries the code. What stays here is
// the client secret, which is the whole point of brokering this at all.
type deviceStartView struct {
	DeviceCode       string `json:"device_code"`
	UserCode         string `json:"user_code"`
	VerificationURI  string `json:"verification_uri"`
	IntervalSeconds  int    `json:"interval_seconds"`
	ExpiresInSeconds int    `json:"expires_in_seconds"`
}

type devicePollView struct {
	Status          string             `json:"status"`
	IntervalSeconds int                `json:"interval_seconds"`
	Session         *deviceSessionView `json:"session"`
}

// deviceSessionView is the session a browser would have been handed in a
// Set-Cookie header.
//
// It travels as JSON because the caller is not a browser and has no cookie jar
// to put it in. What it is, though, is exactly the same value: the caller sends
// it back as a Cookie header and the gate's existing cookie path admits it
// unchanged, which is what keeps this from being a second way to be signed in.
type deviceSessionView struct {
	Value     string    `json:"value"`
	CSRFToken string    `json:"csrf_token"`
	ExpiresAt time.Time `json:"expires_at"`
}

type devicePollRequest struct {
	DeviceCode string `json:"device_code"`
}

// RegisterDeviceRoutes installs sign-in for a caller with no browser.
//
// Both routes are public, and have to be: the entire premise is a caller that
// holds no credential yet. They sit outside every gate group for that reason,
// which is also why the poll handler below repeats the account rules rather
// than inheriting them.
func RegisterDeviceRoutes(engine *gin.Engine, gate *Gate, config OAuthConfig) {
	group := engine.Group(devicePath)
	group.POST("/:provider/start", startDeviceLogin(config))
	group.POST("/:provider/poll", pollDeviceLogin(gate, config))
}

// startDeviceLogin asks the provider for a code a person can type elsewhere.
func startDeviceLogin(config OAuthConfig) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		provider, found := config.Registry.DeviceLookup(ctx.Param("provider"))
		if !found {
			Failure(ctx, http.StatusNotFound, string(domain.CodeOAuthProviderUnavailable),
				"that provider cannot sign anyone in without a browser here", nil)
			return
		}
		startCtx, cancelStart := context.WithTimeout(ctx.Request.Context(), deviceStartBudget)
		defer cancelStart()
		authorization, err := provider.StartDevice(startCtx)
		if err != nil {
			recordDevice(config, ctx, "warn", "auth_login_failed",
				map[string]any{"provider": provider.Name(), "reason": "device_start"})
			Failure(ctx, http.StatusBadGateway, string(domain.CodeOAuthProviderUnavailable),
				"the identity provider could not start a device sign-in", nil)
			return
		}
		Success(ctx, http.StatusOK, deviceStartView{
			DeviceCode:       authorization.DeviceCode,
			UserCode:         authorization.UserCode,
			VerificationURI:  authorization.VerificationURI,
			IntervalSeconds:  wholeSeconds(authorization.Interval),
			ExpiresInSeconds: wholeSeconds(authorization.ExpiresIn),
		}, nil)
	}
}

// pollDeviceLogin reports whether a device code has been approved, and signs
// the person in when it has.
//
// Everything after a provider answers is the browser callback's sequence,
// deliberately and in the same order: SignIn decides whether this identity may
// have an account at all, a fresh session id is minted, the credential is
// signed, the row is written, then the event is recorded. A device login that
// skipped any of it would be a second front door with its own rules, and the
// first rule it would lose is approval.
func pollDeviceLogin(gate *Gate, config OAuthConfig) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		provider, found := config.Registry.DeviceLookup(ctx.Param("provider"))
		if !found {
			Failure(ctx, http.StatusNotFound, string(domain.CodeOAuthProviderUnavailable),
				"that provider cannot sign anyone in without a browser here", nil)
			return
		}
		deviceCode, ok := readDeviceCode(ctx)
		if !ok {
			return
		}

		pollCtx, cancelPoll := context.WithTimeout(ctx.Request.Context(), devicePollBudget)
		defer cancelPoll()
		external, err := provider.PollDevice(pollCtx, deviceCode)
		if err != nil {
			answerDevicePollFailure(ctx, config, provider.Name(), err)
			return
		}

		outcome, err := gate.accounts.SignIn(ctx.Request.Context(), external)
		if err != nil {
			recordDevice(config, ctx, "warn", "auth_login_failed",
				map[string]any{"provider": provider.Name(), "reason": string(oauthFailureCode(err))})
			writeDeviceSignInFailure(ctx, err)
			return
		}
		value, session, err := issueDeviceSession(ctx, gate, outcome.User, observability.SurfaceCLI)
		if err != nil {
			recordDevice(config, ctx, "error", "auth_login_failed",
				map[string]any{"provider": provider.Name(), "reason": "session"})
			Failure(ctx, http.StatusInternalServerError, "INTERNAL", "the session could not be issued", nil)
			return
		}
		if outcome.Created {
			recordDevice(config, ctx, "info", "account_created", map[string]any{
				"user_id": outcome.User.ID, "provider": provider.Name(), "status": string(outcome.User.Status),
			})
			// Best effort, exactly as the browser path treats it. A picture that
			// cannot be fetched costs an initial-letter avatar, never a sign-in.
			if err := gate.accounts.CaptureAvatar(ctx.Request.Context(), outcome.User.ID, external.AvatarURL); err != nil {
				recordDevice(config, ctx, "debug", "account_avatar_skipped",
					map[string]any{"user_id": outcome.User.ID, "provider": provider.Name()})
			}
		}
		recordDevice(config, ctx, "info", "auth_session_issued", map[string]any{
			"user_id": outcome.User.ID, "method": "oauth_device", "provider": provider.Name(),
		})
		Success(ctx, http.StatusOK, devicePollView{
			Status: deviceAuthorized,
			Session: &deviceSessionView{
				Value:     value,
				CSRFToken: session.CSRFToken,
				ExpiresAt: session.ExpiresAt,
			},
		}, nil)
	}
}

// answerDevicePollFailure turns one provider answer that is not an identity
// into the response the caller acts on.
//
// Pending and slow_down are successes, because the poll succeeded and the
// answer is "not yet" -- reporting them as failures would make a CLI print an
// error every few seconds while a person is still typing a code.
func answerDevicePollFailure(ctx *gin.Context, config OAuthConfig, provider string, err error) {
	switch {
	case errors.Is(err, oauth.ErrDevicePending):
		Success(ctx, http.StatusOK, devicePollView{Status: devicePending}, nil)
	case errors.Is(err, oauth.ErrDeviceSlowDown):
		view := devicePollView{Status: deviceSlowDown}
		var slow *oauth.SlowDown
		if errors.As(err, &slow) {
			view.IntervalSeconds = wholeSeconds(slow.Interval)
		}
		Success(ctx, http.StatusOK, view, nil)
	case errors.Is(err, oauth.ErrDeviceExpired):
		Failure(ctx, http.StatusBadRequest, string(domain.CodeOAuthDeviceExpired),
			"the device code expired before it was approved", nil)
	case errors.Is(err, oauth.ErrDeviceDenied):
		Failure(ctx, http.StatusForbidden, string(domain.CodeOAuthDeviceDenied),
			"the sign-in was refused at the provider", nil)
	case errors.Is(err, oauth.ErrDeviceCodeInvalid):
		// The device code is this flow's whole state, so a code the provider
		// never issued is the same failure the browser reports for a state it
		// did not mint.
		Failure(ctx, http.StatusBadRequest, string(domain.CodeOAuthStateInvalid),
			"that device code is not one this sign-in can use", nil)
	default:
		recordDevice(config, ctx, "warn", "auth_login_failed",
			map[string]any{"provider": provider, "reason": "device_poll"})
		Failure(ctx, http.StatusBadGateway, string(domain.CodeOAuthExchangeFailed),
			"the identity provider could not complete the device sign-in", nil)
	}
}

// writeDeviceSignInFailure reports an account decision that stopped a sign-in.
//
// The recognised set is the same one oauthFailureCode names, because it is the
// same decision: these are the answers SignIn gives that a caller can do
// something about. Anything else is this instance failing rather than the
// caller being refused, and is reported as such instead of being dressed up as
// a provider problem.
func writeDeviceSignInFailure(ctx *gin.Context, err error) {
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		switch domainErr.Code {
		case domain.CodeAccountSuspended, domain.CodeRegistrationClosed:
			Failure(ctx, http.StatusForbidden, string(domainErr.Code), domainErr.Message, domainErr.Details)
			return
		case domain.CodeIdentityAlreadyLinked:
			Failure(ctx, http.StatusConflict, string(domainErr.Code), domainErr.Message, domainErr.Details)
			return
		case domain.CodeOAuthIdentityUnusable:
			Failure(ctx, http.StatusBadRequest, string(domainErr.Code), domainErr.Message, domainErr.Details)
			return
		}
	}
	Failure(ctx, http.StatusInternalServerError, "INTERNAL", "the sign-in could not be completed", nil)
}

// issueDeviceSession mints the session a browser sign-in would and hands the
// signed value back instead of setting a cookie.
//
// A fresh session id is always minted, exactly as the browser path does, so a
// sign-in can never adopt a session identifier that was in play before it. The
// surface is passed in rather than derived from the request: the only two
// things requestSurface can read are a session cookie and a bearer token, and a
// caller signing in without a browser holds neither by construction, so it
// would fall through to web and make the session list on the account page name
// the wrong entry point.
func issueDeviceSession(ctx *gin.Context, gate *Gate, user domain.User, surface observability.Surface) (string, auth.Session, error) {
	sessionID, err := app.NewSessionID()
	if err != nil {
		return "", auth.Session{}, err
	}
	value, session, err := gate.manager.IssueSessionForVerifiedSubject(auth.Subject{UserID: user.ID, SessionID: sessionID})
	if err != nil {
		return "", auth.Session{}, err
	}
	// The row is written before the value is handed out, so a credential a
	// caller holds always has something to be revoked by.
	record, err := gate.accounts.StartSession(ctx.Request.Context(), user.ID, sessionID,
		string(surface), session.ExpiresAt.UnixMilli())
	if err != nil {
		return "", auth.Session{}, err
	}
	session.ExpiresAt = time.UnixMilli(record.ExpiresAtMS).UTC()
	return value, session, nil
}

// readDeviceCode decodes the one field a poll carries, writing its own refusal.
func readDeviceCode(ctx *gin.Context) (string, bool) {
	body, ok := readBody(ctx)
	if !ok {
		return "", false
	}
	var request devicePollRequest
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
		strings.TrimSpace(request.DeviceCode) == "" || len(request.DeviceCode) > maxDeviceCodeBytes {
		Failure(ctx, http.StatusBadRequest, string(domain.CodeInvalidArgument),
			"a JSON device_code is required", nil)
		return "", false
	}
	return request.DeviceCode, true
}

// recordDevice writes one authentication event attributed to the terminal.
//
// requestSurface cannot answer this one and OAuthConfig.record therefore cannot
// either: both decide from a session cookie or a bearer token, and a device
// login holds neither. A person at a terminal is what this flow is, so the
// surface is known statically rather than guessed at.
func recordDevice(config OAuthConfig, ctx *gin.Context, level, event string, fields map[string]any) {
	if config.Log == nil {
		return
	}
	if fields == nil {
		fields = map[string]any{}
	}
	fields["request_id"] = RequestID(ctx.Request.Context())
	config.Log(observability.SurfaceCLI, level, event, fields)
}

// wholeSeconds is what a JSON number of seconds should be. Rounding up keeps a
// sub-second interval from becoming a zero a caller would busy-loop on.
func wholeSeconds(duration time.Duration) int {
	if duration <= 0 {
		return 0
	}
	seconds := int(duration / time.Second)
	if duration%time.Second != 0 {
		seconds++
	}
	return seconds
}
