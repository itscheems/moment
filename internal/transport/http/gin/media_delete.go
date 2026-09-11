package ginhttp

import (
	"net/http"
	"net/url"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
)

func deleteMedia(store *media.Store) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeMediaError(ctx, err)
			return
		}
		query, err := url.ParseQuery(ctx.Request.URL.RawQuery)
		if err != nil {
			Failure(ctx, 400, "INVALID_ARGUMENT", "media deletion query is invalid", nil)
			return
		}
		dryRun := false
		if len(query) > 0 {
			values, ok := query["dry_run"]
			if !ok || len(query) != 1 || len(values) != 1 || values[0] != "true" {
				Failure(ctx, 400, "INVALID_ARGUMENT", "only dry_run=true is accepted", nil)
				return
			}
			dryRun = true
		}
		if ctx.Request.ContentLength != 0 || len(ctx.Request.TransferEncoding) != 0 {
			Failure(ctx, 400, "INVALID_ARGUMENT", "media deletion does not accept a body", nil)
			return
		}
		result, err := store.Delete(ctx.Request.Context(), id, dryRun)
		if err != nil {
			writeMediaError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, result, nil)
	}
}
