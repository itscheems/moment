package ginhttp

import (
	"net/http"
	"time"

	gin "github.com/gin-gonic/gin"
)

// Renewal deliberately never sets a cookie or rotates CSRF. Parallel responses
// cannot undo an account switch, and sensitive-operation confirmation is untouched.
func renewAccountSession(gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		current, ok := CurrentSession(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		if !emptyInteractionRequest(ctx) {
			return
		}
		cookie, err := ctx.Cookie(SessionCookieName)
		if err != nil {
			unauthorized(ctx)
			return
		}
		signed, _, err := gate.manager.AuthorizeSession(cookie)
		if err != nil {
			unauthorized(ctx)
			return
		}
		record, err := gate.accounts.RenewSession(ctx.Request.Context(), current.ID, signed.ExpiresAt.UnixMilli())
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, map[string]time.Time{"expires_at": minTime(time.UnixMilli(record.ExpiresAtMS).UTC(), signed.ExpiresAt)}, nil)
	}
}
