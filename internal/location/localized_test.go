package location

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestChineseGlobalSearchResolvesLocalizedAliasWithoutDeviceBias(t *testing.T) {
	var translated bool
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		q := r.URL.Query()
		switch r.URL.Path {
		case "/w/api.php":
			if q.Get("search") != "东京" || q.Get("language") != "zh" || q.Get("uselang") != "en" {
				t.Errorf("unexpected alias query: %v", q)
			}
			_, _ = w.Write([]byte(`{"search":[{"label":"Tokyo"}]}`))
		case "/v1/geocode/autocomplete":
			if q.Get("bias") != "" {
				t.Error("global query received device bias")
			}
			if q.Get("text") == "东京" {
				_, _ = w.Write([]byte(`{"results":[]}`))
				return
			}
			if q.Get("text") != "Tokyo" {
				t.Errorf("unexpected query: %q", q.Get("text"))
			}
			translated = true
			_, _ = w.Write([]byte(`{"results":[{"name":"东京都","formatted":"日本东京都","country":"日本","country_code":"jp","place_id":"tokyo","lat":35.67,"lon":139.76}]}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	provider := newGeoapifyProvider(server.Client(), "test-key", server.URL)
	provider.namesURL = server.URL
	router := Router{geoapify: provider}
	items, err := router.SearchGlobal(context.Background(), "东京", 8)
	if err != nil || !translated || len(items) != 1 || items[0].CountryCode != "JP" {
		t.Fatalf("items=%+v, err=%v", items, err)
	}
}

func TestLocalizedNameFailureDoesNotLoseSuccessfulEmptyGeocoding(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/w/api.php" {
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer server.Close()
	provider := newGeoapifyProvider(server.Client(), "test-key", server.URL)
	provider.namesURL = server.URL
	items, err := provider.Search(context.Background(), "不存在的中文地点", nil, 8)
	if err != nil || len(items) != 0 {
		t.Fatalf("items=%+v, err=%v", items, err)
	}
}
