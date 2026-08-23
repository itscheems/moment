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
	"unicode/utf8"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/commentdto"
	"github.com/moment/moment/internal/transport/postdto"
)

// RegisterCommentRoutes installs the public thread read alongside the
// authenticated owner mutations.
//
// Writing a comment now requires an account. The anonymous visitor path that
// used to live here took an unauthenticated create, an unauthenticated media
// upload, an IP-keyed rate limiter and a honeypot field with it; removing it
// leaves the server with no unauthenticated write surface at all.
//
// Removal is a write-side decision only. Rows an earlier version accepted stay
// readable: author_kind still admits 'guest' wherever a comment is read, so an
// archive taken before this change imports and renders unchanged.
func RegisterCommentRoutes(engine *gin.Engine, service *app.CommentService, gate *Gate) {
	engine.GET("/api/v1/posts/:id/comments", listComments(service))
	engine.GET("/api/v1/posts/:id/comments/:comment_id/replies", listCommentReplies(service))
	// Writing a comment and moderating the conversation are two authorities,
	// and they were one group until a second account could exist. Pinning
	// somebody else's comment or removing it is the site owner's; writing one,
	// and removing your own, belongs to any approved account.
	authored := engine.Group("/api/v1/owner")
	authored.Use(gate.RequireActiveAccount())
	authored.POST("/posts/:id/comments", createComment(service))
	authored.DELETE("/comments/:id", deleteComment(service))

	moderation := engine.Group("/api/v1/owner")
	moderation.Use(gate.RequireOwner())
	moderation.PUT("/posts/:id/comments/:comment_id/pin", changeOwnerCommentPin(service, true))
	moderation.DELETE("/posts/:id/comments/:comment_id/pin", changeOwnerCommentPin(service, false))
}

func changeOwnerCommentPin(service *app.CommentService, active bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if ctx.Request.URL.RawQuery != "" {
			writeCommentError(ctx, invalidCommentArgument("comment pin does not accept query parameters", "query"))
			return
		}
		postID, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		commentID, err := domain.ParseID(ctx.Param("comment_id"))
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		var result app.CommentPin
		if active {
			result, err = service.Pin(ctx.Request.Context(), postID, commentID)
		} else {
			result, err = service.Unpin(ctx.Request.Context(), postID, commentID)
		}
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, commentdto.FromPin(result), nil)
	}
}

func listComments(service *app.CommentService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		postID, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		cursor, limit, err := commentPageQuery(ctx.Request.URL)
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		thread, err := service.ListPage(ctx.Request.Context(), app.ListCommentsInput{EntryID: postID, Cursor: cursor, Limit: limit})
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, commentdto.FromThread(thread), map[string]any{"next_cursor": commentdto.NextCursor(thread.NextCursor)})
	}
}

func listCommentReplies(service *app.CommentService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		postID, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		rootID, err := domain.ParseID(ctx.Param("comment_id"))
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		cursor, limit, err := commentPageQuery(ctx.Request.URL)
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		page, err := service.ListReplies(ctx.Request.Context(), app.ListRepliesInput{EntryID: postID, RootID: rootID, Cursor: cursor, Limit: limit})
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, commentdto.FromReplyPage(page), map[string]any{"next_cursor": commentdto.NextCursor(page.NextCursor)})
	}
}

// commentPageQuery accepts exactly cursor and limit, each at most once and
// never empty. An unknown parameter is rejected rather than ignored: silently
// dropping a filter a caller believed was applied is worse than refusing it.
func commentPageQuery(value *url.URL) (string, int, error) {
	query, err := url.ParseQuery(value.RawQuery)
	if err != nil {
		return "", 0, invalidCommentArgument("query string is malformed", "query")
	}
	for key, values := range query {
		switch key {
		case "cursor", "limit":
		default:
			return "", 0, invalidCommentArgument("unsupported query parameter", key)
		}
		if len(values) != 1 {
			return "", 0, invalidCommentArgument("query parameter must be supplied once", key)
		}
		if values[0] == "" {
			return "", 0, invalidCommentArgument(key+" cannot be empty", key)
		}
	}
	limit := 0
	if values, present := query["limit"]; present {
		parsed, convErr := strconv.Atoi(values[0])
		if convErr != nil {
			return "", 0, invalidCommentArgument("limit must be an integer", "limit")
		}
		if parsed == 0 {
			return "", 0, invalidCommentArgument("limit must be between 1 and 100", "limit")
		}
		limit = parsed
	}
	return query.Get("cursor"), limit, nil
}

func createComment(service *app.CommentService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		postID, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		request, err := decodeCommentWrite(ctx)
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		key, err := oneHeader(ctx.Request.Header, "Idempotency-Key")
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		author, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		result, err := service.Create(ctx.Request.Context(), app.CreateCommentInput{EntryID: postID, ParentID: request.ParentID, ReplyToCommentID: request.ReplyToCommentID, ContentMD: request.ContentMD, Media: request.Media, Location: request.Location, IdempotencyKey: key, Author: author})
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

func deleteComment(service *app.CommentService) gin.HandlerFunc {
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
		actor, ok := CurrentAccount(ctx)
		if !ok {
			unauthorized(ctx)
			return
		}
		result, err := service.DeleteBy(ctx.Request.Context(), id, actor, dryRun)
		if err != nil {
			writeCommentError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, commentdto.FromComment(result.Comment), map[string]any{"deleted": result.Deleted, "dry_run": result.DryRun, "replies_affected": result.RepliesAffected})
	}
}

type commentWriteRequest struct {
	ContentMD        string
	ParentID         *domain.ID
	ReplyToCommentID *domain.ID
	Media            []domain.EntryMediaUsage
	Location         *domain.Location
}

func decodeCommentWrite(ctx *gin.Context) (commentWriteRequest, error) {
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
		case "reply_to_comment_id":
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				request.ReplyToCommentID = nil
				continue
			}
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return commentWriteRequest{}, invalidCommentArgument("reply_to_comment_id must be a comment ULID or null", key)
			}
			id, parseErr := domain.ParseID(value)
			if parseErr != nil {
				return commentWriteRequest{}, parseErr
			}
			request.ReplyToCommentID = &id
		case "media":
			media, decodeErr := decodeCommentMedia(raw)
			if decodeErr != nil {
				return commentWriteRequest{}, decodeErr
			}
			request.Media = media
		case "location":
			location, decodeErr := decodePostLocation(raw)
			if decodeErr != nil {
				return commentWriteRequest{}, decodeErr
			}
			request.Location = location
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
	// Known caller, refused action. Every route that can raise these sits
	// behind the account gate, so authentication already succeeded and 401
	// would tell the caller to sign in again to no effect; both used to fall
	// through to the default and report a refusal as a server fault.
	case domain.CodeCommentsDisabled, domain.CodeUnauthorized, domain.CodeAccountNotApproved:
		Failure(ctx, http.StatusForbidden, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeRateLimited:
		Failure(ctx, http.StatusTooManyRequests, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeDataIntegrityFailed:
		Failure(ctx, http.StatusInternalServerError, string(domainErr.Code), domainErr.Message, domainErr.Details)
	default:
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "comment operation failed", nil)
	}
}
