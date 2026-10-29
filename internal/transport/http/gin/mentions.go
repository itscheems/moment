package ginhttp

import (
	"net/http"
	"strconv"

	gin "github.com/gin-gonic/gin"
)

// RegisterMentionRoutes installs account mention lookup.
func RegisterMentionRoutes(engine *gin.Engine, gate *Gate) {
	engine.GET("/api/v1/users/search", func(ctx *gin.Context) {
		limit, err := strconv.Atoi(ctx.DefaultQuery("limit", "20"))
		if err != nil {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "limit must be an integer", nil)
			return
		}
		offset, err := strconv.Atoi(ctx.DefaultQuery("offset", "0"))
		if err != nil {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "offset must be an integer", nil)
			return
		}
		page, err := gate.accounts.SearchUsers(ctx.Request.Context(), ctx.Query("q"), limit, offset)
		if err != nil {
			writeSiteError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, page, nil)
	})
	engine.GET("/api/v1/users/mention-suggestions", gate.RequireActiveAccount(), func(ctx *gin.Context) {
		limit := 8
		if raw := ctx.Query("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil {
				Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "limit must be an integer", map[string]any{"field": "limit"})
				return
			}
			limit = parsed
		}
		results, err := gate.accounts.SuggestMentions(ctx.Request.Context(), ctx.Query("q"), limit)
		if err != nil {
			writeSiteError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, results, map[string]any{"count": len(results)})
	})
}
