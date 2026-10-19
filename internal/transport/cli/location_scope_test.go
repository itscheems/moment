package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/locationdto"
)

func TestLocationCLIGlobalScopeForwardsToAPIAndRejectsInvalidScope(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path != "/api/v1/locations/search" || r.URL.Query().Get("scope") != "global" || r.URL.Query().Get("q") != "Tokyo" {
			t.Errorf("request=%s", r.URL)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"data": locationdto.FromDomains([]domain.Location{{Name: "Tokyo", DisplayName: "Japan", Provider: "geoapify"}}), "meta": map[string]any{"request_id": "req_global"}})
	}))
	defer server.Close()
	t.Setenv("MOMENT_TOKEN", "location-test-token")
	a := momentTestApplication()
	output := execute(a, "", "--url", server.URL, "location", "search", "--query", "Tokyo", "--scope", "global", "--json")
	if output.exit != ExitSuccess || output.stderr != "" || calls != 1 {
		t.Fatalf("global CLI=%+v calls=%d", output, calls)
	}
	if len(commandData(t, output)["items"].([]any)) != 1 {
		t.Fatal("location result lost")
	}
	output = execute(a, "", "--url", server.URL, "location", "search", "--query", "Tokyo", "--scope", "nearby", "--json")
	if output.exit != ExitInvalidInput || output.stderr != "" || calls != 1 {
		t.Fatalf("invalid scope=%+v calls=%d", output, calls)
	}
}
