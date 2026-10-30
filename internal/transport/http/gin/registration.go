package ginhttp

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"
	"unicode/utf8"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
)

type registrationOptions struct {
	RegistrationMode  domain.RegistrationMode `json:"registration_mode"`
	UsernameMinLength int                     `json:"username_min_length"`
	UsernameMaxLength int                     `json:"username_max_length"`
	PasswordMinBytes  int                     `json:"password_min_bytes"`
	PasswordMaxBytes  int                     `json:"password_max_bytes"`
}

type registrationRequest struct {
	Username          string `json:"username"`
	Password          string `json:"password"`
	DisplayName       string `json:"display_name"`
	VerificationToken string `json:"verification_token"`
}

type registrationResponse struct {
	AccountCreated bool                 `json:"account_created"`
	Username       string               `json:"username"`
	AccountStatus  domain.AccountStatus `json:"account_status"`
	ExpiresAt      time.Time            `json:"expires_at"`
	CSRFToken      string               `json:"csrf_token"`
}

// RegisterRegistrationRoutes exposes policy information and self-registration
// without requiring an existing browser session.
func RegisterRegistrationRoutes(engine *gin.Engine, gate *Gate, cookies CookieOptions) {
	register := engine.Group("/api/v1/register")
	register.GET("/options", func(ctx *gin.Context) {
		mode, err := gate.accounts.CurrentRegistrationMode(ctx.Request.Context())
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		if err := domain.ValidateRegistrationMode(mode); err != nil {
			Failure(ctx, http.StatusInternalServerError, "DATA_INTEGRITY_FAILED", "stored registration mode is invalid", nil)
			return
		}
		Success(ctx, http.StatusOK, registrationOptions{
			RegistrationMode:  mode,
			UsernameMinLength: domain.UsernameMinLength,
			UsernameMaxLength: domain.UsernameMaxLength,
			PasswordMinBytes:  app.PasswordMinBytes,
			PasswordMaxBytes:  app.PasswordMaxBytes,
		}, nil)
	})
	register.POST("/check", func(ctx *gin.Context) {
		var request struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if !decodeRegistrationBody(ctx, &request) {
			return
		}
		username, err := gate.accounts.CheckRegistrationAccount(ctx.Request.Context(), request.Username, request.Password)
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, map[string]string{"username": username}, nil)
	})
	register.POST("/email", func(ctx *gin.Context) {
		var request struct {
			Email string `json:"email"`
		}
		if !decodeRegistrationBody(ctx, &request) {
			return
		}
		if gate.HasSignedInAccount(ctx) {
			Failure(ctx, http.StatusConflict, "ALREADY_SIGNED_IN", "sign out before creating another account", nil)
			return
		}
		receipt, err := gate.accounts.RequestRegistrationEmail(ctx.Request.Context(), request.Email, ctx.ClientIP())
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, receipt, nil)
	})
	register.POST("/email/verify", func(ctx *gin.Context) {
		var request struct {
			ChallengeID string `json:"challenge_id"`
			Code        string `json:"code"`
		}
		if !decodeRegistrationBody(ctx, &request) {
			return
		}
		receipt, err := gate.accounts.VerifyRegistrationEmail(ctx.Request.Context(), request.ChallengeID, request.Code)
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, receipt, nil)
	})
	register.POST("", func(ctx *gin.Context) {
		if !requireJSONContentType(ctx, "registration") {
			return
		}
		if gate.HasSignedInAccount(ctx) {
			Failure(ctx, http.StatusConflict, "ALREADY_SIGNED_IN", "sign out before creating another account", nil)
			return
		}
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		if !utf8.Valid(body) {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "registration JSON must be UTF-8", nil)
			return
		}
		var request registrationRequest
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "a JSON username and password are required", nil)
			return
		}
		if _, err := gate.accounts.CheckRegistrationAccount(ctx.Request.Context(), request.Username, request.Password); err != nil {
			writeAccountError(ctx, err)
			return
		}
		account, err := gate.accounts.RegisterEmailAccount(ctx.Request.Context(), request.Username, request.Password, request.DisplayName, request.VerificationToken)
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		failSession := func() {
			Failure(ctx, http.StatusServiceUnavailable, "REGISTRATION_SESSION_UNAVAILABLE",
				"account created, but sign-in is unavailable; sign in with the chosen password",
				map[string]any{"account_created": true, "username": account.Username, "recovery": "login"})
		}
		sessionID, err := app.NewSessionID()
		if err != nil {
			failSession()
			return
		}
		value, session, err := gate.manager.IssueSessionForVerifiedSubject(auth.Subject{UserID: account.ID, SessionID: sessionID})
		if err != nil {
			failSession()
			return
		}
		if _, err := gate.accounts.StartSession(ctx.Request.Context(), account.ID, sessionID, string(requestSurface(ctx)), session.ExpiresAt.UnixMilli()); err != nil {
			failSession()
			return
		}
		setSessionCookie(ctx, value, session.ExpiresAt, cookies)
		Success(ctx, http.StatusCreated, registrationResponse{
			AccountCreated: true, Username: account.Username, AccountStatus: account.Status,
			ExpiresAt: session.ExpiresAt, CSRFToken: session.CSRFToken,
		}, nil)
	})
}

func decodeRegistrationBody(ctx *gin.Context, destination any) bool {
	if !requireJSONContentType(ctx, "registration") {
		return false
	}
	body, ok := readBody(ctx)
	if !ok {
		return false
	}
	decoder := json.NewDecoder(strings.NewReader(string(body)))
	decoder.DisallowUnknownFields()
	if !utf8.Valid(body) || decoder.Decode(destination) != nil || decoder.Decode(&struct{}{}) != io.EOF {
		Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "registration requires a valid JSON object", nil)
		return false
	}
	return true
}
