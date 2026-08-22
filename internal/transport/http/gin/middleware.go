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
	"github.com/moment/moment/internal/observability"

	"github.com/moment/moment/internal/linkfetch"
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

// contentSecurityPolicy is built once from the embed allowlist so the header and
// the recognizers can never disagree about which origins may be framed.
//
// Without an explicit frame-src the directive falls back to default-src 'self',
// which refuses every third-party player. Opening it is a deliberate, bounded
// relaxation: the set is fixed, auditable, and only ever grows. Everything else
// stays closed, and in particular img-src still refuses remote images -- a card
// thumbnail is served from managed media, never hotlinked.
var contentSecurityPolicy = buildContentSecurityPolicy()

func buildContentSecurityPolicy() string {
	frameSources := append([]string{"'self'"}, linkfetch.EmbeddableOrigins()...)
	return strings.Join([]string{
		"default-src 'self'",
		"img-src 'self' data: blob:",
		"style-src 'self' 'unsafe-inline'",
		"script-src 'self'",
		"connect-src 'self'",
		"frame-src " + strings.Join(frameSources, " "),
		"object-src 'none'",
		"base-uri 'none'",
		"frame-ancestors 'none'",
		"form-action 'self'",
	}, "; ")
}

func securityHeadersMiddleware() gin.HandlerFunc {
	return func(ctx *gin.Context) {
		ctx.Header("X-Content-Type-Options", "nosniff")
		ctx.Header("X-Frame-Options", "DENY")
		ctx.Header("Referrer-Policy", "same-origin")
		ctx.Header("Permissions-Policy", "camera=(), microphone=(), geolocation=(self)")
		ctx.Header("Content-Security-Policy", contentSecurityPolicy)
		ctx.Next()
	}
}

// requestSurface decides which entry point a request arrived through.
//
// Arriving over HTTP does not make something the web. `moment --url … post
// create` is a command a person typed, and a script with a bearer token is
// neither a person nor a browser; recording all three as web would make the
// field mean "HTTP" and answer nothing worth asking.
//
// A browser session is the reliable signal, because the cookie is issued by
// this server and only its own page ever carries one. Everything else is a
// programmatic client, which may name itself more precisely.
func requestSurface(ctx *gin.Context) observability.Surface {
	if cookie, err := ctx.Request.Cookie(SessionCookieName); err == nil && cookie.Value != "" {
		return observability.SurfaceWeb
	}
	if ctx.GetHeader("Authorization") == "" {
		// An unauthenticated read of a public page. Nothing was operated on,
		// and the site being read is the web one.
		return observability.SurfaceWeb
	}
	if declared, ok := observability.ClientSurface(ctx.GetHeader(observability.SurfaceHeader)); ok {
		return declared
	}
	return observability.SurfaceAPI
}

func accessLogMiddleware(log SurfaceLogFunc) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		startedAt := time.Now()
		surface := requestSurface(ctx)
		ctx.Next()
		route := ctx.FullPath()
		if route == "" {
			route = "unmatched"
		}
		log(surface, "info", "http_request", map[string]any{
			"request_id":  RequestID(ctx.Request.Context()),
			"method":      ctx.Request.Method,
			"route":       route,
			"status":      ctx.Writer.Status(),
			"duration_ms": time.Since(startedAt).Milliseconds(),
		})
	}
}

func recoveryMiddleware(log SurfaceLogFunc) gin.HandlerFunc {
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
			log(requestSurface(ctx), "error", "http_panic", map[string]any{
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
