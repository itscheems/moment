package location

import (
	"context"
	"net/url"
	"strconv"
	"strings"

	"github.com/moment/moment/internal/domain"
)

const defaultGeoapifyBaseURL = "https://api.geoapify.com"

type geoapifyProvider struct {
	client  HTTPClient
	key     string
	baseURL string
}

func newGeoapifyProvider(client HTTPClient, key, baseURL string) *geoapifyProvider {
	if baseURL == "" {
		baseURL = defaultGeoapifyBaseURL
	}
	return &geoapifyProvider{client: client, key: key, baseURL: baseURL}
}

type geoapifyResult struct {
	Name         string   `json:"name"`
	Formatted    string   `json:"formatted"`
	AddressLine1 string   `json:"address_line1"`
	AddressLine2 string   `json:"address_line2"`
	Country      string   `json:"country"`
	CountryCode  string   `json:"country_code"`
	State        string   `json:"state"`
	City         string   `json:"city"`
	District     string   `json:"district"`
	Suburb       string   `json:"suburb"`
	Category     string   `json:"category"`
	Categories   []string `json:"categories"`
	ResultType   string   `json:"result_type"`
	PlaceID      string   `json:"place_id"`
	Lat          float64  `json:"lat"`
	Lon          float64  `json:"lon"`
}

type geoapifyResponse struct {
	Results []geoapifyResult `json:"results"`
}

type geoapifyPlacesResponse struct {
	Features []struct {
		Properties geoapifyResult `json:"properties"`
	} `json:"features"`
}

func (provider *geoapifyProvider) Search(ctx context.Context, query string, bias *domain.GeoPoint, limit int) ([]domain.Location, error) {
	values := url.Values{
		"apiKey": {provider.key}, "text": {query}, "format": {"json"},
		"lang": {"zh"}, "limit": {strconv.Itoa(limit)},
	}
	if bias != nil {
		values.Set("bias", "proximity:"+strconv.FormatFloat(bias.Longitude(), 'f', 7, 64)+","+strconv.FormatFloat(bias.Latitude(), 'f', 7, 64))
	}
	var response geoapifyResponse
	if err := getJSON(ctx, provider.client, provider.baseURL, "/v1/geocode/autocomplete", values, &response); err != nil {
		return nil, ErrUnavailable
	}
	return geoapifyLocations(response.Results, limit), nil
}

func (provider *geoapifyProvider) Nearby(ctx context.Context, point domain.GeoPoint, limit int) ([]domain.Location, error) {
	// Reverse geocoding and nearby places run in parallel. The address remains
	// a useful first choice where POI coverage is sparse, while places provide
	// the richer chooser people expect around stations, venues, and shops.
	type resolution struct {
		address   bool
		locations []domain.Location
		err       error
	}
	resolved := make(chan resolution, 2)
	go func() {
		values := url.Values{
			"apiKey": {provider.key}, "lat": {strconv.FormatFloat(point.Latitude(), 'f', 7, 64)},
			"lon": {strconv.FormatFloat(point.Longitude(), 'f', 7, 64)}, "format": {"json"},
			"lang": {"zh"}, "limit": {"1"},
		}
		var response geoapifyResponse
		err := getJSON(ctx, provider.client, provider.baseURL, "/v1/geocode/reverse", values, &response)
		resolved <- resolution{address: true, locations: geoapifyLocations(response.Results, 1), err: err}
	}()
	go func() {
		coordinate := strconv.FormatFloat(point.Longitude(), 'f', 7, 64) + "," + strconv.FormatFloat(point.Latitude(), 'f', 7, 64)
		values := url.Values{
			"apiKey":     {provider.key},
			"categories": {"catering,accommodation,activity,commercial,entertainment,education,leisure,public_transport,religion,service,tourism"},
			"filter":     {"circle:" + coordinate + ",1500"}, "bias": {"proximity:" + coordinate},
			"lang": {"zh"}, "limit": {strconv.Itoa(limit)},
		}
		var response geoapifyPlacesResponse
		err := getJSON(ctx, provider.client, provider.baseURL, "/v2/places", values, &response)
		items := make([]geoapifyResult, 0, len(response.Features))
		for _, feature := range response.Features {
			items = append(items, feature.Properties)
		}
		resolved <- resolution{locations: geoapifyLocations(items, limit), err: err}
	}()

	var address, places []domain.Location
	succeeded := false
	for range 2 {
		result := <-resolved
		if result.err == nil {
			succeeded = true
			if result.address {
				address = result.locations
			} else {
				places = result.locations
			}
		}
	}
	if !succeeded {
		return nil, ErrUnavailable
	}
	locations := append(address, places...)
	if len(locations) > limit {
		locations = locations[:limit]
	}
	return locations, nil
}

func geoapifyLocations(results []geoapifyResult, limit int) []domain.Location {
	locations := make([]domain.Location, 0, min(limit, len(results)))
	for _, result := range results {
		if len(locations) == limit {
			break
		}
		name := firstText(result.Name, result.AddressLine1, result.City, result.District, result.Formatted)
		display := firstText(result.Formatted, strings.TrimSpace(result.AddressLine1+", "+result.AddressLine2), name)
		point, err := domain.NewGeoPoint(result.Lat, result.Lon, domain.CoordinateWGS84)
		if name == "" || display == "" || err != nil {
			continue
		}
		category := result.Category
		if category == "" && len(result.Categories) > 0 {
			category = result.Categories[0]
		}
		location := domain.Location{
			Name: name, DisplayName: display, CountryCode: strings.ToUpper(strings.TrimSpace(result.CountryCode)),
			Country: strings.TrimSpace(result.Country), Region: strings.TrimSpace(result.State),
			City: strings.TrimSpace(result.City), District: firstText(result.District, result.Suburb),
			Category: firstText(category, result.ResultType), Provider: "geoapify",
			ProviderPlaceID: strings.TrimSpace(result.PlaceID), Point: point,
		}
		if location.Validate() == nil {
			locations = append(locations, location)
		}
	}
	return locations
}

func firstText(values ...string) string {
	for _, value := range values {
		if normalized := strings.TrimSpace(strings.Trim(value, ",")); normalized != "" {
			return normalized
		}
	}
	return ""
}
