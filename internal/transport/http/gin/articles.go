package ginhttp

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/postdto"
)

// RegisterArticleRoutes exposes the owner's unfinished articles and the act of
// publishing one. Nothing here is public: a draft is by definition not.
func RegisterArticleRoutes(engine *gin.Engine, posts *app.PostService, gate *Gate) {
	owner := engine.Group("/api/v1/owner")
	owner.Use(gate.RequireOwner())
	owner.GET("/articles/drafts", listArticleDrafts(posts))
	owner.POST("/articles/drafts", createArticleDraft(posts))
	owner.GET("/articles/drafts/:id", getArticleDraft(posts))
	owner.PUT("/articles/drafts/:id", replaceArticleDraft(posts))
	owner.DELETE("/articles/drafts/:id", deleteArticleDraft(posts))
	owner.POST("/articles/drafts/:id/publish", publishArticleDraft(posts))
	// Editing a published article opens a working copy rather than touching
	// the article, so a save while typing changes nothing a reader can see.
	owner.POST("/articles/:id/draft", editArticle(posts))
}

// articleDraftBody is the write shape. body is the document itself rather than
// a string holding one, so a client cannot send something that is not JSON and
// have it stored as if it were.
type articleDraftBody struct {
	Title             string          `json:"title"`
	Body              json.RawMessage `json:"body"`
	CoverMediaID      *string         `json:"cover_media_id"`
	ExpectedUpdatedAt *time.Time      `json:"expected_updated_at"`
}

func listArticleDrafts(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		records, err := posts.ArticleDrafts(ctx.Request.Context())
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromArticleDrafts(records), nil)
	}
}

func getArticleDraft(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		record, err := posts.ArticleDraft(ctx.Request.Context(), id)
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromArticleDraft(record), nil)
	}
}

func createArticleDraft(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		input, err := decodeArticleDraft(ctx)
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		record, err := posts.SaveArticleDraft(ctx.Request.Context(), input)
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		Success(ctx, http.StatusCreated, postdto.FromArticleDraft(record), nil)
	}
}

func replaceArticleDraft(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		input, err := decodeArticleDraft(ctx)
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		input.ID = &id
		record, err := posts.SaveArticleDraft(ctx.Request.Context(), input)
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromArticleDraft(record), nil)
	}
}

func deleteArticleDraft(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		if err := posts.DiscardArticleDraft(ctx.Request.Context(), id); err != nil {
			writeArticleError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, map[string]any{"id": id.String(), "discarded": true}, nil)
	}
}

func editArticle(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		record, err := posts.EditArticle(ctx.Request.Context(), id)
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromArticleDraft(record), nil)
	}
}

func publishArticleDraft(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		var payload struct {
			Visibility string `json:"visibility"`
		}
		if ctx.Request.Body != nil && ctx.Request.ContentLength > 0 {
			if decodeErr := json.NewDecoder(ctx.Request.Body).Decode(&payload); decodeErr != nil {
				writeArticleError(ctx, invalidPostArgument("request body is not valid JSON", "body"))
				return
			}
		}
		post, err := posts.PublishArticleDraft(ctx.Request.Context(), app.PublishArticleDraftInput{
			DraftID:        id,
			Visibility:     domain.Visibility(payload.Visibility),
			IdempotencyKey: ctx.GetHeader("Idempotency-Key"),
		})
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		Success(ctx, http.StatusCreated, postdto.FromPost(post), nil)
	}
}

func decodeArticleDraft(ctx *gin.Context) (app.SaveArticleDraftInput, error) {
	var payload articleDraftBody
	if err := json.NewDecoder(ctx.Request.Body).Decode(&payload); err != nil {
		return app.SaveArticleDraftInput{}, invalidPostArgument("request body is not valid JSON", "body")
	}
	if len(payload.Body) == 0 {
		return app.SaveArticleDraftInput{}, invalidPostArgument("a draft must carry a body", "body")
	}
	input := app.SaveArticleDraftInput{
		Title: payload.Title, BodyJSON: payload.Body, ExpectedUpdatedAt: payload.ExpectedUpdatedAt,
	}
	if payload.CoverMediaID != nil {
		cover, err := domain.ParseID(*payload.CoverMediaID)
		if err != nil {
			return app.SaveArticleDraftInput{}, invalidPostArgument("cover_media_id is not a media id", "cover_media_id")
		}
		input.CoverMediaID = &cover
	}
	return input, nil
}

func writeArticleError(ctx *gin.Context, err error) {
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		writePostError(ctx, err)
		return
	}
	switch domainErr.Code {
	case domain.CodeArticleDraftNotFound:
		Failure(ctx, http.StatusNotFound, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeArticleDraftStale:
		Failure(ctx, http.StatusConflict, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeArticleDraftLimit:
		Failure(ctx, http.StatusUnprocessableEntity, string(domainErr.Code), domainErr.Message, domainErr.Details)
	default:
		writePostError(ctx, err)
	}
}
