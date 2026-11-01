package ginhttp

import (
	"net/http"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
)

const browserAccountsCookieName = "moment_browser_accounts"
const browserAccountsContextKey = "moment.browser_accounts"

type browserAccountView struct {
	Account   accountView `json:"account"`
	Available bool        `json:"available"`
}

// RegisterBrowserAccountRoutes manages only this browser's verified sign-ins.
func RegisterBrowserAccountRoutes(engine *gin.Engine, gate *Gate, cookies CookieOptions, interactions *app.InteractionService) {
	group := engine.Group("/api/v1/browser/accounts")
	group.POST("/bootstrap", gate.RequireAccount(), func(ctx *gin.Context) {
		if !emptyInteractionRequest(ctx) {
			return
		}
		epoch, ok := browserEpoch(ctx, gate)
		if !ok {
			return
		}
		if secret, err := ctx.Cookie(browserAccountsCookieName); err == nil {
			if _, entries, err := gate.accounts.BrowserAccounts(ctx.Request.Context(), secret, epoch); err == nil {
				current, _ := CurrentSession(ctx)
				for _, entry := range entries {
					if entry.Member.SessionID == current.ID {
						writeBrowserAccounts(ctx, gate, secret, epoch)
						return
					}
				}
				set, err := gate.accounts.AddBrowserAccount(ctx.Request.Context(), secret, epoch, current.ID)
				if err != nil {
					writeAccountError(ctx, err)
					return
				}
				setBrowserAccountsCookie(ctx, secret, set.ExpiresAtMS, cookies)
				writeBrowserAccounts(ctx, gate, secret, epoch)
				return
			}
		}
		secret, err := gate.manager.RandomToken(32)
		if err != nil {
			writeGateError(ctx, err)
			return
		}
		csrf, err := gate.manager.RandomToken(32)
		if err != nil {
			writeGateError(ctx, err)
			return
		}
		current, _ := CurrentSession(ctx)
		set, err := gate.accounts.BootstrapBrowserAccounts(ctx.Request.Context(), secret, csrf, epoch, current.ID)
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		setBrowserAccountsCookie(ctx, secret, set.ExpiresAtMS, cookies)
		writeBrowserAccounts(ctx, gate, secret, epoch)
	})
	group.GET("", func(ctx *gin.Context) {
		secret, epoch, ok := authorizeBrowserAccounts(ctx, gate, false)
		if ok {
			writeBrowserAccounts(ctx, gate, secret, epoch)
		}
	})
	group.POST("", func(ctx *gin.Context) {
		secret, epoch, ok := authorizeBrowserAccounts(ctx, gate, true)
		if !ok {
			return
		}
		ctx.Set(browserAccountsContextKey, secret)
		ctx.Set(browserAccountsContextKey+".epoch", epoch)
		exchangeSession(gate, cookies)(ctx)
	})
	group.POST("/:id/activate", func(ctx *gin.Context) {
		secret, epoch, ok := authorizeBrowserAccounts(ctx, gate, true)
		if !ok {
			return
		}
		if !emptyInteractionRequest(ctx) {
			return
		}
		resolved, err := gate.accounts.ActivateBrowserAccount(ctx.Request.Context(), secret, epoch, ctx.Param("id"))
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		if !resumeBrowserMember(ctx, gate, resolved, epoch, cookies) {
			return
		}
		Success(ctx, http.StatusOK, map[string]bool{"switched": true}, nil)
	})
	remove := func(all bool) gin.HandlerFunc {
		return func(ctx *gin.Context) {
			secret, epoch, ok := authorizeBrowserAccounts(ctx, gate, true)
			if !ok {
				return
			}
			if !emptyInteractionRequest(ctx) {
				return
			}
			currentID := ""
			if cookie, err := ctx.Cookie(SessionCookieName); err == nil {
				if issued, err := gate.manager.ValidateSession(cookie); err == nil {
					currentID = issued.UserID
				}
			}
			target := ctx.Param("id")
			if all {
				target = ""
			}
			next, empty, err := gate.accounts.RemoveBrowserAccounts(ctx.Request.Context(), secret, epoch, target, currentID)
			if err != nil {
				writeAccountError(ctx, err)
				return
			}
			changed := all || target == currentID
			if changed {
				clearSessionCookie(ctx, cookies)
				if next != nil && !resumeBrowserMember(ctx, gate, *next, epoch, cookies) {
					return
				}
			}
			if empty {
				clearBrowserAccountsCookie(ctx, cookies)
			}
			Success(ctx, http.StatusOK, map[string]bool{"logged_out": true, "current_changed": changed, "empty": empty}, nil)
		}
	}
	group.DELETE("", remove(true))
	group.DELETE("/:id", remove(false))
	group.GET("/notification-counts", func(ctx *gin.Context) {
		secret, epoch, ok := authorizeBrowserAccounts(ctx, gate, false)
		if !ok {
			return
		}
		_, entries, err := gate.accounts.BrowserAccounts(ctx.Request.Context(), secret, epoch)
		if err != nil {
			writeAccountError(ctx, err)
			return
		}
		counts := map[string]int{}
		if interactions != nil {
			for _, entry := range entries {
				if !entry.Available || !entry.User.CanWritePublicContent() {
					continue
				}
				state, err := interactions.NotificationState(app.WithContentActor(ctx.Request.Context(), entry.User))
				if err != nil {
					writeInteractionError(ctx, err)
					return
				}
				counts[entry.User.ID] = state.NewCount
			}
		}
		Success(ctx, http.StatusOK, map[string]any{"counts": counts}, nil)
	})
}

func browserEpoch(ctx *gin.Context, gate *Gate) (uint64, bool) {
	status, err := gate.manager.Status()
	if err != nil {
		writeGateError(ctx, err)
		return 0, false
	}
	return status.SessionEpoch, true
}

func authorizeBrowserAccounts(ctx *gin.Context, gate *Gate, csrf bool) (string, uint64, bool) {
	secret, err := ctx.Cookie(browserAccountsCookieName)
	if err != nil {
		unauthorized(ctx)
		return "", 0, false
	}
	epoch, ok := browserEpoch(ctx, gate)
	if !ok {
		return "", 0, false
	}
	set, _, err := gate.accounts.BrowserAccounts(ctx.Request.Context(), secret, epoch)
	if err != nil {
		writeAccountError(ctx, err)
		return "", 0, false
	}
	if csrf && !constantTimeEqual(ctx.GetHeader(CSRFHeaderName), set.CSRFToken) {
		Failure(ctx, http.StatusForbidden, "CSRF_INVALID", "a valid browser CSRF token is required", nil)
		return "", 0, false
	}
	return secret, epoch, true
}

func writeBrowserAccounts(ctx *gin.Context, gate *Gate, secret string, epoch uint64) {
	set, entries, err := gate.accounts.BrowserAccounts(ctx.Request.Context(), secret, epoch)
	if err != nil {
		writeAccountError(ctx, err)
		return
	}
	views := make([]browserAccountView, 0, len(entries))
	for _, entry := range entries {
		views = append(views, browserAccountView{Account: publicAccount(entry.User), Available: entry.Available})
	}
	Success(ctx, http.StatusOK, map[string]any{"accounts": views, "csrf_token": set.CSRFToken}, nil)
}

func resumeBrowserMember(ctx *gin.Context, gate *Gate, resolved app.SessionResolution, epoch uint64, cookies CookieOptions) bool {
	value, issued, err := gate.manager.ResumeSession(auth.Subject{UserID: resolved.User.ID, SessionID: resolved.Session.ID}, time.UnixMilli(resolved.Session.ExpiresAtMS), epoch)
	if err != nil {
		writeGateError(ctx, err)
		return false
	}
	setSessionCookie(ctx, value, issued.ExpiresAt, cookies)
	return true
}

func setBrowserAccountsCookie(ctx *gin.Context, secret string, expires int64, cookies CookieOptions) {
	http.SetCookie(ctx.Writer, &http.Cookie{Name: browserAccountsCookieName, Value: secret, Path: "/api/v1", Expires: time.UnixMilli(expires).UTC(), MaxAge: max(1, int(time.Until(time.UnixMilli(expires)).Seconds())), HttpOnly: true, Secure: cookies.Secure, SameSite: http.SameSiteLaxMode})
}
func clearBrowserAccountsCookie(ctx *gin.Context, cookies CookieOptions) {
	http.SetCookie(ctx.Writer, &http.Cookie{Name: browserAccountsCookieName, Path: "/api/v1", Expires: time.Unix(1, 0), MaxAge: -1, HttpOnly: true, Secure: cookies.Secure, SameSite: http.SameSiteLaxMode})
}
