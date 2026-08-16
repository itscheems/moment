package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
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

func TestIngestHEICRealFixturePreservesOriginalAndBuildsDisplayImage(t *testing.T) {
	converter := os.Getenv("MOMENT_TEST_HEIF_CONVERT")
	ffmpeg := os.Getenv("MOMENT_TEST_FFMPEG")
	if converter == "" && ffmpeg == "" {
		t.Skip("set MOMENT_TEST_HEIF_CONVERT or MOMENT_TEST_FFMPEG to run the real HEIC integration")
	}
	fixture := filepath.Join("..", "..", "work", "v0.0.1", "resources", "IMG_1336.HEIC")
	contents, err := os.ReadFile(fixture)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("real HEIC fixture is not available")
	}
	if err != nil {
		t.Fatal(err)
	}
	options := Options{HEIFConvertPath: converter, FFmpegPath: ffmpeg}
	if converter == "" {
		options.HEIFConvertPath = filepath.Join(t.TempDir(), "missing-heif-convert")
	}
	store, runtimeState, _ := newTestStoreWithOptions(t, options)
	value, preview, err := store.Ingest(context.Background(), "IMG_1336.HEIC", bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	if value.MIMEType != "image/heic" || value.ByteSize != int64(len(contents)) || preview.MIMEType != "image/jpeg" {
		t.Fatalf("HEIC result = media(%q,%d) preview(%q)", value.MIMEType, value.ByteSize, preview.MIMEType)
	}
	original, err := store.ReadOriginal(context.Background(), value.ID)
	if err != nil || !bytes.Equal(original.Contents, contents) {
		t.Fatalf("HEIC original changed: err=%v equal=%v", err, bytes.Equal(original.Contents, contents))
	}
	display, err := store.ReadPreview(context.Background(), value.ID)
	if err != nil || int64(len(display.Contents)) > DefaultPreviewMaxBytes {
		t.Fatalf("HEIC display = %d bytes, %v", len(display.Contents), err)
	}
	listed, err := store.List(context.Background())
	if err != nil || len(listed.Items) != 1 || listed.Items[0].PreviewStatus != "ready" {
		t.Fatalf("HEIC preview status = %#v, %v", listed.Items, err)
	}
	status, err := DiagnosePreview(context.Background(), runtimeState.Root, value.SHA256)
	if err != nil || status != "ready" {
		t.Fatalf("HEIC diagnostic status = %q, %v", status, err)
	}
}

func TestIngestMP4RealProcessorPreservesOriginalBuildsBoundedDisplayAndPoster(t *testing.T) {
	ffmpeg := os.Getenv("MOMENT_TEST_FFMPEG")
	ffprobe := os.Getenv("MOMENT_TEST_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("set MOMENT_TEST_FFMPEG and MOMENT_TEST_FFPROBE to run the real FFmpeg integration")
	}
	inputPath := filepath.Join(t.TempDir(), "camera.mp4")
	command := exec.Command(ffmpeg,
		"-nostdin", "-y", "-v", "error",
		"-f", "lavfi", "-i", "testsrc2=size=640x360:rate=30",
		"-f", "lavfi", "-i", "sine=frequency=880:sample_rate=48000",
		"-t", "2", "-c:v", "libx264", "-pix_fmt", "yuv420p",
		"-c:a", "aac", "-movflags", "+faststart", inputPath,
	)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("generate MP4: %v: %s", err, output)
	}
	contents, err := os.ReadFile(inputPath)
	if err != nil {
		t.Fatal(err)
	}
	store, runtimeState, _ := newTestStoreWithOptions(t, Options{FFmpegPath: ffmpeg, FFprobePath: ffprobe})
	value, preview, err := store.Ingest(context.Background(), "camera.mp4", bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	if value.MIMEType != "video/mp4" || preview.MIMEType != "video/mp4" || int64(fileSize(t, preview.Path)) > DefaultPreviewMaxBytes {
		t.Fatalf("MP4 result = media(%q) preview(%q,%d)", value.MIMEType, preview.MIMEType, fileSize(t, preview.Path))
	}
	if !store.existingVideoPoster(value.SHA256) {
		t.Fatal("MP4 ingest returned before its poster was ready")
	}
	derived, err := store.probeVideoFile(context.Background(), preview.Path)
	if err != nil || derived.codec != "h264" || derived.pixelFormat != "yuv420p" || derived.width > DefaultPreviewMaxEdge || derived.height > DefaultPreviewMaxEdge {
		t.Fatalf("derived MP4 contract = %+v, %v", derived, err)
	}
	original, err := store.ReadOriginal(context.Background(), value.ID)
	if err != nil || !bytes.Equal(original.Contents, contents) {
		t.Fatalf("MP4 original changed: err=%v equal=%v", err, bytes.Equal(original.Contents, contents))
	}
	poster, err := store.ReadPoster(context.Background(), value.ID)
	if err != nil || poster.MIMEType != "image/jpeg" {
		t.Fatalf("MP4 poster = %q, %v", poster.MIMEType, err)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(poster.Contents)); err != nil || format != "jpeg" {
		t.Fatalf("poster is not JPEG: %q, %v", format, err)
	}
	listed, err := store.List(context.Background())
	if err != nil || len(listed.Items) != 1 || listed.Items[0].PreviewStatus != "ready" {
		t.Fatalf("MP4 preview status = %#v, %v", listed.Items, err)
	}
	status, err := DiagnosePreview(context.Background(), runtimeState.Root, value.SHA256)
	if err != nil || status != "ready" {
		t.Fatalf("MP4 diagnostic status = %q, %v", status, err)
	}
}

func TestDetectQuickTimeContainerBySignature(t *testing.T) {
	header := make([]byte, 24)
	binary.BigEndian.PutUint32(header[0:4], uint32(len(header)))
	copy(header[4:8], "ftyp")
	copy(header[8:12], "qt  ")
	copy(header[16:20], "qt  ")
	if detected := detectExtendedFormat(header); detected != formatQuickTime {
		t.Fatalf("detected format = %v, want QuickTime", detected)
	}
}

func TestVideoDisplayFilterPlanToneMapsHDRAndKeepsSDRPortable(t *testing.T) {
	hdr := inspected{
		width:           1080,
		height:          1920,
		colorSpace:      "bt2020nc",
		colorTransfer:   "arib-std-b67",
		colorPrimaries:  "bt2020",
		rotationDegrees: -90,
	}
	linux := videoDisplayFilterPlan(hdr, 1600)
	for _, fragment := range []string{"zscale=transfer=linear", "tonemap=mobius", "primaries=bt709", "transfer=bt709", "format=yuv420p"} {
		if !strings.Contains(linux.filter, fragment) {
			t.Fatalf("linux HDR filter %q does not contain %q", linux.filter, fragment)
		}
	}

	sdr := videoDisplayFilterPlan(inspected{width: 1920, height: 1080, colorPrimaries: "bt709", colorTransfer: "bt709"}, 1600)
	if len(sdr.inputArguments) != 0 || strings.Contains(sdr.filter, "tonemap") || !strings.Contains(sdr.filter, "force_original_aspect_ratio=decrease") {
		t.Fatalf("SDR filter plan = %#v", sdr)
	}
}

func TestIngestQuickTimeRealFixturePreservesOriginalAndBuildsMP4Display(t *testing.T) {
	ffmpeg := os.Getenv("MOMENT_TEST_FFMPEG")
	ffprobe := os.Getenv("MOMENT_TEST_FFPROBE")
	if ffmpeg == "" || ffprobe == "" {
		t.Skip("set MOMENT_TEST_FFMPEG and MOMENT_TEST_FFPROBE to run the real QuickTime integration")
	}
	fixture := filepath.Join("..", "..", "work", "v0.0.1", "resources", "IMG_1337.MOV")
	contents, err := os.ReadFile(fixture)
	if errors.Is(err, os.ErrNotExist) {
		t.Skip("real QuickTime fixture is not available")
	}
	if err != nil {
		t.Fatal(err)
	}
	store, _, _ := newTestStoreWithOptions(t, Options{FFmpegPath: ffmpeg, FFprobePath: ffprobe})
	var earlyPoster []byte
	value, preview, err := store.IngestObserved(
		context.Background(),
		"IMG_1337.MOV",
		bytes.NewReader(contents),
		IngestObserver{VideoPosterReady: func(contents []byte) {
			earlyPoster = bytes.Clone(contents)
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if value.MIMEType != "video/quicktime" || value.ByteSize != int64(len(contents)) || preview.MIMEType != "video/mp4" {
		t.Fatalf("QuickTime result = media(%q,%d) preview(%q)", value.MIMEType, value.ByteSize, preview.MIMEType)
	}
	if value.Width != 1080 || value.Height != 1920 {
		t.Fatalf("QuickTime dimensions = %dx%d, want display-oriented 1080x1920", value.Width, value.Height)
	}
	derived, err := store.probeVideoFile(context.Background(), preview.Path)
	if err != nil {
		t.Fatal(err)
	}
	if derived.colorSpace != "bt709" || derived.colorTransfer != "bt709" || derived.colorPrimaries != "bt709" {
		t.Fatalf("QuickTime display color metadata = space %q transfer %q primaries %q", derived.colorSpace, derived.colorTransfer, derived.colorPrimaries)
	}
	original, err := store.ReadOriginal(context.Background(), value.ID)
	if err != nil || !bytes.Equal(original.Contents, contents) {
		t.Fatalf("QuickTime original changed: err=%v equal=%v", err, bytes.Equal(original.Contents, contents))
	}
	poster, err := store.ReadPoster(context.Background(), value.ID)
	if err != nil || poster.MIMEType != "image/jpeg" {
		t.Fatalf("QuickTime poster = %q, %v", poster.MIMEType, err)
	}
	referencePath := filepath.Join(t.TempDir(), "display-frame.png")
	seek := strconv.FormatFloat(videoPosterTime(derived.durationSeconds), 'f', 3, 64)
	if output, err := exec.Command(ffmpeg, "-nostdin", "-y", "-v", "error", "-ss", seek, "-i", preview.Path, "-frames:v", "1", referencePath).CombinedOutput(); err != nil {
		t.Fatalf("extract display frame: %v: %s", err, output)
	}
	referenceFile, err := os.Open(referencePath)
	if err != nil {
		t.Fatal(err)
	}
	reference, _, decodeErr := image.Decode(referenceFile)
	closeErr := referenceFile.Close()
	if decodeErr != nil || closeErr != nil {
		t.Fatalf("decode display frame: %v", errors.Join(decodeErr, closeErr))
	}
	posterImage, _, err := image.Decode(bytes.NewReader(poster.Contents))
	if err != nil {
		t.Fatal(err)
	}
	earlyPosterImage, _, err := image.Decode(bytes.NewReader(earlyPoster))
	if err != nil {
		t.Fatalf("decode early QuickTime poster: %v", err)
	}
	if earlyPosterImage.Bounds() != posterImage.Bounds() {
		t.Fatalf("early QuickTime poster bounds = %v, durable poster bounds = %v", earlyPosterImage.Bounds(), posterImage.Bounds())
	}
	if delta := averageRGBDifference(earlyPosterImage, posterImage); delta > 8 {
		t.Fatalf("early/durable QuickTime poster average RGB difference = %.2f, want <= 8", delta)
	}
	if posterImage.Bounds() != reference.Bounds() {
		t.Fatalf("QuickTime poster bounds = %v, display frame bounds = %v", posterImage.Bounds(), reference.Bounds())
	}
	if delta := averageRGBDifference(posterImage, reference); delta > 6 {
		t.Fatalf("QuickTime poster/display average RGB difference = %.2f, want <= 6", delta)
	}
}

func averageRGBDifference(left, right image.Image) float64 {
	bounds := left.Bounds().Intersect(right.Bounds())
	if bounds.Empty() {
		return 255
	}
	var total uint64
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			leftPixel := color.NRGBAModel.Convert(left.At(x, y)).(color.NRGBA)
			rightPixel := color.NRGBAModel.Convert(right.At(x, y)).(color.NRGBA)
			total += uint64(absInt(int(leftPixel.R) - int(rightPixel.R)))
			total += uint64(absInt(int(leftPixel.G) - int(rightPixel.G)))
			total += uint64(absInt(int(leftPixel.B) - int(rightPixel.B)))
		}
	}
	return float64(total) / float64(bounds.Dx()*bounds.Dy()*3)
}

func absInt(value int) int {
	if value < 0 {
		return -value
	}
	return value
}

func TestConvertHEICFallsBackToFFmpegWhenLibheifIsUnavailable(t *testing.T) {
	jpegPath := filepath.Join(t.TempDir(), "decoded.jpg")
	if err := os.WriteFile(jpegPath, encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 24, 12))), 0o600); err != nil {
		t.Fatal(err)
	}
	ffmpeg := filepath.Join(t.TempDir(), "ffmpeg")
	script := "#!/bin/sh\nfor last do :; done\ncp \"$MOMENT_TEST_HEIC_JPEG\" \"$last\"\n"
	if err := os.WriteFile(ffmpeg, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOMENT_TEST_HEIC_JPEG", jpegPath)
	store, _, _ := newTestStoreWithOptions(t, Options{
		HEIFConvertPath: filepath.Join(t.TempDir(), "missing-heif-convert"),
		FFmpegPath:      ffmpeg,
	})
	converted, err := store.convertHEIC(context.Background(), []byte("synthetic-heic"))
	if err != nil {
		t.Fatal(err)
	}
	if _, format, err := image.DecodeConfig(bytes.NewReader(converted)); err != nil || format != "jpeg" {
		t.Fatalf("fallback output is not JPEG: %q, %v", format, err)
	}
}

func TestExtendedMediaReportsMissingProcessorWithoutPublishing(t *testing.T) {
	header := make([]byte, 24)
	binary.BigEndian.PutUint32(header[0:4], uint32(len(header)))
	copy(header[4:8], "ftyp")
	copy(header[8:12], "heic")
	copy(header[16:20], "heic")
	store, runtimeState, database := newTestStoreWithOptions(t, Options{
		HEIFConvertPath: filepath.Join(t.TempDir(), "missing-heif-convert"),
		FFmpegPath:      filepath.Join(t.TempDir(), "missing-ffmpeg"),
	})
	_, _, err := store.Ingest(context.Background(), "untrusted.heic", bytes.NewReader(header))
	var domainErr *domain.Error
	if !errors.As(err, &domainErr) || domainErr.Code != domain.CodeMediaProcessorUnavailable {
		t.Fatalf("missing processor error = %v", err)
	}
	assertMediaAuthorityEmpty(t, database, runtimeState.Current.OriginalsDir)
}

func TestVideoDurationLimitRejectsLongMediaBeforeTranscode(t *testing.T) {
	probe := filepath.Join(t.TempDir(), "ffprobe")
	script := "#!/bin/sh\nprintf '%s\\n' '{\"streams\":[{\"width\":640,\"height\":360,\"duration\":\"301\",\"codec_name\":\"h264\",\"pix_fmt\":\"yuv420p\"}],\"format\":{\"duration\":\"301\"}}'\n"
	if err := os.WriteFile(probe, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	store, _, _ := newTestStoreWithOptions(t, Options{FFprobePath: probe})
	_, err := store.probeVideoFile(context.Background(), "ignored.mp4")
	assertCode(t, err, domain.CodeMediaTooLarge)
}

func TestRebuildPreviewsRepairsPartialCacheAndIsDryRunCancelableAndIdempotent(t *testing.T) {
	store, _, _ := newTestStore(t, Limits{})
	value, preview, err := store.Ingest(context.Background(), "repair.png", bytes.NewReader(encodeTransparentPNG(t, 32, 16)))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(preview.Path, []byte("partial-corruption"), 0o600); err != nil {
		t.Fatal(err)
	}
	dryRun, err := store.RebuildPreviews(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	if dryRun.Total != 1 || dryRun.Stale != 1 || dryRun.Rebuilt != 0 || !dryRun.DryRun {
		t.Fatalf("dry-run result = %+v", dryRun)
	}
	if contents, _ := os.ReadFile(preview.Path); string(contents) != "partial-corruption" {
		t.Fatal("dry-run changed corrupt preview")
	}

	rebuilt, err := store.RebuildPreviews(context.Background(), false)
	if err != nil {
		t.Fatal(err)
	}
	if rebuilt.Rebuilt != 1 || rebuilt.Stale != 1 || rebuilt.DryRun {
		t.Fatalf("rebuild result = %+v", rebuilt)
	}
	asset, err := store.ReadPreview(context.Background(), value.ID)
	if err != nil || len(asset.Contents) == 0 || asset.MIMEType != "image/png" {
		t.Fatalf("repaired preview = %d bytes %q, %v", len(asset.Contents), asset.MIMEType, err)
	}
	idempotent, err := store.RebuildPreviews(context.Background(), false)
	if err != nil || idempotent.Ready != 1 || idempotent.Rebuilt != 0 {
		t.Fatalf("idempotent rebuild = %+v, %v", idempotent, err)
	}

	if err := os.WriteFile(preview.Path, []byte("corrupt-again"), 0o600); err != nil {
		t.Fatal(err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := store.RebuildPreviews(cancelled, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled rebuild = %v", err)
	}
	if contents, _ := os.ReadFile(preview.Path); string(contents) != "corrupt-again" {
		t.Fatal("cancelled rebuild mutated cache")
	}
}

func TestPruneExactWindowAndFailureOrdering(t *testing.T) {
	store, _, database := newTestStore(t, Limits{})
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	exact, _, err := store.Ingest(context.Background(), "exact.jpg", bytes.NewReader(encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 12, 12)))))
	if err != nil {
		t.Fatal(err)
	}
	store.options.Entropy = bytes.NewReader(bytes.Repeat([]byte{0x43}, 64))
	recent, _, err := store.Ingest(context.Background(), "recent.png", bytes.NewReader(encodeTransparentPNG(t, 13, 13)))
	if err != nil {
		t.Fatal(err)
	}
	setMediaClock(t, database, exact.ID, now.Add(-24*time.Hour))
	setMediaClock(t, database, recent.ID, now.Add(-24*time.Hour+time.Millisecond))

	plan, err := store.Prune(context.Background(), now, true)
	if err != nil || plan.Eligible != 1 || plan.Deleted != 0 || len(plan.Items) != 1 || plan.Items[0].MediaID != exact.ID.String() {
		t.Fatalf("prune plan = %+v, %v", plan, err)
	}
	exactPath, _ := store.resolveOriginal(exact)
	recentPath, _ := store.resolveOriginal(recent)
	if _, err := os.Stat(exactPath); err != nil {
		t.Fatal("dry-run removed exact candidate")
	}
	result, err := store.Prune(context.Background(), now, false)
	if err != nil || result.Deleted != 1 || result.BytesReclaimed != exact.ByteSize || result.CleanupFailures != 0 {
		t.Fatalf("prune result = %+v, %v", result, err)
	}
	if _, err := os.Stat(exactPath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pruned original still exists: %v", err)
	}
	if _, err := os.Stat(recentPath); err != nil {
		t.Fatalf("recent original was removed: %v", err)
	}
	if _, err := store.Get(context.Background(), exact.ID); !domain.HasCode(err, domain.CodeMediaNotFound) {
		t.Fatalf("pruned row lookup = %v", err)
	}

	store2, _, database2 := newTestStore(t, Limits{})
	value, _, err := store2.Ingest(context.Background(), "failure.png", bytes.NewReader(encodeTransparentPNG(t, 14, 14)))
	if err != nil {
		t.Fatal(err)
	}
	setMediaClock(t, database2, value.ID, now.Add(-24*time.Hour))
	original, _ := store2.resolveOriginal(value)
	injected := errors.New("injected database boundary failure")
	store2.beforePruneDelete = func() error { return injected }
	if _, err := store2.Prune(context.Background(), now, false); !errors.Is(err, injected) {
		t.Fatalf("injected prune error = %v", err)
	}
	if _, err := store2.Get(context.Background(), value.ID); err != nil {
		t.Fatalf("failed DB prune removed row: %v", err)
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatalf("failed DB prune removed original: %v", err)
	}

	store2.beforePruneDelete = nil
	store2.removeFile = func(string) error { return errors.New("injected filesystem failure") }
	failedCleanup, err := store2.Prune(context.Background(), now, false)
	if err != nil || failedCleanup.Deleted != 1 || failedCleanup.CleanupFailures != 1 || failedCleanup.BytesReclaimed != 0 {
		t.Fatalf("filesystem failure result = %+v, %v", failedCleanup, err)
	}
	if _, err := store2.Get(context.Background(), value.ID); !domain.HasCode(err, domain.CodeMediaNotFound) {
		t.Fatalf("filesystem failure left authoritative row = %v", err)
	}
	if _, err := os.Stat(original); err != nil {
		t.Fatalf("filesystem failure did not leave recoverable orphan: %v", err)
	}
}

func TestPruneSerializesLateAvatarReferenceAndNeverDeletesReachableOriginal(t *testing.T) {
	store, runtimeState, database := newTestStore(t, Limits{})
	now := time.Date(2026, 8, 12, 12, 0, 0, 0, time.UTC)
	value, _, err := store.Ingest(context.Background(), "avatar.png", bytes.NewReader(encodeTransparentPNG(t, 20, 20)))
	if err != nil {
		t.Fatal(err)
	}
	setMediaClock(t, database, value.ID, now.Add(-24*time.Hour))
	repository, err := sqlite.NewSiteRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	site, err := app.NewSiteService(repository, app.SiteOptions{AllowDevelopmentLocalHTTP: true, Clock: func() time.Time { return now }})
	if err != nil {
		t.Fatal(err)
	}
	entered := make(chan struct{})
	release := make(chan struct{})
	store.beforePruneDelete = func() error {
		close(entered)
		<-release
		return nil
	}
	pruned := make(chan error, 1)
	go func() {
		_, err := store.Prune(context.Background(), now, false)
		pruned <- err
	}()
	<-entered
	avatar := value.ID.String()
	updated := make(chan error, 1)
	go func() {
		_, err := site.Update(context.Background(), app.SiteUpdate{AvatarMediaID: &app.NullableString{Value: &avatar}})
		updated <- err
	}()
	select {
	case err := <-updated:
		t.Fatalf("late reference bypassed exclusive media lock: %v", err)
	case <-time.After(30 * time.Millisecond):
	}
	close(release)
	if err := <-pruned; err != nil {
		t.Fatal(err)
	}
	if err := <-updated; !domain.HasCode(err, domain.CodeMediaNotFound) {
		t.Fatalf("late reference after prune = %v", err)
	}

	store2, runtimeState2, database2 := newTestStore(t, Limits{})
	reachable, _, err := store2.Ingest(context.Background(), "reachable.png", bytes.NewReader(encodeTransparentPNG(t, 21, 21)))
	if err != nil {
		t.Fatal(err)
	}
	setMediaClock(t, database2, reachable.ID, now.Add(-48*time.Hour+time.Second))
	repository2, _ := sqlite.NewSiteRepository(database2, runtimeState2.Locks)
	site2, _ := app.NewSiteService(repository2, app.SiteOptions{AllowDevelopmentLocalHTTP: true, Clock: func() time.Time { return now }})
	reachableID := reachable.ID.String()
	if _, err := site2.Update(context.Background(), app.SiteUpdate{AvatarMediaID: &app.NullableString{Value: &reachableID}}); err != nil {
		t.Fatal(err)
	}
	result, err := store2.Prune(context.Background(), now, false)
	if err != nil || result.Eligible != 0 || result.Deleted != 0 {
		t.Fatalf("reachable prune = %+v, %v", result, err)
	}
	if _, err := store2.ReadOriginal(context.Background(), reachable.ID); err != nil {
		t.Fatalf("reachable original lost: %v", err)
	}
}

func setMediaClock(t *testing.T, database *sqlite.Database, id domain.ID, value time.Time) {
	t.Helper()
	if err := database.WriteTx(context.Background(), func(queries sqlite.Queries) error {
		_, err := queries.ExecContext(context.Background(), `UPDATE media SET unreferenced_since_ms = ? WHERE id = ?`, value.UnixMilli(), id.String())
		return err
	}); err != nil {
		t.Fatal(err)
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

func TestIngestRetainsThirtyMiBOriginalAndServesBoundedPreview(t *testing.T) {
	store, _, _ := newTestStore(t, Limits{})
	original := encodeJPEG(t, image.NewNRGBA(image.Rect(0, 0, 24, 12)))
	original = append(original, make([]byte, int(DefaultMaxBytes)-len(original))...)

	value, preview, err := store.Ingest(context.Background(), "large-source.jpg", bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	if value.ByteSize != int64(len(original)) || value.ByteSize <= 10<<20 {
		t.Fatalf("stored original bytes = %d, want %d and above the preview budget", value.ByteSize, len(original))
	}
	originalAsset, err := store.ReadOriginal(context.Background(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(originalAsset.Contents, original) {
		t.Fatal("stored original was recompressed or changed")
	}
	previewAsset, err := store.ReadPreview(context.Background(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(previewAsset.Contents)) > DefaultPreviewMaxBytes || bytes.Equal(previewAsset.Contents, original) {
		t.Fatalf("preview bytes = %d, budget = %d, original reused = %v", len(previewAsset.Contents), DefaultPreviewMaxBytes, bytes.Equal(previewAsset.Contents, original))
	}
	if preview.Width != 24 || preview.Height != 12 {
		t.Fatalf("preview dimensions = %dx%d, want source dimensions without upscaling", preview.Width, preview.Height)
	}
}

func TestPreviewEncodingPreservesUsefulQualityThenReducesResolutionToFit(t *testing.T) {
	t.Parallel()
	opaque := image.NewNRGBA(image.Rect(0, 0, 256, 192))
	transparent := image.NewNRGBA(image.Rect(0, 0, 256, 192))
	for y := 0; y < 192; y++ {
		for x := 0; x < 256; x++ {
			pixel := color.NRGBA{
				R: uint8(x*37 + y*17),
				G: uint8(x*11 + y*43),
				B: uint8(x*29 + y*7),
				A: 0xff,
			}
			opaque.SetNRGBA(x, y, pixel)
			pixel.A = uint8(64 + (x+y)%192)
			transparent.SetNRGBA(x, y, pixel)
		}
	}

	qualityFloor, err := encodeJPEGPreview(context.Background(), opaque, previewJPEGMinQuality)
	if err != nil {
		t.Fatal(err)
	}
	qualityBudget := int64(len(qualityFloor))
	encoded, output, mimeType, extension, err := encodePreviewWithinBudget(context.Background(), opaque, false, qualityBudget)
	if err != nil {
		t.Fatal(err)
	}
	if int64(len(encoded)) > qualityBudget || output.Bounds() != opaque.Bounds() || mimeType != "image/jpeg" || extension != "jpg" {
		t.Fatalf("quality adaptation = %d bytes %v %q .%s, budget=%d bounds=%v", len(encoded), output.Bounds(), mimeType, extension, qualityBudget, opaque.Bounds())
	}

	for name, source := range map[string]struct {
		image image.Image
		alpha bool
	}{
		"opaque":      {image: opaque, alpha: false},
		"transparent": {image: transparent, alpha: true},
	} {
		t.Run(name, func(t *testing.T) {
			const budget = int64(1 << 10)
			encoded, output, _, _, err := encodePreviewWithinBudget(context.Background(), source.image, source.alpha, budget)
			if err != nil {
				t.Fatal(err)
			}
			if int64(len(encoded)) > budget {
				t.Fatalf("preview bytes = %d, budget = %d", len(encoded), budget)
			}
			if output.Bounds().Dx() >= source.image.Bounds().Dx() || output.Bounds().Dy() >= source.image.Bounds().Dy() {
				t.Fatalf("tight budget did not reduce resolution: got %v from %v", output.Bounds(), source.image.Bounds())
			}
		})
	}
}

func TestPNGAndWebPApplyEXIFOrientation(t *testing.T) {
	t.Parallel()
	store, _, _ := newTestStore(t, Limits{})
	opaque := image.NewNRGBA(image.Rect(0, 0, 24, 12))
	for index := range opaque.Pix {
		opaque.Pix[index] = 0xff
	}
	var pngBuffer bytes.Buffer
	if err := png.Encode(&pngBuffer, opaque); err != nil {
		t.Fatal(err)
	}
	orientedPNG := injectPNGEXIFOrientation(t, pngBuffer.Bytes(), 6)
	pngMedia, pngPreview, err := store.Ingest(context.Background(), "rotated.png", bytes.NewReader(orientedPNG))
	if err != nil {
		t.Fatal(err)
	}
	if pngMedia.Width != 12 || pngMedia.Height != 24 || pngPreview.Width != 12 || pngPreview.Height != 24 {
		t.Fatalf("PNG oriented dimensions = %dx%d/%dx%d, want 12x24/12x24", pngMedia.Width, pngMedia.Height, pngPreview.Width, pngPreview.Height)
	}

	webpBytes, err := base64.StdEncoding.DecodeString(smallWebPBase64)
	if err != nil {
		t.Fatal(err)
	}
	limits, err := (Limits{}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	baseInspection, err := inspectImage(webpBytes, limits)
	if err != nil {
		t.Fatal(err)
	}
	orientedWebP := injectWebPEXIFOrientation(t, webpBytes, baseInspection.width, baseInspection.height, 6)
	webpStore, _, _ := newTestStore(t, Limits{})
	webpMedia, webpPreview, err := webpStore.Ingest(context.Background(), "rotated.webp", bytes.NewReader(orientedWebP))
	if err != nil {
		t.Fatal(err)
	}
	if webpMedia.Width != baseInspection.height || webpMedia.Height != baseInspection.width ||
		webpPreview.Width != baseInspection.height || webpPreview.Height != baseInspection.width {
		t.Fatalf("WebP oriented dimensions = %dx%d/%dx%d, want %dx%d", webpMedia.Width, webpMedia.Height,
			webpPreview.Width, webpPreview.Height, baseInspection.height, baseInspection.width)
	}
}

func TestOrientationMappingsTwoThroughEight(t *testing.T) {
	t.Parallel()
	source := image.NewNRGBA(image.Rect(0, 0, 2, 3))
	value := uint8(1)
	for y := 0; y < 3; y++ {
		for x := 0; x < 2; x++ {
			source.SetNRGBA(x, y, color.NRGBA{R: value, A: 0xff})
			value++
		}
	}
	expected := map[uint8][][]uint8{
		2: {{2, 1}, {4, 3}, {6, 5}},
		3: {{6, 5}, {4, 3}, {2, 1}},
		4: {{5, 6}, {3, 4}, {1, 2}},
		5: {{1, 3, 5}, {2, 4, 6}},
		6: {{5, 3, 1}, {6, 4, 2}},
		7: {{6, 4, 2}, {5, 3, 1}},
		8: {{2, 4, 6}, {1, 3, 5}},
	}
	for orientation, rows := range expected {
		t.Run(strconv.Itoa(int(orientation)), func(t *testing.T) {
			result, err := orient(context.Background(), source, orientation)
			if err != nil {
				t.Fatal(err)
			}
			if result.Bounds().Dy() != len(rows) || result.Bounds().Dx() != len(rows[0]) {
				t.Fatalf("bounds = %v, want %dx%d", result.Bounds(), len(rows[0]), len(rows))
			}
			for y := range rows {
				for x, want := range rows[y] {
					got, _, _, _ := result.At(x, y).RGBA()
					if uint8(got>>8) != want {
						t.Fatalf("pixel (%d,%d) = %d, want %d", x, y, uint8(got>>8), want)
					}
				}
			}
		})
	}
}

func TestGIFFirstFrameUsesLogicalCanvasAndTransparency(t *testing.T) {
	t.Parallel()
	limits, err := (Limits{}).normalized()
	if err != nil {
		t.Fatal(err)
	}
	t.Run("opaque background and offset frame", func(t *testing.T) {
		palette := color.Palette{color.NRGBA{B: 0xff, A: 0xff}, color.NRGBA{R: 0xff, A: 0xff}}
		frame := image.NewPaletted(image.Rect(50, 40, 60, 50), palette)
		for index := range frame.Pix {
			frame.Pix[index] = 1
		}
		contents := encodeGIFCanvas(t, palette, 0, frame)
		inspection, err := inspectImage(contents, limits)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeFull(context.Background(), inspection, contents)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.Bounds() != image.Rect(0, 0, 100, 100) {
			t.Fatalf("canvas bounds = %v, want 100x100", decoded.Bounds())
		}
		if background := color.NRGBAModel.Convert(decoded.At(0, 0)).(color.NRGBA); background.B != 0xff || background.A != 0xff {
			t.Fatalf("background = %#v, want opaque blue", background)
		}
		if foreground := color.NRGBAModel.Convert(decoded.At(55, 45)).(color.NRGBA); foreground.R != 0xff || foreground.A != 0xff {
			t.Fatalf("frame = %#v, want opaque red", foreground)
		}
	})
	t.Run("transparent canvas", func(t *testing.T) {
		palette := color.Palette{color.NRGBA{}, color.NRGBA{R: 0xff, A: 0xff}}
		frame := image.NewPaletted(image.Rect(20, 30, 30, 40), palette)
		frame.Pix[0] = 1
		contents := encodeGIFCanvas(t, palette, 0, frame)
		inspection, err := inspectImage(contents, limits)
		if err != nil {
			t.Fatal(err)
		}
		decoded, err := decodeFull(context.Background(), inspection, contents)
		if err != nil {
			t.Fatal(err)
		}
		if _, _, _, alpha := decoded.At(0, 0).RGBA(); alpha != 0 {
			t.Fatalf("transparent canvas alpha = %#x, want 0", alpha)
		}
		if foreground := color.NRGBAModel.Convert(decoded.At(20, 30)).(color.NRGBA); foreground.R != 0xff || foreground.A != 0xff {
			t.Fatalf("transparent frame foreground = %#v, want opaque red", foreground)
		}
	})
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
	t.Run("WebP canvas and frame dimensions", func(t *testing.T) {
		contents := encodeHeaderOnlyAlphaWebP(4, 4)
		binary.LittleEndian.PutUint16(contents[len(contents)-4:len(contents)-2], 5)
		_, err := inspectImage(contents, limits)
		assertCode(t, err, domain.CodeMediaCorrupt)
	})
	t.Run("WebP chunk padding", func(t *testing.T) {
		contents := encodeHeaderOnlyAlphaWebP(4, 4)
		contents = contents[:len(contents)-1]
		binary.LittleEndian.PutUint32(contents[4:8], uint32(len(contents)-8))
		_, err := inspectImage(contents, limits)
		assertCode(t, err, domain.CodeMediaCorrupt)
	})
	t.Run("truncated EXIF metadata is ignored safely", func(t *testing.T) {
		if orientation, ok := orientationFromEXIF([]byte{'I', 'I', 42}); ok || orientation != 0 {
			t.Fatalf("truncated EXIF orientation = %d/%v, want 0/false", orientation, ok)
		}
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

func TestAlphaWebPHeaderScanAndWeightedPreviewBudget(t *testing.T) {
	t.Parallel()
	store, _, _ := newTestStore(t, Limits{MaxDimension: 4, MaxPixels: 16, PreviewWorkers: 2})
	contents := encodeHeaderOnlyAlphaWebP(4, 4)
	inspection, err := inspectImage(contents, store.options.Limits)
	if err != nil {
		t.Fatalf("header-only alpha WebP inspection expanded pixel data: %v", err)
	}
	if inspection.format != formatWebP || inspection.width != 4 || inspection.height != 4 {
		t.Fatalf("inspection = %+v, want 4x4 WebP", inspection)
	}

	entered := make(chan struct{})
	secondEntered := make(chan struct{})
	release := make(chan struct{})
	var calls atomic.Int32
	store.generateHook = func() {
		if calls.Add(1) == 1 {
			close(entered)
		} else {
			close(secondEntered)
		}
		<-release
	}
	firstContext, cancelFirst := context.WithCancel(context.Background())
	secondContext, cancelSecond := context.WithCancel(context.Background())
	firstResult := make(chan error, 1)
	secondResult := make(chan error, 1)
	go func() {
		_, ensureErr := store.ensurePreviewBytes(firstContext, contents, inspection, strings.Repeat("a", 64))
		firstResult <- ensureErr
	}()
	<-entered
	go func() {
		_, ensureErr := store.ensurePreviewBytes(secondContext, contents, inspection, strings.Repeat("b", 64))
		secondResult <- ensureErr
	}()
	select {
	case <-secondEntered:
		t.Fatal("second maximum-size preview bypassed weighted memory budget")
	case <-time.After(100 * time.Millisecond):
	}
	cancelSecond()
	if err := <-secondResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("weighted wait cancellation = %v, want context.Canceled", err)
	}
	cancelFirst()
	close(release)
	if err := <-firstResult; !errors.Is(err, context.Canceled) {
		t.Fatalf("active preview cancellation = %v, want context.Canceled", err)
	}
	if !store.memory.TryAcquire(store.memoryCapacity) {
		t.Fatal("weighted preview budget leaked after cancellation")
	}
	store.memory.Release(store.memoryCapacity)

	tooLarge := inspection
	tooLarge.width++
	weight := previewMemoryWeight(uint64(tooLarge.width) * uint64(tooLarge.height))
	if weight <= store.memoryCapacity {
		t.Fatalf("test weight = %d, want greater than capacity %d", weight, store.memoryCapacity)
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	_, err = store.ensurePreviewBytes(ctx, contents, tooLarge, strings.Repeat("c", 64))
	assertCode(t, err, domain.CodeMediaDimensionsExceeded)
}

func TestOpenOriginalStreamsFromFileAndRetainsMediaLease(t *testing.T) {
	store, runtimeState, _ := newTestStore(t, Limits{})
	original := encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 12, 8)))
	value, _, err := store.Ingest(context.Background(), "stream.jpg", bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	asset, err := store.OpenOriginal(context.Background(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := asset.Reader.(*os.File); !ok {
		t.Fatalf("stream reader = %T, want *os.File", asset.Reader)
	}
	prefix := make([]byte, 7)
	if _, err := asset.Reader.Read(prefix); err != nil || !bytes.Equal(prefix, original[:len(prefix)]) {
		t.Fatalf("stream prefix = %x, %v", prefix, err)
	}

	blockedContext, cancelBlocked := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelBlocked()
	if lease, err := runtimeState.Locks.Acquire(blockedContext, runtimefs.MediaLock, runtimefs.Exclusive); !errors.Is(err, context.DeadlineExceeded) {
		if lease != nil {
			_ = lease.Release()
		}
		t.Fatalf("exclusive media lock while stream open = %v, want deadline", err)
	}
	if err := asset.Close(); err != nil {
		t.Fatal(err)
	}
	if err := asset.Close(); err != nil {
		t.Fatalf("idempotent close = %v", err)
	}
	availableContext, cancelAvailable := context.WithTimeout(context.Background(), time.Second)
	defer cancelAvailable()
	lease, err := runtimeState.Locks.Acquire(availableContext, runtimefs.MediaLock, runtimefs.Exclusive)
	if err != nil {
		t.Fatalf("exclusive media lock after close = %v", err)
	}
	_ = lease.Release()
}

func TestIngestPreparesDerivedMediaWithoutExclusiveMediaLock(t *testing.T) {
	store, runtimeState, _ := newTestStore(t, Limits{})
	contents := encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 64, 32)))
	entered := make(chan struct{})
	release := make(chan struct{})
	store.generateHook = func() {
		close(entered)
		<-release
	}
	result := make(chan error, 1)
	go func() {
		_, _, err := store.Ingest(context.Background(), "outside-lock.jpg", bytes.NewReader(contents))
		result <- err
	}()
	<-entered

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	lease, err := runtimeState.Locks.Acquire(ctx, runtimefs.MediaLock, runtimefs.Shared)
	if err != nil {
		t.Fatalf("shared media read during derived preparation = %v", err)
	}
	_ = lease.Release()
	close(release)
	if err := <-result; err != nil {
		t.Fatal(err)
	}
}

func TestVideoProcessorLaneBoundsConcurrentExternalWork(t *testing.T) {
	store, _, _ := newTestStore(t, Limits{VideoWorkers: 1})
	entered := make(chan struct{}, 2)
	releaseFirst := make(chan struct{})
	var calls atomic.Int32
	store.processorHook = func(kind string) {
		if kind != "video-test" {
			return
		}
		entered <- struct{}{}
		if calls.Add(1) == 1 {
			<-releaseFirst
		}
	}
	finished := make(chan struct{}, 2)
	for range 2 {
		go func() {
			release, err := store.acquireProcessor(context.Background(), store.videoWorkers, "video-test")
			if err == nil {
				release()
			}
			finished <- struct{}{}
		}()
	}
	<-entered
	select {
	case <-entered:
		t.Fatal("second video processor bypassed the configured lane")
	case <-time.After(50 * time.Millisecond):
	}
	close(releaseFirst)
	<-finished
	<-finished
	if calls.Load() != 2 {
		t.Fatalf("processor entries = %d, want 2", calls.Load())
	}
}

func TestPixelStagesHonorMidFlightCancellation(t *testing.T) {
	t.Parallel()
	opaque := image.NewNRGBA(image.Rect(0, 0, 1_000, 500))
	for index := 3; index < len(opaque.Pix); index += 4 {
		opaque.Pix[index] = 0xff
	}
	tests := []struct {
		name string
		run  func(context.Context) error
	}{
		{name: "transparency scan", run: func(ctx context.Context) error {
			_, err := imageHasTransparency(ctx, opaque)
			return err
		}},
		{name: "resize", run: func(ctx context.Context) error {
			_, err := resize(ctx, opaque, 100)
			return err
		}},
		{name: "orientation", run: func(ctx context.Context) error {
			_, err := orient(ctx, opaque, 6)
			return err
		}},
		{name: "opaque materialization", run: func(ctx context.Context) error {
			_, err := flattenOpaque(ctx, opaque)
			return err
		}},
	}
	for _, testCase := range tests {
		t.Run(testCase.name, func(t *testing.T) {
			ctx := &cancelAfterChecksContext{Context: context.Background(), remaining: 2}
			if err := testCase.run(ctx); !errors.Is(err, context.Canceled) {
				t.Fatalf("mid-flight cancellation error = %v, want context.Canceled", err)
			}
		})
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := (&contextWriter{ctx: canceled, writer: &bytes.Buffer{}}).Write([]byte("preview")); !errors.Is(err, context.Canceled) {
		t.Fatalf("encoder writer cancellation = %v, want context.Canceled", err)
	}
	if err := previewFailure(context.Canceled); !errors.Is(err, context.Canceled) {
		t.Fatalf("preview cancellation mapping = %v, want context.Canceled", err)
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
	return newTestStoreWithOptions(t, Options{Limits: limits})
}

func newTestStoreWithOptions(t *testing.T, options Options) (*Store, *runtimefs.Runtime, *sqlite.Database) {
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
	if options.Clock == nil {
		options.Clock = func() time.Time { return time.Date(2026, 8, 10, 12, 0, 0, 123_000_000, time.UTC) }
	}
	if options.Entropy == nil {
		options.Entropy = bytes.NewReader(bytes.Repeat([]byte{0x42}, 1_024))
	}
	store, err := NewStore(runtimeState, database, options)
	if err != nil {
		t.Fatal(err)
	}
	return store, runtimeState, database
}

func fileSize(t *testing.T, path string) int64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	return info.Size()
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

func encodeGIFCanvas(t *testing.T, palette color.Palette, background byte, frame *image.Paletted) []byte {
	t.Helper()
	animation := &gif.GIF{
		Image:           []*image.Paletted{frame},
		Delay:           []int{5},
		BackgroundIndex: background,
		Config: image.Config{
			ColorModel: palette,
			Width:      100,
			Height:     100,
		},
	}
	var buffer bytes.Buffer
	if err := gif.EncodeAll(&buffer, animation); err != nil {
		t.Fatal(err)
	}
	return buffer.Bytes()
}

func injectPNGEXIFOrientation(t *testing.T, pngBytes []byte, orientation uint16) []byte {
	t.Helper()
	if len(pngBytes) < 33 || !bytes.Equal(pngBytes[:8], pngSignature) {
		t.Fatal("fixture is not PNG")
	}
	chunk := pngChunk("eXIf", makeTIFFOrientation(orientation))
	result := append([]byte{}, pngBytes[:33]...)
	result = append(result, chunk...)
	return append(result, pngBytes[33:]...)
}

func pngChunk(kind string, data []byte) []byte {
	result := make([]byte, 8+len(data)+4)
	binary.BigEndian.PutUint32(result[:4], uint32(len(data)))
	copy(result[4:8], kind)
	copy(result[8:], data)
	checksum := crc32.NewIEEE()
	_, _ = checksum.Write(result[4 : 8+len(data)])
	binary.BigEndian.PutUint32(result[8+len(data):], checksum.Sum32())
	return result
}

func injectWebPEXIFOrientation(t *testing.T, webpBytes []byte, width, height uint32, orientation uint16) []byte {
	t.Helper()
	if len(webpBytes) < 20 || string(webpBytes[:4]) != "RIFF" || string(webpBytes[8:12]) != "WEBP" {
		t.Fatal("fixture is not WebP")
	}
	extended := make([]byte, 10)
	extended[0] = 0x08
	writeUint24LE(extended[4:7], width-1)
	writeUint24LE(extended[7:10], height-1)
	body := append([]byte("WEBP"), webPChunk("VP8X", extended)...)
	body = append(body, webPChunk("EXIF", makeTIFFOrientation(orientation))...)
	body = append(body, webpBytes[12:]...)
	result := make([]byte, 8, 8+len(body))
	copy(result[:4], "RIFF")
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(body)))
	return append(result, body...)
}

func encodeHeaderOnlyAlphaWebP(width, height uint32) []byte {
	extended := make([]byte, 10)
	extended[0] = 0x10
	writeUint24LE(extended[4:7], width-1)
	writeUint24LE(extended[7:10], height-1)
	alpha := make([]byte, 1+width*height)
	frame := make([]byte, 10)
	copy(frame[3:6], []byte{0x9d, 0x01, 0x2a})
	binary.LittleEndian.PutUint16(frame[6:8], uint16(width))
	binary.LittleEndian.PutUint16(frame[8:10], uint16(height))
	body := append([]byte("WEBP"), webPChunk("VP8X", extended)...)
	body = append(body, webPChunk("ALPH", alpha)...)
	body = append(body, webPChunk("VP8 ", frame)...)
	result := make([]byte, 8, 8+len(body))
	copy(result[:4], "RIFF")
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(body)))
	return append(result, body...)
}

func webPChunk(kind string, data []byte) []byte {
	result := make([]byte, 8, 8+len(data)+len(data)%2)
	copy(result[:4], kind)
	binary.LittleEndian.PutUint32(result[4:8], uint32(len(data)))
	result = append(result, data...)
	if len(data)%2 != 0 {
		result = append(result, 0)
	}
	return result
}

func writeUint24LE(destination []byte, value uint32) {
	destination[0] = byte(value)
	destination[1] = byte(value >> 8)
	destination[2] = byte(value >> 16)
}

func injectEXIFOrientation(t *testing.T, jpegBytes []byte, orientation uint16) []byte {
	t.Helper()
	if len(jpegBytes) < 2 || jpegBytes[0] != 0xff || jpegBytes[1] != 0xd8 {
		t.Fatal("fixture is not JPEG")
	}
	tiff := makeTIFFOrientation(orientation)
	segment := append([]byte("Exif\x00\x00"), tiff...)
	marker := []byte{0xff, 0xe1, byte((len(segment) + 2) >> 8), byte(len(segment) + 2)}
	result := append([]byte{}, jpegBytes[:2]...)
	result = append(result, marker...)
	result = append(result, segment...)
	return append(result, jpegBytes[2:]...)
}

func makeTIFFOrientation(orientation uint16) []byte {
	tiff := make([]byte, 26)
	copy(tiff[:2], "II")
	binary.LittleEndian.PutUint16(tiff[2:4], 42)
	binary.LittleEndian.PutUint32(tiff[4:8], 8)
	binary.LittleEndian.PutUint16(tiff[8:10], 1)
	binary.LittleEndian.PutUint16(tiff[10:12], 0x0112)
	binary.LittleEndian.PutUint16(tiff[12:14], 3)
	binary.LittleEndian.PutUint32(tiff[14:18], 1)
	binary.LittleEndian.PutUint16(tiff[18:20], orientation)
	return tiff
}

type cancelAfterChecksContext struct {
	context.Context
	remaining int
}

func (ctx *cancelAfterChecksContext) Err() error {
	ctx.remaining--
	if ctx.remaining <= 0 {
		return context.Canceled
	}
	return nil
}

func assertCode(t *testing.T, err error, code domain.Code) {
	t.Helper()
	if !domain.HasCode(err, code) {
		t.Fatalf("error = %v, want code %s", err, code)
	}
}
