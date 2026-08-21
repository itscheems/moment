package app

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/linkfetch"
)

type stubThumbnailStore struct {
	calls int
	err   error
}

func (store *stubThumbnailStore) Ingest(_ context.Context, _ string, input io.Reader) (domain.ID, string, uint32, uint32, error) {
	store.calls++
	if _, err := io.ReadAll(input); err != nil {
		return "", "", 0, 0, err
	}
	if store.err != nil {
		return "", "", 0, 0, store.err
	}
	id, _ := domain.ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	return id, "image/png", 64, 48, nil
}

func pngBytes(t *testing.T) []byte {
	t.Helper()
	canvas := image.NewRGBA(image.Rect(0, 0, 4, 4))
	canvas.Set(0, 0, color.RGBA{R: 255, A: 255})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, canvas); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func newLinkPreviewFixture(t *testing.T, handler http.HandlerFunc, store LinkThumbnailStore) (*LinkPreviewService, *httptest.Server) {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	resolver := linkfetch.NewResolver(linkfetch.ResolverOptions{
		Fetcher: linkfetch.New(linkfetch.Options{AllowPrivateAddresses: true, TotalTimeout: 5 * time.Second}),
	})
	service, err := NewLinkPreviewService(resolver, store, LinkPreviewOptions{
		Clock: func() time.Time { return time.UnixMilli(1_700_000_000_000) },
	})
	if err != nil {
		t.Fatal(err)
	}
	return service, server
}

func TestLinkPreviewCapturesThumbnailAsManagedMedia(t *testing.T) {
	image := pngBytes(t)
	var server *httptest.Server
	store := &stubThumbnailStore{}
	handler := func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/thumb.png" {
			writer.Header().Set("Content-Type", "image/png")
			_, _ = writer.Write(image)
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(`<html><head>
<meta property="og:title" content="A page">
<meta property="og:site_name" content="Example">
<meta property="og:image" content="` + server.URL + `/thumb.png">
</head></html>`))
	}
	service, srv := newLinkPreviewFixture(t, handler, store)
	server = srv

	preview, err := service.Resolve(context.Background(), server.URL+"/a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preview.Card.ThumbnailMediaID != "01ARZ3NDEKTSV4RRFFQ69G5FAV" {
		t.Fatalf("expected a managed thumbnail, got %q", preview.Card.ThumbnailMediaID)
	}
	if preview.ThumbnailWidth != 64 || preview.ThumbnailHeight != 48 {
		t.Fatalf("unexpected thumbnail geometry: %dx%d", preview.ThumbnailWidth, preview.ThumbnailHeight)
	}
	if preview.Card.Title != "A page" || preview.Card.SiteName != "Example" {
		t.Fatalf("unexpected card text: %#v", preview.Card)
	}
	if err := preview.Card.Validate(); err != nil {
		t.Fatalf("resolved card must satisfy the domain contract: %v", err)
	}
}

func TestLinkPreviewDegradesToTextWhenThumbnailIsRejected(t *testing.T) {
	// An unsupported remote image gets no exemption; the card simply loses its
	// picture instead of failing the whole preview.
	var server *httptest.Server
	store := &stubThumbnailStore{err: domain.NewError(domain.CodeMediaTypeUnsupported, "svg is not supported", nil)}
	handler := func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/thumb.svg" {
			writer.Header().Set("Content-Type", "image/svg+xml")
			_, _ = writer.Write([]byte("<svg xmlns='http://www.w3.org/2000/svg'/>"))
			return
		}
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(`<html><head><meta property="og:title" content="Vector"><meta property="og:image" content="` + server.URL + `/thumb.svg"></head></html>`))
	}
	service, srv := newLinkPreviewFixture(t, handler, store)
	server = srv

	preview, err := service.Resolve(context.Background(), server.URL+"/a")
	if err != nil {
		t.Fatalf("a rejected thumbnail must not fail the preview: %v", err)
	}
	if preview.Card.ThumbnailMediaID != "" {
		t.Fatal("expected no managed thumbnail")
	}
	if preview.Card.Title != "Vector" {
		t.Fatalf("expected the text card to survive, got %#v", preview.Card)
	}
}

func TestLinkPreviewFoldsOriginFailureIntoOneStableCode(t *testing.T) {
	service, server := newLinkPreviewFixture(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusTeapot)
	}, &stubThumbnailStore{})

	_, err := service.Resolve(context.Background(), server.URL+"/a")
	var domainError *domain.Error
	if !errors.As(err, &domainError) || domainError.Code != domain.CodeLinkPreviewUnavailable {
		t.Fatalf("expected LINK_PREVIEW_UNAVAILABLE, got %v", err)
	}
	if domainError.Details != nil {
		t.Fatal("origin failure details must not leak to the client")
	}
}

func TestLinkPreviewRejectsUnsafeURLBeforeAnyRequest(t *testing.T) {
	service, _ := newLinkPreviewFixture(t, func(writer http.ResponseWriter, _ *http.Request) {
		t.Error("no request should be made for an invalid URL")
	}, &stubThumbnailStore{})

	for _, raw := range []string{"", "file:///etc/passwd", "https://user:pw@example.com/"} {
		if _, err := service.Resolve(context.Background(), raw); err == nil {
			t.Fatalf("expected %q to be refused", raw)
		}
	}
}

func TestLinkPreviewWithoutThumbnailStoreStillProducesCard(t *testing.T) {
	service, server := newLinkPreviewFixture(t, func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "text/html")
		_, _ = writer.Write([]byte(`<html><head><meta property="og:title" content="No processors"></head></html>`))
	}, nil)

	preview, err := service.Resolve(context.Background(), server.URL+"/a")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if preview.Card.Title != "No processors" || preview.Card.ThumbnailMediaID != "" {
		t.Fatalf("unexpected card: %#v", preview.Card)
	}
}

func TestDerivedSearchTextMakesAWordlessPostFindable(t *testing.T) {
	// A Moment whose body is only a link still has to be recallable months
	// later by the video's own title.
	cards := []domain.LinkCard{{
		CanonicalURL: "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
		Title:        "Never Gonna Give You Up",
		SiteName:     "YouTube",
		AuthorName:   "Rick Astley",
	}}
	text, err := DerivedSearchText("https://www.youtube.com/watch?v=dQw4w9WgXcQ", cards)
	if err != nil {
		t.Fatal(err)
	}
	for _, term := range []string{"never gonna give you up", "youtube", "rick astley", "youtube.com"} {
		if !strings.Contains(text, term) {
			t.Fatalf("expected %q in derived text %q", term, text)
		}
	}
}

func TestDerivedSearchTextIsUnchangedWithoutCards(t *testing.T) {
	// The historical contract must hold for every existing entry so a rebuild
	// does not rewrite text it did not need to.
	withoutCards, err := DerivedSearchText("一段普通正文", nil)
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := domain.NormalizeContentV2("一段普通正文")
	if err != nil {
		t.Fatal(err)
	}
	if withoutCards != baseline {
		t.Fatalf("card-free derived text drifted: %q vs %q", withoutCards, baseline)
	}
}
