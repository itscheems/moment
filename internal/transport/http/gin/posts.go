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
	"github.com/moment/moment/internal/transport/linkdto"
	"github.com/moment/moment/internal/transport/locationdto"
	"github.com/moment/moment/internal/transport/postdto"
)

// RegisterPostRoutes installs public reading, author mutations and administrative moderation.
func RegisterPostRoutes(engine *gin.Engine, posts *app.PostService, site *app.SiteService, gate *Gate) {
	// The two reads stay public but are not anonymous: a session or token,
	// when one is presented, widens what they return.
	engine.GET("/api/v1/posts", listPosts(posts, site, gate))
	engine.GET("/api/v1/posts/:id", getPost(posts, gate))
	authored := engine.Group("/api/v1")
	authored.Use(gate.RequireActiveAccount())
	authored.POST("/posts", createPost(posts))
	authored.PATCH("/posts/:id", updatePost(posts))
	authored.DELETE("/posts/:id", deletePost(posts))
	authored.PUT("/posts/:id/repost", repostPost(posts, true))
	authored.DELETE("/posts/:id/repost", repostPost(posts, false))
	engine.PUT("/api/v1/posts/:id/pin", gate.RequireAdmin(), pinPost(site))
	engine.DELETE("/api/v1/posts/:id/pin", gate.RequireAdmin(), unpinPost(site))
}

func repostPost(posts *app.PostService, active bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writePostError(ctx, err)
			return
		}
		post, err := posts.SetReposted(ctx.Request.Context(), id, active)
		if err != nil {
			writePostError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromPost(post), map[string]any{"reposted": active})
	}
}

func pinPost(site *app.SiteService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writePostError(ctx, err)
			return
		}
		settings, err := site.Pin(ctx.Request.Context(), id)
		if err != nil {
			writeSiteError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, settings, nil)
	}
}

func unpinPost(site *app.SiteService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writePostError(ctx, err)
			return
		}
		settings, err := site.Unpin(ctx.Request.Context(), id)
		if err != nil {
			writeSiteError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, settings, nil)
	}
}

func listPosts(posts *app.PostService, site *app.SiteService, gate *Gate) gin.HandlerFunc {
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
		input.Viewer = gate.Viewer(ctx)
		page, err := posts.List(ctx.Request.Context(), input)
		if err != nil {
			writePostError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromPage(page), map[string]any{"next_cursor": postdto.NextCursor(page), "prev_cursor": postdto.PrevCursor(page), "refresh_cursor": postdto.RefreshCursor(page)})
	}
}

func getPost(posts *app.PostService, gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writePostError(ctx, err)
			return
		}
		post, err := posts.Get(ctx.Request.Context(), id, gate.Viewer(ctx))
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
		result, err := posts.Create(ctx.Request.Context(), app.CreatePostInput{ContentMD: input.ContentMD, MapCard: input.MapCard, Visibility: input.Visibility, Media: input.Media, Location: input.Location, LinkCards: input.LinkCards, QuotedPostID: input.QuotedPostID, QuotedCommentID: input.QuotedCommentID, IdempotencyKey: idempotencyKey})
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
		if decoded.MapPresent {
			update.MapCard = &decoded.MapCard
		}
		if decoded.MediaPresent {
			media := domain.CloneEntryMediaUsages(decoded.Media)
			update.Media = &media
		}
		if decoded.LocationPresent {
			location := domain.CloneLocation(decoded.Location)
			update.Location = &location
		}
		if decoded.LinkCardPresent {
			cards := domain.CloneLinkCards(decoded.LinkCards)
			if cards == nil {
				cards = []domain.LinkCard{}
			}
			update.LinkCards = &cards
		}
		if decoded.VisibilityPresent {
			visibility := decoded.Visibility
			update.Visibility = &visibility
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
		case "cursor", "limit", "from", "to", "anchor", "kind", "author_user_id":
		default:
			return app.ListPostsInput{}, invalidPostArgument("unsupported query parameter", key)
		}
		if len(values) != 1 {
			return app.ListPostsInput{}, invalidPostArgument("query parameter must be supplied once", key)
		}
	}
	input := app.ListPostsInput{
		AuthorUserID: query.Get("author_user_id"),
		Cursor:       query.Get("cursor"),
		FromDate:     query.Get("from"),
		ToDate:       query.Get("to"),
		Anchor:       query.Get("anchor"),
	}
	if values, present := query["author_user_id"]; present && (len(values) != 1 || domain.ValidateUserID(values[0]) != nil) {
		return app.ListPostsInput{}, invalidPostArgument("author_user_id must be an account ID", "author_user_id")
	}
	// Absent reads both kinds. An empty value is a client that meant to send
	// something, which is worth refusing rather than silently widening.
	if requested, present := query["kind"]; present {
		kind, kindErr := domain.ParseEntryKind(requested[0])
		if kindErr != nil {
			return app.ListPostsInput{}, invalidPostArgument("kind must be moment or article", "kind")
		}
		input.Kind = kind
	}
	if values, present := query["cursor"]; present && values[0] == "" {
		return app.ListPostsInput{}, invalidPostArgument("cursor cannot be empty", "cursor")
	}
	for _, field := range []string{"from", "to", "anchor"} {
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
	ContentMD         string
	ContentPresent    bool
	MapCard           *domain.MapCard
	MapPresent        bool
	Media             []domain.EntryMediaUsage
	MediaPresent      bool
	Location          *domain.Location
	LocationPresent   bool
	LinkCards         []domain.LinkCard
	LinkCardPresent   bool
	QuotedPostID      *domain.ID
	QuotedCommentID   *domain.ID
	QuotePresent      bool
	Visibility        domain.Visibility
	VisibilityPresent bool
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
		case "map_card":
			if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				card, err := domain.ParseMapCard(raw)
				if err != nil {
					return decodedPostWrite{}, err
				}
				result.MapCard = card
			}
			result.MapPresent = true
		case "location":
			location, err := decodePostLocation(raw)
			if err != nil {
				return decodedPostWrite{}, err
			}
			result.Location = location
			result.LocationPresent = true
		case "link_cards":
			cards, err := decodePostLinkCards(raw)
			if err != nil {
				return decodedPostWrite{}, err
			}
			result.LinkCards = cards
			result.LinkCardPresent = true
		case "visibility":
			var value string
			if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) || json.Unmarshal(raw, &value) != nil {
				return decodedPostWrite{}, invalidPostArgument("visibility must be everyone or only_me", "visibility")
			}
			visibility, parseErr := domain.ParseVisibility(value)
			if parseErr != nil {
				return decodedPostWrite{}, parseErr
			}
			result.Visibility = visibility
			result.VisibilityPresent = true
		case "quoted_post_id", "quoted_comment_id":
			if patch || bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				return decodedPostWrite{}, invalidPostArgument(key+" is only accepted for create and must be a canonical ULID", key)
			}
			var value string
			if json.Unmarshal(raw, &value) != nil {
				return decodedPostWrite{}, invalidPostArgument(key+" must be a string", key)
			}
			id, err := domain.ParseID(value)
			if err != nil {
				return decodedPostWrite{}, invalidPostArgument(key+" must be a canonical ULID", key)
			}
			if key == "quoted_post_id" {
				result.QuotedPostID = &id
			} else {
				result.QuotedCommentID = &id
			}
			result.QuotePresent = true

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
	if (!patch && !result.ContentPresent && !result.MediaPresent && !result.QuotePresent && !result.MapPresent && !result.LinkCardPresent) ||
		(patch && !result.ContentPresent && !result.MediaPresent && !result.LocationPresent && !result.MapPresent && !result.LinkCardPresent && !result.VisibilityPresent) {
		return decodedPostWrite{}, invalidPostArgument("request must contain content_md, media, location, link_cards, or visibility", "body")
	}
	if result.QuotedPostID != nil && result.QuotedCommentID != nil {
		return decodedPostWrite{}, invalidPostArgument("quote targets are mutually exclusive", "quoted_comment_id")
	}
	return result, nil
}

// decodePostLinkCards accepts the ordered card list. An omitted key preserves
// the existing cards on a patch, an empty array clears them, and array order
// defines position -- the same convention media usages already follow.
func decodePostLinkCards(raw json.RawMessage) ([]domain.LinkCard, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, invalidPostArgument("link_cards must be an array, not null", "link_cards")
	}
	var wire []linkdto.Card
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&wire); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, invalidPostArgument("link_cards must be an array of canonical link cards", "link_cards")
	}
	cards := make([]domain.LinkCard, 0, len(wire))
	for index, item := range wire {
		card, err := item.Domain(index)
		if err != nil {
			return nil, err
		}
		cards = append(cards, card)
	}
	if err := domain.ValidateLinkCards(cards); err != nil {
		return nil, err
	}
	return cards, nil
}

func decodePostLocation(raw json.RawMessage) (*domain.Location, error) {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	var wire locationdto.Location
	if err := decoder.Decode(&wire); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return nil, invalidPostArgument("location must contain one canonical location object", "location")
	}
	location, err := wire.Domain()
	if err != nil {
		return nil, err
	}
	return &location, nil
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
	case domain.CodeUnauthorized:
		Failure(ctx, http.StatusForbidden, string(domainErr.Code), domainErr.Message, domainErr.Details)
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
