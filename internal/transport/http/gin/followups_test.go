package ginhttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/archive"
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
	route := "/api/v1/media/" + value.ID.String()
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

func TestPreparedExportLengthFailureAndCleanup(t *testing.T) {
	for _, fail := range []bool{false, true} {
		var temporary string
		producer := func(_ context.Context, w io.Writer) (archive.Summary, error) {
			temporary = w.(*exportContextWriter).writer.(*os.File).Name()
			_, err := io.WriteString(w, "complete bytes")
			if fail {
				return archive.Summary{}, errors.New("source failed after producing bytes")
			}
			return archive.Summary{ArchiveBytes: 14}, err
		}
		engine := gin.New()
		engine.GET("/export", preparedExport(producer, archive.FormatHeaderValue, "moment-export.zip", time.Second))
		server := httptest.NewUnstartedServer(engine)
		server.Config.WriteTimeout = 10 * time.Millisecond
		server.Start()
		response, err := server.Client().Get(server.URL + "/export")
		if err != nil {
			server.Close()
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		_ = response.Body.Close()
		server.Close()
		if err != nil {
			t.Fatal(err)
		}
		if fail {
			if response.StatusCode != 500 || bytes.Contains(body, []byte("complete bytes")) {
				t.Fatalf("partial export advertised: %d %s", response.StatusCode, body)
			}
		} else if response.StatusCode != 200 || response.Header.Get("Content-Length") != strconv.Itoa(len(body)) || len(response.TransferEncoding) != 0 {
			t.Fatalf("length: %+v %q", response, body)
		}
		if _, err := os.Stat(temporary); !errors.Is(err, os.ErrNotExist) {
			t.Fatalf("temporary survived: %v", err)
		}
	}
}

func TestSelectedMediaDeletionRejectsActiveMember(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	id := uploadAccountMedia(t, fixture)
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
		request := httptest.NewRequest(http.MethodDelete, "/api/v1/media/"+id+query, nil)
		request.AddCookie(session)
		request.Header.Set(CSRFHeaderName, csrf)
		response := performRequest(fixture.engine, request)
		// An authenticated member receives a permission refusal.
		if response.Code != http.StatusForbidden {
			t.Fatalf("member delete: %d %s", response.Code, response.Body.String())
		}
	}
	if response := performRequest(fixture.engine, headerRequest(http.MethodGet, "/api/v1/media/"+id, fixture.token, nil)); response.Code != http.StatusOK {
		t.Fatalf("member removed media: %d", response.Code)
	}
}

func TestAccountExportHTTPUsesAuthenticatedIdentityAndKeepsAuthorBoundary(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	session, _, member := signInMember(t, fixture)
	exporter, err := archive.NewExporter(archive.Options{DataDir: fixture.runtime.Root, AppVersion: "test"})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{Assets: testAssets(), SessionSigner: fixture.manager, Accounts: fixture.accountService, Archive: exporter})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/api/v1/account/export", nil)
	request.AddCookie(session)
	response := performRequest(engine, request)
	if response.Code != 200 || response.Header().Get("X-Archive-Format") != archive.AccountFormatHeaderValue {
		t.Fatalf("own export: %d %s", response.Code, response.Body.String())
	}
	// Members, including pending accounts, may export themselves but cannot
	// select another account or access the full site backup.
	for _, path := range []string{"/api/v1/account/export?account_id=someone", "/api/v1/backups/export"} {
		request := httptest.NewRequest(http.MethodGet, path, nil)
		request.AddCookie(session)
		got := performRequest(engine, request)
		if got.Code == 200 {
			t.Fatalf("scope escaped: %s", path)
		}
	}
	if got := performRequest(engine, httptest.NewRequest(http.MethodGet, "/api/v1/account/export", nil)); got.Code != 401 {
		t.Fatalf("unauth: %d", got.Code)
	}
	if _, err := fixture.accountService.SetStatus(context.Background(), member.ID, domain.AccountSuspended); err != nil {
		t.Fatal(err)
	}
	if got := performRequest(engine, request); got.Code == 200 {
		t.Fatal("suspended session exported")
	}
}
