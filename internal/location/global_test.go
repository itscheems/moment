package location

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
)

type globalTestProvider struct {
	name    string
	started chan<- string
	release <-chan struct{}
	items   []domain.Location
	err     error
	bias    chan<- *domain.GeoPoint
}

func (p globalTestProvider) Search(ctx context.Context, _ string, bias *domain.GeoPoint, _ int) ([]domain.Location, error) {
	if p.bias != nil {
		p.bias <- bias
	}
	if p.started != nil {
		p.started <- p.name
		select {
		case <-p.release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	return p.items, p.err
}
func (p globalTestProvider) Nearby(context.Context, domain.GeoPoint, int) ([]domain.Location, error) {
	return nil, errors.New("unused")
}
func TestGlobalLookupQueriesAllProvidersConcurrentlyWithoutBias(t *testing.T) {
	started := make(chan string, 2)
	release := make(chan struct{})
	bias := make(chan *domain.GeoPoint, 2)
	geo := []domain.Location{{Name: "Tokyo", Provider: "geoapify"}, {Name: "Paris", Provider: "geoapify"}}
	amap := []domain.Location{{Name: "Shanghai", Provider: "amap"}, {Name: "Beijing", Provider: "amap"}}
	router := Router{geoapify: globalTestProvider{"global", started, release, geo, nil, bias}, amap: globalTestProvider{"china", started, release, amap, nil, bias}}
	type result struct {
		items []domain.Location
		err   error
	}
	done := make(chan result, 1)
	go func() {
		items, err := router.SearchGlobal(context.Background(), "place", 4)
		done <- result{items, err}
	}()
	for range 2 {
		select {
		case <-started:
		case <-time.After(2 * time.Second):
			close(release)
			t.Fatal("providers were queried serially")
		}
	}
	close(release)
	got := <-done
	if got.err != nil || !reflect.DeepEqual(got.items, []domain.Location{geo[0], amap[0], geo[1], amap[1]}) {
		t.Fatalf("global=%+v", got)
	}
	for range 2 {
		if value := <-bias; value != nil {
			t.Fatal("global search received device bias")
		}
	}
	router.amap = globalTestProvider{err: errors.New("down")}
	router.geoapify = globalTestProvider{items: geo}
	values, err := router.SearchGlobal(context.Background(), "place", 1)
	if err != nil || len(values) != 1 || values[0].Name != "Tokyo" {
		t.Fatalf("partial=%+v %v", values, err)
	}
	router.geoapify = globalTestProvider{items: []domain.Location{}}
	values, err = router.SearchGlobal(context.Background(), "missing", 8)
	if err != nil || len(values) != 0 {
		t.Fatalf("empty=%+v %v", values, err)
	}
}
