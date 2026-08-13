package location

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moment/moment/internal/domain"
)

func TestAMapSearchAndNearbyNormalizeProviderResponses(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Query().Get("key") != "test-key" {
			t.Errorf("provider key was not supplied")
		}
		writer.Header().Set("Content-Type", "application/json")
		switch request.URL.Path {
		case "/v3/assistant/inputtips":
			_, _ = writer.Write([]byte(`{"status":"1","tips":[{"id":"B0FFF","name":"静安寺","district":"上海市静安区","address":"南京西路1686号","location":"121.445260,31.223186"}]}`))
		case "/v3/assistant/coordinate/convert":
			_, _ = writer.Write([]byte(`{"status":"1","locations":"121.445260,31.223186"}`))
		case "/v3/geocode/regeo":
			_, _ = writer.Write([]byte(`{"status":"1","regeocode":{"formatted_address":"上海市静安区南京西路","addressComponent":{"province":"上海市","city":[],"district":"静安区"},"pois":[{"id":"B0FFF","name":"静安寺","type":"风景名胜;寺庙","address":"南京西路1686号","location":"121.445260,31.223186","pname":"上海市","cityname":"上海市","adname":"静安区"}]}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	defer server.Close()

	provider := newAMapProvider(server.Client(), "test-key", server.URL)
	items, err := provider.Search(context.Background(), "静安寺", nil, 8)
	if err != nil || len(items) != 1 || items[0].Name != "静安寺" || items[0].ProviderPlaceID != "B0FFF" || items[0].Point == nil || items[0].Point.System != domain.CoordinateGCJ02 {
		t.Fatalf("search = %#v, %v", items, err)
	}

	wgs84, _ := domain.NewGeoPoint(31.2212, 121.4407, domain.CoordinateWGS84)
	items, err = provider.Nearby(context.Background(), *wgs84, 8)
	if err != nil || len(items) != 2 || items[0].Category != "address" || items[1].Name != "静安寺" || items[1].Category != "风景名胜" {
		t.Fatalf("nearby = %#v, %v", items, err)
	}
}
