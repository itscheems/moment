package cli

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestEmailVerificationCommandsUseAnonymousAPI(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "" {
			t.Error("verification forwarded an ambient credential")
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Error(err)
		}
		w.Header().Set("Content-Type", "application/json")
		if r.URL.Path == "/api/v1/register/email" {
			if payload["email"] != "reader@example.test" {
				t.Error("wrong address")
			}
			_, _ = w.Write([]byte(`{"data":{"challenge_id":"challenge","expires_at":"2999-01-01T00:00:00Z","resend_at":"2999-01-01T00:00:00Z"},"meta":{"request_id":"req_email_verification"}}`))
			return
		}
		if r.URL.Path != "/api/v1/register/email/verify" || payload["challenge_id"] != "challenge" || payload["code"] != "123456" {
			t.Error("wrong verification request")
		}
		_, _ = w.Write([]byte(`{"data":{"verification_token":"private-proof","email":"reader@example.test","expires_at":"2999-01-01T00:00:00Z"},"meta":{"request_id":"req_email_verification"}}`))
	}))
	defer server.Close()
	t.Setenv("MOMENT_TOKEN", "ambient-must-not-be-forwarded")
	app := momentTestApplication()
	requested := execute(app, "", "auth", "request-email-code", "--url", server.URL, "--email", "reader@example.test", "--json")
	if requested.stderr != "" || commandData(t, requested)["challenge_id"] != "challenge" {
		t.Fatalf("request=%#v", requested)
	}
	verified := execute(app, "123456\n", "auth", "verify-email-code", "--url", server.URL, "--challenge", "challenge", "--code-file", "-", "--json")
	if verified.stderr != "" || commandData(t, verified)["verification_token"] != "private-proof" || strings.Contains(verified.stdout, "123456") {
		t.Fatalf("verification=%#v", verified)
	}
	missing := execute(app, "", "auth", "request-email-code", "--url", server.URL, "--json")
	if missing.exit != ExitInvalidInput || calls != 2 {
		t.Fatalf("invalid request performed IO: %#v calls=%d", missing, calls)
	}
	direct := execute(app, "", "auth", "request-email-code", "--email", "reader@example.test", "--json")
	if direct.exit != ExitInvalidInput || decodeOneDocument(t, direct.stdout)["error"].(map[string]any)["code"] != "MODE_UNSUPPORTED" {
		t.Fatalf("direct verification=%#v", direct)
	}
}

func TestEmailVerificationRequestHonorsCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
			return
		case <-time.After(100 * time.Millisecond):
			w.WriteHeader(http.StatusServiceUnavailable)
		}
	}))
	defer server.Close()
	result := execute(momentTestApplication(), "", "auth", "request-email-code", "--url", server.URL, "--email", "reader@example.test", "--timeout", "10ms", "--json")
	if result.exit != ExitTimeout || result.stderr != "" {
		t.Fatalf("cancelled request=%#v", result)
	}
}
