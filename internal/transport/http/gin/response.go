package ginhttp

import (
	"context"
	"net/http"

	gin "github.com/gin-gonic/gin"
)

const requestIDContextKey contextKey = "request_id"

type contextKey string

// Error describes a stable, client-safe HTTP failure.
type Error struct {
	Code      string         `json:"code"`
	Message   string         `json:"message"`
	Details   map[string]any `json:"details"`
	RequestID string         `json:"request_id"`
}

type errorEnvelope struct {
	Error Error `json:"error"`
}

type successEnvelope struct {
	Data any            `json:"data"`
	Meta map[string]any `json:"meta"`
}

// RequestID returns the correlation ID carried by a request context.
func RequestID(ctx context.Context) string {
	requestID, _ := ctx.Value(requestIDContextKey).(string)
	return requestID
}

// Success writes the shared Moment success envelope.
func Success(ctx *gin.Context, status int, data any, meta map[string]any) {
	if meta == nil {
		meta = make(map[string]any)
	}
	if _, present := meta["request_id"]; !present {
		meta["request_id"] = RequestID(ctx.Request.Context())
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.JSON(status, successEnvelope{Data: data, Meta: meta})
}

// Failure writes and aborts with the shared Moment error envelope.
func Failure(ctx *gin.Context, status int, code, message string, details map[string]any) {
	if details == nil {
		details = map[string]any{}
	}
	ctx.Header("Cache-Control", "no-store")
	ctx.AbortWithStatusJSON(status, errorEnvelope{Error: Error{
		Code:      code,
		Message:   message,
		Details:   details,
		RequestID: RequestID(ctx.Request.Context()),
	}})
}

func bodyTooLarge(ctx *gin.Context, limit int64) {
	Failure(ctx, http.StatusRequestEntityTooLarge, "REQUEST_BODY_TOO_LARGE", "request body exceeds the allowed size", map[string]any{
		"limit_bytes": limit,
	})
}
