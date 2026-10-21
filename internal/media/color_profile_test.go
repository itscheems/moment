package media

import (
	"bytes"
	"compress/zlib"
	"context"
	"encoding/base64"
	"encoding/binary"
	"image"
	"image/color"
	"os"
	"testing"

	"github.com/moment/moment/internal/domain"
)

// The synthetic profile exercises metadata transport and segment boundaries;
// real iPhone profiles are covered by the processor integration tests below.
func testICC(size int) []byte {
	p := make([]byte, max(132, size))
	binary.BigEndian.PutUint32(p[:4], uint32(len(p)))
	copy(p[16:20], "RGB ")
	copy(p[36:40], "acsp")
	return p
}

func TestImagePreviewsRetainRGBProfilesWithoutCopyingPrivateMetadata(t *testing.T) {
	for _, alpha := range []bool{false, true} {
		t.Run(map[bool]string{false: "JPEG", true: "PNG"}[alpha], func(t *testing.T) {
			profile := testICC(150_000) // multiple JPEG APP2 segments
			metadata, err := encodeICCMetadata(profile, alpha)
			if err != nil {
				t.Fatal(err)
			}
			var original []byte
			if alpha {
				original = encodeTransparentPNG(t, 24, 12)
			} else {
				pixels := image.NewRGBA(image.Rect(0, 0, 24, 12))
				for y := 0; y < 12; y++ {
					for x := 0; x < 24; x++ {
						pixels.Set(x, y, color.NRGBA{R: 25, G: 90, B: 180, A: 255})
					}
				}
				original = encodeJPEG(t, pixels)
				// An unrelated APP1 payload must not follow the color profile.
				private := []byte("Exif\x00\x00private-camera-data")
				segment := []byte{0xff, 0xe1, 0, byte(len(private) + 2)}
				segment = append(segment, private...)
				original = append(append(append([]byte{}, original[:2]...), segment...), original[2:]...)
			}
			original = insertICCMetadata(original, metadata, alpha)
			store, _, _ := newTestStore(t, Limits{})
			value, preview, err := store.Ingest(context.Background(), "profile", bytes.NewReader(original))
			if err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(preview.Path)
			if err != nil {
				t.Fatal(err)
			}
			inspection, err := inspectImage(got, store.options.Limits)
			if err != nil || !bytes.Equal(inspection.iccProfile, profile) {
				t.Fatalf("preview lost profile: %v", err)
			}
			if bytes.Contains(got, []byte("private-camera-data")) {
				t.Fatal("preview copied unrelated source metadata")
			}
			asset, err := store.ReadOriginal(context.Background(), value.ID)
			if err != nil || !bytes.Equal(asset.Contents, original) {
				t.Fatal("immutable original changed")
			}
		})
	}
}

func TestWebPProfileFollowsPixelsIntoJPEGPreview(t *testing.T) {
	plain, err := base64.StdEncoding.DecodeString(smallWebPBase64)
	if err != nil {
		t.Fatal(err)
	}
	info, err := inspectWebP(plain)
	if err != nil {
		t.Fatal(err)
	}
	profile := testICC(536)
	var body bytes.Buffer
	body.WriteString("WEBPVP8X")
	_ = binary.Write(&body, binary.LittleEndian, uint32(10))
	extended := make([]byte, 10)
	extended[0] = 0x20
	for i := 0; i < 3; i++ {
		extended[4+i], extended[7+i] = byte((info.width-1)>>uint(i*8)), byte((info.height-1)>>uint(i*8))
	}
	body.Write(extended)
	body.WriteString("ICCP")
	_ = binary.Write(&body, binary.LittleEndian, uint32(len(profile)))
	body.Write(profile)
	body.Write(plain[12:])
	original := []byte("RIFF")
	original = binary.LittleEndian.AppendUint32(original, uint32(body.Len()))
	original = append(original, body.Bytes()...)
	store, _, _ := newTestStore(t, Limits{})
	_, preview, err := store.Ingest(context.Background(), "profile.webp", bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(preview.Path)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := inspectImage(got, store.options.Limits)
	if err != nil || !bytes.Equal(decoded.iccProfile, profile) {
		t.Fatalf("WebP profile did not reach preview: %v", err)
	}
}

func TestColorProfileCountsAgainstPreviewByteBudget(t *testing.T) {
	profile := testICC(4096)
	metadata, err := encodeICCMetadata(profile, false)
	if err != nil {
		t.Fatal(err)
	}
	original := insertICCMetadata(encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 8, 8))), metadata, false)
	store, _, _ := newTestStore(t, Limits{PreviewMaxBytes: 1024})
	if _, _, err := store.Ingest(context.Background(), "profile.jpg", bytes.NewReader(original)); !domain.HasCode(err, domain.CodeMediaPreviewFailed) {
		t.Fatalf("oversized metadata escaped byte budget: %v", err)
	}
}

func TestColorProfileRejectsIncompleteAndOversizedMetadata(t *testing.T) {
	for _, parts := range [][][]byte{
		{[]byte("ICC_PROFILE\x00\x01\x02fragment")},
		{[]byte("ICC_PROFILE\x00\x01\x01fragment"), []byte("ICC_PROFILE\x00\x01\x01fragment")},
		{[]byte("ICC_PROFILE\x00\x00\x01fragment")},
	} {
		var p jpegICCParts
		var err error
		for _, part := range parts {
			if err = p.add(part); err != nil {
				break
			}
		}
		if err == nil {
			_, err = p.profile()
		}
		if err == nil {
			t.Fatal("invalid ICC segment sequence was accepted")
		}
	}
	var payload bytes.Buffer
	payload.WriteString("profile\x00\x00")
	compressed := zlib.NewWriter(&payload)
	_, _ = compressed.Write(make([]byte, maxICCProfileBytes+1))
	_ = compressed.Close()
	if _, err := decodePNGICC(payload.Bytes()); err == nil {
		t.Fatal("compressed profile exceeded the decompression budget")
	}
	profile := testICC(132)
	binary.BigEndian.PutUint32(profile[128:132], ^uint32(0))
	if err := validateICC(profile); err == nil {
		t.Fatal("overflowing tag table was accepted")
	}
}

func TestIngestColorProfileRealImage(t *testing.T) {
	path := os.Getenv("MOMENT_TEST_COLOR_IMAGE")
	if path == "" {
		t.Skip("set MOMENT_TEST_COLOR_IMAGE to an image with an embedded RGB profile")
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	store, _, _ := newTestStoreWithOptions(t, Options{})
	input, err := store.inspectMedia(context.Background(), original)
	if err != nil || len(input.iccProfile) == 0 || string(input.iccProfile[16:20]) != "RGB " {
		t.Fatalf("fixture has no decoded RGB profile: %v", err)
	}
	value, preview, err := store.Ingest(context.Background(), "color-image", bytes.NewReader(original))
	if err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(preview.Path)
	if err != nil {
		t.Fatal(err)
	}
	output, err := inspectImage(contents, store.options.Limits)
	if err != nil || !bytes.Equal(input.iccProfile, output.iccProfile) {
		t.Fatalf("real image lost its RGB profile: %v", err)
	}
	asset, err := store.ReadOriginal(context.Background(), value.ID)
	if err != nil || !bytes.Equal(asset.Contents, original) {
		t.Fatal("original changed during color-managed preview generation")
	}
}
