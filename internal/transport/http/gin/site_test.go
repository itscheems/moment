package ginhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"testing"
)

func siteRequest(fixture postHTTPFixture, body string) *http.Request {
	request := jsonRequest(http.MethodPatch, "/api/v1/site", body)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	return request
}

func TestSitePublicReadAndConfigurationTriState(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	public := performRequest(fixture.engine, jsonRequest(http.MethodGet, "/api/v1/site", ""))
	if public.Code != http.StatusOK {
		t.Fatalf("public site=%d %s", public.Code, public.Body.String())
	}
	encoded := append([]byte{}, public.Body.Bytes()...)
	initial := decodeDataMap(t, public)
	if initial["title"] != "Moment" || initial["username"] != "writer" || initial["public_base_url"] != nil {
		t.Fatalf("public data=%+v", initial)
	}
	var envelope struct {
		Meta map[string]any `json:"meta"`
	}
	if err := json.Unmarshal(encoded, &envelope); err != nil {
		t.Fatal(err)
	}
	if envelope.Meta["instance_id"] != fixture.instanceID || envelope.Meta["instance_initialized_at"] != fixture.instanceInitializedAt {
		t.Fatalf("instance identity=%+v", envelope.Meta)
	}
	unauthorized := performRequest(fixture.engine, jsonRequest(http.MethodPatch, "/api/v1/site", `{"title":"blocked"}`))
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous configuration=%d", unauthorized.Code)
	}
	response := performRequest(fixture.engine, siteRequest(fixture, `{"title":"Personal Moment","timezone":"Europe/Paris","public_base_url":"http://localhost:80/journal/","comments_enabled":false}`))
	if response.Code != http.StatusOK {
		t.Fatalf("configuration=%d %s", response.Code, response.Body.String())
	}
	data := decodeDataMap(t, response)
	if data["title"] != "Personal Moment" || data["timezone"] != "Europe/Paris" || data["public_base_url"] != "http://localhost/journal" || data["comments_enabled"] != false || data["username"] != "writer" {
		t.Fatalf("configuration=%+v", data)
	}
	cleared := performRequest(fixture.engine, siteRequest(fixture, `{"public_base_url":null}`))
	if cleared.Code != http.StatusOK || decodeDataMap(t, cleared)["public_base_url"] != nil {
		t.Fatalf("URL clear=%d %s", cleared.Code, cleared.Body.String())
	}
}

func TestSitePatchRejectsPersonalFieldsWithoutChangingAccounts(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	before, err := fixture.accounts.ProfileAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, body := range []string{`{"username":"another"}`, `{"display_name":"Another person"}`, `{"bio":"another biography"}`, `{"avatar_media_id":null}`, `{"cover_media_id":null}`} {
		response := performRequest(fixture.engine, siteRequest(fixture, body))
		if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("personal field through site=%d %s", response.Code, response.Body.String())
		}
	}
	after, err := fixture.accounts.Account(context.Background(), before.ID)
	if err != nil || after.Username != before.Username || after.DisplayName != before.DisplayName || after.Bio != before.Bio || after.UpdatedAtMS != before.UpdatedAtMS {
		t.Fatal("site configuration overwrote the selected account")
	}
}

func TestSitePatchStrictJSONCSRFAndCancellation(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	for _, body := range []string{`{}`, `{"title":"changed","extra":true}`, `{"title":"changed"} {}`, `{"comments_enabled":null}`, `{"timezone":"UTC+08:00"}`} {
		response := performRequest(fixture.engine, siteRequest(fixture, body))
		if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("strict update=%d %s", response.Code, response.Body.String())
		}
	}
	cookie, session := exchangeAccountSession(t, fixture.authenticationTestFixture, fixture.token)
	request := jsonRequest(http.MethodPatch, "/api/v1/site", `{"title":"session title"}`)
	request.AddCookie(cookie)
	missing := performRequest(fixture.engine, request)
	if missing.Code != http.StatusForbidden || decodeError(t, missing.Body).Error.Code != "CSRF_INVALID" {
		t.Fatalf("missing CSRF=%d %s", missing.Code, missing.Body.String())
	}
	request = jsonRequest(http.MethodPatch, "/api/v1/site", `{"title":"session title"}`)
	request.AddCookie(cookie)
	request.Header.Set(CSRFHeaderName, session["csrf_token"].(string))
	if response := performRequest(fixture.engine, request); response.Code != http.StatusOK {
		t.Fatalf("session update=%d %s", response.Code, response.Body.String())
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := performRequest(fixture.engine, siteRequest(fixture, `{"title":"cancelled"}`).WithContext(ctx))
	if cancelled.Code != http.StatusRequestTimeout || decodeError(t, cancelled.Body).Error.Code != "CANCELLED" {
		t.Fatalf("cancellation=%d %s", cancelled.Code, cancelled.Body.String())
	}
	settings, err := fixture.site.Get(context.Background())
	if err != nil || settings.Title != "session title" {
		t.Fatal("invalid or cancelled update persisted")
	}
}

func TestOwnProfileChangesAndPictureClearAreIndependentFromSiteConfiguration(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	var contents bytes.Buffer
	if err := png.Encode(&contents, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	stored, _, err := fixture.media.Ingest(fixture.ctx, "avatar.png", &contents)
	if err != nil {
		t.Fatal(err)
	}
	update := jsonRequest(http.MethodPatch, "/api/v1/account/profile", `{"display_name":"Actual writer","avatar_media_id":"`+stored.ID.String()+`"}`)
	update.Header.Set("Authorization", "Bearer "+fixture.token)
	response := performRequest(fixture.engine, update)
	if response.Code != http.StatusOK {
		t.Fatalf("own profile=%d %s", response.Code, response.Body.String())
	}
	if site, err := fixture.site.Get(context.Background()); err != nil || site.DisplayName != "Actual writer" || site.AvatarMediaID == nil || site.Title != "Moment" {
		t.Fatalf("profile projection=%+v,%v", site, err)
	}
	clearRequest := jsonRequest(http.MethodPatch, "/api/v1/account/profile", `{"avatar_media_id":null}`)
	clearRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	response = performRequest(fixture.engine, clearRequest)
	if response.Code != http.StatusOK || decodeDataMap(t, response)["avatar_media_id"] != nil {
		t.Fatalf("explicit picture clear=%d %s", response.Code, response.Body.String())
	}
}

func TestSitePublicJSONContainsOnlyStableDTOFields(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	response := performRequest(fixture.engine, jsonRequest(http.MethodGet, "/api/v1/site", ""))
	data := decodeDataMap(t, response)
	if len(data) != 11 {
		t.Fatalf("site wire fields=%+v", data)
	}
	for _, name := range []string{"registration_mode", "role", "status", "password_hash", "token_hash"} {
		if _, ok := data[name]; ok {
			t.Fatalf("private field %s leaked", name)
		}
	}
}

func TestSiteRegistrationModeIsWritableWithoutPublishingIt(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	for _, mode := range []string{"open", "closed", "approval"} {
		response := performRequest(fixture.engine, siteRequest(fixture, `{"registration_mode":"`+mode+`"}`))
		if response.Code != http.StatusOK {
			t.Fatalf("registration=%d %s", response.Code, response.Body.String())
		}
		settings, err := fixture.site.Get(context.Background())
		if err != nil || string(settings.RegistrationMode) != mode {
			t.Fatal("registration policy was not persisted")
		}
		if decodeDataMap(t, response)["registration_mode"] != nil {
			t.Fatal("private registration policy leaked into public DTO")
		}
	}
}

func TestSiteRoutesRequireAuthenticationDependencies(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	if _, err := New(Config{Assets: testAssets(), SiteSettings: fixture.site}); err == nil {
		t.Fatal("site mutations were mounted without authentication")
	}
}
