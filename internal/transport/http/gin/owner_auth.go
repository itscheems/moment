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
	owner := engine.Group("/api/v1/owner")
	owner.GET("/login-options", loginOptions(manager))
	owner.POST("/session", exchangeSession(gate, cookies))

	protected := owner.Group("")
	protected.Use(gate.RequireOwner())
	protected.DELETE("/session", logoutSession(gate, cookies))
	protected.GET("/status", ownerStatus(manager))
	protected.POST("/token/rotate", rotateToken(manager))
	protected.PUT("/password", setPassword(manager))
}

func loginOptions(manager *auth.Manager) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		status, err := manager.Status()
		if err != nil {
			Failure(ctx, http.StatusServiceUnavailable, "OWNER_NOT_INITIALIZED", "owner authentication is not initialized", nil)
			return
		}
		Success(ctx, http.StatusOK, map[string]bool{"password_enabled": status.PasswordEnabled}, nil)
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
		// A password and a bearer are both the owner's credentials, so the
		// subject is looked up rather than chosen. Resolving it before signing
		// keeps the credential layer free of any knowledge of storage.
		owner, err := gate.accounts.OwnerAccount(ctx.Request.Context())
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		if request.Username != nil && !gate.usernameSignsInHere(ctx, *request.Username, owner) {
			return
		}
		sessionID, err := app.NewSessionID()
		if err != nil {
			Failure(ctx, http.StatusInternalServerError, "INTERNAL", "owner session could not be issued", nil)
			return
		}
		subject := auth.Subject{UserID: owner.ID, SessionID: sessionID}

		var cookieValue string
		var session auth.Session
		if request.Password != nil {
			cookieValue, session, err = gate.manager.IssuePasswordSession(*request.Password, subject)
		} else {
			cookieValue, session, err = gate.manager.IssueSession(*request.Token, subject)
		}
		if err != nil {
			unauthorized(ctx)
			return
		}
		// The row is written before the cookie is handed out, so a cookie a
		// caller holds always has something to be revoked by.
		if _, err := gate.accounts.StartSession(ctx.Request.Context(), owner.ID, sessionID,
			string(requestSurface(ctx)), session.ExpiresAt.UnixMilli()); err != nil {
			Failure(ctx, http.StatusInternalServerError, "INTERNAL", "owner session could not be recorded", nil)
			return
		}
		setSessionCookie(ctx, cookieValue, session.ExpiresAt, cookies)
		Success(ctx, http.StatusOK, sessionResponse{ExpiresAt: session.ExpiresAt, CSRFToken: session.CSRFToken}, nil)
	}
}

// usernameSignsInHere reports whether a typed handle may hold the credential
// this request carries, writing its own refusal when it may not.
//
// Only the owner has a password or a token; every other account reaches this
// instance through a provider. Saying so is more use than a wrong-password
// message, and it leaks nothing -- handles are already public, since each one
// addresses a profile page at the site root.
func (gate *Gate) usernameSignsInHere(ctx *gin.Context, typed string, owner domain.User) bool {
	normalized, err := domain.NormalizeUsername(typed)
	if err != nil {
		Failure(ctx, http.StatusUnauthorized, "ACCOUNT_NOT_FOUND", "no account here holds that username", nil)
		return false
	}
	if strings.EqualFold(normalized, owner.Username) {
		return true
	}
	if _, err := gate.accounts.AccountByUsername(ctx.Request.Context(), normalized); err != nil {
		Failure(ctx, http.StatusUnauthorized, "ACCOUNT_NOT_FOUND", "no account here holds that username", nil)
		return false
	}
	Failure(ctx, http.StatusUnauthorized, "PASSWORD_NOT_AVAILABLE",
		"that account signs in through a third-party provider", nil)
	return false
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
