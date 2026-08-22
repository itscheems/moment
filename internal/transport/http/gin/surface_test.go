package ginhttp

import (
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/moment/moment/internal/observability"
)

// Arriving over HTTP does not make something the web. This stack serves the
// browser, the same CLI in API mode and any script holding a token, so the
// entry point is decided per request; recording all three as web would make
// the field mean "HTTP" and answer nothing worth asking.
func TestHTTPRequestsAreAttributedToTheirEntryPoint(t *testing.T) {
	var mu sync.Mutex
	var seen []observability.Surface
	engine, err := New(Config{
		Assets: testAssets(),
		SurfaceLog: func(surface observability.Surface, _, event string, _ map[string]any) {
			if event != "http_request" {
				return
			}
			mu.Lock()
			defer mu.Unlock()
			seen = append(seen, surface)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	for name, probe := range map[string]struct {
		headers map[string]string
		want    observability.Surface
	}{
		"a browser session is the one attribution the server can verify": {
			headers: map[string]string{"Cookie": SessionCookieName + "=whatever"},
			want:    observability.SurfaceWeb,
		},
		"a bearer token that says nothing is a programmatic client": {
			headers: map[string]string{"Authorization": "Bearer t"},
			want:    observability.SurfaceAPI,
		},
		"a client that names itself is taken at its word": {
			headers: map[string]string{"Authorization": "Bearer t", observability.SurfaceHeader: "cli"},
			want:    observability.SurfaceCLI,
		},
		"an unauthenticated read is the public site being read": {
			headers: nil,
			want:    observability.SurfaceWeb,
		},
		// The claim is refused rather than honoured: web is inferred from
		// holding a session, and letting a header assert it would forge the
		// only attribution here that is actually verifiable.
		"a token holder cannot claim the browser": {
			headers: map[string]string{"Authorization": "Bearer t", observability.SurfaceHeader: "web"},
			want:    observability.SurfaceAPI,
		},
		"nothing can claim to be the system": {
			headers: map[string]string{"Authorization": "Bearer t", observability.SurfaceHeader: "system"},
			want:    observability.SurfaceAPI,
		},
	} {
		t.Run(name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil)
			for key, value := range probe.headers {
				request.Header.Set(key, value)
			}
			engine.ServeHTTP(httptest.NewRecorder(), request)

			mu.Lock()
			defer mu.Unlock()
			if len(seen) != 1 {
				t.Fatalf("the access log recorded %d events, want exactly one", len(seen))
			}
			if seen[0] != probe.want {
				t.Fatalf("surface = %q, want %q", seen[0], probe.want)
			}
			seen = nil
		})
	}
}
