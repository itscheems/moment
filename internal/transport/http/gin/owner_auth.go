package ginhttp

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/auth"
)

const (
	// SessionCookieName is the private browser credential issued by Moment.
	SessionCookieName = "moment_session"
	// CSRFHeaderName carries the token authenticated inside a browser session.
	CSRFHeaderName = "X-CSRF-Token"
	ownerSourceKey = "moment.owner_source"
)

// CookieOptions binds session cookies to the deployment environment. Production
// callers set Secure; development may use loopback HTTP.
type CookieOptions struct {
	Secure bool
}

type sessionRequest struct {
	Token string `json:"token"`
}

type sessionResponse struct {
	ExpiresAt time.Time `json:"expires_at"`
	CSRFToken string    `json:"csrf_token"`
}

// RegisterOwnerRoutes installs the complete authentication transport. Future
// owner feature groups can reuse RequireOwner rather than decode credentials.
func RegisterOwnerRoutes(engine *gin.Engine, manager *auth.Manager, cookies CookieOptions) {
	owner := engine.Group("/api/v1/owner")
	owner.POST("/session", exchangeSession(manager, cookies))

	protected := owner.Group("")
	protected.Use(RequireOwner(manager))
	protected.DELETE("/session", logoutSession(cookies))
	protected.GET("/status", ownerStatus(manager))
	protected.POST("/token/rotate", rotateToken(manager))
}

// RequireOwner accepts either an exact bearer credential or a signed browser
// session. An Authorization header always takes precedence; malformed/invalid
// bearer input never falls back to cookies. Unsafe cookie-authenticated requests
// additionally require the CSRF token bound into the signed session.
func RequireOwner(manager *auth.Manager) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		authorizationValues := ctx.Request.Header.Values("Authorization")
		if len(authorizationValues) > 1 {
			unauthorized(ctx)
			return
		}
		if len(authorizationValues) == 1 && strings.TrimSpace(authorizationValues[0]) != "" {
			parts := strings.Fields(authorizationValues[0])
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || !manager.AuthenticateBearer(parts[1]) {
				unauthorized(ctx)
				return
			}
			ctx.Set(ownerSourceKey, "bearer")
			ctx.Next()
			return
		}

		cookie, err := ctx.Request.Cookie(SessionCookieName)
		if err != nil {
			unauthorized(ctx)
			return
		}
		session, err := manager.ValidateSession(cookie.Value)
		if err != nil {
			unauthorized(ctx)
			return
		}
		if isUnsafeMethod(ctx.Request.Method) && !constantTimeEqual(ctx.GetHeader(CSRFHeaderName), session.CSRFToken) {
			Failure(ctx, http.StatusForbidden, "CSRF_INVALID", "a valid CSRF token is required", nil)
			return
		}
		ctx.Set(ownerSourceKey, "session")
		ctx.Next()
	}
}

func exchangeSession(manager *auth.Manager, cookies CookieOptions) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		var request sessionRequest
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || request.Token == "" {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "a JSON owner token is required", nil)
			return
		}
		cookieValue, session, err := manager.IssueSession(request.Token)
		if err != nil {
			unauthorized(ctx)
			return
		}
		setSessionCookie(ctx, cookieValue, session.ExpiresAt, cookies)
		Success(ctx, http.StatusOK, sessionResponse{ExpiresAt: session.ExpiresAt, CSRFToken: session.CSRFToken}, nil)
	}
}

func logoutSession(cookies CookieOptions) gin.HandlerFunc {
	return func(ctx *gin.Context) {
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
		credential, err := manager.Rotate()
		if err != nil {
			Failure(ctx, http.StatusInternalServerError, "INTERNAL", "owner token rotation failed", nil)
			return
		}
		Success(ctx, http.StatusOK, credential, nil)
	}
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
