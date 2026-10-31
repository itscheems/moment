package ginhttp

import (
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/interactiondto"
)

// RegisterInteractionRoutes installs likes, reply addressing and account inbox reads.
func RegisterInteractionRoutes(engine *gin.Engine, service *app.InteractionService, gate *Gate) {
	engine.GET("/api/v1/likes", func(ctx *gin.Context) {
		query := ctx.Request.URL.Query()
		for key, values := range query {
			if (key != "kind" && key != "ids") || len(values) != 1 || values[0] == "" {
				writeInteractionError(ctx, invalidCommentArgument("invalid like status query", key))
				return
			}
		}
		ids := make([]domain.ID, 0)
		for _, raw := range strings.Split(query.Get("ids"), ",") {
			id, err := domain.ParseID(raw)
			if err != nil {
				writeInteractionError(ctx, err)
				return
			}
			ids = append(ids, id)
		}
		states, err := service.LikeStatuses(ctx.Request.Context(), query.Get("kind"), ids, gate.Viewer(ctx))
		if err != nil {
			writeInteractionError(ctx, err)
			return
		}
		Success(ctx, 200, states, nil)
	})
	for _, kind := range []string{domain.SubjectPost, domain.SubjectComment} {
		resource := "posts"
		if kind == domain.SubjectComment {
			resource = "comments"
		}
		path := "/api/v1/" + resource + "/:id"
		engine.GET(path+"/mentions", func(ctx *gin.Context) {
			if !emptyInteractionRequest(ctx) {
				return
			}
			ref, err := interactionRef(ctx, kind)
			if err != nil {
				writeInteractionError(ctx, err)
				return
			}
			result, err := service.BoundMentions(ctx.Request.Context(), ref, gate.Viewer(ctx))
			if err != nil {
				writeInteractionError(ctx, err)
				return
			}
			Success(ctx, 200, result, nil)
		})
		engine.GET(path+"/like", likeStatus(service, gate, kind))
		engine.PUT(path+"/like", gate.RequireActiveAccount(), changeLike(service, kind, true))
		engine.DELETE(path+"/like", gate.RequireActiveAccount(), changeLike(service, kind, false))
		engine.GET(path+"/reply-context", gate.RequireActiveAccount(), func(ctx *gin.Context) {
			if !emptyInteractionRequest(ctx) {
				return
			}
			ref, err := interactionRef(ctx, kind)
			if err != nil {
				writeInteractionError(ctx, err)
				return
			}
			result, err := service.ReplyContext(ctx.Request.Context(), ref)
			if err != nil {
				writeInteractionError(ctx, err)
				return
			}
			Success(ctx, 200, result, nil)
		})
	}
	group := engine.Group("/api/v1/account/notifications", gate.RequireActiveAccount())
	group.GET("", func(ctx *gin.Context) {
		cursor, limit, err := commentPageQuery(ctx.Request.URL, "scope", "after", "reason")
		if err != nil {
			writeInteractionError(ctx, err)
			return
		}
		page, err := service.Notifications(ctx.Request.Context(), app.NotificationListInput{Scope: ctx.Query("scope"), Cursor: cursor, After: ctx.Query("after"), Reason: ctx.Query("reason"), Limit: limit})
		if err != nil {
			writeInteractionError(ctx, err)
			return
		}
		Success(ctx, 200, interactiondto.FromPage(page), nil)
	})
	group.GET("/state", func(ctx *gin.Context) {
		if !emptyInteractionRequest(ctx) {
			return
		}
		result, err := service.NotificationState(ctx.Request.Context())
		if err != nil {
			writeInteractionError(ctx, err)
			return
		}
		Success(ctx, 200, result, nil)
	})
	hub := newNotificationHub(service)
	group.GET("/stream", notificationStream(service, gate, hub))
	group.GET("/:notification_id", func(ctx *gin.Context) {
		if !emptyInteractionRequest(ctx) {
			return
		}
		id, err := domain.ParseID(ctx.Param("notification_id"))
		if err != nil {
			writeInteractionError(ctx, err)
			return
		}
		result, err := service.Notification(ctx.Request.Context(), id.String())
		if err != nil {
			writeInteractionError(ctx, err)
			return
		}
		Success(ctx, 200, interactiondto.FromItem(result), nil)
	})
	group.POST("/seen", func(ctx *gin.Context) {
		if ctx.Request.URL.RawQuery != "" {
			writeInteractionError(ctx, invalidCommentArgument("view progress does not accept query parameters", "query"))
			return
		}
		typ, _, err := mime.ParseMediaType(ctx.GetHeader("Content-Type"))
		if err != nil || typ != "application/json" {
			Failure(ctx, 415, "INVALID_ARGUMENT", "view progress requires application/json", nil)
			return
		}
		var request struct {
			Scope         string `json:"scope"`
			ThroughCursor string `json:"through_cursor"`
		}
		decoder := json.NewDecoder(ctx.Request.Body)
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&request); err != nil {
			writeInteractionError(ctx, invalidCommentArgument("view progress must be one JSON object", "body"))
			return
		}
		if decoder.Decode(&struct{}{}) != io.EOF {
			writeInteractionError(ctx, invalidCommentArgument("view progress must be one JSON object", "body"))
			return
		}
		result, err := service.SeeNotifications(ctx.Request.Context(), request.Scope, request.ThroughCursor)
		if err != nil {
			writeInteractionError(ctx, err)
			return
		}
		Success(ctx, 200, result, nil)
	})
}

func emptyInteractionRequest(ctx *gin.Context) bool {
	if ctx.Request.URL.RawQuery == "" {
		if ctx.Request.Body == nil {
			return true
		}
		// Empty bodies may arrive with unknown length or chunked framing.
		var probe [1]byte
		n, err := io.ReadFull(ctx.Request.Body, probe[:])
		if n == 0 && err == io.EOF {
			return true
		}
	}
	writeInteractionError(ctx, invalidCommentArgument("this operation accepts no query parameters or body", "request"))
	return false
}
func interactionRef(ctx *gin.Context, kind string) (domain.ContentRef, error) {
	id, err := domain.ParseID(ctx.Param("id"))
	return domain.ContentRef{Kind: kind, ID: id}, err
}
func likeStatus(service *app.InteractionService, gate *Gate, kind string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !emptyInteractionRequest(ctx) {
			return
		}
		ref, err := interactionRef(ctx, kind)
		if err != nil {
			writeInteractionError(ctx, err)
			return
		}
		state, err := service.LikeStatus(ctx.Request.Context(), ref, gate.Viewer(ctx))
		if err != nil {
			writeInteractionError(ctx, err)
			return
		}
		Success(ctx, 200, state, nil)
	}
}
func changeLike(service *app.InteractionService, kind string, active bool) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !emptyInteractionRequest(ctx) {
			return
		}
		ref, err := interactionRef(ctx, kind)
		if err != nil {
			writeInteractionError(ctx, err)
			return
		}
		state, err := service.Like(ctx.Request.Context(), ref, active)
		if err != nil {
			writeInteractionError(ctx, err)
			return
		}
		Success(ctx, 200, state, nil)
	}
}
func writeInteractionError(ctx *gin.Context, err error) {
	var de *domain.Error
	if errors.As(err, &de) && de.Code == domain.CodeNotificationNotFound {
		Failure(ctx, 404, string(de.Code), de.Message, de.Details)
		return
	}
	writeCommentError(ctx, err)
}

func notificationStream(service *app.InteractionService, gate *Gate, hub *notificationHub) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		if !emptyInteractionRequest(ctx) {
			return
		}
		state, err := service.NotificationState(ctx.Request.Context())
		if err != nil {
			writeInteractionError(ctx, err)
			return
		}
		updates, unsubscribe := hub.subscribe(ctx.Request.Context())
		defer unsubscribe()
		controller := http.NewResponseController(ctx.Writer)
		if err := controller.SetWriteDeadline(time.Now().Add(30 * time.Second)); err != nil && !errors.Is(err, http.ErrNotSupported) {
			writeInteractionError(ctx, err)
			return
		}
		ctx.Header("Content-Type", "text/event-stream")
		ctx.Header("Cache-Control", "no-cache, no-store")
		ctx.Header("X-Accel-Buffering", "no")
		send := func(event string, value any) bool {
			_ = controller.SetWriteDeadline(time.Now().Add(30 * time.Second))
			data, _ := json.Marshal(value)
			if _, err := io.WriteString(ctx.Writer, "event: "+event+"\ndata: "+string(data)+"\n\n"); err != nil {
				return false
			}
			return controller.Flush() == nil
		}
		if !send("sync", state) {
			return
		}
		heartbeat := time.NewTicker(15 * time.Second)
		defer heartbeat.Stop()
		for {
			select {
			case <-ctx.Request.Context().Done():
				return
			case update := <-updates:
				user, err := gate.authenticate(ctx)
				if err != nil || !user.CanWritePublicContent() {
					_ = send("closed", map[string]string{"reason": "authentication"})
					return
				}
				if update.err != nil {
					_ = send("closed", map[string]string{"reason": "unavailable"})
					return
				}
				if update.state != state {
					state = update.state
					if !send("sync", state) {
						return
					}
				}
			case <-heartbeat.C:
				if !send("heartbeat", map[string]bool{"ok": true}) {
					return
				}
			}
		}
	}
}
