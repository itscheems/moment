package ginhttp

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/archive"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/observability"
)

type archiveExporterFunc func(context.Context, io.Writer) (archive.Summary, error)

func (function archiveExporterFunc) Export(ctx context.Context, writer io.Writer) (archive.Summary, error) {
	return function(ctx, writer)
}

func TestExportOwnerAuthStrictInputAndDownloadHeaders(t *testing.T) {
	fixture := newOwnerTestFixture(t, false)
	cookie, _ := exchangeOwnerSession(t, fixture, fixture.token)
	var calls atomic.Int64
	exporter := archiveExporterFunc(func(_ context.Context, writer io.Writer) (archive.Summary, error) {
		calls.Add(1)
		_, _ = io.WriteString(writer, "zip")
		return archive.Summary{FormatVersion: 1}, nil
	})
	engine, err := New(Config{
		Assets: testAssets(), OwnerAuth: fixture.manager, Archive: exporter,
		Readiness: func(context.Context) error { return nil }, RequestID: func() string { return "req_export" },
	})
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(engine)
	defer server.Close()

	for _, authorize := range []func(*http.Request){
		func(request *http.Request) { request.Header.Set("Authorization", "Bearer "+fixture.token) },
		func(request *http.Request) { request.AddCookie(cookie) },
	} {
		request, _ := http.NewRequest(http.MethodGet, server.URL+"/api/v1/owner/export", http.NoBody)
		authorize(request)
		response, err := server.Client().Do(request)
		if err != nil {
			t.Fatal(err)
		}
		_, _ = io.Copy(io.Discard, response.Body)
		_ = response.Body.Close()
		if response.StatusCode != http.StatusOK || response.Header.Get("Content-Type") != "application/zip" ||
			response.Header.Get("X-Archive-Format") != archive.FormatHeaderValue ||
			response.Header.Get("Content-Disposition") != `attachment; filename="moment-export.zip"` {
			t.Fatalf("download response = %d %#v", response.StatusCode, response.Header)
		}
	}

	before := calls.Load()
	for _, request := range []*http.Request{
		headerRequest(http.MethodGet, "/api/v1/owner/export?unexpected=1", fixture.token, nil),
		headerRequest(http.MethodGet, "/api/v1/owner/export", fixture.token, strings.NewReader("body")),
	} {
		response := performRequest(engine, request)
		if response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("strict input response = %d %s", response.Code, response.Body.String())
		}
	}
	malformed := httptest.NewRequest(http.MethodGet, "/api/v1/owner/export", nil)
	malformed.AddCookie(cookie)
	malformed.Header.Set("Authorization", "Bearer wrong")
	if response := performRequest(engine, malformed); response.Code != http.StatusUnauthorized {
		t.Fatalf("malformed bearer fell back to cookie: %d", response.Code)
	}
	if calls.Load() != before {
		t.Fatalf("invalid requests invoked exporter: before=%d after=%d", before, calls.Load())
	}
}

func TestExportPreWriteErrorsAndPreparationTimeoutRemainJSON(t *testing.T) {
	for _, test := range []struct {
		name     string
		exporter ArchiveExporter
		budget   time.Duration
		status   int
		code     string
	}{
		{
			name: "domain error", budget: time.Second, status: http.StatusInternalServerError, code: string(domain.CodeExportInvalid),
			exporter: archiveExporterFunc(func(context.Context, io.Writer) (archive.Summary, error) {
				return archive.Summary{}, domain.NewError(domain.CodeExportInvalid, "invalid authority", nil)
			}),
		},
		{
			name: "preparation timeout", budget: 20 * time.Millisecond, status: http.StatusGatewayTimeout, code: "EXPORT_PREPARATION_TIMEOUT",
			exporter: archiveExporterFunc(func(ctx context.Context, _ io.Writer) (archive.Summary, error) {
				<-ctx.Done()
				return archive.Summary{}, context.Cause(ctx)
			}),
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			engine := gin.New()
			engine.GET("/export", streamExportWithBudgets(test.exporter, test.budget, time.Second))
			response := performRequest(engine, httptest.NewRequest(http.MethodGet, "/export", nil))
			if response.Code != test.status || !strings.HasPrefix(response.Header().Get("Content-Type"), "application/json") {
				t.Fatalf("response = %d %#v %s", response.Code, response.Header(), response.Body.String())
			}
			if failure := decodeError(t, response.Body); failure.Error.Code != test.code {
				t.Fatalf("failure = %#v", failure.Error)
			}
		})
	}
}

func TestExportActiveStreamOutlivesGlobalWriteTimeout(t *testing.T) {
	payload := bytes.Repeat([]byte("x"), 8)
	exporter := archiveExporterFunc(func(ctx context.Context, writer io.Writer) (archive.Summary, error) {
		for _, value := range payload {
			if _, err := writer.Write([]byte{value}); err != nil {
				return archive.Summary{}, err
			}
			select {
			case <-ctx.Done():
				return archive.Summary{}, ctx.Err()
			case <-time.After(10 * time.Millisecond):
			}
		}
		return archive.Summary{FormatVersion: 1}, nil
	})
	engine := gin.New()
	engine.Use(recoveryMiddleware(func(observability.Surface, string, string, map[string]any) {}))
	engine.GET("/export", streamExportWithBudgets(exporter, 50*time.Millisecond, 40*time.Millisecond))
	server := httptest.NewUnstartedServer(engine)
	server.Config.WriteTimeout = 25 * time.Millisecond
	server.Start()
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/export")
	if err != nil {
		t.Fatal(err)
	}
	body, err := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if err != nil || !bytes.Equal(body, payload) {
		t.Fatalf("active stream body=%q err=%v", body, err)
	}
}

func TestExportPostFirstByteFailureAbortsTruncatedResponse(t *testing.T) {
	exporter := archiveExporterFunc(func(_ context.Context, writer io.Writer) (archive.Summary, error) {
		if _, err := writer.Write([]byte("partial-zip")); err != nil {
			return archive.Summary{}, err
		}
		return archive.Summary{}, errors.New("late archive failure")
	})
	engine := gin.New()
	engine.Use(recoveryMiddleware(func(observability.Surface, string, string, map[string]any) {}))
	engine.GET("/export", streamExportWithBudgets(exporter, time.Second, time.Second))
	server := httptest.NewServer(engine)
	defer server.Close()
	response, err := server.Client().Get(server.URL + "/export")
	if err != nil {
		t.Fatal(err)
	}
	body, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if response.StatusCode != http.StatusOK || !bytes.Equal(body, []byte("partial-zip")) || readErr == nil {
		t.Fatalf("late failure status=%d body=%q err=%v", response.StatusCode, body, readErr)
	}
}

func TestExportBlockedClientHitsWriteIdleDeadlineAndRequestCancelPropagates(t *testing.T) {
	blocked := make(chan error, 1)
	chunk := bytes.Repeat([]byte("z"), 1<<20)
	exporter := archiveExporterFunc(func(_ context.Context, writer io.Writer) (archive.Summary, error) {
		for index := 0; index < 256; index++ {
			if _, err := writer.Write(chunk); err != nil {
				blocked <- err
				return archive.Summary{}, err
			}
		}
		blocked <- errors.New("socket never blocked")
		return archive.Summary{}, nil
	})
	engine := gin.New()
	engine.Use(recoveryMiddleware(func(observability.Surface, string, string, map[string]any) {}))
	engine.GET("/export", streamExportWithBudgets(exporter, time.Second, 25*time.Millisecond))
	server := httptest.NewUnstartedServer(engine)
	server.Start()
	defer server.Close()

	connection, err := net.Dial("tcp", server.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = connection.Close() }()
	if _, err := io.WriteString(connection, "GET /export HTTP/1.1\r\nHost: test\r\nConnection: close\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-blocked:
		if err == nil || strings.Contains(err.Error(), "never blocked") {
			t.Fatalf("blocked write result = %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("blocked client did not trigger write idle deadline")
	}

	cancelObserved := make(chan error, 1)
	cancelExporter := archiveExporterFunc(func(ctx context.Context, writer io.Writer) (archive.Summary, error) {
		if _, err := writer.Write([]byte("started")); err != nil {
			return archive.Summary{}, err
		}
		<-ctx.Done()
		cancelObserved <- ctx.Err()
		return archive.Summary{}, ctx.Err()
	})
	cancelEngine := gin.New()
	cancelEngine.Use(recoveryMiddleware(func(observability.Surface, string, string, map[string]any) {}))
	cancelEngine.GET("/export", streamExportWithBudgets(cancelExporter, time.Second, time.Second))
	cancelServer := httptest.NewServer(cancelEngine)
	defer cancelServer.Close()
	ctx, cancel := context.WithCancel(context.Background())
	request, _ := http.NewRequestWithContext(ctx, http.MethodGet, cancelServer.URL+"/export", http.NoBody)
	response, err := cancelServer.Client().Do(request)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	_ = response.Body.Close()
	select {
	case err := <-cancelObserved:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("request cancellation = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not reach exporter")
	}
}

func headerRequest(method, target, token string, body io.Reader) *http.Request {
	request := httptest.NewRequest(method, target, body)
	request.Header.Set("Authorization", "Bearer "+token)
	return request
}
