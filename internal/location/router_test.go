package location

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"github.com/moment/moment/internal/domain"
)

type stubProvider struct {
	searchItems []domain.Location
	nearbyItems []domain.Location
	err         error
	searchCalls int
	nearbyCalls int
}

func (provider *stubProvider) Search(context.Context, string, *domain.GeoPoint, int) ([]domain.Location, error) {
	provider.searchCalls++
	return provider.searchItems, provider.err
}

func (provider *stubProvider) Nearby(context.Context, domain.GeoPoint, int) ([]domain.Location, error) {
	provider.nearbyCalls++
	return provider.nearbyItems, provider.err
}

func TestRouterChoosesRegionalProviderAndFallsBack(t *testing.T) {
	shanghai, _ := domain.NewGeoPoint(31.2304, 121.4737, domain.CoordinateWGS84)
	paris, _ := domain.NewGeoPoint(48.8566, 2.3522, domain.CoordinateWGS84)
	amapItem := domain.Location{Name: "静安寺", DisplayName: "上海市静安区", CountryCode: "CN", Country: "中国", Provider: "amap"}
	geoItem := domain.Location{Name: "Tour Eiffel", DisplayName: "Paris, France", CountryCode: "FR", Country: "France", Provider: "geoapify"}

	t.Run("mainland nearby uses AMap", func(t *testing.T) {
		amap := &stubProvider{nearbyItems: []domain.Location{amapItem}}
		geo := &stubProvider{nearbyItems: []domain.Location{geoItem}}
		items, err := (&Router{amap: amap, geoapify: geo}).Nearby(context.Background(), *shanghai, 8)
		if err != nil || !reflect.DeepEqual(items, []domain.Location{amapItem}) || amap.nearbyCalls != 1 || geo.nearbyCalls != 0 {
			t.Fatalf("items=%#v err=%v calls=%d/%d", items, err, amap.nearbyCalls, geo.nearbyCalls)
		}
	})

	t.Run("outside-mainland search uses Geoapify", func(t *testing.T) {
		amap := &stubProvider{searchItems: []domain.Location{amapItem}}
		geo := &stubProvider{searchItems: []domain.Location{geoItem}}
		items, err := (&Router{amap: amap, geoapify: geo}).Search(context.Background(), "Eiffel", paris, 8)
		if err != nil || !reflect.DeepEqual(items, []domain.Location{geoItem}) || geo.searchCalls != 1 || amap.searchCalls != 0 {
			t.Fatalf("items=%#v err=%v calls=%d/%d", items, err, geo.searchCalls, amap.searchCalls)
		}
	})

	t.Run("empty or failed primary falls back", func(t *testing.T) {
		amap := &stubProvider{err: errors.New("provider down")}
		geo := &stubProvider{searchItems: []domain.Location{geoItem}}
		items, err := (&Router{amap: amap, geoapify: geo}).Search(context.Background(), "Paris", nil, 8)
		if err != nil || !reflect.DeepEqual(items, []domain.Location{geoItem}) || amap.searchCalls != 1 || geo.searchCalls != 1 {
			t.Fatalf("items=%#v err=%v calls=%d/%d", items, err, amap.searchCalls, geo.searchCalls)
		}
	})
}

func TestRouterReportsUnavailableWithoutConfiguredProviders(t *testing.T) {
	if _, err := (&Router{}).Search(context.Background(), "place", nil, 8); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("error = %v", err)
	}
}
