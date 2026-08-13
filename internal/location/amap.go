package location

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	"github.com/moment/moment/internal/domain"
)

const defaultAMapBaseURL = "https://restapi.amap.com"

type amapProvider struct {
	client  HTTPClient
	key     string
	baseURL string
}

func newAMapProvider(client HTTPClient, key, baseURL string) *amapProvider {
	if baseURL == "" {
		baseURL = defaultAMapBaseURL
	}
	return &amapProvider{client: client, key: key, baseURL: baseURL}
}

type amapString string

func (value *amapString) UnmarshalJSON(data []byte) error {
	var text string
	if json.Unmarshal(data, &text) == nil {
		*value = amapString(text)
		return nil
	}
	var list []string
	if json.Unmarshal(data, &list) == nil {
		*value = amapString(strings.Join(list, ""))
		return nil
	}
	return ErrUnavailable
}

type amapTip struct {
	ID       amapString `json:"id"`
	Name     amapString `json:"name"`
	District amapString `json:"district"`
	Address  amapString `json:"address"`
	Location amapString `json:"location"`
}

type amapTipsResponse struct {
	Status string    `json:"status"`
	Tips   []amapTip `json:"tips"`
}

type amapPOI struct {
	ID       amapString `json:"id"`
	Name     amapString `json:"name"`
	Type     amapString `json:"type"`
	Address  amapString `json:"address"`
	Location amapString `json:"location"`
	Province amapString `json:"pname"`
	City     amapString `json:"cityname"`
	District amapString `json:"adname"`
}

type amapRegeoResponse struct {
	Status    string `json:"status"`
	Regeocode struct {
		FormattedAddress amapString `json:"formatted_address"`
		AddressComponent struct {
			Province amapString `json:"province"`
			City     amapString `json:"city"`
			District amapString `json:"district"`
		} `json:"addressComponent"`
		POIs []amapPOI `json:"pois"`
	} `json:"regeocode"`
}

func (provider *amapProvider) Search(ctx context.Context, query string, bias *domain.GeoPoint, limit int) ([]domain.Location, error) {
	values := url.Values{"key": {provider.key}, "keywords": {query}, "output": {"JSON"}}
	if bias != nil {
		point, err := provider.gcjPoint(ctx, *bias)
		if err != nil {
			return nil, err
		}
		values.Set("location", formatPoint(point))
	}
	var response amapTipsResponse
	if err := getJSON(ctx, provider.client, provider.baseURL, "/v3/assistant/inputtips", values, &response); err != nil || response.Status != "1" {
		return nil, ErrUnavailable
	}
	result := make([]domain.Location, 0, min(limit, len(response.Tips)))
	for _, tip := range response.Tips {
		if len(result) == limit {
			break
		}
		location, ok := amapTipLocation(tip)
		if ok {
			result = append(result, location)
		}
	}
	return result, nil
}

func (provider *amapProvider) Nearby(ctx context.Context, point domain.GeoPoint, limit int) ([]domain.Location, error) {
	converted, err := provider.gcjPoint(ctx, point)
	if err != nil {
		return nil, err
	}
	values := url.Values{
		"key": {provider.key}, "location": {formatPoint(converted)},
		"radius": {"3000"}, "extensions": {"all"}, "roadlevel": {"0"},
		"output": {"JSON"},
	}
	var response amapRegeoResponse
	if err := getJSON(ctx, provider.client, provider.baseURL, "/v3/geocode/regeo", values, &response); err != nil || response.Status != "1" {
		return nil, ErrUnavailable
	}
	result := make([]domain.Location, 0, limit)
	formatted := strings.TrimSpace(string(response.Regeocode.FormattedAddress))
	if formatted != "" {
		location := domain.Location{
			Name: formatted, DisplayName: formatted, CountryCode: "CN", Country: "中国",
			Region:   strings.TrimSpace(string(response.Regeocode.AddressComponent.Province)),
			City:     strings.TrimSpace(string(response.Regeocode.AddressComponent.City)),
			District: strings.TrimSpace(string(response.Regeocode.AddressComponent.District)),
			Category: "address", Provider: "amap", Point: &converted,
		}
		if location.Validate() == nil {
			result = append(result, location)
		}
	}
	for _, poi := range response.Regeocode.POIs {
		if len(result) == limit {
			break
		}
		location, ok := amapPOILocation(poi)
		if ok {
			result = append(result, location)
		}
	}
	return result, nil
}

func (provider *amapProvider) gcjPoint(ctx context.Context, point domain.GeoPoint) (domain.GeoPoint, error) {
	if point.System == domain.CoordinateGCJ02 {
		return point, nil
	}
	values := url.Values{
		"key": {provider.key}, "locations": {formatPoint(point)},
		"coordsys": {"gps"}, "output": {"JSON"},
	}
	var response struct {
		Status    string `json:"status"`
		Locations string `json:"locations"`
	}
	if err := getJSON(ctx, provider.client, provider.baseURL, "/v3/assistant/coordinate/convert", values, &response); err != nil || response.Status != "1" {
		return domain.GeoPoint{}, ErrUnavailable
	}
	converted, ok := parsePoint(response.Locations, domain.CoordinateGCJ02)
	if !ok {
		return domain.GeoPoint{}, ErrUnavailable
	}
	return converted, nil
}

func amapTipLocation(tip amapTip) (domain.Location, bool) {
	name, display := strings.TrimSpace(string(tip.Name)), strings.TrimSpace(string(tip.Address))
	if name == "" {
		return domain.Location{}, false
	}
	if display == "" {
		display = strings.TrimSpace(string(tip.District))
	}
	if display == "" {
		display = name
	}
	location := domain.Location{
		Name: name, DisplayName: display, CountryCode: "CN", Country: "中国",
		District: strings.TrimSpace(string(tip.District)), Provider: "amap",
		ProviderPlaceID: strings.TrimSpace(string(tip.ID)),
	}
	if point, ok := parsePoint(string(tip.Location), domain.CoordinateGCJ02); ok {
		location.Point = &point
	}
	return location, location.Validate() == nil
}

func amapPOILocation(poi amapPOI) (domain.Location, bool) {
	name := strings.TrimSpace(string(poi.Name))
	if name == "" {
		return domain.Location{}, false
	}
	address := strings.TrimSpace(strings.Join([]string{string(poi.Province), string(poi.City), string(poi.District), string(poi.Address)}, ""))
	if address == "" {
		address = name
	}
	location := domain.Location{
		Name: name, DisplayName: address, CountryCode: "CN", Country: "中国",
		Region: strings.TrimSpace(string(poi.Province)), City: strings.TrimSpace(string(poi.City)),
		District: strings.TrimSpace(string(poi.District)), Category: boundedAMapCategory(string(poi.Type)),
		Provider: "amap", ProviderPlaceID: strings.TrimSpace(string(poi.ID)),
	}
	if point, ok := parsePoint(string(poi.Location), domain.CoordinateGCJ02); ok {
		location.Point = &point
	}
	return location, location.Validate() == nil
}

func boundedAMapCategory(value string) string {
	value = strings.TrimSpace(value)
	parts := strings.Split(value, ";")
	if len(parts) > 0 {
		value = parts[0]
	}
	return value
}

func formatPoint(point domain.GeoPoint) string {
	return fmt.Sprintf("%.6f,%.6f", point.Longitude(), point.Latitude())
}

func parsePoint(value string, system domain.CoordinateSystem) (domain.GeoPoint, bool) {
	parts := strings.Split(value, ",")
	if len(parts) != 2 {
		return domain.GeoPoint{}, false
	}
	longitude, err1 := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
	latitude, err2 := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
	point, err3 := domain.NewGeoPoint(latitude, longitude, system)
	if err1 != nil || err2 != nil || err3 != nil {
		return domain.GeoPoint{}, false
	}
	return *point, true
}
