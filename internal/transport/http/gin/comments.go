package ginhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"time"
	"unicode/utf8"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/transport/commentdto"
	"github.com/moment/moment/internal/transport/postdto"
)

var errInvalidCommentRateLimit = errors.New("comment rate limit and window must be greater than zero")

const (
	defaultGuestCommentLimit  = 5
	defaultGuestCommentWindow = time.Minute
)

// RegisterCommentRoutes installs public guest reads/writes and distinct
// authenticated owner mutation routes.
func RegisterCommentRoutes(engine *gin.Engine, service *app.CommentService, mediaStore *media.Store, manager *auth.Manager, limiter CommentRateLimiter) {
	if limiter == nil {
		limiter, _ = NewCommentRateLimiter(defaultGuestCommentLimit, defaultGuestCommentWindow, nil)
	}
	engine.GET("/api/v1/posts/:id/comments", listComments(service))
	engine.POST("/api/v1/posts/:id/comments", createGuestComment(service, limiter))
	if mediaStore != nil {
		engine.POST("/api/v1/posts/:id/comment-media", uploadGuestCommentMedia(service, mediaStore, limiter))
	}
	owner := engine.Group("/api/v1/owner")
	owner.Use(RequireOwner(manager))
	owner.POST("/posts/:id/comments", createOwnerComment(service))
	owner.DELETE("/comments/:id", deleteOwnerComment(service))
}

func listComments(service *app.CommentService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.RawQuery != "" {
			writeCommentError(ctx, invalidCommentArgument("comment list does not accept query parameters", "query"))
			return
		}
		postID, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		thread, err := service.List(ctx.Request.Context(), postID)
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, commentdto.FromThread(thread), nil)
	}
}

func createGuestComment(service *app.CommentService, limiter CommentRateLimiter) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		postID, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		request, err := decodeCommentWrite(ctx, false)
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		if request.Honeypot != "" {
			writeCommentError(ctx, domain.NewError(domain.CodeCommentRejected, "comment submission was rejected", nil))
			return
		}
		allowed, retry := limiter.Allow(ctx.ClientIP())
		if !allowed {
			seconds := max(1, int((retry+time.Second-1)/time.Second))
			ctx.Header("Retry-After", strconv.Itoa(seconds))
			writeCommentError(ctx, domain.NewError(domain.CodeRateLimited, "comment rate limit exceeded", map[string]any{"retry_after_seconds": seconds}))
			return
		}
		key, err := oneHeader(ctx.Request.Header, "Idempotency-Key")
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		result, err := service.CreateGuest(ctx.Request.Context(), app.CreateCommentInput{EntryID: postID, ParentID: request.ParentID, AuthorName: request.AuthorName, ContentMD: request.ContentMD, Media: request.Media, IdempotencyKey: key})
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		writeCreatedComment(ctx, result)
	}
}

func createOwnerComment(service *app.CommentService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		postID, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		request, err := decodeCommentWrite(ctx, true)
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		key, err := oneHeader(ctx.Request.Header, "Idempotency-Key")
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		result, err := service.CreateOwner(ctx.Request.Context(), app.CreateCommentInput{EntryID: postID, ParentID: request.ParentID, ContentMD: request.ContentMD, Media: request.Media, IdempotencyKey: key})
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		writeCreatedComment(ctx, result)
	}
}

func writeCreatedComment(ctx *gin.Context, result app.CreateCommentResult) {
	status := http.StatusCreated
	if result.Replayed {
		status = http.StatusOK
	}
	Success(ctx, status, commentdto.FromComment(result.Comment), map[string]any{"replayed": result.Replayed})
}

func deleteOwnerComment(service *app.CommentService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		dryRun, err := commentDeleteQuery(ctx.Request.URL)
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		result, err := service.Delete(ctx.Request.Context(), id, dryRun)
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, commentdto.FromComment(result.Comment), map[string]any{"deleted": result.Deleted, "dry_run": result.DryRun, "replies_affected": result.RepliesAffected})
	}
}

type commentWriteRequest struct {
	AuthorName string
	ContentMD  string
	ParentID   *domain.ID
	Honeypot   string
	Media      []domain.EntryMediaUsage
}

func decodeCommentWrite(ctx *gin.Context, owner bool) (commentWriteRequest, error) {
	mediaType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		Failure(ctx, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "comment writes require application/json", map[string]any{"field": "content_type"})
		return commentWriteRequest{}, errResponseWritten
	}
	body, ok := readBody(ctx)
	if !ok {
		return commentWriteRequest{}, errResponseWritten
	}
	if !utf8.Valid(body) {
		return commentWriteRequest{}, invalidCommentArgument("request body must be valid UTF-8 JSON", "body")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return commentWriteRequest{}, invalidCommentArgument("request must be one JSON object", "body")
	}
	request := commentWriteRequest{Media: []domain.EntryMediaUsage{}}
	seen := make(map[string]bool)
	for decoder.More() {
		keyToken, keyErr := decoder.Token()
		key, valid := keyToken.(string)
		if keyErr != nil || !valid || seen[key] {
			return commentWriteRequest{}, invalidCommentArgument("comment fields must be supplied at most once", "body")
		}
		seen[key] = true
		var raw json.RawMessage
		if decoder.Decode(&raw) != nil {
			return commentWriteRequest{}, invalidCommentArgument("comment field is invalid", key)
		}
		switch key {
		case "content_md":
			if decodeRequiredString(raw, &request.ContentMD) != nil {
				return commentWriteRequest{}, invalidCommentArgument("content_md must be a string", key)
			}
		case "parent_id":
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				request.ParentID = nil
				continue
			}
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return commentWriteRequest{}, invalidCommentArgument("parent_id must be a comment ULID or null", key)
			}
			id, parseErr := domain.ParseID(value)
			if parseErr != nil {
				return commentWriteRequest{}, parseErr
			}
			request.ParentID = &id
		case "media":
			media, decodeErr := decodeCommentMedia(raw)
			if decodeErr != nil {
				return commentWriteRequest{}, decodeErr
			}
			request.Media = media
		case "author_name":
			if owner || decodeRequiredString(raw, &request.AuthorName) != nil {
				return commentWriteRequest{}, invalidCommentArgument("author_name is not accepted for owner writes", key)
			}
		case "website":
			if owner || decodeRequiredString(raw, &request.Honeypot) != nil {
				return commentWriteRequest{}, invalidCommentArgument("website is not accepted for owner writes", key)
			}
		default:
			return commentWriteRequest{}, invalidCommentArgument("unsupported comment field", key)
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return commentWriteRequest{}, invalidCommentArgument("request must be one JSON object", "body")
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return commentWriteRequest{}, invalidCommentArgument("request must contain exactly one JSON document", "body")
	}
	if !seen["content_md"] {
		return commentWriteRequest{}, invalidCommentArgument("request must contain content_md", "content_md")
	}
	if !owner && !seen["author_name"] {
		return commentWriteRequest{}, invalidCommentArgument("request must contain author_name", "author_name")
	}
	return request, nil
}

func decodeCommentMedia(raw json.RawMessage) ([]domain.EntryMediaUsage, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, invalidCommentArgument("media must be an array, not null", "media")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, invalidCommentArgument("media must be an array", "media")
	}
	usages := make([]domain.EntryMediaUsage, 0, domain.MaxCommentMediaUsages)
	for decoder.More() {
		var wire postdto.MediaUsage
		if err := decoder.Decode(&wire); err != nil {
			return nil, invalidCommentArgument("each media usage requires exactly media_id, position, and alt_text", "media")
		}
		id, err := domain.ParseID(wire.MediaID)
		if err != nil {
			return nil, invalidCommentArgument("media_id must be a canonical ULID", "media")
		}
		usages = append(usages, domain.EntryMediaUsage{MediaID: id, Position: wire.Position, AltText: wire.AltText})
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return nil, invalidCommentArgument("media must be one JSON array", "media")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, invalidCommentArgument("media must contain exactly one JSON array", "media")
	}
	if err := domain.ValidateCommentMediaUsages(usages); err != nil {
		return nil, err
	}
	return usages, nil
}

func uploadGuestCommentMedia(service *app.CommentService, store *media.Store, limiter CommentRateLimiter) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		postID, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		if err := service.ValidateCreateTarget(ctx.Request.Context(), postID); err != nil {
			writeCommentError(ctx, err)
			return
		}
		allowed, retry := limiter.Allow(ctx.ClientIP())
		if !allowed {
			seconds := max(1, int((retry+time.Second-1)/time.Second))
			ctx.Header("Retry-After", strconv.Itoa(seconds))
			writeCommentError(ctx, domain.NewError(domain.CodeRateLimited, "comment rate limit exceeded", map[string]any{"retry_after_seconds": seconds}))
			return
		}
		uploadMedia(store)(ctx)
	}
}

func decodeRequiredString(raw []byte, destination *string) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("null string")
	}
	return json.Unmarshal(raw, destination)
}

func commentDeleteQuery(value *url.URL) (bool, error) {
	query, err := url.ParseQuery(value.RawQuery)
	if err != nil {
		return false, invalidCommentArgument("query string is malformed", "query")
	}
	for key, values := range query {
		if key != "dry_run" || len(values) != 1 {
			return false, invalidCommentArgument("only dry_run may be supplied once", key)
		}
	}
	if len(query) == 0 {
		return false, nil
	}
	if query.Get("dry_run") != "true" {
		return false, invalidCommentArgument("dry_run must be true when supplied", "dry_run")
	}
	return true, nil
}

func invalidCommentArgument(message, field string) error {
	return domain.NewError(domain.CodeInvalidArgument, message, map[string]any{"field": field})
}

func writeCommentError(ctx *gin.Context, err error) {
	if errors.Is(err, errResponseWritten) {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		Failure(ctx, http.StatusRequestTimeout, "CANCELLED", "the comment operation was cancelled", nil)
		return
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "comment operation failed", nil)
		return
	}
	switch domainErr.Code {
	case domain.CodeInvalidArgument:
		Failure(ctx, http.StatusBadRequest, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeContentEmpty, domain.CodeContentTooLarge, domain.CodeExternalImageNotAllowed, domain.CodeCommentRejected:
		Failure(ctx, http.StatusUnprocessableEntity, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeMediaTypeUnsupported, domain.CodeMediaTooLarge, domain.CodeMediaDimensionsExceeded:
		Failure(ctx, http.StatusBadRequest, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodePostNotFound, domain.CodeCommentNotFound, domain.CodeMediaNotFound:
		Failure(ctx, http.StatusNotFound, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeCommentDepthExceeded, domain.CodeIdempotencyKeyReused:
		Failure(ctx, http.StatusConflict, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeCommentsDisabled:
		Failure(ctx, http.StatusForbidden, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeRateLimited:
		Failure(ctx, http.StatusTooManyRequests, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeDataIntegrityFailed:
		Failure(ctx, http.StatusInternalServerError, string(domainErr.Code), domainErr.Message, domainErr.Details)
	default:
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "comment operation failed", nil)
	}
}
