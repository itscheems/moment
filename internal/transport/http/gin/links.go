package ginhttp

import (
	"context"
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/linkdto"
)

// RegisterLinkRoutes mounts the owner-only unfurl endpoint.
//
// Only an authenticated owner can make Moment request an arbitrary URL. A
// visitor comment is never unfurled: doing so would turn an anonymous text box
// into an outbound request trigger.
func RegisterLinkRoutes(engine *gin.Engine, links *app.LinkPreviewService, gate *Gate) {
	previews := engine.Group("/api/v1/links")
	previews.Use(gate.RequireActiveAccount())
	previews.GET("/unfurl", unfurlLink(links))
}

func unfurlLink(links *app.LinkPreviewService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		raw := ctx.Query("url")
		if raw == "" {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "url is required", map[string]any{"field": "url"})
			return
		}
		preview, err := links.Resolve(ctx.Request.Context(), raw)
		if err != nil {
			writeLinkError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, linkdto.FromPreview(preview), nil)
	}
}

func writeLinkError(ctx *gin.Context, err error) {
	var domainError *domain.Error
	if errors.As(err, &domainError) {
		switch domainError.Code {
		case domain.CodeInvalidArgument:
			Failure(ctx, http.StatusBadRequest, string(domainError.Code), domainError.Message, domainError.Details)
			return
		case domain.CodeLinkPreviewUnavailable:
			Failure(ctx, http.StatusServiceUnavailable, string(domainError.Code), domainError.Message, domainError.Details)
			return
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		Failure(ctx, http.StatusRequestTimeout, "INTERNAL", "link preview was cancelled", nil)
		return
	}
	Failure(ctx, http.StatusInternalServerError, "INTERNAL", "link preview failed", nil)
}
