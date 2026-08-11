package ginhttp

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/observability"
)

func testAssets() fstest.MapFS {
	return fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte("<!doctype html><title>Moment shell</title>")},
		"assets/app.js": &fstest.MapFile{Data: []byte("console.log('moment')")},
	}
}

func performRequest(engine http.Handler, request *http.Request) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	engine.ServeHTTP(recorder, request)
	return recorder
}

func decodeError(t *testing.T, body io.Reader) errorEnvelope {
	t.Helper()
	var envelope errorEnvelope
	if err := json.NewDecoder(body).Decode(&envelope); err != nil {
		t.Fatalf("decode error envelope: %v", err)
	}
	return envelope
}

func TestHealthReadinessHeadersAndRequestIDs(t *testing.T) {
	engine, err := New(Config{
		Assets:    testAssets(),
		RequestID: func() string { return "req_health" },
	})
	if err != nil {
		t.Fatal(err)
	}

	health := performRequest(engine, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if health.Code != http.StatusOK {
		t.Fatalf("health status = %d, body = %s", health.Code, health.Body.String())
	}
	for header, want := range map[string]string{
		"X-Request-ID":           "req_health",
		"X-Content-Type-Options": "nosniff",
		"X-Frame-Options":        "DENY",
		"Referrer-Policy":        "same-origin",
		"Permissions-Policy":     "camera=(), microphone=(), geolocation=()",
	} {
		if got := health.Header().Get(header); got != want {
			t.Errorf("%s = %q, want %q", header, got, want)
		}
	}
	csp := health.Header().Get("Content-Security-Policy")
	for _, directive := range []string{
		"default-src 'self'", "img-src 'self' data:", "script-src 'self'", "connect-src 'self'",
		"object-src 'none'", "base-uri 'none'", "frame-ancestors 'none'", "form-action 'self'",
	} {
		if !strings.Contains(csp, directive) {
			t.Errorf("Content-Security-Policy missing %q: %q", directive, csp)
		}
	}

	ready := performRequest(engine, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusServiceUnavailable {
		t.Fatalf("ready status = %d, body = %s", ready.Code, ready.Body.String())
	}
	failure := decodeError(t, ready.Body)
	if failure.Error.Code != "NOT_READY" || failure.Error.RequestID != "req_health" {
		t.Fatalf("ready error = %#v", failure.Error)
	}

	readyEngine, err := New(Config{
		Assets:    testAssets(),
		RequestID: func() string { return "req_ready" },
		Readiness: func(context.Context) error { return nil },
	})
	if err != nil {
		t.Fatal(err)
	}
	ready = performRequest(readyEngine, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	if ready.Code != http.StatusOK || !strings.Contains(ready.Body.String(), `"status":"ready"`) {
		t.Fatalf("ready status = %d, body = %s", ready.Code, ready.Body.String())
	}
}

func TestPanicRecoveryAndAccessLogsDoNotLeakRequestData(t *testing.T) {
	var logOutput bytes.Buffer
	logger, err := observability.New(observability.Config{
		Level:  "debug",
		Format: "json",
		Writer: &logOutput,
		Now:    func() time.Time { return time.Unix(0, 0) },
	})
	if err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{
		Assets:    testAssets(),
		RequestID: func() string { return "req_panic" },
		Log: func(level, event string, fields map[string]any) {
			logger.Event(level, event, fields)
		},
		Register: func(engine *gin.Engine) {
			engine.POST("/api/v1/posts/:id", func(*gin.Context) {
				panic("post body private-panic-value")
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/posts/secret-id?q=complete-search-term", strings.NewReader("private post body"))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer owner-token")
	request.Header.Set("Cookie", "moment_session=session-secret")
	response := performRequest(engine, request)
	if response.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}
	failure := decodeError(t, response.Body)
	if failure.Error.Code != "INTERNAL" || failure.Error.RequestID != "req_panic" {
		t.Fatalf("error = %#v", failure.Error)
	}

	logs := logOutput.String()
	for _, secret := range []string{"private-panic-value", "private post body", "owner-token", "session-secret", "complete-search-term", "secret-id"} {
		if strings.Contains(logs, secret) {
			t.Fatalf("log leaked %q: %s", secret, logs)
		}
	}
	lines := strings.Split(strings.TrimSpace(logs), "\n")
	if len(lines) != 2 {
		t.Fatalf("log lines = %d, logs = %q", len(lines), logs)
	}
	for _, line := range lines {
		var event map[string]any
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			t.Fatalf("decode structured log: %v", err)
		}
		if event["request_id"] != "req_panic" || event["route"] != "/api/v1/posts/:id" {
			t.Fatalf("event = %#v", event)
		}
	}
}

func TestBodyLimitsRejectKnownAndStreamingOverflows(t *testing.T) {
	var handlerCalls int
	engine, err := New(Config{
		Assets: testAssets(),
		Limits: BodyLimits{JSON: 4, Multipart: 8, Archive: 12, Default: 3},
		Register: func(engine *gin.Engine) {
			readHandler := func(ctx *gin.Context) {
				handlerCalls++
				body, ok := readBody(ctx)
				if !ok {
					return
				}
				Success(ctx, http.StatusOK, map[string]int{"bytes": len(body)}, nil)
			}
			engine.POST("/api/v1/read", readHandler)
			engine.POST("/api/v1/owner/import/inspect", readHandler)
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	known := httptest.NewRequest(http.MethodPost, "/api/v1/read", strings.NewReader("12345"))
	known.Header.Set("Content-Type", "application/json")
	knownResponse := performRequest(engine, known)
	if knownResponse.Code != http.StatusRequestEntityTooLarge || handlerCalls != 0 {
		t.Fatalf("known status = %d, calls = %d, body = %s", knownResponse.Code, handlerCalls, knownResponse.Body.String())
	}
	if decodeError(t, knownResponse.Body).Error.Code != "REQUEST_BODY_TOO_LARGE" {
		t.Fatal("known-length overflow did not use stable error")
	}

	streamed := httptest.NewRequest(http.MethodPost, "/api/v1/read", strings.NewReader("12345"))
	streamed.Header.Set("Content-Type", "application/json")
	streamed.ContentLength = -1
	streamedResponse := performRequest(engine, streamed)
	if streamedResponse.Code != http.StatusRequestEntityTooLarge || handlerCalls != 1 {
		t.Fatalf("streamed status = %d, calls = %d, body = %s", streamedResponse.Code, handlerCalls, streamedResponse.Body.String())
	}

	for _, test := range []struct {
		contentType string
		size        int
	}{
		{contentType: "multipart/form-data; boundary=moment", size: 9},
		{contentType: "application/zip", size: 13},
		{contentType: "application/octet-stream", size: 4},
	} {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/read", strings.NewReader(strings.Repeat("x", test.size)))
		request.Header.Set("Content-Type", test.contentType)
		response := performRequest(engine, request)
		if response.Code != http.StatusRequestEntityTooLarge {
			t.Fatalf("content type %s status = %d", test.contentType, response.Code)
		}
	}

	routeAwareArchive := httptest.NewRequest(http.MethodPost, "/api/v1/owner/import/inspect", strings.NewReader("1234567890"))
	routeAwareArchive.Header.Set("Content-Type", "application/octet-stream")
	routeAwareResponse := performRequest(engine, routeAwareArchive)
	if routeAwareResponse.Code != http.StatusOK {
		t.Fatalf("route-aware archive status = %d, body = %s", routeAwareResponse.Code, routeAwareResponse.Body.String())
	}
}

func TestAPINotFoundAndMethodErrorsTakePrecedenceOverSPA(t *testing.T) {
	engine, err := New(Config{
		Assets: testAssets(),
		Register: func(engine *gin.Engine) {
			engine.GET("/api/v1/probe", func(ctx *gin.Context) {
				Success(ctx, http.StatusOK, map[string]bool{"ok": true}, nil)
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	method := performRequest(engine, httptest.NewRequest(http.MethodPost, "/api/v1/probe", nil))
	if method.Code != http.StatusMethodNotAllowed || decodeError(t, method.Body).Error.Code != "METHOD_NOT_ALLOWED" {
		t.Fatalf("method status = %d", method.Code)
	}
	missingAPI := performRequest(engine, httptest.NewRequest(http.MethodGet, "/api/v1/missing", nil))
	if missingAPI.Code != http.StatusNotFound || decodeError(t, missingAPI.Body).Error.Code != "API_ROUTE_NOT_FOUND" {
		t.Fatalf("missing API status = %d", missingAPI.Code)
	}
	spa := performRequest(engine, httptest.NewRequest(http.MethodGet, "/m/01ABC", nil))
	if spa.Code != http.StatusOK || !strings.Contains(spa.Body.String(), "Moment shell") {
		t.Fatalf("SPA status = %d, body = %s", spa.Code, spa.Body.String())
	}
	asset := performRequest(engine, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if asset.Code != http.StatusOK || !strings.Contains(asset.Body.String(), "console.log") {
		t.Fatalf("asset status = %d, body = %s", asset.Code, asset.Body.String())
	}
	missingAsset := performRequest(engine, httptest.NewRequest(http.MethodGet, "/assets/missing.js", nil))
	if missingAsset.Code != http.StatusNotFound || decodeError(t, missingAsset.Body).Error.Code != "ASSET_NOT_FOUND" {
		t.Fatalf("missing asset status = %d", missingAsset.Code)
	}
}

func TestForwardedClientIPRequiresExplicitTrustedProxy(t *testing.T) {
	build := func(proxies []string) *gin.Engine {
		engine, err := New(Config{
			Assets:         testAssets(),
			TrustedProxies: proxies,
			Register: func(engine *gin.Engine) {
				engine.GET("/api/v1/ip", func(ctx *gin.Context) {
					Success(ctx, http.StatusOK, map[string]string{"client_ip": ctx.ClientIP()}, nil)
				})
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return engine
	}
	request := func() *http.Request {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/ip", nil)
		request.RemoteAddr = "192.0.2.10:4321"
		request.Header.Set("X-Forwarded-For", "198.51.100.8")
		return request
	}

	untrusted := performRequest(build(nil), request())
	if !strings.Contains(untrusted.Body.String(), `"client_ip":"192.0.2.10"`) {
		t.Fatalf("untrusted body = %s", untrusted.Body.String())
	}
	trusted := performRequest(build([]string{"192.0.2.0/24"}), request())
	if !strings.Contains(trusted.Body.String(), `"client_ip":"198.51.100.8"`) {
		t.Fatalf("trusted body = %s", trusted.Body.String())
	}
}

func TestInvalidTrustedProxyConfigurationIsRejected(t *testing.T) {
	if _, err := New(Config{Assets: testAssets(), TrustedProxies: []string{"not-an-ip-or-cidr"}}); err == nil {
		t.Fatal("expected invalid trusted proxy error")
	}
}

func TestRequestContextCancellationReachesHandler(t *testing.T) {
	observed := make(chan struct{}, 1)
	engine, err := New(Config{
		Assets: testAssets(),
		Register: func(engine *gin.Engine) {
			engine.GET("/api/v1/wait", func(ctx *gin.Context) {
				<-ctx.Request.Context().Done()
				observed <- struct{}{}
			})
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	requestContext, cancel := context.WithCancel(context.Background())
	request := httptest.NewRequest(http.MethodGet, "/api/v1/wait", nil).WithContext(requestContext)
	done := make(chan struct{})
	go func() {
		performRequest(engine, request)
		close(done)
	}()
	cancel()
	select {
	case <-observed:
	case <-time.After(time.Second):
		t.Fatal("request cancellation did not reach handler")
	}
	<-done
}
