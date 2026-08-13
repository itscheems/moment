// Package location integrates external geocoding providers behind Moment's
// small, provider-independent application port.
package location

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/moment/moment/internal/domain"
)

var ErrUnavailable = errors.New("location provider unavailable")

type HTTPClient interface {
	Do(*http.Request) (*http.Response, error)
}

type Options struct {
	AMapAPIKey      string
	GeoapifyAPIKey  string
	Client          HTTPClient
	RequestTimeout  time.Duration
	AMapBaseURL     string
	GeoapifyBaseURL string
}

type provider interface {
	Search(context.Context, string, *domain.GeoPoint, int) ([]domain.Location, error)
	Nearby(context.Context, domain.GeoPoint, int) ([]domain.Location, error)
}

type Router struct {
	amap     provider
	geoapify provider
}

func NewFromEnvironment() *Router {
	return New(Options{
		AMapAPIKey:     os.Getenv("AMAP_API_KEY"),
		GeoapifyAPIKey: os.Getenv("GEOAPIFY_API_KEY"),
	})
}

func New(options Options) *Router {
	client := options.Client
	if client == nil {
		client = &http.Client{Timeout: durationOr(options.RequestTimeout, 4*time.Second)}
	}
	router := &Router{}
	if strings.TrimSpace(options.AMapAPIKey) != "" {
		router.amap = newAMapProvider(client, strings.TrimSpace(options.AMapAPIKey), options.AMapBaseURL)
	}
	if strings.TrimSpace(options.GeoapifyAPIKey) != "" {
		router.geoapify = newGeoapifyProvider(client, strings.TrimSpace(options.GeoapifyAPIKey), options.GeoapifyBaseURL)
	}
	return router
}

func (router *Router) Search(ctx context.Context, query string, bias *domain.GeoPoint, limit int) ([]domain.Location, error) {
	primary, fallback := router.amap, router.geoapify
	if bias != nil && !inMainlandChina(*bias) {
		primary, fallback = router.geoapify, router.amap
	} else if bias != nil && inMainlandChina(*bias) {
		primary, fallback = router.amap, router.geoapify
	}
	return resolve(ctx, primary, fallback, func(ctx context.Context, item provider) ([]domain.Location, error) {
		return item.Search(ctx, query, bias, limit)
	})
}

func (router *Router) Nearby(ctx context.Context, point domain.GeoPoint, limit int) ([]domain.Location, error) {
	primary, fallback := router.geoapify, router.amap
	if inMainlandChina(point) {
		primary, fallback = router.amap, router.geoapify
	}
	return resolve(ctx, primary, fallback, func(ctx context.Context, item provider) ([]domain.Location, error) {
		return item.Nearby(ctx, point, limit)
	})
}

func resolve(ctx context.Context, primary, fallback provider, call func(context.Context, provider) ([]domain.Location, error)) ([]domain.Location, error) {
	if primary != nil {
		items, err := call(ctx, primary)
		if err == nil && len(items) > 0 {
			return items, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	if fallback != nil {
		items, err := call(ctx, fallback)
		if err == nil {
			return items, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
	}
	return nil, ErrUnavailable
}

func inMainlandChina(point domain.GeoPoint) bool {
	latitude, longitude := point.Latitude(), point.Longitude()
	// A routing hint, not a geographic authority. Provider results remain the
	// authority and Geoapify is the automatic fallback near the boundary.
	return latitude >= 18.0 && latitude <= 54.5 && longitude >= 73.0 && longitude <= 135.1
}

func durationOr(value, fallback time.Duration) time.Duration {
	if value > 0 {
		return value
	}
	return fallback
}
