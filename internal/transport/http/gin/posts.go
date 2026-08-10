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
		content, err := decodePostContent(ctx)
		if err != nil {
			writePostError(ctx, err)
			return
		}
		idempotencyKey, err := oneHeader(ctx.Request.Header, "Idempotency-Key")
		if err != nil {
			writePostError(ctx, err)
			return
		}
		result, err := posts.Create(ctx.Request.Context(), app.CreatePostInput{ContentMD: content, IdempotencyKey: idempotencyKey})
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
		content, err := decodePostContent(ctx)
		if err != nil {
			writePostError(ctx, err)
			return
		}
		post, err := posts.Update(ctx.Request.Context(), app.UpdatePostInput{ID: id, ContentMD: content})
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

func decodePostContent(ctx *gin.Context) (string, error) {
	mediaType, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
	if err != nil || mediaType != "application/json" {
		Failure(ctx, http.StatusUnsupportedMediaType, "INVALID_ARGUMENT", "post writes require application/json", map[string]any{"field": "content_type"})
		return "", errResponseWritten
	}
	body, ok := readBody(ctx)
	if !ok {
		return "", errResponseWritten
	}
	if !utf8.Valid(body) {
		return "", invalidPostArgument("request body must be valid UTF-8 JSON", "body")
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return "", invalidPostArgument("request must be one JSON object", "body")
	}
	seen := false
	content := ""
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok || key != "content_md" || seen {
			return "", invalidPostArgument("request must contain content_md exactly once", "content_md")
		}
		var raw json.RawMessage
		if err := decoder.Decode(&raw); err != nil || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &content) != nil {
			return "", invalidPostArgument("content_md must be a string", "content_md")
		}
		seen = true
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || !seen {
		return "", invalidPostArgument("request must contain content_md", "content_md")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return "", invalidPostArgument("request must contain exactly one JSON document", "body")
	}
	return content, nil
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
	case domain.CodePostNotFound:
		Failure(ctx, http.StatusNotFound, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeIdempotencyKeyReused:
		Failure(ctx, http.StatusConflict, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeDataIntegrityFailed:
		Failure(ctx, http.StatusInternalServerError, string(domainErr.Code), domainErr.Message, domainErr.Details)
	default:
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "post operation failed", nil)
	}
}
