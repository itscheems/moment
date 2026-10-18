package ginhttp

import (
	"context"
	"net/http"
	"testing"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

type locationHTTPResolver struct {
	searchBias *domain.GeoPoint
	nearby     *domain.GeoPoint
}

func (resolver *locationHTTPResolver) Search(_ context.Context, _ string, bias *domain.GeoPoint, _ int) ([]domain.Location, error) {
	if bias != nil {
		biasCopy := *bias
		resolver.searchBias = &biasCopy
	}
	return []domain.Location{{Name: "静安寺", DisplayName: "上海市静安区", CountryCode: "CN", Country: "中国", Provider: "amap"}}, nil
}

func (resolver *locationHTTPResolver) Nearby(_ context.Context, point domain.GeoPoint, _ int) ([]domain.Location, error) {
	resolver.nearby = &point
	return []domain.Location{{Name: "上海展览中心", DisplayName: "上海市静安区", CountryCode: "CN", Country: "中国", Provider: "amap", Point: &point}}, nil
}

func TestLocationHTTPRequiresActiveAccountAndReturnsCanonicalSnapshots(t *testing.T) {
	fixture := newAuthenticationTestFixture(t, false)
	resolver := &locationHTTPResolver{}
	service, err := app.NewLocationService(resolver)
	if err != nil {
		t.Fatal(err)
	}
	RegisterLocationRoutes(fixture.engine.(*gin.Engine), service, fixture.gate)

	unauthorized := performRequest(fixture.engine, jsonRequest(http.MethodGet, "/api/v1/locations/search?q=静安寺", ""))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized = %d %s", unauthorized.Code, unauthorized.Body.String())
	}
	search := jsonRequest(http.MethodGet, "/api/v1/locations/search?q=静安寺&latitude=31.23&longitude=121.47&limit=3", "")
	search.Header.Set("Authorization", "Bearer "+fixture.token)
	response := performRequest(fixture.engine, search)
	if response.Code != http.StatusOK || resolver.searchBias == nil || resolver.searchBias.System != domain.CoordinateWGS84 {
		t.Fatalf("search = %d %s bias=%#v", response.Code, response.Body.String(), resolver.searchBias)
	}
	items := decodeDataMap(t, response)["items"].([]any)
	item := items[0].(map[string]any)
	if len(item) != 13 || item["provider"] != "amap" || item["latitude"] != nil {
		t.Fatalf("item = %#v", item)
	}

	nearby := jsonRequest(http.MethodGet, "/api/v1/locations/nearby?latitude=31.23&longitude=121.47", "")
	nearby.Header.Set("Authorization", "Bearer "+fixture.token)
	response = performRequest(fixture.engine, nearby)
	if response.Code != http.StatusOK || resolver.nearby == nil {
		t.Fatalf("nearby = %d %s", response.Code, response.Body.String())
	}

	for _, path := range []string{
		"/api/v1/locations/search?q=x&extra=1",
		"/api/v1/locations/search?q=x&latitude=1",
		"/api/v1/locations/nearby?latitude=91&longitude=0",
		"/api/v1/locations/nearby?latitude=1&longitude=2&limit=21",
	} {
		request := jsonRequest(http.MethodGet, path, "")
		request.Header.Set("Authorization", "Bearer "+fixture.token)
		invalid := performRequest(fixture.engine, request)
		if invalid.Code != http.StatusBadRequest || decodeError(t, invalid.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("%s = %d %s", path, invalid.Code, invalid.Body.String())
		}
	}
}

type globalHTTPResolver struct {
	locationHTTPResolver
	called bool
}

func (r *globalHTTPResolver) SearchGlobal(context.Context, string, int) ([]domain.Location, error) {
	r.called = true
	return []domain.Location{{Name: "Tokyo", DisplayName: "Japan", Provider: "geoapify"}}, nil
}
func TestGlobalLocationHTTPUsesGlobalScopeAndValidatesScope(t *testing.T) {
	f := newAuthenticationTestFixture(t, false)
	r := &globalHTTPResolver{}
	s, err := app.NewLocationService(r)
	if err != nil {
		t.Fatal(err)
	}
	RegisterLocationRoutes(f.engine.(*gin.Engine), s, f.gate)
	req := jsonRequest(http.MethodGet, "/api/v1/locations/search?q=Tokyo&scope=global&latitude=31&longitude=121", "")
	req.Header.Set("Authorization", "Bearer "+f.token)
	response := performRequest(f.engine, req)
	if response.Code != 200 || !r.called || r.searchBias != nil {
		t.Fatalf("global=%d %s", response.Code, response.Body.String())
	}
	bad := jsonRequest(http.MethodGet, "/api/v1/locations/search?q=Tokyo&scope=nearby", "")
	bad.Header.Set("Authorization", "Bearer "+f.token)
	if response := performRequest(f.engine, bad); response.Code != 400 {
		t.Fatalf("scope=%d %s", response.Code, response.Body.String())
	}
}
