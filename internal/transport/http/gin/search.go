package ginhttp

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"strconv"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/searchdto"
)

// RegisterSearchRoutes exposes public read-only search and archive aggregates.
// Like the post reads, they answer according to whoever is signed in.
func RegisterSearchRoutes(engine *gin.Engine, service *app.SearchService, gate *Gate) {
	engine.GET("/api/v1/search", searchEntries(service, gate))
	engine.GET("/api/v1/archive/months", gate.RequireAccount(), archiveMonths(service))
	engine.GET("/api/v1/archive/month", gate.RequireAccount(), archiveMonth(service))
}

func searchEntries(service *app.SearchService, gate *Gate) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		query, err := exactQuery(ctx, map[string]bool{"q": true, "from": false, "to": false, "cursor": false, "limit": false, "sort": false})
		if err != nil {
			writeSearchError(ctx, err)
			return
		}
		input := app.SearchInput{Sort: app.SearchSort(query.Get("sort")), Query: query.Get("q"), FromDate: query.Get("from"), ToDate: query.Get("to"), Cursor: query.Get("cursor"), Viewer: gate.Viewer(ctx)}
		if query.Has("limit") {
			input.Limit, err = strconv.Atoi(query.Get("limit"))
			if err != nil || input.Limit == 0 {
				writeSearchError(ctx, searchArgument("limit must be an integer between 1 and 100", "limit"))
				return
			}
		}
		page, err := service.Search(ctx.Request.Context(), input)
		if err != nil {
			writeSearchError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, searchdto.FromSearch(page), map[string]any{"next_cursor": searchdto.NextCursor(page)})
	}
}

func archiveMonths(service *app.SearchService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		query, err := exactQuery(ctx, map[string]bool{"from": false, "to": false})
		if err != nil {
			writeSearchError(ctx, err)
			return
		}
		result, err := service.ArchiveMonths(ctx.Request.Context(), app.ArchiveMonthsInput{FromDate: query.Get("from"), ToDate: query.Get("to"), Viewer: archiveViewer(ctx)})
		if err != nil {
			writeSearchError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, searchdto.FromMonths(result), nil)
	}
}

func archiveMonth(service *app.SearchService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		query, err := exactQuery(ctx, map[string]bool{"month": true})
		if err != nil {
			writeSearchError(ctx, err)
			return
		}
		result, err := service.ArchiveMonth(ctx.Request.Context(), query.Get("month"), archiveViewer(ctx))
		if err != nil {
			writeSearchError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, searchdto.FromMonth(result), nil)
	}
}

func exactQuery(ctx *gin.Context, allowed map[string]bool) (url.Values, error) {
	query, err := url.ParseQuery(ctx.Request.URL.RawQuery)
	if err != nil {
		return nil, searchArgument("query string is malformed", "query")
	}
	for key, values := range query {
		required, ok := allowed[key]
		if !ok {
			return nil, searchArgument("unsupported query parameter", key)
		}
		if len(values) != 1 {
			return nil, searchArgument("query parameter must be supplied once", key)
		}
		if values[0] == "" && (required || key == "from" || key == "to" || key == "cursor" || key == "limit") {
			return nil, searchArgument("query parameter cannot be empty", key)
		}
	}
	for key, required := range allowed {
		if required && !query.Has(key) {
			return nil, searchArgument("required query parameter is missing", key)
		}
	}
	return query, nil
}

func searchArgument(message, field string) error {
	return domain.NewError(domain.CodeInvalidArgument, message, map[string]any{"field": field})
}

func writeSearchError(ctx *gin.Context, err error) {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		Failure(ctx, http.StatusRequestTimeout, "CANCELLED", "the search operation was cancelled", nil)
		return
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		if domainErr.Code == domain.CodeUnauthorized {
			unauthorized(ctx)
			return
		}
		if domainErr.Code == domain.CodeInvalidArgument {
			Failure(ctx, http.StatusBadRequest, string(domainErr.Code), domainErr.Message, domainErr.Details)
			return
		}
		if domainErr.Code == domain.CodeDataIntegrityFailed {
			Failure(ctx, http.StatusInternalServerError, string(domainErr.Code), domainErr.Message, domainErr.Details)
			return
		}
	}
	Failure(ctx, http.StatusInternalServerError, "INTERNAL", "search operation failed", nil)
}

func archiveViewer(ctx *gin.Context) app.Viewer {
	user, ok := CurrentAccount(ctx)
	return app.ViewerFor(user, ok)
}
