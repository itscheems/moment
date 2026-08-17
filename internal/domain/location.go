package domain

import (
	"math"
	"strings"
	"unicode/utf8"
)

const (
	// MaxLocationNameCodePoints and related ceilings bound portable location
	// snapshot text independently of provider-specific limits.
	MaxLocationNameCodePoints = 160
	// MaxLocationDisplayNameCodePoints bounds a human-readable full address.
	MaxLocationDisplayNameCodePoints = 500
	// MaxLocationPartCodePoints bounds each normalized address component.
	MaxLocationPartCodePoints = 120
	// MaxLocationProviderIDCodePoints bounds an opaque provider identifier.
	MaxLocationProviderIDCodePoints = 512
)

// CoordinateSystem names the authority used by a stored coordinate. Browser
// geolocation and Geoapify use WGS84; domestic AMap results use GCJ-02.
type CoordinateSystem string

const (
	// CoordinateWGS84 and CoordinateGCJ02 are the coordinate authorities Moment
	// can persist without silently converting values.
	CoordinateWGS84 CoordinateSystem = "wgs84"
	// CoordinateGCJ02 identifies coordinates returned by domestic AMap APIs.
	CoordinateGCJ02 CoordinateSystem = "gcj02"
)

// GeoPoint stores decimal coordinates as E7 integers so snapshots round-trip
// through SQLite, JSON, archives, and CLI output without float drift.
type GeoPoint struct {
	LatitudeE7  int64
	LongitudeE7 int64
	System      CoordinateSystem
}

// NewGeoPoint validates decimal coordinates and stores them without float drift.
func NewGeoPoint(latitude, longitude float64, system CoordinateSystem) (*GeoPoint, error) {
	if math.IsNaN(latitude) || math.IsInf(latitude, 0) || latitude < -90 || latitude > 90 {
		return nil, NewError(CodeInvalidArgument, "latitude must be between -90 and 90", map[string]any{"field": "latitude"})
	}
	if math.IsNaN(longitude) || math.IsInf(longitude, 0) || longitude < -180 || longitude > 180 {
		return nil, NewError(CodeInvalidArgument, "longitude must be between -180 and 180", map[string]any{"field": "longitude"})
	}
	point := &GeoPoint{
		LatitudeE7:  int64(math.Round(latitude * 1e7)),
		LongitudeE7: int64(math.Round(longitude * 1e7)),
		System:      system,
	}
	if err := point.Validate(); err != nil {
		return nil, err
	}
	return point, nil
}

// Latitude returns the stored E7 latitude as decimal degrees.
func (point GeoPoint) Latitude() float64 { return float64(point.LatitudeE7) / 1e7 }

// Longitude returns the stored E7 longitude as decimal degrees.
func (point GeoPoint) Longitude() float64 { return float64(point.LongitudeE7) / 1e7 }

// Validate enforces coordinate range and authority invariants.
func (point GeoPoint) Validate() error {
	if point.LatitudeE7 < -900000000 || point.LatitudeE7 > 900000000 {
		return NewError(CodeInvalidArgument, "latitude must be between -90 and 90", map[string]any{"field": "latitude"})
	}
	if point.LongitudeE7 < -1800000000 || point.LongitudeE7 > 1800000000 {
		return NewError(CodeInvalidArgument, "longitude must be between -180 and 180", map[string]any{"field": "longitude"})
	}
	if point.System != CoordinateWGS84 && point.System != CoordinateGCJ02 {
		return NewError(CodeInvalidArgument, "coordinate_system is not supported", map[string]any{"field": "coordinate_system"})
	}
	return nil
}

// Location is the immutable provider-independent snapshot attached to a post
// or comment. Provider requests are never needed to render stored content.
type Location struct {
	Name            string
	DisplayName     string
	CountryCode     string
	Country         string
	Region          string
	City            string
	District        string
	Category        string
	Provider        string
	ProviderPlaceID string
	Point           *GeoPoint
}

// Validate enforces the canonical provider-independent location contract.
func (location Location) Validate() error {
	if err := validateLocationText("name", location.Name, MaxLocationNameCodePoints, true); err != nil {
		return err
	}
	if err := validateLocationText("display_name", location.DisplayName, MaxLocationDisplayNameCodePoints, true); err != nil {
		return err
	}
	parts := []struct {
		field string
		value string
		limit int
	}{
		{"country_code", location.CountryCode, MaxLocationPartCodePoints},
		{"country", location.Country, MaxLocationPartCodePoints},
		{"region", location.Region, MaxLocationPartCodePoints},
		{"city", location.City, MaxLocationPartCodePoints},
		{"district", location.District, MaxLocationPartCodePoints},
		{"category", location.Category, MaxLocationPartCodePoints},
		{"provider_place_id", location.ProviderPlaceID, MaxLocationProviderIDCodePoints},
	}
	for _, part := range parts {
		if err := validateLocationText(part.field, part.value, part.limit, false); err != nil {
			return err
		}
	}
	if location.CountryCode != strings.ToUpper(location.CountryCode) || (location.CountryCode != "" && len(location.CountryCode) != 2) {
		return NewError(CodeInvalidArgument, "country_code must be an uppercase ISO alpha-2 code", map[string]any{"field": "country_code"})
	}
	if location.Provider != "manual" && location.Provider != "amap" && location.Provider != "geoapify" {
		return NewError(CodeInvalidArgument, "location provider is not supported", map[string]any{"field": "provider"})
	}
	if location.Provider == "manual" && location.ProviderPlaceID != "" {
		return NewError(CodeInvalidArgument, "manual locations cannot have a provider place ID", map[string]any{"field": "provider_place_id"})
	}
	if location.Point != nil {
		if err := location.Point.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// CloneLocation returns a deep copy suitable for immutable projections.
func CloneLocation(location *Location) *Location {
	if location == nil {
		return nil
	}
	clone := *location
	if location.Point != nil {
		point := *location.Point
		clone.Point = &point
	}
	return &clone
}

func validateLocationText(field, value string, limit int, required bool) error {
	if !utf8.ValidString(value) || value != strings.TrimSpace(value) {
		return NewError(CodeInvalidArgument, field+" must be normalized UTF-8 text", map[string]any{"field": field})
	}
	count := utf8.RuneCountInString(value)
	if required && count == 0 {
		return NewError(CodeInvalidArgument, field+" is required", map[string]any{"field": field})
	}
	if count > limit {
		return NewError(CodeInvalidArgument, field+" exceeds its code-point limit", map[string]any{"field": field, "limit": limit})
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return NewError(CodeInvalidArgument, field+" cannot contain control characters", map[string]any{"field": field})
		}
	}
	return nil
}
