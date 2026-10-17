package ginhttp

import (
	"crypto/subtle"
	"encoding/json"
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
	SessionCookieName    = "moment_session"
	CSRFHeaderName       = "X-CSRF-Token"
	accountCredentialKey = "moment.credential_id"
)

type CookieOptions struct{ Secure bool }
type sessionRequest struct {
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

// RegisterAuthenticationRoutes installs account sign-in and personal credentials.
func RegisterAuthenticationRoutes(engine *gin.Engine, gate *Gate, cookies CookieOptions) {
	login := engine.Group("/api/v1/login")
	login.GET("/methods", func(ctx *gin.Context) {
		Success(ctx, http.StatusOK, map[string]any{"methods": []string{"password", "token"}}, nil)
	})
	login.POST("/session", exchangeSession(gate, cookies))
	login.DELETE("/session", gate.RequireAccount(), logoutSession(gate, cookies))
	account := engine.Group("/api/v1/account")
	account.Use(gate.RequireAccount())
	account.GET("/security", accountSecurity(gate))
	account.PUT("/password", setAccountPassword(gate, cookies))
	account.GET("/tokens", listAccountTokens(gate))
	account.POST("/tokens", gate.RequireActiveAccount(), createAccountToken(gate))
	account.DELETE("/tokens/:id", revokeAccountToken(gate))
}

func exchangeSession(gate *Gate, cookies CookieOptions) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		var request sessionRequest
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || (request.Token == nil) == (request.Password == nil) {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "exactly one account password or token is required", nil)
			return
		}
		var account domain.User
		var err error
		if request.Token != nil {
			var verified app.TokenAuthorization
			verified, err = gate.accounts.ResolveAPIToken(ctx.Request.Context(), *request.Token)
			account = verified.User
			if err == nil && request.Username != nil && strings.TrimSpace(*request.Username) != "" && !strings.EqualFold(account.Username, strings.TrimSpace(*request.Username)) {
				unauthorized(ctx)
				return
			}
		} else {
			if request.Username == nil || strings.TrimSpace(*request.Username) == "" {
				Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "a username is required for password sign-in", map[string]any{"field": "username"})
				return
			}
			account, err = gate.accounts.VerifyPassword(ctx.Request.Context(), *request.Username, *request.Password)
		}
		if err != nil {
			writeGateError(ctx, accountAuthenticationError(err))
			return
		}
		sessionID, err := app.NewSessionID()
		if err != nil {
			Failure(ctx, http.StatusInternalServerError, "INTERNAL", "the session could not be issued", nil)
			return
		}
		value, session, err := gate.manager.IssueSessionForVerifiedSubject(auth.Subject{UserID: account.ID, SessionID: sessionID})
		if err != nil {
			Failure(ctx, http.StatusServiceUnavailable, "AUTH_UNAVAILABLE", "session signing is unavailable", nil)
			return
		}
		if _, err := gate.accounts.StartSession(ctx.Request.Context(), account.ID, sessionID, string(requestSurface(ctx)), session.ExpiresAt.UnixMilli()); err != nil {
			writeAccountError(ctx, err)
			return
		}
		setSessionCookie(ctx, value, session.ExpiresAt, cookies)
		Success(ctx, http.StatusOK, sessionResponse{ExpiresAt: session.ExpiresAt, CSRFToken: session.CSRFToken}, nil)
	}
}

func accountSecurity(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		account, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		methods, err := gate.accounts.AccountSignInMethods(ctx.Request.Context(), account.ID)
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, map[string]any{"methods": methods, "password_enabled": domain.CredentialKindsContain(methods, domain.CredentialPassword)}, nil)
	}
}

func setAccountPassword(gate *Gate, cookies CookieOptions) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		account, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		var request passwordRequest
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "a JSON account password is required", nil)
			return
		}
		if err := gate.accounts.SetAccountPassword(ctx.Request.Context(), account.ID, request.Password); err != nil {
			writeAccountError(ctx, err)
			return
		}
		clearSessionCookie(ctx, cookies)
		Success(ctx, http.StatusOK, map[string]bool{"password_enabled": true, "reauthentication_required": true}, nil)
	}
}

func listAccountTokens(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		account, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		tokens, err := gate.accounts.APITokens(ctx.Request.Context(), account.ID)
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, tokens, nil)
	}
}

func createAccountToken(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		account, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		var request struct {
			Label string `json:"label"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "a JSON token label is required", nil)
			return
		}
		token, err := gate.accounts.CreateAPIToken(ctx.Request.Context(), account.ID, request.Label)
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		Success(ctx, http.StatusCreated, token, nil)
	}
}

func revokeAccountToken(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		account, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		if err := gate.accounts.RevokeAPIToken(ctx.Request.Context(), account.ID, ctx.Param("id")); err != nil {
			writeAccountError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, map[string]bool{"revoked": true}, nil)
	}
}

func CurrentCredentialID(ctx *gin.Context) (string, bool) {
	value, ok := ctx.Get(accountCredentialKey)
	id, valid := value.(string)
	return id, ok && valid && id != ""
}

func unauthorized(ctx *gin.Context) {
	Failure(ctx, http.StatusUnauthorized, "UNAUTHORIZED", "account authentication is required", nil)
}

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

func isUnsafeMethod(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions && method != http.MethodTrace
}

func constantTimeEqual(left, right string) bool {
	if left == "" || right == "" || len(left) != len(right) {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(left), []byte(right)) == 1
}
