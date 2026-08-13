package app

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/moment/moment/internal/domain"
)

const (
	DefaultLocationResultLimit = 8
	MaxLocationResultLimit     = 20
	MaxLocationQueryCodePoints = 120
)

type LocationResolver interface {
	Search(context.Context, string, *domain.GeoPoint, int) ([]domain.Location, error)
	Nearby(context.Context, domain.GeoPoint, int) ([]domain.Location, error)
}

type LocationService struct{ resolver LocationResolver }

func NewLocationService(resolver LocationResolver) (*LocationService, error) {
	if resolver == nil {
		return nil, errors.New("location resolver is required")
	}
	return &LocationService{resolver: resolver}, nil
}

func (service *LocationService) Search(ctx context.Context, query string, bias *domain.GeoPoint, limit int) ([]domain.Location, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	query = strings.TrimSpace(query)
	if query == "" || !utf8.ValidString(query) || utf8.RuneCountInString(query) > MaxLocationQueryCodePoints {
		return nil, domain.NewError(domain.CodeInvalidArgument, "location query must contain between 1 and 120 code points", map[string]any{"field": "query"})
	}
	if bias != nil {
		if err := bias.Validate(); err != nil {
			return nil, err
		}
	}
	limit, err := normalizeLocationLimit(limit)
	if err != nil {
		return nil, err
	}
	items, err := service.resolver.Search(ctx, query, bias, limit)
	if err != nil {
		return nil, normalizeLocationError(err)
	}
	return validateLocationResults(items, limit)
}

func (service *LocationService) Nearby(ctx context.Context, point domain.GeoPoint, limit int) ([]domain.Location, error) {
	if err := contextError(ctx); err != nil {
		return nil, err
	}
	if err := point.Validate(); err != nil {
		return nil, err
	}
	limit, err := normalizeLocationLimit(limit)
	if err != nil {
		return nil, err
	}
	items, err := service.resolver.Nearby(ctx, point, limit)
	if err != nil {
		return nil, normalizeLocationError(err)
	}
	return validateLocationResults(items, limit)
}

func normalizeLocationLimit(limit int) (int, error) {
	if limit == 0 {
		return DefaultLocationResultLimit, nil
	}
	if limit < 1 || limit > MaxLocationResultLimit {
		return 0, domain.NewError(domain.CodeInvalidArgument, "location limit must be between 1 and 20", map[string]any{"field": "limit"})
	}
	return limit, nil
}

func validateLocationResults(items []domain.Location, limit int) ([]domain.Location, error) {
	if len(items) > limit {
		return nil, domain.NewError(domain.CodeDataIntegrityFailed, "location provider returned too many results", nil)
	}
	result := make([]domain.Location, 0, len(items))
	seen := make(map[string]struct{}, len(items))
	for index, item := range items {
		if err := item.Validate(); err != nil {
			return nil, domain.NewError(domain.CodeDataIntegrityFailed, "location provider returned an invalid result", map[string]any{"index": index})
		}
		key := locationResultKey(item)
		if _, duplicate := seen[key]; duplicate {
			continue
		}
		seen[key] = struct{}{}
		result = append(result, *domain.CloneLocation(&item))
	}
	return result, nil
}

func locationResultKey(item domain.Location) string {
	if item.ProviderPlaceID != "" {
		return item.Provider + "\x00id\x00" + item.ProviderPlaceID
	}
	key := item.Provider + "\x00text\x00" + item.Name + "\x00" + item.DisplayName
	if item.Point != nil {
		key += "\x00" + string(item.Point.System) + "\x00" +
			strconv.FormatInt(item.Point.LatitudeE7, 10) + "\x00" +
			strconv.FormatInt(item.Point.LongitudeE7, 10)
	}
	return key
}

func normalizeLocationError(err error) error {
	if err == nil {
		return nil
	}
	var domainErr *domain.Error
	if errors.As(err, &domainErr) {
		return err
	}
	return domain.NewError(domain.CodeLocationProviderUnavailable, "location suggestions are temporarily unavailable", nil)
}
