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
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
)

// RegisterMediaRoutes installs reference-authorized reads and account-scoped uploads.
func RegisterMediaRoutes(engine *gin.Engine, store *media.Store, gate *Gate) {
	for _, path := range []string{"/api/v1/media/:id", "/api/v1/media/:id/preview", "/api/v1/media/:id/poster"} {
		kind := "original"
		if strings.HasSuffix(path, "/preview") {
			kind = "preview"
		}
		if strings.HasSuffix(path, "/poster") {
			kind = "poster"
		}
		engine.GET(path, mediaDeadlines(false, mediaTransferIdle, mediaTransferMax), getMediaAsset(store, kind, gate))
		engine.HEAD(path, mediaDeadlines(false, mediaTransferIdle, mediaTransferMax), getMediaAsset(store, kind, gate))
	}
	uploads := engine.Group("/api/v1")
	uploads.Use(gate.RequireActiveAccount())
	engine.DELETE("/api/v1/media/:id", gate.RequireAdmin(), deleteMedia(store))
	posters := newUploadPosterCache()
	uploads.POST("/media", mediaDeadlines(true, mediaTransferIdle, mediaTransferMax), uploadMedia(store, posters))
	uploads.GET("/media/uploads/:uploadID/poster", getUploadPoster(posters))
}

const uploadIDHeader = "X-Moment-Upload-ID"

type uploadPosterEntry struct {
	contents  []byte
	expiresAt time.Time
}

type uploadPosterKey struct{ accountID, uploadID string }

type uploadPosterCache struct {
	mu      sync.RWMutex
	entries map[uploadPosterKey]uploadPosterEntry
}

func newUploadPosterCache() *uploadPosterCache {
	return &uploadPosterCache{entries: make(map[uploadPosterKey]uploadPosterEntry)}
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

func (cache *uploadPosterCache) put(accountID, id string, contents []byte) {
	cache.mu.Lock()
	defer cache.mu.Unlock()
	now := time.Now()
	for key, entry := range cache.entries {
		if now.After(entry.expiresAt) {
			delete(cache.entries, key)
		}
	}
	cache.entries[uploadPosterKey{accountID: accountID, uploadID: id}] = uploadPosterEntry{contents: bytes.Clone(contents), expiresAt: now.Add(5 * time.Minute)}
}

func (cache *uploadPosterCache) get(accountID, id string) ([]byte, bool) {
	cache.mu.RLock()
	entry, found := cache.entries[uploadPosterKey{accountID: accountID, uploadID: id}]
	cache.mu.RUnlock()
	if !found {
		return nil, false
	}
	if time.Now().After(entry.expiresAt) {
		cache.remove(accountID, id)
		return nil, false
	}
	return entry.contents, true
}

func (cache *uploadPosterCache) remove(accountID, id string) {
	cache.mu.Lock()
	delete(cache.entries, uploadPosterKey{accountID: accountID, uploadID: id})
	cache.mu.Unlock()
}

func uploadMedia(store *media.Store, posterCaches ...*uploadPosterCache) gin.HandlerFunc {
	var posters *uploadPosterCache
	if len(posterCaches) > 0 {
		posters = posterCaches[0]
	}
	return func(ctx *gin.Context) {
		author, present := app.ContentActor(ctx.Request.Context())
		if !present {
			unauthorized(ctx)
			return
		}
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
			var timeout interface{ Timeout() bool }
			if errors.As(err, &timeout) && timeout.Timeout() {
				Failure(ctx, http.StatusRequestTimeout, "MEDIA_UPLOAD_TIMEOUT", "media upload stopped making progress", nil)
				return
			}
			var oversized *http.MaxBytesError
			if errors.As(err, &oversized) {
				bodyTooLarge(ctx, oversized.Limit)
				return
			}
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
		operationContext, cancel := context.WithTimeout(ctx.Request.Context(), mediaPreparationMax)
		defer cancel()
		_ = http.NewResponseController(ctx.Writer).SetWriteDeadline(time.Now().Add(mediaPreparationMax))
		observer := media.IngestObserver{}
		if uploadID != "" {
			observer.VideoPosterReady = func(contents []byte) { posters.put(author.ID, uploadID, contents) }
		}
		value, preview, err := store.IngestObserved(operationContext, fileHeader.Filename, file, observer)
		if err != nil {
			if uploadID != "" {
				posters.remove(author.ID, uploadID)
			}
			writeMediaError(ctx, err)
			return
		}
		Success(ctx, http.StatusCreated, media.NewUploadResult(value, preview), nil)
	}
}

func getUploadPoster(posters *uploadPosterCache) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		author, present := app.ContentActor(ctx.Request.Context())
		if !present {
			unauthorized(ctx)
			return
		}
		uploadID := ctx.Param("uploadID")
		if !validUploadID(uploadID) {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "media upload id is invalid", map[string]any{"path": "uploadID"})
			return
		}
		contents, ready := posters.get(author.ID, uploadID)
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

func getMediaAsset(store *media.Store, kind string, gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			Failure(ctx, http.StatusBadRequest, "INVALID_ARGUMENT", "media id must be a canonical ULID", map[string]any{"field": "id"})
			return
		}
		// A file is as visible as the posts it appears in. The identifier is
		// unguessable, but an address that was once public may have been
		// copied anywhere, and hiding the post has to take its pictures with
		// it. Whether the file exists at all is not revealed to a reader who
		// may not see it.
		visibility, err := store.Visibility(ctx.Request.Context(), id)
		if err != nil {
			writeMediaError(ctx, err)
			return
		}
		readable, err := store.CanRead(ctx.Request.Context(), id, gate.Viewer(ctx))
		if err != nil {
			writeMediaError(ctx, err)
			return
		}
		if !readable {
			writeMediaError(ctx, domain.NewError(domain.CodeMediaNotFound, fmt.Sprintf("media %s does not exist", id), map[string]any{"media_id": id.String()}))
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
		if visibility != domain.VisibilityEveryone {
			// Only its reader's own browser may keep a copy; a shared cache
			// in front of the site would hand it to the next visitor.
			ctx.Header("Cache-Control", "private, max-age=0, must-revalidate")
		} else if kind == "original" {
			ctx.Header("Cache-Control", "public, max-age=31536000, immutable")
		} else {
			// Derived assets keep a stable public URL while their recipe version
			// advances. Revalidate that URL so a corrected preview or poster is
			// not pinned in browser caches for a year; the recipe-bearing ETag
			// still makes unchanged responses a cheap 304.
			ctx.Header("Cache-Control", "public, max-age=0, must-revalidate")
		}
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
	case domain.CodeMediaInUse:
		status = http.StatusConflict
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
