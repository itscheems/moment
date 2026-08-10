package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	"github.com/moment/moment/internal/storage/sqlite"
)

// smallWebPBase64 is derived from x/image's BSD-licensed
// gopher-doc.1bpp.lossless.webp fixture. See testdata/LICENSE.x-image.
const smallWebPBase64 = "UklGRrIBAABXRUJQVlA4TKUBAAAvSsAYAA8w//M///MfeJAkbXvaSG7m8Q3GfYSBJekwQztm/IcZlgwnmWImn2BK7aFmBtnVir6q//8VOkFE/xm4baTIu8c48ArEo6+B3zFKYln3pqClSCKX0begFTAXFOLXHSyF8cCNcZEG4OywuA4KVVfJCiArU7GAgJI8+lJP/OKMT/fBAjevg1cYB7YVkFuWga2lyPi5I0HFy5YTpWIHg0RZpkniRVW9odHAKOwosWuOGdxIyn2OvaCDvhg/we6TwadPBPbqBV58MsLmMJ8yZnOWk8SRz4N+QoyPL+MnamzMvcE1rHNEr91F9GKZPVUcS9w7PhhH36suB9qPeYb/oLk6cuTiJ0wOK3m5h1cKjW6EVZCYMK7dxcKCBdgP9HkKr9gkAO2P8GKZGWVdIAatQa+1IDpt6qyorVwdy01xdW8Jkfk6xjEXmVQQ+HQdFr6OKhIN34dXWq0+0qr6EJSCeeVLH9+gvGTLyqM65PQ44ihzlTXxQKjKbAvshXgir7Lil9w4L2bvMycmjQcqXaMCO6BlY28i+FOLzbfI1vEqxAhotocAAA=="

func TestIngestSupportedFormatsUsesSignaturesAndCanonicalAuthority(t *testing.T) {
	t.Parallel()
	webpBytes, err := base64.StdEncoding.DecodeString(smallWebPBase64)
	if err != nil {
		t.Fatal(err)
	}
	tests := []struct {
		name         string
		claimedName  string
		contents     []byte
		wantOriginal string
		wantPreview  string
	}{
		{name: "JPEG", claimedName: "actually-a-gif.gif", contents: encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 24, 12))), wantOriginal: "image/jpeg", wantPreview: "image/jpeg"},
		{name: "PNG alpha", claimedName: "opaque.jpg", contents: encodeTransparentPNG(t, 20, 10), wantOriginal: "image/png", wantPreview: "image/png"},
		{name: "WebP", claimedName: "webp.txt", contents: webpBytes, wantOriginal: "image/webp", wantPreview: "image/jpeg"},
		{name: "GIF", claimedName: "animation.jpeg", contents: encodeGIF(t, 2), wantOriginal: "image/gif", wantPreview: "image/jpeg"},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			store, runtimeState, database := newTestStore(t, Limits{})
			media, preview, err := store.Ingest(context.Background(), testCase.claimedName, bytes.NewReader(testCase.contents))
			if err != nil {
				t.Fatalf("ingest: %v", err)
			}
			if media.MIMEType != testCase.wantOriginal || preview.MIMEType != testCase.wantPreview {
				t.Fatalf("MIME original/preview = %q/%q, want %q/%q", media.MIMEType, preview.MIMEType, testCase.wantOriginal, testCase.wantPreview)
			}
			expectedPath, ok := expectedStoragePath(media.SHA256, media.MIMEType)
			if !ok || media.StoragePath != expectedPath {
				t.Fatalf("storage path = %q, want %q", media.StoragePath, expectedPath)
			}
			originalPath := filepath.Join(runtimeState.Current.Root, filepath.FromSlash(media.StoragePath))
			stored, readErr := os.ReadFile(originalPath)
			if readErr != nil || !bytes.Equal(stored, testCase.contents) {
				t.Fatalf("immutable original changed: err=%v equal=%v", readErr, bytes.Equal(stored, testCase.contents))
			}
			if strings.HasPrefix(preview.Path, runtimeState.Current.Root) || !strings.HasPrefix(preview.Path, filepath.Join(runtimeState.Root, "cache", "previews", RecipeVersion)) {
				t.Fatalf("preview path %q is not in top-level recipe cache", preview.Path)
			}
			var unreferenced int64
			if err := database.ReadTx(context.Background(), func(queries sqlite.Queries) error {
				return queries.QueryRowContext(context.Background(), `SELECT unreferenced_since_ms FROM media WHERE id = ?`, media.ID.String()).Scan(&unreferenced)
			}); err != nil {
				t.Fatal(err)
			}
			if media.UnreferencedSince == nil || unreferenced != media.CreatedAt.UnixMilli() {
				t.Fatalf("unreferenced_since_ms = %d, want %d", unreferenced, media.CreatedAt.UnixMilli())
			}
		})
	}
}

func TestIngestDeduplicatesBytesAndProducesBoundedOrientedPreview(t *testing.T) {
	t.Parallel()
	store, _, database := newTestStore(t, Limits{})
	wide := image.NewRGBA(image.Rect(0, 0, 2_000, 1_000))
	for y := 0; y < 1_000; y++ {
		for x := 0; x < 2_000; x++ {
			wide.SetRGBA(x, y, color.RGBA{R: uint8(x), G: uint8(y), B: 90, A: 255})
		}
	}
	rotated := injectEXIFOrientation(t, encodeJPEG(t, wide), 6)
	first, preview, err := store.Ingest(context.Background(), "first.png", bytes.NewReader(rotated))
	if err != nil {
		t.Fatal(err)
	}
	if first.Width != 1_000 || first.Height != 2_000 || preview.Width != 800 || preview.Height != 1_600 {
		t.Fatalf("oriented dimensions original/preview = %dx%d/%dx%d", first.Width, first.Height, preview.Width, preview.Height)
	}
	second, secondPreview, err := store.Ingest(context.Background(), "second.gif", bytes.NewReader(rotated))
	if err != nil {
		t.Fatal(err)
	}
	if second.ID != first.ID || secondPreview.Path != preview.Path {
		t.Fatalf("duplicate returned media/preview %q/%q, want %q/%q", second.ID, secondPreview.Path, first.ID, preview.Path)
	}
	var count int
	if err := database.ReadTx(context.Background(), func(queries sqlite.Queries) error {
		return queries.QueryRowContext(context.Background(), `SELECT count(*) FROM media`).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 1 {
		t.Fatalf("deduplicated row count = %d, want 1", count)
	}
}

func TestHeaderBudgetsRejectBeforeFullDecode(t *testing.T) {
	t.Parallel()
	limits, err := (Limits{}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("unsupported signature", func(t *testing.T) {
		_, err := inspectImage([]byte("<svg/>"), limits)
		assertCode(t, err, domain.CodeMediaTypeUnsupported)
	})
	t.Run("oversized PNG header wins over truncated body", func(t *testing.T) {
		header := make([]byte, 33)
		copy(header, pngSignature)
		binary.BigEndian.PutUint32(header[8:12], 13)
		copy(header[12:16], "IHDR")
		binary.BigEndian.PutUint32(header[16:20], DefaultMaxDimension+1)
		binary.BigEndian.PutUint32(header[20:24], 1)
		header[24] = 8
		header[25] = 2
		_, err := inspectImage(header, limits)
		assertCode(t, err, domain.CodeMediaDimensionsExceeded)
	})
	t.Run("truncated PNG", func(t *testing.T) {
		contents := encodeTransparentPNG(t, 4, 4)
		_, err := inspectImage(contents[:32], limits)
		assertCode(t, err, domain.CodeMediaCorrupt)
	})
	t.Run("GIF frames", func(t *testing.T) {
		low := limits
		low.MaxGIFFrames = 1
		_, err := inspectImage(encodeGIF(t, 2), low)
		assertCode(t, err, domain.CodeMediaDimensionsExceeded)
	})
	t.Run("GIF cumulative pixels", func(t *testing.T) {
		low := limits
		low.MaxGIFFramePixels = 15
		_, err := inspectImage(encodeGIF(t, 2), low)
		assertCode(t, err, domain.CodeMediaDimensionsExceeded)
	})
	t.Run("animated WebP policy", func(t *testing.T) {
		contents := make([]byte, 30)
		copy(contents[:4], "RIFF")
		binary.LittleEndian.PutUint32(contents[4:8], uint32(len(contents)-8))
		copy(contents[8:12], "WEBP")
		copy(contents[12:16], "VP8X")
		binary.LittleEndian.PutUint32(contents[16:20], 10)
		contents[20] = 0x02
		_, err := inspectImage(contents, limits)
		assertCode(t, err, domain.CodeMediaTypeUnsupported)
	})
}

func TestByteBudgetAndCancellationLeaveNoAuthority(t *testing.T) {
	t.Parallel()
	store, runtimeState, database := newTestStore(t, Limits{MaxBytes: 32})
	_, _, err := store.Ingest(context.Background(), "large.png", bytes.NewReader(make([]byte, 33)))
	assertCode(t, err, domain.CodeMediaTooLarge)

	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	_, _, err = store.Ingest(canceled, "cancel.png", bytes.NewReader(encodeTransparentPNG(t, 2, 2)))
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled ingest error = %v", err)
	}
	var count int
	if err := database.ReadTx(context.Background(), func(queries sqlite.Queries) error {
		return queries.QueryRowContext(context.Background(), `SELECT count(*) FROM media`).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("authority rows after rejected inputs = %d", count)
	}
	entries, err := os.ReadDir(runtimeState.Current.OriginalsDir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("original authority contains rejected files: %v", entries)
	}
}

func TestMissingPreviewSingleflightRebuildsOnceAndRetriesAfterCancellation(t *testing.T) {
	t.Parallel()
	store, _, _ := newTestStore(t, Limits{})
	media, preview, err := store.Ingest(context.Background(), "alpha.png", bytes.NewReader(encodeTransparentPNG(t, 80, 40)))
	if err != nil {
		t.Fatal(err)
	}
	golden, err := os.ReadFile(preview.Path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(preview.Path); err != nil {
		t.Fatal(err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.EnsurePreview(canceled, media.ID); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled preview error = %v", err)
	}

	var generations atomic.Int32
	entered := make(chan struct{})
	release := make(chan struct{})
	var once sync.Once
	store.generateHook = func() {
		generations.Add(1)
		once.Do(func() { close(entered) })
		<-release
	}
	const callers = 12
	results := make(chan Preview, callers)
	errorsChannel := make(chan error, callers)
	for range callers {
		go func() {
			result, ensureErr := store.EnsurePreview(context.Background(), media.ID)
			results <- result
			errorsChannel <- ensureErr
		}()
	}
	<-entered
	close(release)
	for range callers {
		if ensureErr := <-errorsChannel; ensureErr != nil {
			t.Fatal(ensureErr)
		}
		if result := <-results; result.Path != preview.Path {
			t.Fatalf("rebuilt path = %q, want %q", result.Path, preview.Path)
		}
	}
	if generations.Load() != 1 {
		t.Fatalf("preview generation count = %d, want 1", generations.Load())
	}
	rebuilt, err := os.ReadFile(preview.Path)
	if err != nil || !bytes.Equal(rebuilt, golden) {
		t.Fatalf("recipe rebuild changed bytes: err=%v equal=%v", err, bytes.Equal(rebuilt, golden))
	}
	matches, err := filepath.Glob(filepath.Join(filepath.Dir(preview.Path), ".partial-*"))
	if err != nil || len(matches) != 0 {
		t.Fatalf("preview partials after rebuild = %v, err=%v", matches, err)
	}
}

func TestInterruptedPreviewAndOriginalCommitsAreInvisibleAndRetryable(t *testing.T) {
	t.Parallel()
	store, runtimeState, database := newTestStore(t, Limits{})
	contents := encodeTransparentPNG(t, 24, 12)
	injected := errors.New("injected interruption")
	store.beforePreviewCommit = func() error { return injected }
	_, _, err := store.Ingest(context.Background(), "interrupted.png", bytes.NewReader(contents))
	assertCode(t, err, domain.CodeMediaPreviewFailed)
	assertMediaAuthorityEmpty(t, database, runtimeState.Current.OriginalsDir)
	assertNoPartials(t, runtimeState.Root)

	store.beforePreviewCommit = nil
	store.beforeOriginalCommit = func() error { return injected }
	_, _, err = store.Ingest(context.Background(), "interrupted.png", bytes.NewReader(contents))
	if !errors.Is(err, injected) {
		t.Fatalf("interrupted original error = %v", err)
	}
	assertMediaAuthorityEmpty(t, database, runtimeState.Current.OriginalsDir)
	assertNoPartials(t, runtimeState.Root)

	store.beforeOriginalCommit = nil
	media, preview, err := store.Ingest(context.Background(), "retry.png", bytes.NewReader(contents))
	if err != nil {
		t.Fatalf("retry ingest: %v", err)
	}
	if _, err := os.Stat(preview.Path); err != nil {
		t.Fatalf("retry preview is missing: %v", err)
	}
	if media.UnreferencedSince == nil {
		t.Fatal("new media is not marked unreferenced")
	}
	if err := database.WriteTx(context.Background(), func(queries sqlite.Queries) error {
		_, err := queries.ExecContext(context.Background(), `UPDATE media SET unreferenced_since_ms = NULL WHERE id = ?`, media.ID.String())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	reloaded, found, err := store.findBySHA(context.Background(), media.SHA256)
	if err != nil || !found || reloaded.UnreferencedSince != nil {
		t.Fatalf("referenced NULL state = found %v value %v err %v", found, reloaded.UnreferencedSince, err)
	}
}

func assertMediaAuthorityEmpty(t *testing.T, database *sqlite.Database, originals string) {
	t.Helper()
	var count int
	if err := database.ReadTx(context.Background(), func(queries sqlite.Queries) error {
		return queries.QueryRowContext(context.Background(), `SELECT count(*) FROM media`).Scan(&count)
	}); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("media authority rows = %d, want 0", count)
	}
	files := 0
	if err := filepath.WalkDir(originals, func(_ string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			files++
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if files != 0 {
		t.Fatalf("original authority files = %d, want 0", files)
	}
}

func assertNoPartials(t *testing.T, root string) {
	t.Helper()
	var partials []string
	if err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if strings.HasPrefix(entry.Name(), ".partial-") {
			partials = append(partials, path)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if len(partials) != 0 {
		t.Fatalf("partial artifacts remain: %v", partials)
	}
}

func newTestStore(t *testing.T, limits Limits) (*Store, *runtimefs.Runtime, *sqlite.Database) {
	t.Helper()
	runtimeState, err := runtimefs.Initialize(context.Background(), runtimefs.Config{
		DataDir:     t.TempDir(),
		Environment: runtimefs.EnvironmentDevelopment,
	})
	if err != nil {
		t.Fatal(err)
	}
	database, err := sqlite.Open(context.Background(), runtimeState, sqlite.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = database.Close() })
	store, err := NewStore(runtimeState, database, Options{
		Limits:  limits,
		Clock:   func() time.Time { return time.Date(2026, 8, 10, 12, 0, 0, 123_000_000, time.UTC) },
		Entropy: bytes.NewReader(bytes.Repeat([]byte{0x42}, 1_024)),
	})
	if err != nil {
		t.Fatal(err)
	}
	return store, runtimeState, database
}

func encodeTransparentPNG(t *testing.T, width, height int) []byte {
	t.Helper()
	value := image.NewNRGBA(image.Rect(0, 0, width, height))
	for index := range value.Pix {
		value.Pix[index] = 0x88
	}
	value.SetNRGBA(0, 0, color.NRGBA{R: 255, A: 0})
	var buffer bytes.Buffer
	if err := png.Encode(&buffer, value); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func encodeJPEG(t *testing.T, value image.Image) []byte {
	t.Helper()
	var buffer bytes.Buffer
	if err := jpeg.Encode(&buffer, value, &jpeg.Options{Quality: 90}); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func encodeGIF(t *testing.T, frames int) []byte {
	t.Helper()
	palette := color.Palette{color.Black, color.White}
	animation := &gif.GIF{}
	for index := 0; index < frames; index++ {
		frame := image.NewPaletted(image.Rect(0, 0, 4, 4), palette)
		for pixel := range frame.Pix {
			frame.Pix[pixel] = uint8(index % 2)
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

func injectEXIFOrientation(t *testing.T, jpegBytes []byte, orientation uint16) []byte {
	t.Helper()
	if len(jpegBytes) < 2 || jpegBytes[0] != 0xff || jpegBytes[1] != 0xd8 {
		t.Fatal("fixture is not JPEG")
	}
	tiff := make([]byte, 26)
	copy(tiff[:2], "II")
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	binary.LittleEndian.PutUint16(tiff[8:10], 1)
	binary.LittleEndian.PutUint16(tiff[10:12], 0x0112)
	binary.LittleEndian.PutUint16(tiff[12:14], 3)
	binary.LittleEndian.PutUint32(tiff[14:18], 1)
	binary.LittleEndian.PutUint16(tiff[18:20], orientation)
	segment := append([]byte("Exif\x00\x00"), tiff...)
	marker := []byte{0xff, 0xe1, byte((len(segment) + 2) >> 8), byte(len(segment) + 2)}
	result := append([]byte{}, jpegBytes[:2]...)
	result = append(result, marker...)
	result = append(result, segment...)
	return append(result, jpegBytes[2:]...)
}

func assertCode(t *testing.T, err error, code domain.Code) {
	t.Helper()
	if !domain.HasCode(err, code) {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}
