package ginhttp

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
)

// RegisterMediaRoutes installs explicit immutable original/preview delivery
// and owner-authenticated ingestion. Preview failures never route to originals.
func RegisterMediaRoutes(engine *gin.Engine, store *media.Store, manager *auth.Manager) {
	for _, path := range []string{"/api/v1/media/:id", "/api/v1/media/:id/preview", "/api/v1/media/:id/poster"} {
		kind := "original"
		if strings.HasSuffix(path, "/preview") {
			kind = "preview"
		}
		if strings.HasSuffix(path, "/poster") {
			kind = "poster"
		}
		engine.GET(path, getMediaAsset(store, kind))
		engine.HEAD(path, getMediaAsset(store, kind))
	}
	owner := engine.Group("/api/v1/owner")
	owner.Use(RequireOwner(manager))
	owner.POST("/media", uploadMedia(store))
}

func uploadMedia(store *media.Store) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		fileHeader, err := ctx.FormFile("file")
		if err != nil {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "multipart field 'file' is required", map[string]any{"field": "file"})
			return
		}
		if ctx.Request.MultipartForm != nil {
			defer func() { _ = ctx.Request.MultipartForm.RemoveAll() }()
		}
		file, err := fileHeader.Open()
		if err != nil {
			Failure(ctx, http.StatusBadRequest, "MEDIA_CORRUPT", "uploaded media could not be opened", nil)
			return
		}
		defer func() { _ = file.Close() }()
		value, preview, err := store.Ingest(ctx.Request.Context(), fileHeader.Filename, file)
		if err != nil {
			writeMediaError(ctx, err)
			return
		}
		Success(ctx, http.StatusCreated, media.NewUploadResult(value, preview), nil)
	}
}

func getMediaAsset(store *media.Store, kind string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "media id must be a canonical ULID", map[string]any{"field": "id"})
			return
		}
		var asset *media.StreamAsset
		switch kind {
		case "preview":
			asset, err = store.OpenPreview(ctx.Request.Context(), id)
		case "poster":
			asset, err = store.OpenPoster(ctx.Request.Context(), id)
		default:
			asset, err = store.OpenOriginal(ctx.Request.Context(), id)
		}
		if err != nil {
			writeMediaError(ctx, err)
			return
		}
		defer func() { _ = asset.Close() }()
		ctx.Header("Content-Type", asset.MIMEType)
		ctx.Header("Cache-Control", "public, max-age=31536000, immutable")
		ctx.Header("ETag", asset.ETag)
		ctx.Header("X-Content-Type-Options", "nosniff")
		if matchesETag(ctx.GetHeader("If-None-Match"), asset.ETag) {
			ctx.Status(http.StatusNotModified)
			return
		}
		http.ServeContent(ctx.Writer, ctx.Request, "", time.Time{}, asset.Reader)
	}
}

func matchesETag(header, current string) bool {
	for _, candidate := range strings.Split(header, ",") {
		candidate = strings.TrimSpace(candidate)
		if candidate == "*" || candidate == current || strings.TrimPrefix(candidate, "W/") == current {
			return true
		}
	}
	return false
}

func writeMediaError(ctx *gin.Context, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		Failure(ctx, http.StatusRequestTimeout, "CANCELLED", "the media operation was cancelled", nil)
		return
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "media operation failed", nil)
		return
	}
	var status int
	switch domainErr.Code {
	case domain.CodeInvalidArgument, domain.CodeMediaTypeUnsupported, domain.CodeMediaTooLarge, domain.CodeMediaDimensionsExceeded:
		status = http.StatusBadRequest
	case domain.CodeMediaProcessorUnavailable:
		status = http.StatusServiceUnavailable
	case domain.CodeMediaNotFound:
		status = http.StatusNotFound
	case domain.CodeMediaCorrupt, domain.CodeMediaPreviewFailed, domain.CodeDataIntegrityFailed:
		status = http.StatusInternalServerError
	default:
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "media operation failed", nil)
		return
	}
	message := strings.TrimSpace(domainErr.Message)
	if message == "" {
		message = fmt.Sprintf("media operation failed with %s", domainErr.Code)
	}
	Failure(ctx, status, string(domainErr.Code), message, domainErr.Details)
}
