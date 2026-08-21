package ginhttp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestContentSecurityPolicyOpensOnlyAllowlistedFrameSources(t *testing.T) {
	policy := contentSecurityPolicy
	if !strings.Contains(policy, "frame-src 'self' https://player.bilibili.com https://www.youtube-nocookie.com") {
		t.Fatalf("frame-src must list exactly the allowlisted embed origins: %q", policy)
	}
	// The rest of the posture must not have loosened. In particular a card
	// thumbnail is served from managed media, so remote images stay refused.
	for _, directive := range []string{
		"default-src 'self'",
		"img-src 'self' data: blob:",
		"script-src 'self'",
		"connect-src 'self'",
		"object-src 'none'",
		"frame-ancestors 'none'",
	} {
		if !strings.Contains(policy, directive) {
			t.Fatalf("policy lost %q: %q", directive, policy)
		}
	}
	if strings.Contains(policy, "https://www.youtube.com") {
		t.Fatalf("only the privacy-enhanced YouTube origin may be framed: %q", policy)
	}
}

func TestSecurityHeadersEmitTheGeneratedPolicy(t *testing.T) {
	engine, err := New(Config{Assets: testAssets()})
	if err != nil {
		t.Fatal(err)
	}
	response := performRequest(engine, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if got := response.Header().Get("Content-Security-Policy"); got != contentSecurityPolicy {
		t.Fatalf("unexpected policy header: %q", got)
	}
}
