package media

import (
	"bytes"
	"context"
	"image"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/moment/moment/internal/domain"
)

func TestConvertHEICRejectsPartialFFmpegImage(t *testing.T) {
	directory := t.TempDir()
	jpegPath := filepath.Join(directory, "tile.jpg")
	if err := os.WriteFile(jpegPath, encodeJPEG(t, image.NewRGBA(image.Rect(0, 0, 512, 512))), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("MOMENT_TEST_HEIC_JPEG", jpegPath)
	ffmpeg := heicTestTool(t, "ffmpeg", "for last do :; done\ncp \"$MOMENT_TEST_HEIC_JPEG\" \"$last\"\n")
	ffprobe := heicTestTool(t, "ffprobe", "printf '%s\\n' '{\"stream_groups\":[{\"type\":\"Tile Grid\",\"disposition\":{\"default\":1},\"components\":[{\"width\":4032,\"height\":3024}]}]}'\n")
	store, _, _ := newTestStoreWithOptions(t, Options{
		HEIFConvertPath: filepath.Join(directory, "missing-heif-convert"),
		SIPSPath:        filepath.Join(directory, "missing-sips"),
		FFmpegPath:      ffmpeg,
		FFprobePath:     ffprobe,
	})
	_, err := store.convertHEIC(context.Background(), []byte("synthetic-heic"))
	assertCode(t, err, domain.CodeMediaPreviewFailed)
}

func TestHEICPrimaryDimensionsIdentifyCompleteImage(t *testing.T) {
	for _, scenario := range []struct {
		name   string
		probe  string
		width  uint32
		height uint32
		code   domain.Code
	}{
		{
			name:   "primary grid after auxiliary grid",
			probe:  `{"stream_groups":[{"type":"Tile Grid","disposition":{"default":0},"components":[{"width":2016,"height":1512}]},{"type":"Tile Grid","disposition":{"default":1},"components":[{"width":4032,"height":3024}]}],"streams":[{"codec_type":"video","width":512,"height":512,"disposition":{"dependent":1}}]}`,
			width:  4032,
			height: 3024,
		},
		{
			name:   "untiled primary stream",
			probe:  `{"streams":[{"codec_type":"video","width":320,"height":240,"disposition":{"default":0}},{"codec_type":"video","width":4032,"height":3024,"disposition":{"default":1}}]}`,
			width:  4032,
			height: 3024,
		},
		{
			name:   "single untiled stream",
			probe:  `{"streams":[{"codec_type":"video","width":24,"height":12}]}`,
			width:  24,
			height: 12,
		},
		{
			name:  "do not accept thumbnail when grid primary is unknown",
			probe: `{"stream_groups":[{"type":"Tile Grid","components":[{"width":4032,"height":3024}]}],"streams":[{"codec_type":"video","width":320,"height":240,"disposition":{"default":1}}]}`,
			code:  domain.CodeMediaPreviewFailed,
		},
		{
			name:  "dependent tile is not a single primary image",
			probe: `{"streams":[{"codec_type":"video","width":512,"height":512,"disposition":{"dependent":1}}]}`,
			code:  domain.CodeMediaPreviewFailed,
		},
		{
			name:  "missing source geometry",
			probe: `{}`,
			code:  domain.CodeMediaPreviewFailed,
		},
		{
			name:  "oversize primary image is rejected before conversion",
			probe: `{"stream_groups":[{"type":"Tile Grid","disposition":{"default":1},"components":[{"width":20000,"height":3024}]}]}`,
			code:  domain.CodeMediaDimensionsExceeded,
		},
		{
			name:  "ambiguous primary groups",
			probe: `{"stream_groups":[{"type":"Tile Grid","disposition":{"default":1},"components":[{"width":4032,"height":3024}]},{"type":"Tile Grid","disposition":{"default":1},"components":[{"width":2016,"height":1512}]}]}`,
			code:  domain.CodeMediaPreviewFailed,
		},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			probe := heicTestTool(t, "ffprobe", "printf '%s\\n' '"+scenario.probe+"'\n")
			store, _, _ := newTestStoreWithOptions(t, Options{FFprobePath: probe})
			dimensions, err := store.probeHEICPrimaryDimensions(context.Background(), "original.heic")
			if scenario.code != "" {
				assertCode(t, err, scenario.code)
				return
			}
			if err != nil || dimensions.Width != scenario.width || dimensions.Height != scenario.height {
				t.Fatalf("primary dimensions = %+v, %v", dimensions, err)
			}
		})
	}
}

func TestIngestHEICRealSIPSFallbackPreservesPrimaryImage(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("sips is a macOS image processor")
	}
	fixture := os.Getenv("MOMENT_TEST_HEIC_FALLBACK_FIXTURE")
	if fixture == "" {
		t.Skip("set MOMENT_TEST_HEIC_FALLBACK_FIXTURE to the IMG_0650.HEIC portrait fixture")
	}
	contents, err := os.ReadFile(fixture)
	if err != nil {
		t.Fatal(err)
	}
	store, _, _ := newTestStoreWithOptions(t, Options{
		Limits:          Limits{PreviewMaxEdge: 800},
		HEIFConvertPath: filepath.Join(t.TempDir(), "missing-heif-convert"),
		SIPSPath:        "/usr/bin/sips",
		FFmpegPath:      filepath.Join(t.TempDir(), "missing-ffmpeg"),
	})
	value, _, err := store.Ingest(context.Background(), "IMG_0650.HEIC", bytes.NewReader(contents))
	if err != nil {
		t.Fatal(err)
	}
	if value.Width != 3024 || value.Height != 4032 {
		t.Fatalf("HEIC dimensions = %dx%d, want complete display-oriented 3024x4032", value.Width, value.Height)
	}
	original, err := store.ReadOriginal(context.Background(), value.ID)
	if err != nil || !bytes.Equal(original.Contents, contents) {
		t.Fatalf("HEIC fallback changed original: %v", err)
	}
	preview, err := store.ReadPreview(context.Background(), value.ID)
	if err != nil {
		t.Fatal(err)
	}
	decoded, err := inspectImage(preview.Contents, store.options.Limits)
	if err != nil {
		t.Fatal(err)
	}
	if decoded.width != 600 || decoded.height != 800 || len(decoded.iccProfile) == 0 {
		t.Fatalf("HEIC fallback preview = %dx%d, ICC bytes = %d", decoded.width, decoded.height, len(decoded.iccProfile))
	}
	if ffmpeg, ffprobe := os.Getenv("MOMENT_TEST_FFMPEG"), os.Getenv("MOMENT_TEST_FFPROBE"); ffmpeg != "" && ffprobe != "" {
		store.options.SIPSPath = filepath.Join(t.TempDir(), "missing-sips")
		store.options.FFmpegPath = ffmpeg
		store.options.FFprobePath = ffprobe
		dimensions, err := store.probeHEICPrimaryDimensions(context.Background(), fixture)
		if err != nil || dimensions.Width != 4032 || dimensions.Height != 3024 {
			t.Fatalf("real HEIC primary dimensions = %+v, %v", dimensions, err)
		}
		converted, err := store.convertHEIC(context.Background(), contents)
		if err != nil {
			assertCode(t, err, domain.CodeMediaPreviewFailed)
			return
		}
		decoded, err := inspectImage(converted, store.options.Limits)
		if err != nil {
			t.Fatal(err)
		}
		if decoded.width*decoded.height != 4032*3024 {
			t.Fatalf("FFmpeg returned an incomplete primary image: %dx%d", decoded.width, decoded.height)
		}
	}
}

func heicTestTool(t *testing.T, name, script string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}
