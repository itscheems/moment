// Package locationdto owns the exact owner/CLI wire representation for one
// selectable or stored location snapshot.
package locationdto

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/moment/moment/internal/domain"
)

type Location struct {
	Name             string   `json:"name"`
	DisplayName      string   `json:"display_name"`
	CountryCode      string   `json:"country_code"`
	Country          string   `json:"country"`
	Region           string   `json:"region"`
	City             string   `json:"city"`
	District         string   `json:"district"`
	Category         string   `json:"category"`
	Provider         string   `json:"provider"`
	ProviderPlaceID  string   `json:"provider_place_id"`
	Latitude         *float64 `json:"latitude"`
	Longitude        *float64 `json:"longitude"`
	CoordinateSystem *string  `json:"coordinate_system"`
}

type Results struct {
	Items []Location `json:"items"`
}

// UnmarshalJSON keeps persisted/provider location snapshots exact across the
// owner API and CLI. A partial or duplicate object must never silently turn
// into a different place.
func (wire *Location) UnmarshalJSON(data []byte) error {
	*wire = Location{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("location must be an object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok || seen[key] {
			return errors.New("location fields must appear exactly once")
		}
		seen[key] = true
		switch key {
		case "name":
			err = decoder.Decode(&wire.Name)
		case "display_name":
			err = decoder.Decode(&wire.DisplayName)
		case "country_code":
			err = decoder.Decode(&wire.CountryCode)
		case "country":
			err = decoder.Decode(&wire.Country)
		case "region":
			err = decoder.Decode(&wire.Region)
		case "city":
			err = decoder.Decode(&wire.City)
		case "district":
			err = decoder.Decode(&wire.District)
		case "category":
			err = decoder.Decode(&wire.Category)
		case "provider":
			err = decoder.Decode(&wire.Provider)
		case "provider_place_id":
			err = decoder.Decode(&wire.ProviderPlaceID)
		case "latitude":
			err = decodeNullableFloat(decoder, &wire.Latitude)
		case "longitude":
			err = decodeNullableFloat(decoder, &wire.Longitude)
		case "coordinate_system":
			err = decodeNullableString(decoder, &wire.CoordinateSystem)
		default:
			return errors.New("location contains an unknown field")
		}
		if err != nil {
			return errors.New("location field has an invalid type")
		}
	}
	required := []string{"name", "display_name", "country_code", "country", "region", "city", "district", "category", "provider", "provider_place_id", "latitude", "longitude", "coordinate_system"}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') {
		return errors.New("location must be one object")
	}
	for _, field := range required {
		if !seen[field] {
			return errors.New("location is missing a required field")
		}
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("location contains trailing data")
	}
	return nil
}

func decodeNullableFloat(decoder *json.Decoder, destination **float64) error {
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var value float64
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*destination = &value
	return nil
}

func decodeNullableString(decoder *json.Decoder, destination **string) error {
	var raw json.RawMessage
	if err := decoder.Decode(&raw); err != nil {
		return err
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	var value string
	if err := json.Unmarshal(raw, &value); err != nil {
		return err
	}
	*destination = &value
	return nil
}

func FromDomain(location domain.Location) Location {
	wire := Location{
		Name: location.Name, DisplayName: location.DisplayName,
		CountryCode: location.CountryCode, Country: location.Country,
		Region: location.Region, City: location.City, District: location.District,
		Category: location.Category, Provider: location.Provider,
		ProviderPlaceID: location.ProviderPlaceID,
	}
	if location.Point != nil {
		latitude := location.Point.Latitude()
		longitude := location.Point.Longitude()
		system := string(location.Point.System)
		wire.Latitude = &latitude
		wire.Longitude = &longitude
		wire.CoordinateSystem = &system
	}
	return wire
}

func FromDomains(locations []domain.Location) Results {
	items := make([]Location, 0, len(locations))
	for _, location := range locations {
		items = append(items, FromDomain(location))
	}
	return Results{Items: items}
}

func (wire Location) Domain() (domain.Location, error) {
	location := domain.Location{
		Name: wire.Name, DisplayName: wire.DisplayName,
		CountryCode: wire.CountryCode, Country: wire.Country,
		Region: wire.Region, City: wire.City, District: wire.District,
		Category: wire.Category, Provider: wire.Provider,
		ProviderPlaceID: wire.ProviderPlaceID,
	}
	if (wire.Latitude != nil) != (wire.Longitude != nil) || (wire.Latitude != nil) != (wire.CoordinateSystem != nil) {
		return domain.Location{}, domain.NewError(domain.CodeInvalidArgument, "location coordinates must be supplied together", map[string]any{"field": "location"})
	}
	if wire.Latitude != nil {
		point, err := domain.NewGeoPoint(*wire.Latitude, *wire.Longitude, domain.CoordinateSystem(*wire.CoordinateSystem))
		if err != nil {
			return domain.Location{}, err
		}
		location.Point = point
	}
	if err := location.Validate(); err != nil {
		return domain.Location{}, err
	}
	return location, nil
}

func Validate(wire Location) error {
	_, err := wire.Domain()
	return err
}

func ValidateResults(results Results) error {
	if results.Items == nil {
		return errors.New("location results items must be an array")
	}
	for _, item := range results.Items {
		if err := Validate(item); err != nil {
			return err
		}
	}
	return nil
}
