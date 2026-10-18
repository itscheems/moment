package ginhttp

import (
	"net/http"
	"strings"
	"testing"
)

const httpMapJSON = `{"places":[{"name":"Ginza","display_name":"Tokyo","latitude":35.6712345,"longitude":139.7654321,"coordinate_system":"wgs84"}],"connect_points":false}`

func TestPostHTTPMapOnlyPatchAndPrivateLocationBoundary(t *testing.T) {
	f := newPostHTTPFixture(t, 0)
	request := jsonRequest(http.MethodPost, "/api/v1/posts", `{"map_card":`+httpMapJSON+`,"location":{"name":"old location","display_name":"private coordinates","country_code":"","country":"","region":"","city":"","district":"","category":"","provider":"manual","provider_place_id":"","latitude":31.2,"longitude":121.4,"coordinate_system":"gcj02"}}`)
	request.Header.Set("Authorization", "Bearer "+f.token)
	response := performRequest(f.engine, request)
	if response.Code != 201 {
		t.Fatalf("create=%d %s", response.Code, response.Body.String())
	}
	data := decodeDataMap(t, response)
	id := data["id"].(string)
	location := data["location"].(map[string]any)
	if len(location) != 2 || data["map_card"] == nil || strings.Contains(response.Body.String(), "provider_place_id") {
		t.Fatalf("public shape=%s", response.Body.String())
	}
	update := func(body string) int {
		r := jsonRequest(http.MethodPatch, "/api/v1/posts/"+id, body)
		r.Header.Set("Authorization", "Bearer "+f.token)
		return performRequest(f.engine, r).Code
	}
	if code := update(`{"map_card":null}`); code != 422 {
		t.Fatalf("clear empty=%d", code)
	}
	if code := update(`{"content_md":"trip"}`); code != 200 {
		t.Fatalf("edit=%d", code)
	}
	got := performRequest(f.engine, jsonRequest(http.MethodGet, "/api/v1/posts/"+id, ""))
	if got.Code != 200 || decodeDataMap(t, got)["map_card"] == nil {
		t.Fatalf("preserve=%s", got.Body.String())
	}
	if code := update(`{"map_card":null}`); code != 200 {
		t.Fatalf("clear=%d", code)
	}
	got = performRequest(f.engine, jsonRequest(http.MethodGet, "/api/v1/posts/"+id, ""))
	if decodeDataMap(t, got)["map_card"] != nil {
		t.Fatal("map was not cleared")
	}
	if code := update(`{"map_card":` + httpMapJSON + `,"visibility":"only_me"}`); code != 200 {
		t.Fatalf("hide=%d", code)
	}
	got = performRequest(f.engine, jsonRequest(http.MethodGet, "/api/v1/posts/"+id, ""))
	if got.Code != 404 {
		t.Fatalf("private map leaked=%s", got.Body.String())
	}
	for _, bad := range []string{`{"map_card":{"places":[],"connect_points":false}}`, `{"map_card":` + strings.Replace(httpMapJSON, `"latitude":35.6712345`, `"latitude":91`, 1) + `}`, `{"map_card":` + strings.Replace(httpMapJSON, `"name":"Ginza"`, `"provider":"google","name":"Ginza"`, 1) + `}`} {
		r := jsonRequest(http.MethodPost, "/api/v1/posts", bad)
		r.Header.Set("Authorization", "Bearer "+f.token)
		if response := performRequest(f.engine, r); response.Code != 400 {
			t.Fatalf("invalid=%d %s", response.Code, response.Body.String())
		}
	}
}
func TestMapCSPAllowsOnlyPreciseTileOriginAndLocalWorkers(t *testing.T) {
	directives := map[string]string{}
	for _, directive := range strings.Split(contentSecurityPolicy, ";") {
		parts := strings.Fields(directive)
		directives[parts[0]] = strings.Join(parts[1:], " ")
	}
	if directives["connect-src"] != "'self' https://tiles.openfreemap.org" || directives["img-src"] != "'self' data: blob: https://tiles.openfreemap.org" || directives["worker-src"] != "'self'" || strings.Contains(contentSecurityPolicy, "*") {
		t.Fatalf("CSP=%s", contentSecurityPolicy)
	}
}
