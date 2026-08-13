package location

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/moment/moment/internal/domain"
)

func TestGeoapifySearchUsesBiasAndNormalizesResult(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/geocode/autocomplete" || request.URL.Query().Get("apiKey") != "test-key" || !strings.HasPrefix(request.URL.Query().Get("bias"), "proximity:") {
			t.Errorf("unexpected request %s?%s", request.URL.Path, request.URL.RawQuery)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"results":[{"name":"Eiffel Tower","formatted":"5 Avenue Anatole France, Paris, France","country":"France","country_code":"fr","state":"Île-de-France","city":"Paris","district":"7th arrondissement","category":"tourism.attraction","place_id":"place-1","lat":48.85837,"lon":2.294481}]}`))
	}))
	defer server.Close()

	bias, _ := domain.NewGeoPoint(48.8566, 2.3522, domain.CoordinateWGS84)
	items, err := newGeoapifyProvider(server.Client(), "test-key", server.URL).Search(context.Background(), "Eiffel", bias, 3)
	if err != nil || len(items) != 1 || items[0].CountryCode != "FR" || items[0].Provider != "geoapify" || items[0].Point == nil || items[0].Point.System != domain.CoordinateWGS84 {
		t.Fatalf("items = %#v, %v", items, err)
	}
}

func TestGeoapifyNearbyCombinesReverseAddressAndPlaces(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v1/geocode/reverse":
			_, _ = writer.Write([]byte(`{"results":[{"name":"Champ de Mars","formatted":"Champ de Mars, Paris, France","country":"France","country_code":"fr","city":"Paris","place_id":"address-1","lat":48.85837,"lon":2.294481}]}`))
		case "/v2/places":
			if request.URL.Query().Get("filter") == "" || request.URL.Query().Get("bias") == "" {
				t.Errorf("places request has no spatial filter: %s", request.URL.RawQuery)
			}
			_, _ = writer.Write([]byte(`{"features":[{"properties":{"name":"Eiffel Tower","formatted":"5 Avenue Anatole France, Paris, France","country":"France","country_code":"fr","city":"Paris","categories":["tourism.attraction"],"place_id":"place-1","lat":48.85837,"lon":2.294481}}]}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	point, _ := domain.NewGeoPoint(48.85837, 2.294481, domain.CoordinateWGS84)
	items, err := newGeoapifyProvider(server.Client(), "test-key", server.URL).Nearby(context.Background(), *point, 8)
	if err != nil || len(items) != 2 {
		t.Fatalf("nearby = %#v, %v", items, err)
	}
	names := map[string]domain.Location{}
	for _, item := range items {
		names[item.Name] = item
	}
	if names["Eiffel Tower"].Category != "tourism.attraction" || names["Champ de Mars"].Provider != "geoapify" {
		t.Fatalf("nearby = %#v", items)
	}
}
