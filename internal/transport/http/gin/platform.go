// Package ginhttp owns Moment's Gin transport and keeps it out of application
// and domain packages.
package ginhttp

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strings"
	"sync"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/media"
)

// LogFunc receives only bounded, preselected event fields.
type LogFunc func(level, event string, fields map[string]any)

// ReadinessFunc reports whether authoritative application dependencies are
// ready. A nil probe is deliberately unready rather than optimistically green.
type ReadinessFunc func(context.Context) error

// BodyLimits are streaming hard limits applied before handlers read requests.
type BodyLimits struct {
	JSON      int64
	Multipart int64
	Archive   int64
	Default   int64
}

// Config composes the reusable HTTP platform. Register is transport-only and
// may add feature handlers without exposing Gin to application/domain code.
type Config struct {
	Assets                fs.FS
	Readiness             ReadinessFunc
	OwnerAuth             *auth.Manager
	SiteSettings          *app.SiteService
	InstanceID            string
	InstanceInitializedAt time.Time
	Posts                 *app.PostService
	Locations             *app.LocationService
	Comments              *app.CommentService
	Search                *app.SearchService
	ShareMetadata         *app.ShareMetadataService
	CommentLimiter        CommentRateLimiter
	Media                 *media.Store
	Archive               ArchiveExporter
	ImportInspector       ImportInspector
	ImportCommitter       ImportCommitter
	Doctor                DoctorReporter
	SecureCookies         bool
	TrustedProxies        []string
	RequestID             func() string
	Log                   LogFunc
	Limits                BodyLimits
	Register              func(*gin.Engine)
}

const (
	defaultJSONLimit      = int64(1 << 20)
	defaultMultipartLimit = int64(64 << 20)
	defaultArchiveLimit   = int64(64 << 30)
	defaultBodyLimit      = int64(1 << 20)
)

var releaseModeOnce sync.Once

// New builds a gin.New engine with an explicit, ordered middleware stack.
func New(config Config) (*gin.Engine, error) {
	if config.Assets == nil {
		return nil, errors.New("embedded web assets are required")
	}
	config.Limits = completeLimits(config.Limits)
	if err := validateLimits(config.Limits); err != nil {
		return nil, err
	}
	if config.RequestID == nil {
		config.RequestID = randomRequestID
	}
	if config.Log == nil {
		config.Log = func(string, string, map[string]any) {}
	}

	releaseModeOnce.Do(func() { gin.SetMode(gin.ReleaseMode) })
	engine := gin.New()
	engine.HandleMethodNotAllowed = true
	engine.ForwardedByClientIP = len(config.TrustedProxies) > 0
	engine.RemoteIPHeaders = []string{"X-Forwarded-For", "X-Real-IP"}
	if err := engine.SetTrustedProxies(config.TrustedProxies); err != nil {
		return nil, err
	}
	engine.MaxMultipartMemory = min(config.Limits.Multipart, 8<<20)
	engine.Use(
		requestIDMiddleware(config.RequestID),
		securityHeadersMiddleware(),
		accessLogMiddleware(config.Log),
		recoveryMiddleware(config.Log),
		bodyLimitMiddleware(config.Limits),
	)

	engine.GET("/healthz", func(ctx *gin.Context) {
		Success(ctx, http.StatusOK, map[string]string{"status": "alive"}, nil)
	})
	engine.GET("/readyz", func(ctx *gin.Context) {
		if config.Readiness == nil {
			Failure(ctx, http.StatusServiceUnavailable, "NOT_READY", "application readiness is not configured", nil)
			return
		}
		if err := config.Readiness(ctx.Request.Context()); err != nil {
			Failure(ctx, http.StatusServiceUnavailable, "NOT_READY", "application dependencies are not ready", nil)
			return
		}
		Success(ctx, http.StatusOK, map[string]string{"status": "ready"}, nil)
	})
	if config.OwnerAuth != nil {
		RegisterOwnerRoutes(engine, config.OwnerAuth, CookieOptions{Secure: config.SecureCookies})
	}
	if config.SiteSettings != nil {
		if config.OwnerAuth == nil {
			return nil, errors.New("site settings routes require owner authentication")
		}
		RegisterSiteRoutes(engine, config.SiteSettings, config.OwnerAuth, config.InstanceID, config.InstanceInitializedAt)
		RegisterMentionRoutes(engine, config.SiteSettings)
	}
	if config.Media != nil {
		if config.OwnerAuth == nil {
			return nil, errors.New("media routes require owner authentication")
		}
		RegisterMediaRoutes(engine, config.Media, config.OwnerAuth)
	}
	if config.Posts != nil {
		if config.OwnerAuth == nil || config.SiteSettings == nil {
			return nil, errors.New("post routes require owner authentication and site settings")
		}
		RegisterPostRoutes(engine, config.Posts, config.SiteSettings, config.OwnerAuth)
	}
	if config.Locations != nil {
		if config.OwnerAuth == nil {
			return nil, errors.New("location routes require owner authentication")
		}
		RegisterLocationRoutes(engine, config.Locations, config.OwnerAuth)
	}
	if config.Comments != nil {
		if config.OwnerAuth == nil {
			return nil, errors.New("comment routes require owner authentication")
		}
		RegisterCommentRoutes(engine, config.Comments, config.Media, config.OwnerAuth, config.CommentLimiter)
	}
	if config.Search != nil {
		RegisterSearchRoutes(engine, config.Search)
	}
	if config.Archive != nil {
		if config.OwnerAuth == nil {
			return nil, errors.New("archive export route requires owner authentication")
		}
		RegisterExportRoutes(engine, config.Archive, config.OwnerAuth)
	}
	if config.ImportInspector != nil {
		if config.OwnerAuth == nil {
			return nil, errors.New("import inspection route requires owner authentication")
		}
		RegisterImportInspectRoutes(engine, config.ImportInspector, config.OwnerAuth)
	}
	if config.ImportCommitter != nil {
		if config.OwnerAuth == nil {
			return nil, errors.New("import commit routes require owner authentication")
		}
		RegisterImportCommitRoutes(engine, config.ImportCommitter, config.OwnerAuth)
	}
	if config.Doctor != nil {
		if config.OwnerAuth == nil {
			return nil, errors.New("doctor route requires owner authentication")
		}
		RegisterDoctorRoutes(engine, config.Doctor, config.OwnerAuth)
	}

	if config.Register != nil {
		config.Register(engine)
	}
	engine.NoMethod(func(ctx *gin.Context) {
		Failure(ctx, http.StatusMethodNotAllowed, "METHOD_NOT_ALLOWED", "the request method is not allowed for this route", nil)
	})
	engine.NoRoute(func(ctx *gin.Context) {
		if isAPIPath(ctx.Request.URL.Path) {
			Failure(ctx, http.StatusNotFound, "API_ROUTE_NOT_FOUND", "the API route does not exist", nil)
			return
		}
		if ctx.Request.Method != http.MethodGet && ctx.Request.Method != http.MethodHead {
			Failure(ctx, http.StatusNotFound, "ROUTE_NOT_FOUND", "the route does not exist", nil)
			return
		}
		serveWeb(config.Assets, config.ShareMetadata, ctx)
	})
	return engine, nil
}

func completeLimits(limits BodyLimits) BodyLimits {
	if limits.JSON == 0 {
		limits.JSON = defaultJSONLimit
	}
	if limits.Multipart == 0 {
		limits.Multipart = defaultMultipartLimit
	}
	if limits.Archive == 0 {
		limits.Archive = defaultArchiveLimit
	}
	if limits.Default == 0 {
		limits.Default = defaultBodyLimit
	}
	return limits
}

func validateLimits(limits BodyLimits) error {
	if limits.JSON <= 0 || limits.Multipart <= 0 || limits.Archive <= 0 || limits.Default <= 0 {
		return errors.New("body limits must be greater than zero")
	}
	return nil
}

func serveWeb(assets fs.FS, share *app.ShareMetadataService, ctx *gin.Context) {
	assetPath := strings.TrimPrefix(path.Clean("/"+ctx.Request.URL.Path), "/")
	if assetPath != "" && assetPath != "." {
		if data, err := fs.ReadFile(assets, assetPath); err == nil {
			contentType := mime.TypeByExtension(path.Ext(assetPath))
			if contentType == "" {
				contentType = http.DetectContentType(data)
			}
			ctx.Header("Cache-Control", "public, max-age=31536000, immutable")
			writeWebContent(ctx, http.StatusOK, contentType, data)
			return
		}
		if path.Ext(assetPath) != "" {
			Failure(ctx, http.StatusNotFound, "ASSET_NOT_FOUND", "the web asset does not exist", nil)
			return
		}
	}
	index, err := fs.ReadFile(assets, "index.html")
	if err != nil {
		Failure(ctx, http.StatusInternalServerError, "WEB_ASSETS_UNAVAILABLE", "the embedded web application is unavailable", nil)
		return
	}
	if metadata, ok := shareMetadataForRequest(ctx, share); ok {
		index = injectShareMetadata(index, metadata)
	}
	ctx.Header("Cache-Control", "no-cache")
	writeWebContent(ctx, http.StatusOK, "text/html; charset=utf-8", index)
}

func writeWebContent(ctx *gin.Context, status int, contentType string, data []byte) {
	ctx.Header("Content-Type", contentType)
	ctx.Status(status)
	if ctx.Request.Method != http.MethodHead {
		_, _ = ctx.Writer.Write(data)
	}
}

func isAPIPath(requestPath string) bool {
	return requestPath == "/api/v1" || strings.HasPrefix(requestPath, "/api/v1/")
}

func randomRequestID() string {
	var value [12]byte
	if _, err := rand.Read(value[:]); err != nil {
		return "req_unknown"
	}
	return "req_" + hex.EncodeToString(value[:])
}
