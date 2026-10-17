package ginhttp

import (
	"context"
	"encoding/json"
	"net/http"
	"sync"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

const httpAvatarID = "01ARZ3NDEKTSV4RRFFQ69G5FAY"

type httpSiteTransactions struct {
	mu       sync.Mutex
	settings app.SiteSettings
	media    map[string]bool
	entries  map[string]bool
	// updateErr stands in for a constraint the database enforces and this
	// fixture cannot. The store below is a map; users_username_unique is not.
	updateErr error
}

func (transactions *httpSiteTransactions) Read(ctx context.Context, operation func(app.SiteStore) error) error {
	transactions.mu.Lock()
	defer transactions.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	return operation(&httpSiteStore{settings: &transactions.settings, media: transactions.media, entries: transactions.entries})
}

func (transactions *httpSiteTransactions) Write(ctx context.Context, operation func(app.SiteStore) error) error {
	transactions.mu.Lock()
	defer transactions.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	cloned := transactions.settings
	store := &httpSiteStore{settings: &cloned, media: transactions.media, entries: transactions.entries, updateErr: transactions.updateErr}
	if err := operation(store); err != nil {
		return err
	}
	transactions.settings = cloned
	return nil
}

type httpSiteStore struct {
	settings  *app.SiteSettings
	media     map[string]bool
	entries   map[string]bool
	updateErr error
}

func (store *httpSiteStore) SiteSettings(context.Context) (app.SiteSettings, error) {
	return *store.settings, nil
}

func (store *httpSiteStore) MediaExists(_ context.Context, id domain.ID) (bool, error) {
	return store.media[id.String()], nil
}

func (store *httpSiteStore) EntryExists(_ context.Context, id domain.ID) (bool, error) {
	return store.entries[id.String()], nil
}

func (store *httpSiteStore) UpdateSiteSettings(_ context.Context, settings app.SiteSettings) error {
	if store.updateErr != nil {
		return store.updateErr
	}
	*store.settings = settings
	return nil
}

func (store *httpSiteStore) ReconcileMediaReferences(context.Context, []domain.ID, time.Time) error {
	return nil
}

func newSiteHTTPFixture(t *testing.T) (authenticationTestFixture, *httpSiteTransactions) {
	t.Helper()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Millisecond)
	_, err = auth.InitializeOffline(context.Background(), runtimeState, auth.Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(runtimeState.Root, auth.Options{Now: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	transactions := &httpSiteTransactions{
		settings: app.SiteSettings{ProfileUserID: "0123456789abcdef0123456789abcdef", Username: "writer", DisplayName: "Moment", Title: "Moment", Timezone: "UTC", CommentsEnabled: true, RegistrationMode: domain.RegistrationApproval},
		media:    map[string]bool{httpAvatarID: true},
	}
	service, err := app.NewSiteService(transactions, app.SiteOptions{AllowDevelopmentLocalHTTP: true})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{
		Assets:                testAssets(),
		SessionSigner:         manager,
		Accounts:              testAccountsForRuntime(t, runtimeState),
		SiteSettings:          service,
		InstanceID:            runtimeState.Instance.ID,
		InstanceInitializedAt: runtimeState.Instance.InitializedAt,
		Readiness:             service.Ready,
		RequestID:             func() string { return "req_site" },
	})
	if err != nil {
		t.Fatal(err)
	}
	return authenticationTestFixture{
		manager: manager, token: issueTestAPIToken(t, testAccountsForRuntime(t, runtimeState)), engine: engine, now: &now,
		instanceID: runtimeState.Instance.ID, instanceInitializedAt: runtimeState.Instance.InitializedAt.UTC().Format(time.RFC3339Nano),
	}, transactions
}

func TestSitePublicReadAndOwnerPatchTriState(t *testing.T) {
	fixture, transactions := newSiteHTTPFixture(t)
	publicRequest := jsonRequest(http.MethodGet, "/api/v1/site", "")
	publicRequest.Host = "private.internal:8443"
	publicRequest.Header.Set("X-Forwarded-Host", "attacker.example")
	public := performRequest(fixture.engine, publicRequest)
	if public.Code != http.StatusOK {
		t.Fatalf("public status = %d, body = %s", public.Code, public.Body.String())
	}
	var publicEnvelope struct {
		Data map[string]any `json:"data"`
		Meta map[string]any `json:"meta"`
	}
	if err := json.NewDecoder(public.Body).Decode(&publicEnvelope); err != nil {
		t.Fatalf("decode public site: %v", err)
	}
	data := publicEnvelope.Data
	if data["title"] != "Moment" || data["timezone"] != "UTC" || data["sha256"] != nil || data["storage_path"] != nil {
		t.Fatalf("public data = %#v", data)
	}
	if publicEnvelope.Meta["instance_id"] != fixture.instanceID || publicEnvelope.Meta["instance_initialized_at"] != fixture.instanceInitializedAt || publicEnvelope.Meta["request_id"] != "req_site" {
		t.Fatalf("public meta = %#v", publicEnvelope.Meta)
	}

	unauthorized := performRequest(fixture.engine, jsonRequest(http.MethodPatch, "/api/v1/site", `{"title":"blocked"}`))
	if unauthorized.Code != http.StatusUnauthorized || decodeError(t, unauthorized.Body).Error.Code != "UNAUTHORIZED" {
		t.Fatalf("unauthorized status = %d", unauthorized.Code)
	}

	requestBody := `{"title":"Personal Moment","bio":"personal log","timezone":"Asia/Shanghai","public_base_url":"http://localhost:80/journal/","avatar_media_id":"` + httpAvatarID + `","cover_media_id":"` + httpAvatarID + `","comments_enabled":false}`
	request := jsonRequest(http.MethodPatch, "/api/v1/site", requestBody)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	updated := performRequest(fixture.engine, request)
	if updated.Code != http.StatusOK {
		t.Fatalf("updated status = %d, body = %s", updated.Code, updated.Body.String())
	}
	data = decodeDataMap(t, updated)
	if data["title"] != "Personal Moment" || data["comments_enabled"] != false || data["public_base_url"] != "http://localhost/journal" || data["avatar_media_id"] != httpAvatarID || data["cover_media_id"] != httpAvatarID {
		t.Fatalf("updated data = %#v", data)
	}

	clearRequest := jsonRequest(http.MethodPatch, "/api/v1/site", `{"public_base_url":null,"avatar_media_id":null,"cover_media_id":null}`)
	clearRequest.Header.Set("Authorization", "Bearer "+fixture.token)
	cleared := performRequest(fixture.engine, clearRequest)
	if cleared.Code != http.StatusOK {
		t.Fatalf("clear status = %d, body = %s", cleared.Code, cleared.Body.String())
	}
	data = decodeDataMap(t, cleared)
	if data["public_base_url"] != nil || data["avatar_media_id"] != nil || data["cover_media_id"] != nil || data["bio"] != "personal log" || data["comments_enabled"] != false {
		t.Fatalf("cleared data = %#v", data)
	}
	if transactions.settings.PublicBaseURL != nil || transactions.settings.AvatarMediaID != nil || transactions.settings.CoverMediaID != nil {
		t.Fatalf("clear did not persist: %#v", transactions.settings)
	}
}

func TestSitePatchStrictJSONCSRFAndCancellation(t *testing.T) {
	fixture, transactions := newSiteHTTPFixture(t)
	for name, body := range map[string]string{
		"empty":            `{}`,
		"unknown":          `{"title":"changed","extra":true}`,
		"multiple":         `{"title":"changed"} {"bio":"extra"}`,
		"null nonnullable": `{"comments_enabled":null}`,
	} {
		t.Run(name, func(t *testing.T) {
			request := jsonRequest(http.MethodPatch, "/api/v1/site", body)
			request.Header.Set("Authorization", "Bearer "+fixture.token)
			response := performRequest(fixture.engine, request)
			if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
				t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
			}
			if transactions.settings.Title != "Moment" {
				t.Fatalf("strict JSON partially changed settings: %#v", transactions.settings)
			}
		})
	}

	cookie, session := exchangeAccountSession(t, fixture, fixture.token)
	missingCSRF := jsonRequest(http.MethodPatch, "/api/v1/site", `{"bio":"blocked"}`)
	missingCSRF.AddCookie(cookie)
	if response := performRequest(fixture.engine, missingCSRF); response.Code != http.StatusForbidden || decodeError(t, response.Body).Error.Code != "CSRF_INVALID" {
		t.Fatalf("missing CSRF status = %d, body = %s", response.Code, response.Body.String())
	}
	withCSRF := jsonRequest(http.MethodPatch, "/api/v1/site", `{"bio":"from session"}`)
	withCSRF.AddCookie(cookie)
	withCSRF.Header.Set(CSRFHeaderName, session["csrf_token"].(string))
	if response := performRequest(fixture.engine, withCSRF); response.Code != http.StatusOK {
		t.Fatalf("session update status = %d, body = %s", response.Code, response.Body.String())
	}

	cancelledContext, cancel := context.WithCancel(context.Background())
	cancel()
	cancelled := jsonRequest(http.MethodPatch, "/api/v1/site", `{"title":"cancelled"}`).WithContext(cancelledContext)
	cancelled.Header.Set("Authorization", "Bearer "+fixture.token)
	response := performRequest(fixture.engine, cancelled)
	if response.Code != http.StatusRequestTimeout || decodeError(t, response.Body).Error.Code != "CANCELLED" {
		t.Fatalf("cancelled status = %d, body = %s", response.Code, response.Body.String())
	}
	if transactions.settings.Title != "Moment" {
		t.Fatalf("cancelled update persisted: %#v", transactions.settings)
	}
}

func TestSiteRoutesRequireAuthWhenConfigured(t *testing.T) {
	transactions := &httpSiteTransactions{settings: app.SiteSettings{ProfileUserID: "0123456789abcdef0123456789abcdef", Username: "writer", DisplayName: "Moment", Title: "Moment", Timezone: "UTC", CommentsEnabled: true}}
	service, err := app.NewSiteService(transactions, app.SiteOptions{})
	if err != nil {
		t.Fatal(err)
	}
	_, err = New(Config{Assets: testAssets(), SiteSettings: service})
	if err == nil {
		t.Fatal("site routes without owner authentication were accepted")
	}
}

func TestSitePublicJSONContainsOnlyStableDTOFields(t *testing.T) {
	fixture, _ := newSiteHTTPFixture(t)
	response := performRequest(fixture.engine, jsonRequest(http.MethodGet, "/api/v1/site", ""))
	var envelope struct {
		Data map[string]json.RawMessage `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 11 {
		t.Fatalf("public DTO fields = %v", envelope.Data)
	}
}

// One handle column, one answer, whichever form the owner reached it from.
//
// The account route refuses a reserved handle with USERNAME_RESERVED and a
// handle somebody else holds with 409 USERNAME_TAKEN. This route validated the
// shape only and reported the constraint failure as INTERNAL, so the settings
// form showed "operation failed" for a refusal it already has words for.
func TestOwnerSitePatchAnswersHandleRefusalsLikeTheAccountRoute(t *testing.T) {
	fixture, transactions := newSiteHTTPFixture(t)

	reserved := jsonRequest(http.MethodPatch, "/api/v1/site", `{"username":"search"}`)
	reserved.Header.Set("Authorization", "Bearer "+fixture.token)
	response := performRequest(fixture.engine, reserved)
	if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "USERNAME_RESERVED" {
		t.Fatalf("reserved status = %d, body = %s", response.Code, response.Body.String())
	}
	if transactions.settings.Username != "writer" {
		t.Fatalf("reserved handle persisted: %#v", transactions.settings)
	}

	transactions.updateErr = domain.NewError(domain.CodeUsernameTaken, "that username is taken", map[string]any{"field": "username"})
	taken := jsonRequest(http.MethodPatch, "/api/v1/site", `{"username":"meihua"}`)
	taken.Header.Set("Authorization", "Bearer "+fixture.token)
	response = performRequest(fixture.engine, taken)
	if response.Code != http.StatusConflict || decodeError(t, response.Body).Error.Code != "USERNAME_TAKEN" {
		t.Fatalf("taken status = %d, body = %s", response.Code, response.Body.String())
	}
}

// The owner can close registration without editing the database.
//
// The field existed on SiteUpdate and on the row, and nothing in between could
// carry a value from one to the other: the request struct had no member for it
// and DisallowUnknownFields turned the attempt into a 400. It stays out of the
// public projection, which is what TestSitePublicJSONContainsOnlyStableDTOFields
// holds the line on.
func TestOwnerSitePatchWritesRegistrationModeWithoutPublishingIt(t *testing.T) {
	fixture, transactions := newSiteHTTPFixture(t)

	for _, mode := range []string{"open", "closed", "approval"} {
		request := jsonRequest(http.MethodPatch, "/api/v1/site", `{"registration_mode":"`+mode+`"}`)
		request.Header.Set("Authorization", "Bearer "+fixture.token)
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusOK {
			t.Fatalf("mode %q status = %d, body = %s", mode, response.Code, response.Body.String())
		}
		if string(transactions.settings.RegistrationMode) != mode {
			t.Fatalf("stored registration mode = %q, want %q", transactions.settings.RegistrationMode, mode)
		}
		if _, published := decodeDataMap(t, response)["registration_mode"]; published {
			t.Fatalf("owner response published registration_mode: %s", response.Body.String())
		}
	}

	rejected := jsonRequest(http.MethodPatch, "/api/v1/site", `{"registration_mode":"everyone"}`)
	rejected.Header.Set("Authorization", "Bearer "+fixture.token)
	response := performRequest(fixture.engine, rejected)
	if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
		t.Fatalf("invalid mode status = %d, body = %s", response.Code, response.Body.String())
	}
	if transactions.settings.RegistrationMode != "approval" {
		t.Fatalf("invalid mode changed the row: %#v", transactions.settings)
	}

	public := performRequest(fixture.engine, jsonRequest(http.MethodGet, "/api/v1/site", ""))
	if _, published := decodeDataMap(t, public)["registration_mode"]; published {
		t.Fatalf("public site published registration_mode: %s", public.Body.String())
	}
}
