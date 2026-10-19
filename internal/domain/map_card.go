package domain

import (
	"bytes"
	"encoding/json"
	"io"
	"strings"
)

// MaxMapPlaces bounds the ordered places in one map snapshot.
const MaxMapPlaces = 20

// MaxMapCardBytes limits a map snapshot's encoded JSON size.
const MaxMapCardBytes = 64 << 10

// MapPlace is an explicitly published point. Ordinary location labels remain
// coarse; map points carry their original coordinate authority without rounding
// through repeated provider conversions.
type MapPlace struct {
	Name        string
	DisplayName string
	Point       GeoPoint
}

type mapPlaceJSON struct {
	Name             string           `json:"name"`
	DisplayName      string           `json:"display_name"`
	Latitude         *float64         `json:"latitude"`
	Longitude        *float64         `json:"longitude"`
	CoordinateSystem CoordinateSystem `json:"coordinate_system"`
}

// MarshalJSON exposes source coordinates in the portable map format.
func (place MapPlace) MarshalJSON() ([]byte, error) {
	lat, lng := place.Point.Latitude(), place.Point.Longitude()
	return json.Marshal(mapPlaceJSON{place.Name, place.DisplayName, &lat, &lng, place.Point.System})
}

// UnmarshalJSON validates a portable place and retains its coordinate system.
func (place *MapPlace) UnmarshalJSON(data []byte) error {
	var wire mapPlaceJSON
	if err := decodeMapJSON(data, &wire); err != nil {
		return err
	}
	if wire.Latitude == nil || wire.Longitude == nil {
		return mapCardError("map places require latitude and longitude")
	}
	point, err := NewGeoPoint(*wire.Latitude, *wire.Longitude, wire.CoordinateSystem)
	if err != nil {
		return err
	}
	*place = MapPlace{Name: wire.Name, DisplayName: wire.DisplayName, Point: *point}
	return place.Validate()
}

// Validate checks the place labels and source coordinates.
func (place MapPlace) Validate() error {
	if err := validateLocationText("name", place.Name, MaxLocationNameCodePoints, true); err != nil {
		return err
	}
	if err := validateLocationText("display_name", place.DisplayName, MaxLocationDisplayNameCodePoints, true); err != nil {
		return err
	}
	return place.Point.Validate()
}

// MapCard is a bounded value snapshot owned by its containing post or article.
// It has no independent identity or mutation lifecycle.
type MapCard struct {
	Places        []MapPlace `json:"places"`
	ConnectPoints bool       `json:"connect_points"`
}

// Validate checks the snapshot's place count, order settings and places.
func (card MapCard) Validate() error {
	if len(card.Places) < 1 || len(card.Places) > MaxMapPlaces {
		return mapCardError("a map must contain between 1 and 20 places")
	}
	if card.ConnectPoints && len(card.Places) < 2 {
		return mapCardError("connecting places requires at least two places")
	}
	for _, place := range card.Places {
		if err := place.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// UnmarshalJSON decodes a bounded, validated map snapshot.
func (card *MapCard) UnmarshalJSON(data []byte) error {
	type value MapCard
	var decoded value
	if err := decodeMapJSON(data, &decoded); err != nil {
		return err
	}
	*card = MapCard(decoded)
	return card.Validate()
}

// ParseMapCard reads one complete portable map snapshot.
func ParseMapCard(data []byte) (*MapCard, error) {
	var card MapCard
	if err := json.Unmarshal(data, &card); err != nil {
		return nil, err
	}
	return &card, nil
}

// CloneMapCard copies a snapshot without sharing its places slice.
func CloneMapCard(card *MapCard) *MapCard {
	if card == nil {
		return nil
	}
	return &MapCard{Places: append([]MapPlace(nil), card.Places...), ConnectPoints: card.ConnectPoints}
}

// Text projects place labels for summaries and search indexing.
func (card MapCard) Text() string {
	var names []string
	for _, place := range card.Places {
		names = append(names, place.Name+" "+place.DisplayName)
	}
	return strings.Join(names, "\n")
}

func decodeMapJSON(data []byte, target any) error {
	if len(data) > MaxMapCardBytes || !json.Valid(data) {
		return mapCardError("map card must be bounded valid JSON")
	}
	if err := rejectDuplicateJSONFields(data); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return mapCardError("map card contains invalid fields")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return mapCardError("map card must contain one JSON value")
	}
	return nil
}

func mapCardError(message string) error {
	return NewError(CodeInvalidArgument, message, map[string]any{"field": "map_card"})
}

// rejectDuplicateJSONFields also protects map snapshots nested in documents.
func rejectDuplicateJSONFields(data []byte) error {
	check := json.NewDecoder(bytes.NewReader(data))
	var visit func() error
	visit = func() error {
		token, err := check.Token()
		if err != nil {
			return err
		}
		if delim, ok := token.(json.Delim); ok {
			seen := map[string]bool{}
			for check.More() {
				if delim == '{' {
					key, err := check.Token()
					if err != nil {
						return err
					}
					name, ok := key.(string)
					if !ok || seen[name] {
						return mapCardError("map fields must appear once")
					}
					seen[name] = true
				}
				if err := visit(); err != nil {
					return err
				}
			}
			_, err = check.Token()
		}
		return err
	}
	if err := visit(); err != nil {
		return mapCardError("map fields are invalid or duplicated")
	}
	return nil
}
