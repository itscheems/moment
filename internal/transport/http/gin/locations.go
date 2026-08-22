package ginhttp

import (
	"errors"
	"net/http"
	"net/url"
	"strconv"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/locationdto"
)

// RegisterLocationRoutes mounts owner-authenticated place discovery endpoints.
func RegisterLocationRoutes(engine *gin.Engine, locations *app.LocationService, gate *Gate) {
	owner := engine.Group("/api/v1/owner/locations")
	owner.Use(gate.RequireOwner())
	owner.GET("/search", searchLocations(locations))
	owner.GET("/nearby", nearbyLocations(locations))
}

func searchLocations(locations *app.LocationService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		query, err := exactLocationQuery(ctx, map[string]bool{"q": true, "latitude": false, "longitude": false, "limit": false})
		if err != nil {
			writeLocationError(ctx, err)
			return
		}
		limit, err := optionalLocationLimit(query)
		if err != nil {
			writeLocationError(ctx, err)
			return
		}
		bias, err := optionalLocationPoint(query)
		if err != nil {
			writeLocationError(ctx, err)
			return
		}
		items, err := locations.Search(ctx.Request.Context(), query.Get("q"), bias, limit)
		if err != nil {
			writeLocationError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, locationdto.FromDomains(items), nil)
	}
}

func nearbyLocations(locations *app.LocationService) gin.HandlerFunc {
	return func(ctx *gin.Context) {
		query, err := exactLocationQuery(ctx, map[string]bool{"latitude": true, "longitude": true, "limit": false})
		if err != nil {
			writeLocationError(ctx, err)
			return
		}
		limit, err := optionalLocationLimit(query)
		if err != nil {
			writeLocationError(ctx, err)
			return
		}
		point, err := requiredLocationPoint(query)
		if err != nil {
			writeLocationError(ctx, err)
			return
		}
		items, err := locations.Nearby(ctx.Request.Context(), *point, limit)
		if err != nil {
			writeLocationError(ctx, err)
			return
		}
		Success(ctx, http.StatusOK, locationdto.FromDomains(items), nil)
	}
}

func exactLocationQuery(ctx *gin.Context, allowed map[string]bool) (url.Values, error) {
	query, err := url.ParseQuery(ctx.Request.URL.RawQuery)
	if err != nil {
		return nil, invalidPostArgument("query string is malformed", "query")
	}
	for key, values := range query {
		required, ok := allowed[key]
		if !ok {
			return nil, invalidPostArgument("unsupported query parameter", key)
		}
		if len(values) != 1 || values[0] == "" {
			return nil, invalidPostArgument("query parameter must be supplied once and cannot be empty", key)
		}
		_ = required
	}
	for key, required := range allowed {
		if required && query.Get(key) == "" {
			return nil, invalidPostArgument("required query parameter is missing", key)
		}
	}
	return query, nil
}

func optionalLocationLimit(query url.Values) (int, error) {
	if query.Get("limit") == "" {
		return 0, nil
	}
	limit, err := strconv.Atoi(query.Get("limit"))
	if err != nil {
		return 0, invalidPostArgument("limit must be an integer", "limit")
	}
	return limit, nil
}

func optionalLocationPoint(query url.Values) (*domain.GeoPoint, error) {
	latitude, longitude := query.Get("latitude"), query.Get("longitude")
	if latitude == "" && longitude == "" {
		return nil, nil
	}
	if latitude == "" || longitude == "" {
		return nil, invalidPostArgument("latitude and longitude must be supplied together", "location")
	}
	return requiredLocationPoint(query)
}

func requiredLocationPoint(query url.Values) (*domain.GeoPoint, error) {
	latitude, err := strconv.ParseFloat(query.Get("latitude"), 64)
	if err != nil {
		return nil, invalidPostArgument("latitude must be a number", "latitude")
	}
	longitude, err := strconv.ParseFloat(query.Get("longitude"), 64)
	if err != nil {
		return nil, invalidPostArgument("longitude must be a number", "longitude")
	}
	return domain.NewGeoPoint(latitude, longitude, domain.CoordinateWGS84)
}

func writeLocationError(ctx *gin.Context, err error) {
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) {
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "location operation failed", nil)
		return
	}
	switch domainErr.Code {
	case domain.CodeInvalidArgument:
		Failure(ctx, http.StatusBadRequest, string(domainErr.Code), domainErr.Message, domainErr.Details)
	case domain.CodeLocationProviderUnavailable:
		Failure(ctx, http.StatusServiceUnavailable, string(domainErr.Code), domainErr.Message, nil)
	default:
		Failure(ctx, http.StatusInternalServerError, "INTERNAL", "location operation failed", nil)
	}
}
