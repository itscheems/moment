package ginhttp

import (
	"context"
	"errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	gin "github.com/gin-gonic/gin"
)

const bodyLimitKey = "moment.body_limit"

func requestIDMiddleware(nextID func() string) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		requestID := nextID()
		requestContext := context.WithValue(ctx.Request.Context(), requestIDContextKey, requestID)
		ctx.Request = ctx.Request.WithContext(requestContext)
		ctx.Header("X-Request-ID", requestID)
		ctx.Next()
	}
}

func securityHeadersMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Header("X-Content-Type-Options", "nosniff")
		ctx.Header("X-Frame-Options", "DENY")
		ctx.Header("Referrer-Policy", "same-origin")
		ctx.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=(self)")
		ctx.Header("Content-Security-Policy", "default-src 'self'; img-src 'self' data: blob:; style-src 'self' 'unsafe-inline'; script-src 'self'; connect-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'; form-action 'self'")
		ctx.Next()
	}
}

func accessLogMiddleware(log LogFunc) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		startedAt := time.Now()
		ctx.Next()
		route := ctx.FullPath()
		if route == "" {
			route = "unmatched"
		}
		log("info", "http_request", map[string]any{
			"request_id":  RequestID(ctx.Request.Context()),
			"method":      ctx.Request.Method,
			"route":       route,
			"status":      ctx.Writer.Status(),
			"duration_ms": time.Since(startedAt).Milliseconds(),
		})
	}
}

func recoveryMiddleware(log LogFunc) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		defer func() {
			recovered := recover()
			if recovered == nil {
				return
			}
			// Preserve net/http's documented connection-abort sentinel. Streaming
			// archive failures after headers must be observable as a truncated
			// response, never converted into a successful partial ZIP.
			if recoveredErr, ok := recovered.(error); ok && errors.Is(recoveredErr, http.ErrAbortHandler) {
				panic(recoveredErr)
			}
			route := ctx.FullPath()
			if route == "" {
				route = "unmatched"
			}
			log("error", "http_panic", map[string]any{
				"request_id": RequestID(ctx.Request.Context()),
				"method":     ctx.Request.Method,
				"route":      route,
			})
			if !ctx.Writer.Written() {
				Failure(ctx, http.StatusInternalServerError, "INTERNAL", "an internal error prevented the request from completing", nil)
				return
			}
			ctx.Abort()
		}()
		ctx.Next()
	}
}

func bodyLimitMiddleware(limits BodyLimits) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		limit := bodyLimitFor(ctx.Request, limits)
		ctx.Set(bodyLimitKey, limit)
		if ctx.Request.Body == nil || ctx.Request.Body == http.NoBody {
			ctx.Next()
			return
		}
		if ctx.Request.ContentLength > limit {
			bodyTooLarge(ctx, limit)
			return
		}
		ctx.Request.Body = http.MaxBytesReader(ctx.Writer, ctx.Request.Body, limit)
		ctx.Next()
	}
}

func bodyLimitFor(request *http.Request, limits BodyLimits) int64 {
	if request.URL.Path == "/api/v1/owner/import/inspect" {
		return limits.Archive
	}
	if request.URL.Path == "/api/v1/owner/import/merge/commit" {
		return limits.MergeJSON
	}
	contentType, _, _ := mime.ParseMediaType(request.Header.Get("Content-Type"))
	switch {
	case contentType == "multipart/form-data":
		return limits.Multipart
	case contentType == "application/zip", contentType == "application/x-zip-compressed":
		return limits.Archive
	case contentType == "application/json", strings.HasSuffix(contentType, "+json"):
		return limits.JSON
	default:
		return limits.Default
	}
}

// readBody is the bounded primitive used by transport handlers after the
// middleware has wrapped the request stream.
func readBody(ctx *gin.Context) ([]byte, bool) {
	data, err := io.ReadAll(ctx.Request.Body)
	if err == nil {
		return data, true
	}
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		limit, _ := ctx.Get(bodyLimitKey)
		bodyTooLarge(ctx, limit.(int64))
		return nil, false
	}
	Failure(ctx, http.StatusBadRequest, "REQUEST_BODY_INVALID", "request body could not be read", nil)
	return nil, false
}
