package ginhttp

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
)

const (
	// SessionCookieName is the private browser credential issued by Moment.
	SessionCookieName = "moment_session"
	// CSRFHeaderName carries the token authenticated inside a browser session.
	CSRFHeaderName = "X-CSRF-Token"
	ownerSourceKey = "moment.owner_source"
	ownerGrantKey  = "moment.owner_grant"
)

// CookieOptions binds session cookies to the deployment environment. Production
// callers set Secure; development may use loopback HTTP.
type CookieOptions struct {
	Secure bool
}

type sessionRequest struct {
	// Username names who is signing in. It is optional because the owner token
	// is a break-glass credential that has to keep working when nothing else
	// does, including when nobody remembers the handle. When it is sent it is
	// enforced: a form that collects a name and then ignores it would sign
	// every correct password in as the owner no matter what was typed above it.
	Username *string `json:"username"`
	Token    *string `json:"token"`
	Password *string `json:"password"`
}

type passwordRequest struct {
	Password string `json:"password"`
}

type sessionResponse struct {
	ExpiresAt time.Time `json:"expires_at"`
	CSRFToken string    `json:"csrf_token"`
}

// RegisterOwnerRoutes installs the complete authentication transport. Future
// owner feature groups can reuse the gate rather than decode credentials.
func RegisterOwnerRoutes(engine *gin.Engine, gate *Gate, cookies CookieOptions) {
	manager := gate.manager

	// Signing in and signing out belong to whoever is doing them, not to the
	// owner, so they sit beside the provider and device routes rather than
	// under /owner. Ending a session was owner-scoped until now, which meant a
	// member who signed in through a provider and landed unapproved could not
	// sign out again: the refusal that protects owner powers was standing in
	// front of the one action that takes a power away.
	login := engine.Group("/api/v1/login")
	login.GET("/methods", signInMethods(gate))
	login.POST("/session", exchangeSession(gate, cookies))
	ending := login.Group("")
	ending.Use(gate.RequireAccount())
	ending.DELETE("/session", logoutSession(gate, cookies))

	// What remains under /owner is administration of the instance's own
	// credentials, which is the owner's alone.
	owner := engine.Group("/api/v1/owner")
	owner.Use(gate.RequireOwner())
	owner.GET("/status", ownerStatus(manager))
	owner.POST("/token/rotate", rotateToken(manager))
	owner.PUT("/password", setPassword(manager))
}

// signInMethods reports the credential kinds this instance accepts.
//
// It answers for the site rather than for a named account on purpose: a
// per-handle answer would tell an anonymous caller which handles exist and what
// each one carries, which is an enumeration oracle in exchange for nothing the
// form needs before somebody has typed a secret.
func signInMethods(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		owner, err := gate.accounts.OwnerAccount(ctx.Request.Context())
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		methods, err := gate.SignInMethods(ctx.Request.Context(), owner)
		if err != nil {
			Failure(ctx, http.StatusServiceUnavailable, "OWNER_NOT_INITIALIZED", "owner authentication is not initialized", nil)
			return
		}
		names := make([]string, 0, len(methods))
		for _, method := range methods {
			names = append(names, string(method))
		}
		Success(ctx, http.StatusOK, map[string]any{"methods": names}, nil)
	}
}

// RequireOwner accepts either an exact bearer credential or a signed browser
// session. An Authorization header always takes precedence; malformed/invalid
// bearer input never falls back to cookies. Unsafe cookie-authenticated requests
// additionally require the CSRF token bound into the signed session.
func exchangeSession(gate *Gate, cookies CookieOptions) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		var request sessionRequest
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF ||
			(request.Token == nil) == (request.Password == nil) ||
			(request.Token != nil && *request.Token == "") || (request.Password != nil && *request.Password == "") {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "exactly one owner password or token is required", nil)
			return
		}
		kind := domain.CredentialPassword
		secret := ""
		if request.Token != nil {
			kind, secret = domain.CredentialToken, *request.Token
		} else {
			secret = *request.Password
		}

		account, ok := gate.signingInAs(ctx, request.Username)
		if !ok {
			return
		}
		sessionID, err := app.NewSessionID()
		if err != nil {
			Failure(ctx, http.StatusInternalServerError, "INTERNAL", "the session could not be issued", nil)
			return
		}
		subject := auth.Subject{UserID: account.ID, SessionID: sessionID}

		// Whether this account signs in this way is the credential layer's
		// question, not this handler's. Answering it here would put the same
		// judgement in two voices, and the second one always drifts.
		cookieValue, session, err := gate.IssueSignIn(ctx.Request.Context(), account, kind, secret, subject)
		if err != nil {
			var refusal *domain.Error
			if errors.As(err, &refusal) {
				writeAccountError(ctx, err)
				return
			}
			// Anything else is a secret that did not verify. It says nothing
			// about the account, so neither does the answer.
			unauthorized(ctx)
			return
		}
		// The row is written before the cookie is handed out, so a cookie a
		// caller holds always has something to be revoked by.
		if _, err := gate.accounts.StartSession(ctx.Request.Context(), account.ID, sessionID,
			string(requestSurface(ctx)), session.ExpiresAt.UnixMilli()); err != nil {
			Failure(ctx, http.StatusInternalServerError, "INTERNAL", "the session could not be recorded", nil)
			return
		}
		setSessionCookie(ctx, cookieValue, session.ExpiresAt, cookies)
		Success(ctx, http.StatusOK, sessionResponse{ExpiresAt: session.ExpiresAt, CSRFToken: session.CSRFToken}, nil)
	}
}

// signingInAs resolves the account a sign-in is for, writing its own refusal
// when no account answers to the handle.
//
// It does not judge what that account may present -- resolving who is signing
// in and deciding what they can sign in with are two questions, and keeping
// them apart is what lets the second one change without this one noticing.
//
// A missing handle resolves to the owner. That is the break-glass path: the
// owner token has to work when the database is what needs repairing, including
// when nobody remembers the handle.
func (gate *Gate) signingInAs(ctx *gin.Context, typed *string) (domain.User, bool) {
	if typed == nil || strings.TrimSpace(*typed) == "" {
		owner, err := gate.accounts.OwnerAccount(ctx.Request.Context())
		if err != nil {
			writeAccountError(ctx, err)
			return domain.User{}, false
		}
		return owner, true
	}
	// A handle that could never exist and one nobody holds are the same fact,
	// and splitting them would only describe the validator. Handles are public
	// -- each addresses a profile page at the site root -- so saying which half
	// of the form was wrong leaks nothing and saves a password reset that was
	// never going to help.
	normalized, err := domain.NormalizeUsername(*typed)
	if err != nil {
		Failure(ctx, http.StatusUnauthorized, string(domain.CodeAccountNotFound),
			"no account here holds that username", nil)
		return domain.User{}, false
	}
	account, err := gate.accounts.AccountByUsername(ctx.Request.Context(), normalized)
	if err != nil {
		Failure(ctx, http.StatusUnauthorized, string(domain.CodeAccountNotFound),
			"no account here holds that username", nil)
		return domain.User{}, false
	}
	return account, true
}

func setPassword(manager *auth.Manager) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		var request passwordRequest
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || request.Password == "" {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "a JSON owner password is required", nil)
			return
		}
		grant, ok := ownerGrant(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		status, err := manager.SetPasswordAuthorized(grant, request.Password)
		if errors.Is(err, auth.ErrInvalidPassword) {
			Failure(ctx, http.StatusBadRequest, "PASSWORD_INVALID", "password must be 8 to 72 bytes without surrounding whitespace", nil)
			return
		}
		if errors.Is(err, auth.ErrUnauthorized) {
			unauthorized(ctx)
			return
		}
		if err != nil {
			Failure(ctx, http.StatusInternalServerError, "INTERNAL", "owner password update failed", nil)
			return
		}
		Success(ctx, http.StatusOK, status, nil)
	}
}

// logoutSession revokes the session it was called with, then clears the cookie.
//
// Clearing the cookie alone was the previous behaviour and was not a logout: a
// copied cookie value stayed valid for the rest of its twelve hours, because
// nothing server-side had been told to stop honouring it.
func logoutSession(gate *Gate, cookies CookieOptions) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if session, ok := CurrentSession(ctx); ok {
			if err := gate.accounts.EndSession(ctx.Request.Context(), session.ID); err != nil {
				Failure(ctx, http.StatusInternalServerError, "INTERNAL", "session could not be ended", nil)
				return
			}
		}
		clearSessionCookie(ctx, cookies)
		Success(ctx, http.StatusOK, map[string]bool{"logged_out": true}, nil)
	}
}

func ownerStatus(manager *auth.Manager) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		status, err := manager.Status()
		if err != nil {
			Failure(ctx, http.StatusServiceUnavailable, "OWNER_NOT_INITIALIZED", "owner authentication is not initialized", nil)
			return
		}
		Success(ctx, http.StatusOK, status, nil)
	}
}

func rotateToken(manager *auth.Manager) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		grant, ok := ownerGrant(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		credential, err := manager.RotateAuthorized(grant)
		if errors.Is(err, auth.ErrUnauthorized) {
			unauthorized(ctx)
			return
		}
		if err != nil {
			Failure(ctx, http.StatusInternalServerError, "INTERNAL", "owner token rotation failed", nil)
			return
		}
		Success(ctx, http.StatusOK, credential, nil)
	}
}

func ownerGrant(ctx *gin.Context) (auth.Grant, bool) {
	value, exists := ctx.Get(ownerGrantKey)
	if !exists {
		return auth.Grant{}, false
	}
	grant, ok := value.(auth.Grant)
	return grant, ok
}

func setSessionCookie(ctx *gin.Context, value string, expiresAt time.Time, options CookieOptions) {
	http.SetCookie(ctx.Writer, &http.Cookie{
		Name:     SessionCookieName,
		Value:    value,
		Path:     "/",
		Expires:  expiresAt,
		MaxAge:   max(1, int(time.Until(expiresAt).Seconds())),
		HttpOnly: true,
		Secure:   options.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func clearSessionCookie(ctx *gin.Context, options CookieOptions) {
	http.SetCookie(ctx.Writer, &http.Cookie{
		Name:     SessionCookieName,
		Value:    "",
		Path:     "/",
		Expires:  time.Unix(1, 0).UTC(),
		MaxAge:   -1,
		HttpOnly: true,
		Secure:   options.Secure,
		SameSite: http.SameSiteLaxMode,
	})
}

func unauthorized(ctx *gin.Context) {
	Failure(ctx, http.StatusUnauthorized, "UNAUTHORIZED", "owner authentication is required", nil)
}

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions && method != http.MethodTrace
}

func constantTimeEqual(left, right string) bool {
	if left == "" || right == "" || len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
