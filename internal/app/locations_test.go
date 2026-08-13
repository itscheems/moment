package app

import (
	"testing"

	"github.com/moment/moment/internal/domain"
)

func TestValidateLocationResultsDeduplicatesStableProviderPlaces(t *testing.T) {
	t.Parallel()
	items := []domain.Location{
		{Name: "静安寺", DisplayName: "上海市静安区", Provider: "amap", ProviderPlaceID: "B0FF"},
		{Name: "静安寺", DisplayName: "上海市静安区南京西路", Provider: "amap", ProviderPlaceID: "B0FF"},
		{Name: "静安公园", DisplayName: "上海市静安区", Provider: "amap", ProviderPlaceID: "B1FF"},
	}

	results, err := validateLocationResults(items, 8)
	if err != nil {
		t.Fatalf("validate location results: %v", err)
	}
	if len(results) != 2 {
		t.Fatalf("result count = %d, want 2", len(results))
	}
	if results[0].DisplayName != items[0].DisplayName {
		t.Fatalf("first result changed: %#v", results[0])
	}
}
