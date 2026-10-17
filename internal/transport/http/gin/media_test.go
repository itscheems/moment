package ginhttp

import (
	"bytes"
	"context"
	"image"
	"image/color"
	"image/gif"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	gin "github.com/gin-gonic/gin"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/auth"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/media"
	"github.com/moment/moment/internal/runtimefs"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestMediaDeliveryHeadersConditionalsAndExplicitGIFOriginal(t *testing.T) {
	engine, store, _, runtimeState := newMediaHTTPFixture(t)
	original := testGIF(t)
	value, preview, err := store.Ingest(fixtureContentContext(t, testAccountsForRuntime(t, runtimeState)), "animated.gif", bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	accounts := testAccountsForRuntime(t, runtimeState)
	profile, err := accounts.ProfileAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	avatar := value.ID.String()
	if _, err := accounts.UpdateProfile(context.Background(), profile.ID, app.ProfileUpdate{AvatarMediaID: &app.NullableString{Value: &avatar}}); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(preview.Path); err != nil {
		t.Fatal(err)
	}

	previewResponse := performRequest(engine, httptest.NewRequest(http.MethodGet, "/api/v1/media/"+value.ID.String()+"/preview?v="+media.RecipeVersion, nil))
	if previewResponse.Code != http.StatusOK || previewResponse.Header().Get("Content-Type") != "image/jpeg" ||
		previewResponse.Header().Get("Cache-Control") != "public, max-age=0, must-revalidate" ||
		previewResponse.Header().Get("X-Content-Type-Options") != "nosniff" || previewResponse.Header().Get("ETag") == "" {
		t.Fatalf("preview response = %d headers=%v body=%s", previewResponse.Code, previewResponse.Header(), previewResponse.Body.String())
	}
	if bytes.Equal(previewResponse.Body.Bytes(), original) || previewResponse.Header().Get("Content-Length") != stringInt(len(previewResponse.Body.Bytes())) {
		t.Fatal("preview silently used original bytes or wrong length")
	}
	rangeRequest := httptest.NewRequest(http.MethodGet, "/api/v1/media/"+value.ID.String()+"/preview", nil)
	rangeRequest.Header.Set("Range", "bytes=0-9")
	partial := performRequest(engine, rangeRequest)
	if partial.Code != http.StatusPartialContent || partial.Body.Len() != 10 || partial.Header().Get("Accept-Ranges") != "bytes" ||
		partial.Header().Get("Content-Range") != "bytes 0-9/"+stringInt(len(previewResponse.Body.Bytes())) {
		t.Fatalf("range response = %d headers=%v body=%d", partial.Code, partial.Header(), partial.Body.Len())
	}

	head := performRequest(engine, httptest.NewRequest(http.MethodHead, "/api/v1/media/"+value.ID.String()+"/preview", nil))
	if head.Code != http.StatusOK || head.Body.Len() != 0 || head.Header().Get("Content-Length") != previewResponse.Header().Get("Content-Length") {
		t.Fatalf("HEAD response = %d headers=%v body=%q", head.Code, head.Header(), head.Body.String())
	}
	conditionalRequest := httptest.NewRequest(http.MethodGet, "/api/v1/media/"+value.ID.String()+"/preview", nil)
	conditionalRequest.Header.Set("If-None-Match", "W/"+previewResponse.Header().Get("ETag"))
	conditional := performRequest(engine, conditionalRequest)
	if conditional.Code != http.StatusNotModified || conditional.Body.Len() != 0 || conditional.Header().Get("ETag") != previewResponse.Header().Get("ETag") {
		t.Fatalf("conditional = %d headers=%v body=%q", conditional.Code, conditional.Header(), conditional.Body.String())
	}

	originalResponse := performRequest(engine, httptest.NewRequest(http.MethodGet, "/api/v1/media/"+value.ID.String(), nil))
	if originalResponse.Code != http.StatusOK || originalResponse.Header().Get("Content-Type") != "image/gif" ||
		originalResponse.Header().Get("Cache-Control") != "public, max-age=31536000, immutable" || !bytes.Equal(originalResponse.Body.Bytes(), original) {
		t.Fatalf("explicit original = %d %q equal=%v", originalResponse.Code, originalResponse.Header().Get("Content-Type"), bytes.Equal(originalResponse.Body.Bytes(), original))
	}

	if err := os.Remove(preview.Path); err != nil {
		t.Fatal(err)
	}
	originalPath := filepath.Join(runtimeState.Current.Root, filepath.FromSlash(value.StoragePath))
	if err := os.Remove(originalPath); err != nil {
		t.Fatal(err)
	}
	failed := performRequest(engine, httptest.NewRequest(http.MethodGet, "/api/v1/media/"+value.ID.String()+"/preview", nil))
	if failed.Code != http.StatusInternalServerError || decodeError(t, failed.Body).Error.Code != "MEDIA_CORRUPT" || bytes.Equal(failed.Body.Bytes(), original) {
		t.Fatalf("preview failure fell back: %d %s", failed.Code, failed.Body.String())
	}
}

func TestAccountMediaUploadRequiresAuthenticationAndReturnsManagedURLs(t *testing.T) {
	engine, _, token, _ := newMediaHTTPFixture(t)
	contents := testGIF(t)
	request := mediaUploadRequest(t, contents)
	unauthorized := performRequest(engine, request)
	if unauthorized.Code != http.StatusUnauthorized {
		t.Fatalf("unauthorized upload = %d", unauthorized.Code)
	}
	authorized := mediaUploadRequest(t, contents)
	authorized.Header.Set("Authorization", "Bearer "+token)
	response := performRequest(engine, authorized)
	if response.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", response.Code, response.Body.String())
	}
	data := decodeDataMap(t, response)
	if !strings.HasPrefix(data["original_url"].(string), "/api/v1/media/") || !strings.HasSuffix(data["preview_url"].(string), "/preview?v="+media.RecipeVersion) {
		t.Fatalf("upload data = %#v", data)
	}
}

func TestUploaderCanReadEarlyVideoPosterWithoutCachingIt(t *testing.T) {
	engine, _, _, _ := newMediaHTTPFixture(t)
	posters := newUploadPosterCache()
	const uploadID = "upload-0123456789abcdef"
	poster := []byte{0xff, 0xd8, 0xff, 0xd9}
	posters.put("0123456789abcdef0123456789abcdef", uploadID, poster)

	router := gin.New()
	router.GET("/poster/:uploadID", getUploadPoster(posters))
	request := httptest.NewRequest(http.MethodGet, "/poster/"+uploadID, nil)
	request = request.WithContext(app.WithContentActor(request.Context(), domain.User{ID: "0123456789abcdef0123456789abcdef", Role: domain.UserRoleMember, Status: domain.AccountActive}))
	response := performRequest(router, request)
	if response.Code != http.StatusOK || response.Header().Get("Content-Type") != "image/jpeg" ||
		response.Header().Get("Cache-Control") != "no-store" || !bytes.Equal(response.Body.Bytes(), poster) {
		t.Fatalf("early poster response = %d headers=%v equal=%v", response.Code, response.Header(), bytes.Equal(response.Body.Bytes(), poster))
	}

	missing := performRequest(engine, httptest.NewRequest(http.MethodGet, "/api/v1/media/uploads/another-0123456789/poster", nil))
	if missing.Code != http.StatusUnauthorized {
		t.Fatalf("unauthenticated early poster = %d", missing.Code)
	}
}

func newMediaHTTPFixture(t *testing.T) (http.Handler, *media.Store, string, *runtimefs.Runtime) {
	t.Helper()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{DataDir: t.TempDir(), Environment: runtimefs.EnvironmentDevelopment})
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlitestore.Open(context.Background(), runtimeState, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	testAccounts(t, database, runtimeState)
	t.Cleanup(func() { _ = database.Close() })
	store, err := media.NewStore(runtimeState, database, media.Options{Clock: func() time.Time { return time.UnixMilli(1_800_000_000_000) }, Entropy: bytes.NewReader(bytes.Repeat([]byte{0x31}, 128))})
	if err != nil {
		t.Fatal(err)
	}
	_, err = auth.InitializeOffline(context.Background(), runtimeState, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	manager, err := auth.NewManager(runtimeState.Root, auth.Options{})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.Load(); err != nil {
		t.Fatal(err)
	}
	engine, err := New(Config{Assets: testAssets(), SessionSigner: manager, Accounts: testAccounts(t, database, runtimeState), Media: store, Readiness: func(context.Context) error { return nil }, RequestID: func() string { return "req_media" }})
	if err != nil {
		t.Fatal(err)
	}
	return engine, store, issueTestAPIToken(t, testAccounts(t, database, runtimeState)), runtimeState
}

func mediaUploadRequest(t *testing.T, contents []byte) *http.Request {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "animated.gif")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := io.Copy(part, bytes.NewReader(contents)); err != nil {
		t.Fatal(err)
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/media", &body)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	return request
}

func testGIF(t *testing.T) []byte {
	t.Helper()
	palette := color.Palette{color.Black, color.White}
	animation := &gif.GIF{}
	for index := range 2 {
		frame := image.NewPaletted(image.Rect(0, 0, 4, 4), palette)
		for pixel := range frame.Pix {
			frame.Pix[pixel] = uint8(index)
		}
		animation.Image = append(animation.Image, frame)
		animation.Delay = append(animation.Delay, 5)
	}
	var buffer bytes.Buffer
	if err := gif.EncodeAll(&buffer, animation); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func stringInt(value int) string { return strconv.Itoa(value) }

func TestUploadPostersAreIsolatedByAccountEvenWhenUploadIDsCollide(t *testing.T) {
	const uploadID = "shared-upload-0123456789"
	first := domain.User{ID: "0123456789abcdef0123456789abcdef", Role: domain.UserRoleMember, Status: domain.AccountActive}
	second := domain.User{ID: "fedcba9876543210fedcba9876543210", Role: domain.UserRoleAdmin, Status: domain.AccountActive}
	cache := newUploadPosterCache()
	cache.put(first.ID, uploadID, []byte("first poster"))
	cache.put(second.ID, uploadID, []byte("second poster"))
	router := gin.New()
	router.GET("/poster/:uploadID", getUploadPoster(cache))
	for _, user := range []domain.User{first, second} {
		request := httptest.NewRequest(http.MethodGet, "/poster/"+uploadID, nil)
		request = request.WithContext(app.WithContentActor(request.Context(), user))
		response := performRequest(router, request)
		expected := "first poster"
		if user.ID == second.ID {
			expected = "second poster"
		}
		if response.Code != http.StatusOK || response.Body.String() != expected {
			t.Fatalf("cross-account poster=%d %q", response.Code, response.Body.String())
		}
	}
	cache.remove(first.ID, uploadID)
	if _, ok := cache.get(first.ID, uploadID); ok {
		t.Fatal("removed poster remained visible")
	}
	if value, ok := cache.get(second.ID, uploadID); !ok || string(value) != "second poster" {
		t.Fatal("one account removed another account's poster")
	}
	anonymous := performRequest(router, httptest.NewRequest(http.MethodGet, "/poster/"+uploadID, nil))
	if anonymous.Code != http.StatusUnauthorized {
		t.Fatal("anonymous caller read a private upload poster")
	}
}

func TestUnreferencedUploadIsPrivateToItsUploaderAndNeverPubliclyCached(t *testing.T) {
	engine, store, token, state := newMediaHTTPFixture(t)
	ctx := fixtureContentContext(t, testAccountsForRuntime(t, state))
	object, _, err := store.Ingest(ctx, "private-upload.gif", bytes.NewReader(testGIF(t)))
	if err != nil {
		t.Fatal(err)
	}
	route := "/api/v1/media/" + object.ID.String()
	anonymous := performRequest(engine, httptest.NewRequest(http.MethodGet, route, nil))
	if anonymous.Code != http.StatusNotFound {
		t.Fatalf("unreferenced upload exposed=%d", anonymous.Code)
	}
	own := performRequest(engine, headerRequest(http.MethodGet, route, token, nil))
	if own.Code != http.StatusOK || !strings.HasPrefix(own.Header().Get("Cache-Control"), "private,") || strings.Contains(own.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("private upload cache=%d %q", own.Code, own.Header().Get("Cache-Control"))
	}
}
