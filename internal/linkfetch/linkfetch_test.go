package linkfetch

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
)

func loopbackFetcher() *Fetcher {
	return New(Options{AllowPrivateAddresses: true, TotalTimeout: 5 * time.Second})
}

func TestFetcherRefusesNonPublicDestinations(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte("<html><head><title>private</title></head></html>"))
	}))
	defer server.Close()

	// A production fetcher keeps its address filter on, so the loopback test
	// server is refused even though the URL itself is well formed.
	guarded := New(Options{TotalTimeout: 3 * time.Second})
	_, err := guarded.Get(context.Background(), server.URL, []string{"text/html"}, DefaultMaxDocumentBytes)
	if !errors.Is(err, ErrBlockedAddress) {
		t.Fatalf("expected a blocked address, got %v", err)
	}
}

func TestFetcherRejectsNonHTTPSchemes(t *testing.T) {
	fetcher := loopbackFetcher()
	for _, raw := range []string{"file:///etc/passwd", "gopher://example.com", "ftp://example.com/x"} {
		if _, err := fetcher.Get(context.Background(), raw, nil, 0); err == nil {
			t.Fatalf("expected %q to be rejected", raw)
		}
	}
}

func TestFetcherRevalidatesEveryRedirectHop(t *testing.T) {
	blocked := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer blocked.Close()

	var redirector *httptest.Server
	redirector = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/to-scheme":
			http.Redirect(writer, request, "file:///etc/passwd", http.StatusFound)
		case "/loop":
			http.Redirect(writer, request, redirector.URL+"/loop", http.StatusFound)
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer redirector.Close()

	fetcher := loopbackFetcher()
	if _, err := fetcher.Get(context.Background(), redirector.URL+"/to-scheme", nil, 0); err == nil {
		t.Fatal("expected a redirect to a non-http scheme to be refused")
	}
	if _, err := fetcher.Get(context.Background(), redirector.URL+"/loop", nil, 0); err == nil {
		t.Fatal("expected a redirect loop to hit the hop limit")
	}
}

func TestFetcherEnforcesContentTypeAndBodyCeiling(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/pdf":
			writer.Header().Set("Content-Type", "application/pdf")
			_, _ = writer.Write([]byte("%PDF-1.4"))
		case "/big":
			writer.Header().Set("Content-Type", "text/html; charset=utf-8")
			_, _ = writer.Write([]byte(strings.Repeat("a", 4096)))
		default:
			writer.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	fetcher := loopbackFetcher()
	if _, err := fetcher.Get(context.Background(), server.URL+"/pdf", []string{"text/html"}, 0); err == nil {
		t.Fatal("expected an unaccepted content type to be refused")
	}
	if _, err := fetcher.Get(context.Background(), server.URL+"/big", []string{"text/html"}, 1024); err == nil {
		t.Fatal("expected an oversized body to be refused")
	}
	response, err := fetcher.Get(context.Background(), server.URL+"/big", []string{"text/html"}, 8192)
	if err != nil {
		t.Fatalf("expected a bounded body to be accepted: %v", err)
	}
	if response.ContentType != "text/html" {
		t.Fatalf("expected the media type without parameters, got %q", response.ContentType)
	}
}

func TestFetcherRefusesNon2xxStatus(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()
	if _, err := loopbackFetcher().Get(context.Background(), server.URL, nil, 0); err == nil {
		t.Fatal("expected a 500 response to be refused")
	}
}

func TestIsPublicAddressRejectsMetadataAndPrivateRanges(t *testing.T) {
	for _, raw := range []string{
		"127.0.0.1", "10.0.0.5", "172.16.0.1", "192.168.1.1", "169.254.169.254",
		"0.0.0.0", "100.64.0.1", "::1", "fd00::1", "fe80::1", "224.0.0.1",
	} {
		if isPublicAddress(mustIP(raw)) {
			t.Fatalf("expected %s to be refused", raw)
		}
	}
	for _, raw := range []string{"1.1.1.1", "142.250.72.14", "2001:4860:4860::8888"} {
		if !isPublicAddress(mustIP(raw)) {
			t.Fatalf("expected %s to be allowed", raw)
		}
	}
}

func TestRecognizeYouTubeURLForms(t *testing.T) {
	providers := DefaultProviders()
	for _, raw := range []string{
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		"https://www.youtube.com/watch?v=dQw4w9WgXcQ&list=PL1&t=42",
		"https://youtu.be/dQw4w9WgXcQ",
		"https://www.youtube.com/shorts/dQw4w9WgXcQ",
		"https://www.youtube.com/live/dQw4w9WgXcQ",
		"https://m.youtube.com/watch?v=dQw4w9WgXcQ",
	} {
		recognition, ok := Recognize(providers, raw)
		if !ok {
			t.Fatalf("expected %q to be recognized", raw)
		}
		if recognition.CanonicalURL != "https://www.youtube.com/watch?v=dQw4w9WgXcQ" {
			t.Fatalf("%q: unexpected canonical %q", raw, recognition.CanonicalURL)
		}
		if recognition.EmbedURL != "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ" {
			t.Fatalf("%q: unexpected embed %q", raw, recognition.EmbedURL)
		}
		if !IsEmbeddable(recognition.EmbedURL) {
			t.Fatalf("%q: expected an allowlisted embed origin", raw)
		}
	}
	if _, ok := Recognize(providers, "https://www.youtube.com/watch?v=short"); ok {
		t.Fatal("expected an invalid video id to be refused")
	}
}

func TestRecognizeBilibiliURLForms(t *testing.T) {
	providers := DefaultProviders()
	recognition, ok := Recognize(providers, "https://www.bilibili.com/video/BV1B7411m7LV?p=2&spm=abc")
	if !ok {
		t.Fatal("expected a Bilibili video to be recognized")
	}
	if recognition.CanonicalURL != "https://www.bilibili.com/video/BV1B7411m7LV" {
		t.Fatalf("unexpected canonical %q", recognition.CanonicalURL)
	}
	if !strings.Contains(recognition.EmbedURL, "bvid=BV1B7411m7LV") || !strings.Contains(recognition.EmbedURL, "&p=2") {
		t.Fatalf("unexpected embed %q", recognition.EmbedURL)
	}
	// b23.tv deliberately has no recognizer: it expands during the guarded
	// redirect walk instead.
	if _, ok := Recognize(providers, "https://b23.tv/BV1B7411m7LV"); ok {
		t.Fatal("expected the short-link host to be left to the fetcher")
	}
}

func TestIsEmbeddableRejectsUnlistedOrigin(t *testing.T) {
	if IsEmbeddable("https://evil.example.com/embed/x") {
		t.Fatal("expected an unlisted origin to be refused")
	}
	if IsEmbeddable("https://www.youtube.com/embed/x") {
		t.Fatal("expected the non-privacy-enhanced origin to be refused")
	}
	if !IsEmbeddable("https://www.youtube-nocookie.com/embed/x") {
		t.Fatal("expected the allowlisted origin to be accepted")
	}
}

func TestResolveBuildsVideoCardFromOpenGraph(t *testing.T) {
	var server *httptest.Server
	server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = fmt.Fprintf(writer, `<html><head>
<link rel="canonical" href="https://www.youtube.com/watch?v=dQw4w9WgXcQ">
<meta property="og:title" content="  A   captured title  ">
<meta property="og:description" content="A description">
<meta property="og:site_name" content="YouTube">
<meta property="og:type" content="video.other">
<meta property="og:image" content="%s/thumb.jpg">
<meta property="og:video:duration" content="212">
</head><body><p>ignored body text</p></body></html>`, server.URL)
	}))
	defer server.Close()

	// Resolve is pointed at the local server, not at youtube.com, so the test
	// never reaches the public internet. The recognizer still fires because the
	// fetched document declares a YouTube canonical URL -- the same path a
	// short-link redirect takes.
	resolver := NewResolver(ResolverOptions{Fetcher: loopbackFetcher()})
	preview, err := resolver.Resolve(context.Background(), server.URL+"/watch")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preview.Provider != domain.LinkProviderYouTube || preview.Kind != domain.LinkKindVideo {
		t.Fatalf("unexpected provider/kind: %v/%v", preview.Provider, preview.Kind)
	}
	if preview.Title != "A captured title" {
		t.Fatalf("expected collapsed whitespace, got %q", preview.Title)
	}
	if preview.EmbedURL != "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ" {
		t.Fatalf("unexpected embed %q", preview.EmbedURL)
	}
	if preview.DurationSeconds == nil || *preview.DurationSeconds != 212 {
		t.Fatalf("expected a parsed duration, got %v", preview.DurationSeconds)
	}
	if !strings.HasSuffix(preview.ImageURL, "/thumb.jpg") {
		t.Fatalf("unexpected image %q", preview.ImageURL)
	}
}

func TestResolveKeepsUnrecognizedPageAsStaticCard(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(`<html><head>
<meta property="og:title" content="A blog post">
<meta property="og:type" content="video.other">
<meta name="twitter:player" content="https://evil.example.com/player">
</head></html>`))
	}))
	defer server.Close()

	resolver := NewResolver(ResolverOptions{Fetcher: loopbackFetcher()})
	preview, err := resolver.Resolve(context.Background(), server.URL+"/post")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preview.Provider != domain.LinkProviderGeneric {
		t.Fatalf("expected the generic provider, got %v", preview.Provider)
	}
	// A page may declare itself a video and even advertise a player, but only an
	// allowlisted recognizer can produce an embed.
	if preview.Kind != domain.LinkKindLink || preview.EmbedURL != "" {
		t.Fatalf("expected a static card, got kind=%v embed=%q", preview.Kind, preview.EmbedURL)
	}
	if preview.SiteName == "" {
		t.Fatal("expected a host label to stand in for a missing site name")
	}
}

func TestResolveCachesByCanonicalSource(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		requests++
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(`<html><head><meta property="og:title" content="cached"></head></html>`))
	}))
	defer server.Close()

	resolver := NewResolver(ResolverOptions{Fetcher: loopbackFetcher()})
	for range 3 {
		if _, err := resolver.Resolve(context.Background(), server.URL+"/a"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	}
	if requests != 1 {
		t.Fatalf("expected one origin request, got %d", requests)
	}
}

func mustIP(raw string) net.IP { return net.ParseIP(raw) }
