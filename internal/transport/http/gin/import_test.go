package ginhttp

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/domain"
)

type importInspectorFunc func(context.Context, io.Reader, uint64) (archive.Inspection, error)

func (function importInspectorFunc) Inspect(ctx context.Context, input io.Reader, epoch uint64) (archive.Inspection, error) {
	return function(ctx, input, epoch)
}

func TestImportInspectOwnerUploadEpochAndEnvelope(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	called := 0
	inspector := importInspectorFunc(func(_ context.Context, input io.Reader, epoch uint64) (archive.Inspection, error) {
		called++
		body, err := io.ReadAll(input)
		if err != nil {
			return archive.Inspection{}, err
		}
		if string(body) != "zip" || epoch != 1 {
			t.Fatalf("body=%q epoch=%d", body, epoch)
		}
		return archive.Inspection{
			FormatVersion: 1, StagingToken: "token", ExpiresAt: time.Date(2026, 8, 11, 0, 0, 0, 0, time.UTC),
			Disk: archive.DiskEstimate{SpaceOK: true}, Warnings: []string{},
		}, nil
	})
	engine, err := New(Config{
		Assets: testAssets(), OwnerAuth: fixture.manager, ImportInspector: inspector,
		Readiness: func(context.Context) error { return nil }, RequestID: func() string { return "req_import" },
	})
	if err != nil {
		t.Fatal(err)
	}
	request := headerRequest(http.MethodPost, "/api/v1/owner/import/inspect", fixture.token, strings.NewReader("zip"))
	request.Header.Set("Content-Type", "application/zip")
	response := performRequest(engine, request)
	if response.Code != http.StatusCreated || !strings.Contains(response.Body.String(), `"staging_token":"token"`) || !strings.Contains(response.Body.String(), `"request_id":"req_import"`) {
		t.Fatalf("response = %d %s", response.Code, response.Body.String())
	}
	if called != 1 {
		t.Fatalf("calls = %d", called)
	}

	for _, request := range []*http.Request{
		headerRequest(http.MethodPost, "/api/v1/owner/import/inspect", "wrong", strings.NewReader("zip")),
		headerRequest(http.MethodPost, "/api/v1/owner/import/inspect?x=1", fixture.token, strings.NewReader("zip")),
		headerRequest(http.MethodPost, "/api/v1/owner/import/inspect", fixture.token, strings.NewReader("zip")),
	} {
		if request.URL.RawQuery == "" && request.Header.Get("Authorization") == "Bearer "+fixture.token {
			request.Header.Set("Content-Type", "application/json")
		} else {
			request.Header.Set("Content-Type", "application/zip")
		}
		result := performRequest(engine, request)
		if result.Code < 400 {
			t.Fatalf("strict request succeeded: %d %s", result.Code, result.Body.String())
		}
	}
	if called != 1 {
		t.Fatalf("invalid requests invoked inspector: %d", called)
	}
}

func TestImportInspectMapsLimitsDomainAndCancellation(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	tests := []struct {
		name      string
		inspector ImportInspector
		limit     int64
		body      string
		status    int
		code      string
	}{
		{name: "body limit", limit: 2, body: "zip", inspector: importInspectorFunc(func(_ context.Context, input io.Reader, _ uint64) (archive.Inspection, error) {
			_, err := io.ReadAll(input)
			return archive.Inspection{}, err
		}), status: http.StatusRequestEntityTooLarge, code: "REQUEST_BODY_TOO_LARGE"},
		{name: "disk", body: "z", inspector: importInspectorFunc(func(context.Context, io.Reader, uint64) (archive.Inspection, error) {
			return archive.Inspection{}, domain.NewError(domain.CodeImportDiskSpaceInsufficient, "low disk", map[string]any{"required_additional_bytes": 10})
		}), status: http.StatusInsufficientStorage, code: "IMPORT_DISK_SPACE_INSUFFICIENT"},
		{name: "in progress", body: "z", inspector: importInspectorFunc(func(context.Context, io.Reader, uint64) (archive.Inspection, error) {
			return archive.Inspection{}, domain.NewError(domain.CodeImportInProgress, "active", nil)
		}), status: http.StatusConflict, code: "IMPORT_IN_PROGRESS"},
		{name: "cancel", body: "z", inspector: importInspectorFunc(func(context.Context, io.Reader, uint64) (archive.Inspection, error) {
			return archive.Inspection{}, context.Canceled
		}), status: http.StatusRequestTimeout, code: "CANCELLED"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			limit := test.limit
			if limit == 0 {
				limit = 1 << 20
			}
			engine, err := New(Config{
				Assets: testAssets(), OwnerAuth: fixture.manager, ImportInspector: test.inspector,
				Readiness: func(context.Context) error { return nil }, Limits: BodyLimits{Archive: limit},
			})
			if err != nil {
				t.Fatal(err)
			}
			request := headerRequest(http.MethodPost, "/api/v1/owner/import/inspect", fixture.token, strings.NewReader(test.body))
			request.Header.Set("Content-Type", "application/zip")
			response := performRequest(engine, request)
			if response.Code != test.status || decodeError(t, response.Body).Error.Code != test.code {
				t.Fatalf("response = %d %s", response.Code, response.Body.String())
			}
		})
	}
}

func TestImportInspectConfigRequiresOwnerAuth(t *testing.T) {
	inspector := importInspectorFunc(func(context.Context, io.Reader, uint64) (archive.Inspection, error) {
		return archive.Inspection{}, errors.New("must not run")
	})
	if _, err := New(Config{Assets: testAssets(), ImportInspector: inspector}); err == nil {
		t.Fatal("import inspector without owner auth succeeded")
	}
}
