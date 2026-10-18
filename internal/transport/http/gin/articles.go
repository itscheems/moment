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

// RegisterArticleRoutes installs author-scoped article and draft management resources.
func RegisterArticleRoutes(engine *gin.Engine, posts *app.PostService, gate *Gate) {
	routes := engine.Group("/api/v1")
	routes.Use(gate.RequireActiveAccount())
	routes.GET("/articles/drafts", listArticleDrafts(posts))
	routes.POST("/articles/drafts", createArticleDraft(posts))
	routes.GET("/articles/drafts/:id", getArticleDraft(posts))
	routes.PUT("/articles/drafts/:id", replaceArticleDraft(posts))
	routes.DELETE("/articles/drafts/:id", deleteArticleDraft(posts))
	routes.POST("/articles/drafts/:id/publish", publishArticleDraft(posts))
	routes.POST("/articles/:id/draft", editArticle(posts))
	routes.GET("/articles", listManagedArticles(posts))
	routes.GET("/articles/:id", getManagedArticle(posts))
	routes.POST("/articles/:id/unpublish", withdrawArticle(posts))
	routes.DELETE("/articles/:id", deletePost(posts))
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
		records, err := posts.ArticleDraftSummaries(ctx.Request.Context())
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromArticleDraftSummaries(records), nil)
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
		input.IdempotencyKey = ctx.GetHeader("Idempotency-Key")
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

func listManagedArticles(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		page, err := posts.PublishedArticles(ctx.Request.Context(), app.ListPostsInput{Cursor: ctx.Query("cursor"), Limit: 20})
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromPage(page), map[string]any{"next_cursor": postdto.NextCursor(page), "prev_cursor": postdto.PrevCursor(page), "refresh_cursor": postdto.RefreshCursor(page)})
	}
}
func getManagedArticle(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		post, err := posts.ManagedArticle(ctx.Request.Context(), id)
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromPost(post), nil)
	}
}
func withdrawArticle(posts *app.PostService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		id, err := domain.ParseID(ctx.Param("id"))
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		record, err := posts.WithdrawArticle(ctx.Request.Context(), id)
		if err != nil {
			writeArticleError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, postdto.FromArticleDraft(record), nil)
	}
}
