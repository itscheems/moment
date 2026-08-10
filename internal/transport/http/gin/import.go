package ginhttp

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
)

// ImportInspector is the transport-independent hostile archive preflight.
type ImportInspector interface {
	Inspect(context.Context, io.Reader, uint64) (archive.Inspection, error)
}

// RegisterImportInspectRoutes installs the owner-only raw ZIP upload endpoint.
func RegisterImportInspectRoutes(engine *gin.Engine, inspector ImportInspector, manager *auth.Manager) {
	owner := engine.Group("/api/v1/owner")
	owner.Use(RequireOwner(manager))
	owner.POST("/import/inspect", inspectImport(inspector, manager))
}

func inspectImport(inspector ImportInspector, manager *auth.Manager) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.RawQuery != "" {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "import inspection does not accept query parameters", map[string]any{"field": "query"})
			return
		}
		mediaType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
		if err != nil || mediaType != "application/zip" {
			Failure(ctx, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "import inspection requires application/zip", map[string]any{"field": "content_type"})
			return
		}
		grant, ok := ownerGrant(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		epoch, err := manager.SessionEpochAuthorized(grant)
		if err != nil {
			unauthorized(ctx)
			return
		}
		result, err := inspector.Inspect(ctx.Request.Context(), ctx.Request.Body, epoch)
		if err == nil {
			Success(ctx, http.StatusCreated, result, nil)
			return
		}
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			limit, _ := ctx.Get(bodyLimitKey)
			bodyTooLarge(ctx, limit.(int64))
			return
		}
		if ctx.Request.Context().Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			Failure(ctx, http.StatusRequestTimeout, "CANCELLED", "import inspection was cancelled", nil)
			return
		}
		var domainErr *domain.Error
		if errors.As(err, &domainErr) {
			status := http.StatusUnprocessableEntity
			switch domainErr.Code {
			case domain.CodeInvalidArgument:
				status = http.StatusBadRequest
			case domain.CodeUnauthorized:
				status = http.StatusUnauthorized
			case domain.CodeImportInProgress, domain.CodeImportStagingInvalid:
				status = http.StatusConflict
			case domain.CodeImportStagingExpired:
				status = http.StatusGone
			case domain.CodeImportDiskSpaceInsufficient:
				status = http.StatusInsufficientStorage
			}
			Failure(ctx, status, string(domainErr.Code), domainErr.Message, domainErr.Details)
			return
		}
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "import inspection could not complete", nil)
	}
}
