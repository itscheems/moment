package ginhttp

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
)

func TestSearchHTTPStrictQueriesAggregatesAndCursor(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	*fixture.now = time.Date(2026, time.January, 1, 0, 30, 0, 0, time.UTC)
	if _, err := fixture.posts.Create(context.Background(), app.CreatePostInput{ContentMD: "旧城雨 first"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.posts.Create(context.Background(), app.CreatePostInput{ContentMD: "旧城雨 second"}); err != nil {
		t.Fatal(err)
	}

	response := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/search?q=%E6%97%A7%E5%9F%8E%E9%9B%A8&limit=1", nil))
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"year_counts":[{"year":2026,"count":2}]`) || !strings.Contains(response.Body.String(), `"next_cursor":"`) {
		t.Fatalf("search = %d %s", response.Code, response.Body.String())
	}
	for _, target := range []string{
		"/api/v1/search", "/api/v1/search?q=", "/api/v1/search?q=a&q=b", "/api/v1/search?q=a&unknown=1",
		"/api/v1/search?q=a&limit=0", "/api/v1/search?q=a&from=", "/api/v1/archive/month?month=2026-1",
		"/api/v1/archive/months?from=2026-01-02&to=2026-01-01",
	} {
		invalid := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, target, nil))
		if invalid.Code != http.StatusBadRequest || decodeError(t, invalid.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("%s = %d %s", target, invalid.Code, invalid.Body.String())
		}
	}

	months := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/archive/months", nil))
	if months.Code != http.StatusOK || !strings.Contains(months.Body.String(), `"year":2026,"month":1,"count":2`) {
		t.Fatalf("archive months = %d %s", months.Code, months.Body.String())
	}
	days := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/archive/month?month=2026-01", nil))
	if days.Code != http.StatusOK || !strings.Contains(days.Body.String(), `"day":1,"count":2`) {
		t.Fatalf("archive month = %d %s", days.Code, days.Body.String())
	}
	listing := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/posts?from=2026-01-01&to=2026-01-02", nil))
	if listing.Code != http.StatusOK || strings.Count(listing.Body.String(), `"content_md":"旧城雨`) != 2 {
		t.Fatalf("archive day listing = %d %s", listing.Code, listing.Body.String())
	}
}
