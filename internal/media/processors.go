package media

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/moment/moment/internal/domain"
)

type probeDocument struct {
	Streams []struct {
		Width       uint32 `json:"width"`
		Height      uint32 `json:"height"`
		Duration    string `json:"duration"`
		Codec       string `json:"codec_name"`
		PixelFormat string `json:"pix_fmt"`
		ColorRange  string `json:"color_range"`
		ColorSpace  string `json:"color_space"`
		ColorTRC    string `json:"color_transfer"`
		ColorPrime  string `json:"color_primaries"`
		Tags        struct {
			Rotate string `json:"rotate"`
		} `json:"tags"`
		SideData []struct {
			Rotation float64 `json:"rotation"`
		} `json:"side_data_list"`
	} `json:"streams"`
	Format struct {
		Duration string `json:"duration"`
	} `json:"format"`
}

func detectExtendedFormat(contents []byte) format {
	if len(contents) < 16 || string(contents[4:8]) != "ftyp" {
		return 0
	}
	brands := make([]string, 0, (len(contents)-8)/4)
	for offset := 8; offset+4 <= len(contents) && offset < 64; offset += 4 {
		brands = append(brands, string(contents[offset:offset+4]))
	}
	for _, brand := range brands {
		switch brand {
		case "heic", "heix", "hevc", "hevx", "heim", "heis":
			return formatHEIC
		}
	}
	for _, brand := range brands {
		switch brand {
		case "isom", "iso2", "mp41", "mp42", "avc1", "M4V ", "MSNV":
			return formatMP4
		case "qt  ":
			return formatQuickTime
		}
	}
	return 0
}

func (store *Store) inspectMedia(ctx context.Context, contents []byte) (inspected, error) {
	if value, err := inspectImage(contents, store.options.Limits); err == nil {
		return value, nil
	} else if detectExtendedFormat(contents) == 0 {
		return inspected{}, err
	}
	switch detectExtendedFormat(contents) {
	case formatHEIC:
		converted, err := store.convertHEIC(ctx, contents)
		if err != nil {
			return inspected{}, err
		}
		decoded, err := inspectImage(converted, store.options.Limits)
		if err != nil {
			return inspected{}, domain.NewError(domain.CodeMediaCorrupt, "HEIC decoder produced an invalid image", nil)
		}
		decoded.format = formatHEIC
		decoded.decodedSource = converted
		return decoded, nil
	case formatMP4, formatQuickTime:
		return store.probeVideoBytes(ctx, contents, detectExtendedFormat(contents))
	default:
		return inspected{}, domain.NewError(domain.CodeMediaTypeUnsupported, "media signature is not supported", nil)
	}
}

func (store *Store) toolPath(configured, name string) (string, error) {
	if configured != "" {
		info, err := os.Stat(configured)
		if err == nil && info.Mode().IsRegular() && info.Mode()&0o111 != 0 {
			return configured, nil
		}
		return "", processorUnavailable(name)
	}
	path, err := exec.LookPath(name)
	if err != nil {
		return "", processorUnavailable(name)
	}
	return path, nil
}

func processorUnavailable(name string) error {
	return domain.NewError(domain.CodeMediaProcessorUnavailable, "required media processor is unavailable", map[string]any{"processor": name})
}

func isProcessorUnavailable(err error) bool {
	var domainErr *domain.Error
	return errors.As(err, &domainErr) && domainErr.Code == domain.CodeMediaProcessorUnavailable
}

func (store *Store) noteProcessor(kind string) {
	if store.processorHook != nil {
		store.processorHook(kind)
	}
}

func (store *Store) acquireProcessor(ctx context.Context, lane chan struct{}, kind string) (func(), error) {
	select {
	case lane <- struct{}{}:
		store.noteProcessor(kind)
		return func() { <-lane }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (store *Store) runTool(ctx context.Context, configured, name string, arguments ...string) ([]byte, error) {
	path, err := store.toolPath(configured, name)
	if err != nil {
		return nil, err
	}
	commandCtx, cancel := context.WithTimeout(ctx, store.options.ProcessorTimeout)
	defer cancel()
	command := exec.CommandContext(commandCtx, path, arguments...)
	command.Env = append(os.Environ(), "LC_ALL=C", "LANG=C")
	var stderr bytes.Buffer
	command.Stderr = &stderr
	output, err := command.Output()
	if commandCtx.Err() != nil {
		return nil, domain.NewError(domain.CodeMediaPreviewFailed, "media processor timed out", map[string]any{"processor": name})
	}
	if err != nil {
		message := strings.TrimSpace(stderr.String())
		if len(message) > 240 {
			message = message[:240]
		}
		return nil, domain.NewError(domain.CodeMediaCorrupt, "media processor rejected the file", map[string]any{"processor": name, "diagnostic": message})
	}
	return output, nil
}

func (store *Store) privateTempDir() (string, error) {
	directory, err := os.MkdirTemp("", "moment-media-*")
	if err != nil {
		return "", err
	}
	if err := os.Chmod(directory, 0o700); err != nil {
		_ = os.RemoveAll(directory)
		return "", err
	}
	return directory, nil
}

func writePrivateInput(directory, name string, contents []byte) (string, error) {
	path := filepath.Join(directory, name)
	if err := os.WriteFile(path, contents, 0o600); err != nil {
		return "", err
	}
	return path, nil
}

func (store *Store) convertHEIC(ctx context.Context, contents []byte) ([]byte, error) {
	release, err := store.acquireProcessor(ctx, store.imageWorkers, "heic")
	if err != nil {
		return nil, err
	}
	defer release()
	directory, err := store.privateTempDir()
	if err != nil {
		return nil, err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	input, err := writePrivateInput(directory, "original.heic", contents)
	if err != nil {
		return nil, err
	}
	output := filepath.Join(directory, "decoded.jpg")
	if _, err := store.runTool(ctx, store.options.HEIFConvertPath, "heif-convert", "-q", "95", input, output); err != nil {
		if !isProcessorUnavailable(err) {
			return nil, err
		}
		if _, fallbackErr := store.runTool(
			ctx,
			store.options.FFmpegPath,
			"ffmpeg",
			"-nostdin", "-y", "-v", "error",
			"-i", input,
			"-frames:v", "1",
			"-q:v", "2",
			output,
		); fallbackErr != nil {
			return nil, fallbackErr
		}
	}
	converted, err := os.ReadFile(output)
	if err != nil || len(converted) == 0 || int64(len(converted)) > store.options.Limits.MaxImageBytes {
		return nil, domain.NewError(domain.CodeMediaCorrupt, "HEIC conversion did not produce a bounded image", nil)
	}
	return converted, nil
}

func (store *Store) probeVideoBytes(ctx context.Context, contents []byte, sourceFormat format) (inspected, error) {
	directory, err := store.privateTempDir()
	if err != nil {
		return inspected{}, err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	input, err := writePrivateInput(directory, "original."+sourceFormat.extension(), contents)
	if err != nil {
		return inspected{}, err
	}
	return store.probeVideoFileAs(ctx, input, sourceFormat)
}

func (store *Store) probeVideoFile(ctx context.Context, input string) (inspected, error) {
	return store.probeVideoFileAs(ctx, input, formatMP4)
}

func (store *Store) probeVideoFileAs(ctx context.Context, input string, sourceFormat format) (inspected, error) {
	release, err := store.acquireProcessor(ctx, store.videoWorkers, "ffprobe")
	if err != nil {
		return inspected{}, err
	}
	defer release()
	return store.probeVideoFileUnlocked(ctx, input, sourceFormat)
}

func (store *Store) probeVideoFileUnlocked(ctx context.Context, input string, sourceFormat format) (inspected, error) {
	output, err := store.runTool(ctx, store.options.FFprobePath, "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height,duration,codec_name,pix_fmt,color_range,color_space,color_transfer,color_primaries:stream_tags=rotate:stream_side_data=rotation:format=duration", "-of", "json", input)
	if err != nil {
		return inspected{}, err
	}
	var document probeDocument
	decoder := json.NewDecoder(bytes.NewReader(output))
	if err := decoder.Decode(&document); err != nil || len(document.Streams) != 1 {
		return inspected{}, domain.NewError(domain.CodeMediaCorrupt, "video probe did not return one video stream", nil)
	}
	stream := document.Streams[0]
	if stream.Width == 0 || stream.Height == 0 {
		return inspected{}, domain.NewError(domain.CodeMediaCorrupt, "video dimensions are missing", nil)
	}
	width, height := stream.Width, stream.Height
	rotation := 0.0
	for _, sideData := range stream.SideData {
		if sideData.Rotation != 0 {
			rotation = sideData.Rotation
			break
		}
	}
	if rotation == 0 && stream.Tags.Rotate != "" {
		rotation, _ = strconv.ParseFloat(stream.Tags.Rotate, 64)
	}
	quarterTurns := int(rotation/90) % 4
	if quarterTurns < 0 {
		quarterTurns += 4
	}
	if quarterTurns == 1 || quarterTurns == 3 {
		width, height = height, width
	}
	if err := validateDimensions(width, height, store.options.Limits); err != nil {
		return inspected{}, err
	}
	durationText := stream.Duration
	if durationText == "" || durationText == "N/A" {
		durationText = document.Format.Duration
	}
	duration, err := strconv.ParseFloat(durationText, 64)
	if err != nil || duration <= 0 {
		return inspected{}, domain.NewError(domain.CodeMediaCorrupt, "video duration is invalid", nil)
	}
	if duration > DefaultMaxVideoDuration.Seconds() {
		return inspected{}, domain.NewError(domain.CodeMediaTooLarge, "video exceeds the supported duration", map[string]any{"max_duration_seconds": int(DefaultMaxVideoDuration.Seconds())})
	}
	return inspected{
		format:          sourceFormat,
		width:           width,
		height:          height,
		durationSeconds: duration,
		codec:           stream.Codec,
		pixelFormat:     stream.PixelFormat,
		colorRange:      stream.ColorRange,
		colorSpace:      stream.ColorSpace,
		colorTransfer:   stream.ColorTRC,
		colorPrimaries:  stream.ColorPrime,
		rotationDegrees: int(rotation),
	}, nil
}

func (value inspected) isHDRVideo() bool {
	return value.colorPrimaries == "bt2020" || value.colorTransfer == "arib-std-b67" || value.colorTransfer == "smpte2084"
}

type videoFilterPlan struct {
	inputArguments []string
	filter         string
}

func scaledEvenDimensions(width, height, maxEdge uint32) (uint32, uint32) {
	if width == 0 || height == 0 || maxEdge == 0 {
		return width, height
	}
	if width <= maxEdge && height <= maxEdge {
		return width - width%2, height - height%2
	}
	scale := float64(maxEdge) / float64(max(width, height))
	scaledWidth := max(uint32(2), uint32(float64(width)*scale))
	scaledHeight := max(uint32(2), uint32(float64(height)*scale))
	return scaledWidth - scaledWidth%2, scaledHeight - scaledHeight%2
}

func videoToolboxRotationFilter(rotation int) string {
	switch ((rotation % 360) + 360) % 360 {
	case 270:
		return "transpose_vt=clock,"
	case 90:
		return "transpose_vt=cclock,"
	case 180:
		return "transpose_vt=reversal,"
	default:
		return ""
	}
}

func videoDisplayFilterPlan(inspection inspected, maxEdge uint32, goos string) videoFilterPlan {
	scale := fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2", maxEdge, maxEdge)
	if !inspection.isHDRVideo() {
		return videoFilterPlan{filter: scale + ",setsar=1"}
	}
	if goos == "darwin" {
		width, height := scaledEvenDimensions(inspection.width, inspection.height, maxEdge)
		filter := videoToolboxRotationFilter(inspection.rotationDegrees) + fmt.Sprintf(
			"scale_vt=w=%d:h=%d:color_matrix=bt709:color_primaries=bt709:color_transfer=bt709,hwdownload,format=p010le,format=yuv420p,setsar=1",
			width,
			height,
		)
		return videoFilterPlan{
			inputArguments: []string{"-hwaccel", "videotoolbox", "-hwaccel_output_format", "videotoolbox_vld"},
			filter:         filter,
		}
	}
	return videoFilterPlan{filter: "zscale=transfer=linear:npl=100,format=gbrpf32le,tonemap=mobius:desat=0,zscale=primaries=bt709:transfer=bt709:matrix=bt709:range=limited,format=yuv420p," + scale + ",setsar=1"}
}

func (store *Store) transcodeVideoFile(ctx context.Context, input string, inspection inspected, output string) (inspected, error) {
	plan := videoDisplayFilterPlan(inspection, store.options.Limits.PreviewMaxEdge, runtime.GOOS)
	targetBits := float64(store.options.Limits.PreviewMaxBytes-256*1024) * 8
	videoRate := int64(targetBits/inspection.durationSeconds) - 96_000
	videoRate = max(96_000, min(videoRate, 4_000_000))
	attempts := [][]string{
		{"-crf", "23"},
		{"-crf", "28"},
		{"-b:v", strconv.FormatInt(videoRate, 10), "-maxrate", strconv.FormatInt(videoRate, 10), "-bufsize", strconv.FormatInt(videoRate*2, 10)},
	}
	var lastErr error
	for index, rateArguments := range attempts {
		attempt := fmt.Sprintf("%s.attempt-%d.mp4", output, index+1)
		arguments := []string{"-nostdin", "-y", "-v", "error"}
		arguments = append(arguments, plan.inputArguments...)
		arguments = append(arguments, "-i", input, "-map", "0:v:0", "-map", "0:a?", "-map_metadata", "-1", "-vf", plan.filter, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "medium")
		arguments = append(arguments, rateArguments...)
		arguments = append(arguments, "-c:a", "aac", "-b:a", "96k", "-color_primaries", "bt709", "-color_trc", "bt709", "-colorspace", "bt709", "-color_range", "tv", "-movflags", "+faststart", attempt)
		if _, err := store.runTool(ctx, store.options.FFmpegPath, "ffmpeg", arguments...); err != nil {
			lastErr = err
			continue
		}
		derived, err := store.validateDerivedVideo(ctx, attempt, inspection)
		if err != nil {
			lastErr = err
			_ = os.Remove(attempt)
			continue
		}
		if err := os.Rename(attempt, output); err != nil {
			return inspected{}, err
		}
		return derived, nil
	}
	if lastErr != nil {
		return inspected{}, lastErr
	}
	return inspected{}, domain.NewError(domain.CodeMediaPreviewFailed, "video display version exceeds the byte budget", map[string]any{"max_bytes": store.options.Limits.PreviewMaxBytes})
}

func (store *Store) validateDerivedVideo(ctx context.Context, output string, source inspected) (inspected, error) {
	info, err := os.Stat(output)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > store.options.Limits.PreviewMaxBytes {
		return inspected{}, domain.NewError(domain.CodeMediaPreviewFailed, "video display version exceeds the byte budget", map[string]any{"max_bytes": store.options.Limits.PreviewMaxBytes})
	}
	derived, err := store.probeVideoFileUnlocked(ctx, output, formatMP4)
	if err != nil {
		return inspected{}, err
	}
	if derived.codec != "h264" || derived.pixelFormat != "yuv420p" || derived.width > store.options.Limits.PreviewMaxEdge || derived.height > store.options.Limits.PreviewMaxEdge {
		return inspected{}, domain.NewError(domain.CodeMediaPreviewFailed, "video display version is not browser compatible", nil)
	}
	if source.isHDRVideo() && (derived.colorPrimaries != "bt709" || derived.colorTransfer != "bt709" || derived.colorSpace != "bt709") {
		return inspected{}, domain.NewError(domain.CodeMediaPreviewFailed, "HDR video display version is not tagged as SDR BT.709", nil)
	}
	tolerance := max(1.0, source.durationSeconds*0.03)
	if difference := derived.durationSeconds - source.durationSeconds; difference > tolerance || difference < -tolerance {
		return inspected{}, domain.NewError(domain.CodeMediaPreviewFailed, "video display duration does not match the original", nil)
	}
	header, err := os.ReadFile(output)
	if err != nil || !mp4HasFastStart(header) {
		return inspected{}, domain.NewError(domain.CodeMediaPreviewFailed, "video display version is not optimized for progressive playback", nil)
	}
	return derived, nil
}

func mp4HasFastStart(contents []byte) bool {
	moov := bytes.Index(contents, []byte("moov"))
	mdat := bytes.Index(contents, []byte("mdat"))
	return moov >= 0 && mdat >= 0 && moov < mdat
}

func videoPosterTime(durationSeconds float64) float64 {
	latestUsableFrame := max(0, durationSeconds-0.05)
	return min(3, max(0.5, durationSeconds*0.1), latestUsableFrame)
}

func (store *Store) generateVideoPosterFile(ctx context.Context, input, output string, inspection inspected) error {
	plan := videoDisplayFilterPlan(inspection, store.options.Limits.PreviewMaxEdge, runtime.GOOS)
	seek := strconv.FormatFloat(videoPosterTime(inspection.durationSeconds), 'f', 3, 64)
	arguments := []string{"-nostdin", "-y", "-v", "error"}
	arguments = append(arguments, plan.inputArguments...)
	arguments = append(arguments, "-ss", seek, "-i", input, "-frames:v", "1", "-vf", plan.filter, "-q:v", "3", output)
	_, err := store.runTool(ctx, store.options.FFmpegPath, "ffmpeg", arguments...)
	return err
}

func verifyExtendedSignature(contents []byte, value Media) error {
	detected := detectExtendedFormat(contents)
	if detected == 0 || detected.mimeType() != value.MIMEType {
		return errors.New("extended media signature does not match metadata")
	}
	return nil
}
