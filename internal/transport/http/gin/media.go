package ginhttp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
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
	posters := newUploadPosterCache()
	owner.POST("/media", uploadMedia(store, posters))
	owner.GET("/media-uploads/:uploadID/poster", getUploadPoster(posters))
}

const uploadIDHeader = "X-Moment-Upload-ID"

type uploadPosterEntry struct {
	contents  []byte
	expiresAt time.Time
}

type uploadPosterCache struct {
	mu      sync.RWMutex
	entries map[string]uploadPosterEntry
}

func newUploadPosterCache() *uploadPosterCache {
	return &uploadPosterCache{entries: make(map[string]uploadPosterEntry)}
}

func validUploadID(value string) bool {
	if len(value) < 16 || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z') ||
			(character >= '0' && character <= '9') || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func (cache *uploadPosterCache) put(id string, contents []byte) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := time.Now()
	for key, entry := range cache.entries {
		if now.After(entry.expiresAt) {
			delete(cache.entries, key)
		}
	}
	cache.entries[id] = uploadPosterEntry{contents: bytes.Clone(contents), expiresAt: now.Add(5 * time.Minute)}
}

func (cache *uploadPosterCache) get(id string) ([]byte, bool) {
	cache.mu.RLock()
	entry, found := cache.entries[id]
	cache.mu.RUnlock()
	if !found {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		cache.remove(id)
		return nil, false
	}
	return entry.contents, true
}

func (cache *uploadPosterCache) remove(id string) {
	cache.mu.Lock()
	delete(cache.entries, id)
	cache.mu.Unlock()
}

func uploadMedia(store *media.Store, posterCaches ...*uploadPosterCache) gin.HandlerFunc {
	var posters *uploadPosterCache
	if len(posterCaches) > 0 {
		posters = posterCaches[0]
	}
	return func(ctx *gin.Context) {
		uploadID := strings.TrimSpace(ctx.GetHeader(uploadIDHeader))
		if posters == nil {
			uploadID = ""
		}
		if uploadID != "" && !validUploadID(uploadID) {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "media upload id is invalid", map[string]any{"header": uploadIDHeader})
			return
		}
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
		observer := media.IngestObserver{}
		if uploadID != "" {
			observer.VideoPosterReady = func(contents []byte) { posters.put(uploadID, contents) }
		}
		value, preview, err := store.IngestObserved(ctx.Request.Context(), fileHeader.Filename, file, observer)
		if err != nil {
			if uploadID != "" {
				posters.remove(uploadID)
			}
			writeMediaError(ctx, err)
			return
		}
		Success(ctx, http.StatusCreated, media.NewUploadResult(value, preview), nil)
	}
}

func getUploadPoster(posters *uploadPosterCache) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		uploadID := ctx.Param("uploadID")
		if !validUploadID(uploadID) {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "media upload id is invalid", map[string]any{"path": "uploadID"})
			return
		}
		contents, ready := posters.get(uploadID)
		if !ready {
			ctx.Header("Cache-Control", "no-store")
			ctx.Status(http.StatusNotFound)
			return
		}
		ctx.Header("Content-Type", "image/jpeg")
		ctx.Header("Cache-Control", "no-store")
		ctx.Header("X-Content-Type-Options", "nosniff")
		ctx.Data(http.StatusOK, "image/jpeg", contents)
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
