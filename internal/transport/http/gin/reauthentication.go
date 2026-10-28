package ginhttp

import (
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/domain"
)

const (
	reauthenticationCookieName = "moment_reauthentication"
	// ReauthenticationHeader carries a base64url-encoded current password for API clients.
	ReauthenticationHeader = "X-Moment-Password"
	reauthenticationTTL    = 5 * time.Minute
)

type reauthenticationGrant struct {
	Purpose      string `json:"purpose"`
	AccountID    string `json:"account_id"`
	CredentialID string `json:"credential_id"`
	ExpiresAtMS  int64  `json:"expires_at_ms"`
}

// RequireReauthentication adds independent credential confirmation to a route
// which has already admitted the account. It never replaces the normal gate.
func (gate *Gate) RequireReauthentication() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if gate.requireReauthentication(ctx) {
			ctx.Next()
		}
	}
}

func (gate *Gate) requireReauthentication(ctx *gin.Context) bool {
	account, ok := CurrentAccount(ctx)
	if !ok {
		unauthorized(ctx)
		return false
	}
	credential, ok := CurrentCredentialID(ctx)
	if !ok {
		unauthorized(ctx)
		return false
	}
	if strings.HasPrefix(credential, "session:") {
		if _, valid := gate.reauthenticationExpiry(ctx); valid {
			return true
		}
	} else {
		password := ""
		values := ctx.Request.Header.Values(ReauthenticationHeader)
		if len(values) > 1 {
			return reauthenticationRequired(ctx)
		}
		if len(values) == 1 {
			decoded, err := base64.RawURLEncoding.DecodeString(values[0])
			if err != nil || len(decoded) > 72 {
				return reauthenticationRequired(ctx)
			}
			password = string(decoded)
		}
		token := ""
		if password == "" {
			token = strings.TrimPrefix(credentialBearer(ctx), "Bearer ")
		}
		if err := gate.accounts.VerifySensitiveCredential(ctx.Request.Context(), account.ID, password, token); err == nil {
			return true
		} else if !domain.HasCode(err, domain.CodeUnauthorized) {
			writeAccountError(ctx, err)
			return false
		}
	}
	return reauthenticationRequired(ctx)
}

func credentialBearer(ctx *gin.Context) string {
	fields := strings.Fields(ctx.GetHeader("Authorization"))
	if len(fields) == 2 && strings.EqualFold(fields[0], "Bearer") {
		return "Bearer " + fields[1]
	}
	return ""
}

func reauthenticationRequired(ctx *gin.Context) bool {
	Failure(ctx, http.StatusForbidden, "REAUTHENTICATION_REQUIRED", "confirm your current credential before this operation", nil)
	return false
}

func confirmAccountCredential(gate *Gate, cookies CookieOptions) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !requireJSONContentType(ctx, "credential confirmation") {
			return
		}
		body, ok := readBody(ctx)
		if !ok {
			return
		}
		var request struct {
			Password string `json:"password"`
			Token    string `json:"token"`
		}
		decoder := json.NewDecoder(strings.NewReader(string(body)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil || decoder.Decode(&struct{}{}) != io.EOF || (request.Password == "") == (request.Token == "") {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "one current password or API token is required", nil)
			return
		}
		account, _ := CurrentAccount(ctx)
		credential, _ := CurrentCredentialID(ctx)
		if _, ok := CurrentSession(ctx); !ok {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "browser confirmation requires a session", nil)
			return
		}
		if err := gate.accounts.VerifySensitiveCredential(ctx.Request.Context(), account.ID, request.Password, request.Token); err != nil {
			if domain.HasCode(err, domain.CodeUnauthorized) {
				reauthenticationRequired(ctx)
			} else {
				writeAccountError(ctx, err)
			}
			return
		}
		if expires, ok := issueReauthentication(ctx, gate, cookies, account.ID, credential); ok {
			Success(ctx, http.StatusOK, map[string]any{"expires_at": expires.UTC()}, nil)
		}
	}
}

func revokeAllAccountCredentials(gate *Gate, cookies CookieOptions) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		account, _ := CurrentAccount(ctx)
		if err := gate.accounts.RevokeAccountCredentials(ctx.Request.Context(), account.ID); err != nil {
			writeAccountError(ctx, err)
			return
		}
		clearSessionCookie(ctx, cookies)
		Success(ctx, http.StatusOK, map[string]bool{"revoked": true}, nil)
	}
}

func issueReauthentication(ctx *gin.Context, gate *Gate, cookies CookieOptions, accountID, credential string) (time.Time, bool) {
	expires := gate.now().Add(reauthenticationTTL)
	payload, err := json.Marshal(reauthenticationGrant{Purpose: "sensitive-operation", AccountID: accountID, CredentialID: credential, ExpiresAtMS: expires.UnixMilli()})
	if err != nil {
		Failure(ctx, 500, "INTERNAL", "credential confirmation failed", nil)
		return time.Time{}, false
	}
	value, err := gate.manager.SealState(payload)
	if err != nil {
		Failure(ctx, 503, "AUTH_UNAVAILABLE", "credential confirmation is unavailable", nil)
		return time.Time{}, false
	}
	http.SetCookie(ctx.Writer, &http.Cookie{Name: reauthenticationCookieName, Value: value, Path: "/api/v1", Expires: expires, MaxAge: int(reauthenticationTTL.Seconds()), HttpOnly: true, Secure: cookies.Secure, SameSite: http.SameSiteLaxMode})
	return expires, true
}

func (gate *Gate) reauthenticationExpiry(ctx *gin.Context) (time.Time, bool) {
	account, accountOK := CurrentAccount(ctx)
	credential, credentialOK := CurrentCredentialID(ctx)
	if !accountOK || !credentialOK || !strings.HasPrefix(credential, "session:") {
		return time.Time{}, false
	}
	cookie, err := ctx.Request.Cookie(reauthenticationCookieName)
	if err != nil {
		return time.Time{}, false
	}
	payload, err := gate.manager.OpenState(cookie.Value)
	if err != nil {
		return time.Time{}, false
	}
	var grant reauthenticationGrant
	now := gate.now().UnixMilli()
	if json.Unmarshal(payload, &grant) != nil || grant.Purpose != "sensitive-operation" || grant.AccountID != account.ID || grant.CredentialID != credential || grant.ExpiresAtMS <= now || grant.ExpiresAtMS > now+reauthenticationTTL.Milliseconds() {
		return time.Time{}, false
	}
	return time.UnixMilli(grant.ExpiresAtMS).UTC(), true
}

func confirmedCredential(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		expires, confirmed := gate.reauthenticationExpiry(ctx)
		var until *time.Time
		if confirmed {
			until = &expires
		}
		Success(ctx, http.StatusOK, map[string]any{"confirmed": confirmed, "expires_at": until}, nil)
	}
}
