package ginhttp

import (
	"net/http"
	"strconv"

	gin "github.com/gin-gonic/gin"
)

// RegisterMentionRoutes mounts the bounded mention suggestion endpoint.
//
// Approved accounts only. While the answer was the site's single owner this was
// a public fact about a public profile; now it enumerates the whole active
// roster, over a leading-wildcard LIKE no index can serve, and it exists for
// one purpose -- completing a mention somebody is typing. The people who need
// it are therefore exactly the people who may write something a mention can
// appear in, which is what RequireActiveAccount already names. Left open, it
// paged the membership list out to any visitor, and charged the database for
// the scan, on behalf of a composer that visitor cannot open.
//
// Pending accounts are refused with everyone else, and that is the point rather
// than a side effect: an account nobody has admitted yet is an application, and
// an autocomplete that returned applications would be a way to read the review
// queue.
func RegisterMentionRoutes(engine *gin.Engine, gate *Gate) {
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
