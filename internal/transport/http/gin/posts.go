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
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/postdto"
)

// RegisterPostRoutes exposes public plain-text reads and owner-only mutations
// over one PostService. SiteService supplies the authoritative IANA timezone
// used by application-owned date range interpretation.
func RegisterPostRoutes(engine *gin.Engine, posts *app.PostService, site *app.SiteService, manager *auth.Manager) {
	engine.GET("/api/v1/posts", listPosts(posts, site))
	engine.GET("/api/v1/posts/:id", getPost(posts))
	owner := engine.Group("/api/v1/owner")
	owner.Use(RequireOwner(manager))
	owner.POST("/posts", createPost(posts))
	owner.PATCH("/posts/:id", updatePost(posts))
	owner.DELETE("/posts/:id", deletePost(posts))
}

func listPosts(posts *app.PostService, site *app.SiteService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		input, err := postListInput(ctx)
		if err != nil {
			writePostError(ctx, err)
			return
		}
		settings, err := site.Get(ctx.Request.Context())
		if err != nil {
			writePostError(ctx, err)
			return
		}
		input.Timezone = settings.Timezone
		page, err := posts.List(ctx.Request.Context(), input)
		if err != nil {
			writePostError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromPage(page), map[string]any{"next_cursor": postdto.NextCursor(page)})
	}
}

func getPost(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writePostError(ctx, err)
			return
		}
		post, err := posts.Get(ctx.Request.Context(), id)
		if err != nil {
			writePostError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromPost(post), nil)
	}
}

func createPost(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		input, err := decodePostWrite(ctx, false)
		if err != nil {
			writePostError(ctx, err)
			return
		}
		idempotencyKey, err := oneHeader(ctx.Request.Header, "Idempotency-Key")
		if err != nil {
			writePostError(ctx, err)
			return
		}
		result, err := posts.Create(ctx.Request.Context(), app.CreatePostInput{ContentMD: input.ContentMD, Media: input.Media, IdempotencyKey: idempotencyKey})
		if err != nil {
			writePostError(ctx, err)
			return
		}
		status := http.StatusCreated
		if result.Replayed {
			status = http.StatusOK
		}
		Success(ctx, status, postdto.FromPost(result.Post), map[string]any{"replayed": result.Replayed})
	}
}

func updatePost(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writePostError(ctx, err)
			return
		}
		decoded, err := decodePostWrite(ctx, true)
		if err != nil {
			writePostError(ctx, err)
			return
		}
		update := app.UpdatePostInput{ID: id, ContentMD: decoded.ContentMD, PreserveContent: !decoded.ContentPresent}
		if decoded.MediaPresent {
			media := domain.CloneEntryMediaUsages(decoded.Media)
			update.Media = &media
		}
		post, err := posts.Update(ctx.Request.Context(), update)
		if err != nil {
			writePostError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromPost(post), nil)
	}
}

func deletePost(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writePostError(ctx, err)
			return
		}
		post, err := posts.Delete(ctx.Request.Context(), id)
		if err != nil {
			writePostError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromPost(post), map[string]any{"deleted": true})
	}
}

func postListInput(ctx *gin.Context) (app.ListPostsInput, error) {
	query, err := url.ParseQuery(ctx.Request.URL.RawQuery)
	if err != nil {
		return app.ListPostsInput{}, invalidPostArgument("query string is malformed", "query")
	}
	for key, values := range query {
		switch key {
		case "cursor", "limit", "from", "to":
		default:
			return app.ListPostsInput{}, invalidPostArgument("unsupported query parameter", key)
		}
		if len(values) != 1 {
			return app.ListPostsInput{}, invalidPostArgument("query parameter must be supplied once", key)
		}
	}
	input := app.ListPostsInput{
		Cursor:   query.Get("cursor"),
		FromDate: query.Get("from"),
		ToDate:   query.Get("to"),
	}
	if values, present := query["cursor"]; present && values[0] == "" {
		return app.ListPostsInput{}, invalidPostArgument("cursor cannot be empty", "cursor")
	}
	for _, field := range []string{"from", "to"} {
		if values, present := query[field]; present && values[0] == "" {
			return app.ListPostsInput{}, invalidPostArgument(field+" cannot be empty", field)
		}
	}
	if values, present := query["limit"]; present {
		limit, err := strconv.Atoi(values[0])
		if err != nil {
			return app.ListPostsInput{}, invalidPostArgument("limit must be an integer", "limit")
		}
		if limit == 0 {
			return app.ListPostsInput{}, invalidPostArgument("limit must be between 1 and 100", "limit")
		}
		input.Limit = limit
	}
	return input, nil
}

type decodedPostWrite struct {
	ContentMD      string
	ContentPresent bool
	Media          []domain.EntryMediaUsage
	MediaPresent   bool
}

func decodePostWrite(ctx *gin.Context, patch bool) (decodedPostWrite, error) {
	mediaType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		Failure(ctx, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "post writes require application/json", map[string]any{"field": "content_type"})
		return decodedPostWrite{}, errResponseWritten
	}
	body, ok := readBody(ctx)
	if !ok {
		return decodedPostWrite{}, errResponseWritten
	}
	if !utf8.Valid(body) {
		return decodedPostWrite{}, invalidPostArgument("request body must be valid UTF-8 JSON", "body")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return decodedPostWrite{}, invalidPostArgument("request must be one JSON object", "body")
	}
	seen := map[string]bool{}
	result := decodedPostWrite{Media: []domain.EntryMediaUsage{}}
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, valid := keyToken.(string)
		if err != nil || !valid || seen[key] {
			return decodedPostWrite{}, invalidPostArgument("post fields must be supplied exactly once", "body")
		}
		seen[key] = true
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil {
			return decodedPostWrite{}, invalidPostArgument("post field is malformed", key)
		}
		switch key {
		case "content_md":
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &result.ContentMD) != nil {
				return decodedPostWrite{}, invalidPostArgument("content_md must be a string", "content_md")
			}
			result.ContentPresent = true
		case "media":
			media, err := decodePostMedia(raw)
			if err != nil {
				return decodedPostWrite{}, err
			}
			result.Media = media
			result.MediaPresent = true
		default:
			return decodedPostWrite{}, invalidPostArgument("request contains an unsupported field", key)
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return decodedPostWrite{}, invalidPostArgument("request must be one JSON object", "body")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return decodedPostWrite{}, invalidPostArgument("request must contain exactly one JSON document", "body")
	}
	if (!patch && !result.ContentPresent) || (patch && !result.ContentPresent && !result.MediaPresent) {
		return decodedPostWrite{}, invalidPostArgument("request must contain content_md or media", "body")
	}
	return result, nil
}

func decodePostMedia(raw json.RawMessage) ([]domain.EntryMediaUsage, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, invalidPostArgument("media must be an array, not null", "media")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('[') {
		return nil, invalidPostArgument("media must be an array", "media")
	}
	usages := make([]domain.EntryMediaUsage, 0, domain.MaxEntryMediaUsages)
	for decoder.More() {
		var wire postdto.MediaUsage
		if err := decoder.Decode(&wire); err != nil {
			return nil, invalidPostArgument("each media usage requires exactly media_id, position, and alt_text", "media")
		}
		id, err := domain.ParseID(wire.MediaID)
		if err != nil {
			return nil, invalidPostArgument("media_id must be a canonical ULID", "media")
		}
		usages = append(usages, domain.EntryMediaUsage{MediaID: id, Position: wire.Position, AltText: wire.AltText})
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim(']') {
		return nil, invalidPostArgument("media must be one JSON array", "media")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return nil, invalidPostArgument("media must contain exactly one JSON array", "media")
	}
	if err := domain.ValidateEntryMediaUsages(usages); err != nil {
		return nil, err
	}
	return usages, nil
}

var errResponseWritten = errors.New("HTTP response already written")

func oneHeader(header http.Header, name string) (string, error) {
	values := header.Values(name)
	if len(values) > 1 {
		return "", invalidPostArgument(name+" must be supplied once", name)
	}
	if len(values) == 0 {
		return "", nil
	}
	return values[0], nil
}

func invalidPostArgument(message, field string) error {
	return domain.NewError(domain.CodeInvalidArgument, message, map[string]any{"field": field})
}

func writePostError(ctx *gin.Context, err error) {
	if errors.Is(err, errResponseWritten) {
		return
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		Failure(ctx, http.StatusRequestTimeout, "CANCELLED", "the post operation was cancelled", nil)
		return
	}
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "post operation failed", nil)
		return
	}
	switch domainErr.Code {
	case domain.CodeInvalidArgument:
		Failure(ctx, http.StatusBadRequest, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeContentEmpty, domain.CodeContentTooLarge, domain.CodeExternalImageNotAllowed:
		Failure(ctx, http.StatusUnprocessableEntity, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodePostNotFound, domain.CodeMediaNotFound:
		Failure(ctx, http.StatusNotFound, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeMediaTooLarge:
		Failure(ctx, http.StatusUnprocessableEntity, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeIdempotencyKeyReused:
		Failure(ctx, http.StatusConflict, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeDataIntegrityFailed:
		Failure(ctx, http.StatusInternalServerError, string(domainErr.Code), domainErr.Message, domainErr.Details)
	default:
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "post operation failed", nil)
	}
}
