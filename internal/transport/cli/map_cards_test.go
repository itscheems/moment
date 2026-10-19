package cli

import (
	"reflect"
	"testing"
)

const cliMapJSON = `{"places":[{"name":"Ginza","display_name":"Tokyo","latitude":35.6712345,"longitude":139.7654321,"coordinate_system":"wgs84"}],"connect_points":false}`

func TestMapOnlyCLIDirectAPIParityAndClear(t *testing.T) {
	f := newPostCLIFixture(t)
	a := momentTestApplication()
	t.Setenv("MOMENT_TOKEN", f.token)
	direct := execute(a, cliMapJSON, "post", "create", "--data-dir", f.dataDir, "--as", "writer", "--map-json", "-", "--idempotency-key", "cli-map", "--json")
	if direct.exit != ExitSuccess || direct.stderr != "" {
		t.Fatalf("direct=%+v", direct)
	}
	created := commandData(t, direct)
	id := created["id"].(string)
	replay := execute(a, cliMapJSON, "post", "create", "--data-dir", f.dataDir, "--as", "writer", "--map-json", "-", "--idempotency-key", "cli-map", "--json")
	if replay.exit != ExitSuccess || commandData(t, replay)["id"] != id {
		t.Fatalf("replay=%+v", replay)
	}
	api := commandData(t, execute(a, "", "--url", f.server.URL, "post", "get", id, "--json"))
	if !reflect.DeepEqual(created, api) {
		t.Fatalf("direct/API differ: %#v %#v", created, api)
	}
	rejected := execute(a, "", "--url", f.server.URL, "post", "update", id, "--clear-map", "--json")
	if rejected.exit != ExitConflict {
		t.Fatalf("cleared map-only post=%+v", rejected)
	}
	updated := commandData(t, execute(a, "", "--url", f.server.URL, "post", "update", id, "--content", "trip", "--json"))
	if updated["map_card"] == nil {
		t.Fatal("omitted map was lost")
	}
	cleared := commandData(t, execute(a, "", "post", "update", id, "--data-dir", f.dataDir, "--as", "writer", "--clear-map", "--json"))
	if cleared["map_card"] != nil {
		t.Fatal("map was not cleared")
	}
	restored := commandData(t, execute(a, cliMapJSON, "--url", f.server.URL, "post", "update", id, "--map-json", "-", "--json"))
	directGet := commandData(t, execute(a, "", "post", "get", id, "--data-dir", f.dataDir, "--as", "writer", "--json"))
	if !reflect.DeepEqual(restored, directGet) {
		t.Fatal("API replacement diverged")
	}
	apiCreate := execute(a, cliMapJSON, "--url", f.server.URL, "post", "create", "--map-json", "-", "--json")
	if apiCreate.exit != ExitSuccess || commandData(t, apiCreate)["map_card"] == nil {
		t.Fatalf("API create=%+v", apiCreate)
	}
	for _, args := range [][]string{{"--map-json", "-", "--clear-map"}, {"--map-json", "-", "--location-json", "-"}} {
		all := []string{"post", "update", id, "--data-dir", f.dataDir, "--as", "writer", "--json"}
		all = append(all, args...)
		output := execute(a, cliMapJSON, all...)
		if output.exit != ExitInvalidInput || output.stderr != "" {
			t.Fatalf("invalid flags=%+v", output)
		}
	}
	invalid := execute(a, `{"places":[]}`, "post", "create", "--data-dir", f.dataDir, "--as", "writer", "--map-json", "-", "--json")
	if invalid.exit != ExitInvalidInput {
		t.Fatalf("invalid map=%+v", invalid)
	}
}
