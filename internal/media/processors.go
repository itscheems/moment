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
	"strconv"
	"strings"

	"github.com/moment/moment/internal/domain"
)

type probeDocument struct {
	Streams []struct {
		Width    uint32 `json:"width"`
		Height   uint32 `json:"height"`
		Duration string `json:"duration"`
		Codec    string `json:"codec_name"`
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
	case formatMP4:
		return store.probeVideoBytes(ctx, contents)
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
		return nil, err
	}
	converted, err := os.ReadFile(output)
	if err != nil || len(converted) == 0 || int64(len(converted)) > store.options.Limits.MaxBytes {
		return nil, domain.NewError(domain.CodeMediaCorrupt, "HEIC conversion did not produce a bounded image", nil)
	}
	return converted, nil
}

func (store *Store) probeVideoBytes(ctx context.Context, contents []byte) (inspected, error) {
	directory, err := store.privateTempDir()
	if err != nil {
		return inspected{}, err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	input, err := writePrivateInput(directory, "original.mp4", contents)
	if err != nil {
		return inspected{}, err
	}
	return store.probeVideoFile(ctx, input)
}

func (store *Store) probeVideoFile(ctx context.Context, input string) (inspected, error) {
	output, err := store.runTool(ctx, store.options.FFprobePath, "ffprobe", "-v", "error", "-select_streams", "v:0", "-show_entries", "stream=width,height,duration,codec_name:format=duration", "-of", "json", input)
	if err != nil {
		return inspected{}, err
	}
	var document probeDocument
	decoder := json.NewDecoder(bytes.NewReader(output))
	if err := decoder.Decode(&document); err != nil || len(document.Streams) != 1 {
		return inspected{}, domain.NewError(domain.CodeMediaCorrupt, "MP4 probe did not return one video stream", nil)
	}
	stream := document.Streams[0]
	if stream.Width == 0 || stream.Height == 0 {
		return inspected{}, domain.NewError(domain.CodeMediaCorrupt, "MP4 video dimensions are missing", nil)
	}
	if err := validateDimensions(stream.Width, stream.Height, store.options.Limits); err != nil {
		return inspected{}, err
	}
	durationText := stream.Duration
	if durationText == "" || durationText == "N/A" {
		durationText = document.Format.Duration
	}
	duration, err := strconv.ParseFloat(durationText, 64)
	if err != nil || duration <= 0 || duration > 24*60*60 {
		return inspected{}, domain.NewError(domain.CodeMediaCorrupt, "MP4 video duration is invalid", nil)
	}
	return inspected{format: formatMP4, width: stream.Width, height: stream.Height, durationSeconds: duration}, nil
}

func (store *Store) transcodeVideo(ctx context.Context, contents []byte, inspection inspected, output string) error {
	directory, err := store.privateTempDir()
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	input, err := writePrivateInput(directory, "original.mp4", contents)
	if err != nil {
		return err
	}
	targetBits := float64(store.options.Limits.PreviewMaxBytes-256*1024) * 8
	videoRate := int64(targetBits/inspection.durationSeconds) - 128_000
	if videoRate < 160_000 {
		videoRate = 160_000
	}
	if videoRate > 6_000_000 {
		videoRate = 6_000_000
	}
	filter := fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease:force_divisible_by=2", store.options.Limits.PreviewMaxEdge, store.options.Limits.PreviewMaxEdge)
	_, err = store.runTool(ctx, store.options.FFmpegPath, "ffmpeg", "-nostdin", "-y", "-v", "error", "-i", input, "-map", "0:v:0", "-map", "0:a?", "-vf", filter, "-c:v", "libx264", "-pix_fmt", "yuv420p", "-preset", "medium", "-b:v", strconv.FormatInt(videoRate, 10), "-maxrate", strconv.FormatInt(videoRate, 10), "-bufsize", strconv.FormatInt(videoRate*2, 10), "-c:a", "aac", "-b:a", "128k", "-movflags", "+faststart", output)
	if err != nil {
		return err
	}
	info, err := os.Stat(output)
	if err != nil || !info.Mode().IsRegular() || info.Size() <= 0 || info.Size() > store.options.Limits.PreviewMaxBytes {
		return domain.NewError(domain.CodeMediaPreviewFailed, "video display version exceeds the byte budget", map[string]any{"max_bytes": store.options.Limits.PreviewMaxBytes})
	}
	return nil
}

func (store *Store) generateVideoPoster(ctx context.Context, contents []byte, output string) error {
	directory, err := store.privateTempDir()
	if err != nil {
		return err
	}
	defer func() { _ = os.RemoveAll(directory) }()
	input, err := writePrivateInput(directory, "original.mp4", contents)
	if err != nil {
		return err
	}
	filter := fmt.Sprintf("scale='min(%d,iw)':'min(%d,ih)':force_original_aspect_ratio=decrease", store.options.Limits.PreviewMaxEdge, store.options.Limits.PreviewMaxEdge)
	_, err = store.runTool(ctx, store.options.FFmpegPath, "ffmpeg", "-nostdin", "-y", "-v", "error", "-ss", "0", "-i", input, "-frames:v", "1", "-vf", filter, "-q:v", "3", output)
	return err
}

func verifyExtendedSignature(contents []byte, value Media) error {
	detected := detectExtendedFormat(contents)
	if detected == 0 || detected.mimeType() != value.MIMEType {
		return errors.New("extended media signature does not match metadata")
	}
	return nil
}
