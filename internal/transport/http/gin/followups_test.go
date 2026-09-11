package ginhttp

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
)

func TestCapabilitiesAndSelectedMediaDeletionHTTP(t *testing.T) {
	engine, store, token, _ := newMediaHTTPFixture(t)
	response := performRequest(engine, httptest.NewRequest(http.MethodGet, "/api/v1/capabilities", nil))
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	limits := decodeDataMap(t, response)["limits"].(map[string]any)
	if limits["max_post_code_points"] != float64(domain.MaxContentCodePoints) || limits["max_video_bytes"] != float64(media.DefaultMaxVideoBytes) {
		t.Fatal(limits)
	}
	value, _, err := store.Ingest(context.Background(), "a.gif", bytes.NewReader(testGIF(t)))
	if err != nil {
		t.Fatal(err)
	}
	route := "/api/v1/owner/media/" + value.ID.String()
	if got := performRequest(engine, httptest.NewRequest(http.MethodDelete, route, nil)); got.Code != 401 {
		t.Fatalf("unauth delete: %d", got.Code)
	}
	for _, query := range []string{"?dry_run=%zz", "?dry_run=true;ignored", "?dry_run=false", "?dry_run=true&dry_run=true"} {
		got := performRequest(engine, headerRequest(http.MethodDelete, route+query, token, nil))
		if got.Code != 400 {
			t.Fatalf("invalid query %s: %d %s", query, got.Code, got.Body.String())
		}
	}
	dry := performRequest(engine, headerRequest(http.MethodDelete, route+"?dry_run=true", token, nil))
	if dry.Code != 200 || decodeDataMap(t, dry)["deleted"] != false {
		t.Fatalf("dry: %d %s", dry.Code, dry.Body.String())
	}
	removed := performRequest(engine, headerRequest(http.MethodDelete, route, token, nil))
	if removed.Code != 200 || decodeDataMap(t, removed)["deleted"] != true {
		t.Fatalf("delete: %d %s", removed.Code, removed.Body.String())
	}
	if got := performRequest(engine, httptest.NewRequest(http.MethodGet, "/api/v1/media/"+value.ID.String(), nil)); got.Code != 404 {
		t.Fatalf("original survived: %d", got.Code)
	}
}

func TestSelectedMediaDeletionRejectsActiveMember(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	id := uploadOwnerMedia(t, fixture)
	session, csrf, member := signInMember(t, fixture)
	if _, err := fixture.accountService.SetStatus(context.Background(), member.ID, domain.AccountActive); err != nil {
		t.Fatal(err)
	}
	current := httptest.NewRequest(http.MethodGet, "/api/v1/account/session", nil)
	current.AddCookie(session)
	if got := performRequest(fixture.engine, current); got.Code != http.StatusOK {
		t.Fatalf("member credential is not active: %d", got.Code)
	}
	for _, query := range []string{"", "?dry_run=true"} {
		request := httptest.NewRequest(http.MethodDelete, "/api/v1/owner/media/"+id+query, nil)
		request.AddCookie(session)
		request.Header.Set(CSRFHeaderName, csrf)
		response := performRequest(fixture.engine, request)
		// Existing owner gates report UNAUTHORIZED for an authenticated non-owner.
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("member delete: %d %s", response.Code, response.Body.String())
		}
	}
	if response := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/media/"+id, nil)); response.Code != http.StatusOK {
		t.Fatalf("member removed media: %d", response.Code)
	}
}
