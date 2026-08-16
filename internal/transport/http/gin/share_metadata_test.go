package ginhttp

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

const shareHTTPPostID = "01K2ABCDEF0123456789ABCDE0"

type shareHTTPSite struct {
	settings app.SiteSettings
	err      error
}

func (site shareHTTPSite) Get(context.Context) (app.SiteSettings, error) {
	return site.settings, site.err
}

type shareHTTPPosts struct {
	id   domain.ID
	post app.Post
}

func (posts shareHTTPPosts) Get(_ context.Context, id domain.ID) (app.Post, error) {
	if id != posts.id {
		return app.Post{}, domain.NewError(domain.CodePostNotFound, "missing", nil)
	}
	return posts.post, nil
}

func TestShareMetadataShellEscapesCanonicalPostAndPreservesSPABoundaries(t *testing.T) {
	postID, err := domain.ParseID(shareHTTPPostID)
	if err != nil {
		t.Fatal(err)
	}
	mediaID, err := domain.ParseID("01K2ABCDEF0123456789ABCDE2")
	if err != nil {
		t.Fatal(err)
	}
	base := "https://public.example/memory"
	service, err := app.NewShareMetadataService(
		shareHTTPSite{settings: app.SiteSettings{Title: `乔乔"><script>alert(1)</script>`, Bio: "沿着日常留下坐标", PublicBaseURL: &base}},
		shareHTTPPosts{id: postID, post: app.Post{
			Entry: domain.Entry{ID: postID, Kind: domain.KindMoment, ContentMD: "# 安全标题\n\n[只保留文字](https://secret.example/path)", CreatedAt: time.UnixMilli(1), UpdatedAt: time.UnixMilli(1), PublishedAt: time.UnixMilli(1)},
			Media: []app.MediaUsageProjection{{MediaID: mediaID, Position: 0, MIMEType: "image/jpeg", Width: 32, Height: 32}},
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{
		"index.html":    &fstest.MapFile{Data: []byte(`<!doctype html><html><head>` + shareMetadataStart + `<meta name="description" content="fallback"><title>Moment</title>` + shareMetadataEnd + `</head><body><div id="root"></div></body></html>`)},
		"assets/app.js": &fstest.MapFile{Data: []byte("app")},
	}
	engine, err := New(Config{Assets: assets, ShareMetadata: service})
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "/m/"+shareHTTPPostID, nil)
	request.Host = "attacker.invalid"
	request.Header.Set("X-Forwarded-Host", "forwarded-attacker.invalid")
	response := performRequest(engine, request)
	body := response.Body.String()
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "text/html; charset=utf-8" || response.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("post shell response = %d headers=%v", response.Code, response.Header())
	}
	for _, expected := range []string{
		`<meta property="og:type" content="article" />`,
		`<link rel="canonical" href="https://public.example/memory/m/` + shareHTTPPostID + `" />`,
		`<meta property="og:image" content="https://public.example/memory/api/v1/media/` + mediaID.String() + `/preview" />`,
		`<meta name="twitter:card" content="summary_large_image" />`,
		`安全标题 只保留文字`,
		`乔乔&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;`,
	} {
		if !strings.Contains(body, expected) {
			t.Errorf("shell does not contain %q: %s", expected, body)
		}
	}
	for _, forbidden := range []string{"attacker.invalid", "forwarded-attacker.invalid", "secret.example", "<script>alert(1)</script>"} {
		if strings.Contains(body, forbidden) {
			t.Errorf("shell leaked %q: %s", forbidden, body)
		}
	}
	if strings.Count(body, "<title>") != 1 || strings.Contains(body, "fallback") {
		t.Fatalf("static metadata was not replaced exactly: %s", body)
	}

	missing := performRequest(engine, httptest.NewRequest(http.MethodGet, "/m/01K2ABCDEF0123456789ABCDE7", nil))
	if missing.Code != http.StatusOK || strings.Contains(missing.Body.String(), "安全标题") || !strings.Contains(missing.Body.String(), `name="robots" content="noindex"`) || strings.Contains(missing.Body.String(), "/m/01K2ABCDEF0123456789ABCDE7") {
		t.Fatalf("missing post shell = %d %s", missing.Code, missing.Body.String())
	}
	malformed := performRequest(engine, httptest.NewRequest(http.MethodGet, "/m/not-an-id", nil))
	if malformed.Code != http.StatusOK || !strings.Contains(malformed.Body.String(), `name="robots" content="noindex"`) {
		t.Fatalf("malformed post shell = %d %s", malformed.Code, malformed.Body.String())
	}
	unclean := performRequest(engine, httptest.NewRequest(http.MethodGet, "/private/../", nil))
	if unclean.Code != http.StatusOK || !strings.Contains(unclean.Body.String(), `name="robots" content="noindex"`) || strings.Contains(unclean.Body.String(), `rel="canonical" href="https://public.example/memory/private`) {
		t.Fatalf("unclean path was canonicalized as a public route: %d %s", unclean.Code, unclean.Body.String())
	}

	search := performRequest(engine, httptest.NewRequest(http.MethodGet, "/search?q=%3Cscript%3E", nil))
	if !strings.Contains(search.Body.String(), `https://public.example/memory/search`) || strings.Contains(search.Body.String(), "%3Cscript%3E") {
		t.Fatalf("search shell canonicalized request input: %s", search.Body.String())
	}
	head := performRequest(engine, httptest.NewRequest(http.MethodHead, "/archive", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Type") != "text/html; charset=utf-8" {
		t.Fatalf("archive HEAD = %d headers=%v body=%q", head.Code, head.Header(), head.Body.String())
	}
	api := performRequest(engine, httptest.NewRequest(http.MethodGet, "/api/v1/not-real", nil))
	if api.Code != http.StatusNotFound || strings.Contains(api.Body.String(), "<html") {
		t.Fatalf("API fallback was swallowed: %d %s", api.Code, api.Body.String())
	}
	asset := performRequest(engine, httptest.NewRequest(http.MethodGet, "/assets/app.js", nil))
	if asset.Code != http.StatusOK || asset.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" {
		t.Fatalf("asset response = %d headers=%v", asset.Code, asset.Header())
	}
}

func TestShareMetadataWithoutPublicBaseOmitsAbsoluteFields(t *testing.T) {
	metadata := app.ShareMetadata{Title: "Moment", Description: "记下此刻", Type: "website", ImageURL: ""}
	body := string(renderShareMetadata(metadata))
	for _, forbidden := range []string{`rel="canonical"`, `property="og:url"`, `property="og:image"`, `name="twitter:image"`} {
		if strings.Contains(body, forbidden) {
			t.Fatalf("metadata without public base contains %q: %s", forbidden, body)
		}
	}
	if !strings.Contains(body, `name="twitter:card" content="summary"`) {
		t.Fatalf("metadata without image did not select summary card: %s", body)
	}
}

func TestShareMetadataReadFailureRendersGenericNoIndexShell(t *testing.T) {
	service, err := app.NewShareMetadataService(
		shareHTTPSite{err: errors.New("database unavailable")},
		shareHTTPPosts{},
	)
	if err != nil {
		t.Fatal(err)
	}
	assets := fstest.MapFS{
		"index.html": &fstest.MapFile{Data: []byte(`<!doctype html><html><head>` + shareMetadataStart + `<title>static</title>` + shareMetadataEnd + `</head><body><div id="root"></div></body></html>`)},
	}
	engine, err := New(Config{Assets: assets, ShareMetadata: service})
	if err != nil {
		t.Fatal(err)
	}
	response := performRequest(engine, httptest.NewRequest(http.MethodGet, "/m/"+shareHTTPPostID, nil))
	body := response.Body.String()
	if response.Code != http.StatusOK || !strings.Contains(body, `<title>Moment</title>`) || !strings.Contains(body, `name="robots" content="noindex"`) || strings.Contains(body, `rel="canonical"`) {
		t.Fatalf("read failure shell = %d %s", response.Code, body)
	}
}
