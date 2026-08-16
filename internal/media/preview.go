package media

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"math"
	"os"
	"path/filepath"
	"strings"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

const (
	previewJPEGMaxQuality   = 90
	previewJPEGMinQuality   = 60
	previewJPEGQualityStep  = 5
	previewScaleNumerator   = 4
	previewScaleDenominator = 5
)

func (store *Store) ensurePreviewBytes(ctx context.Context, contents []byte, inspection inspected, sha string) (Preview, error) {
	if inspection.format == formatHEIC {
		converted := inspection.decodedSource
		if len(converted) == 0 {
			var err error
			converted, err = store.convertHEIC(ctx, contents)
			if err != nil {
				return Preview{}, previewFailure(err)
			}
		}
		decoded, err := inspectImage(converted, store.options.Limits)
		if err != nil {
			return Preview{}, previewFailure(err)
		}
		return store.ensurePreviewBytes(ctx, converted, decoded, sha)
	}
	if inspection.format == formatMP4 {
		return store.ensureVideoPreview(ctx, contents, inspection, sha)
	}
	if preview, ok := store.existingPreview(sha); ok {
		preview.AnimatedOriginal = inspection.format == formatGIF
		return preview, nil
	}
	select {
	case store.workers <- struct{}{}:
		defer func() { <-store.workers }()
	case <-ctx.Done():
		return Preview{}, ctx.Err()
	}
	memoryWeight := previewMemoryWeight(uint64(inspection.width) * uint64(inspection.height))
	if memoryWeight > store.memoryCapacity {
		return Preview{}, domain.NewError(domain.CodeMediaDimensionsExceeded, "image exceeds the preview memory budget", map[string]any{
			"estimated_bytes": memoryWeight,
			"budget_bytes":    store.memoryCapacity,
		})
	}
	if err := store.memory.Acquire(ctx, memoryWeight); err != nil {
		return Preview{}, err
	}
	defer store.memory.Release(memoryWeight)
	if store.generateHook != nil {
		store.generateHook()
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	decoded, err := decodeFull(ctx, inspection, contents)
	if err != nil {
		return Preview{}, previewFailure(err)
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	alpha, err := imageHasTransparency(ctx, decoded)
	if err != nil {
		return Preview{}, err
	}
	resized, err := resize(ctx, decoded, store.options.Limits.PreviewMaxEdge)
	if err != nil {
		return Preview{}, err
	}
	oriented, err := orient(ctx, resized, inspection.orientation)
	if err != nil {
		return Preview{}, err
	}
	encoded, outputImage, mimeType, extension, err := encodePreviewWithinBudget(
		ctx,
		oriented,
		alpha,
		store.options.Limits.PreviewMaxBytes,
	)
	if err != nil {
		return Preview{}, err
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	path := store.previewPath(sha, extension)
	if err := store.writeCacheAtomic(ctx, path, encoded, store.beforePreviewCommit); err != nil {
		return Preview{}, previewFailure(err)
	}
	bounds := outputImage.Bounds()
	return Preview{
		Path:             path,
		RecipeVersion:    RecipeVersion,
		SHA256:           sha,
		MIMEType:         mimeType,
		Width:            uint32(bounds.Dx()),
		Height:           uint32(bounds.Dy()),
		AnimatedOriginal: inspection.format == formatGIF,
	}, nil
}

func (store *Store) ensureVideoPreview(ctx context.Context, contents []byte, inspection inspected, sha string) (Preview, error) {
	path := store.previewPath(sha, "mp4")
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= store.options.Limits.PreviewMaxBytes {
		return Preview{Path: path, RecipeVersion: RecipeVersion, SHA256: sha, MIMEType: "video/mp4", Width: inspection.width, Height: inspection.height}, nil
	}
	select {
	case store.workers <- struct{}{}:
		defer func() { <-store.workers }()
	case <-ctx.Done():
		return Preview{}, ctx.Err()
	}
	directory, err := store.privateTempDir()
	if err != nil {
		return Preview{}, previewFailure(err)
	}
	defer func() { _ = os.RemoveAll(directory) }()
	partialPath := filepath.Join(directory, "display.mp4")
	if err := store.transcodeVideo(ctx, contents, inspection, partialPath); err != nil {
		return Preview{}, previewFailure(err)
	}
	partial, err := os.Open(partialPath)
	if err != nil {
		return Preview{}, previewFailure(err)
	}
	encoded, readErr := readBounded(ctx, partial, store.options.Limits.PreviewMaxBytes)
	closeErr := partial.Close()
	if readErr != nil || closeErr != nil {
		return Preview{}, previewFailure(errors.Join(readErr, closeErr))
	}
	if err := store.writeCacheAtomic(ctx, path, encoded, store.beforePreviewCommit); err != nil {
		return Preview{}, previewFailure(err)
	}
	return Preview{Path: path, RecipeVersion: RecipeVersion, SHA256: sha, MIMEType: "video/mp4", Width: inspection.width, Height: inspection.height}, nil
}

func (store *Store) ensureVideoPoster(ctx context.Context, media Media, contents []byte) (string, error) {
	path := store.previewPath(media.SHA256, "poster.jpg")
	if info, err := os.Stat(path); err == nil && info.Mode().IsRegular() && info.Size() > 0 && info.Size() <= store.options.Limits.PreviewMaxBytes {
		return path, nil
	}
	directory, err := store.privateTempDir()
	if err != nil {
		return "", previewFailure(err)
	}
	defer func() { _ = os.RemoveAll(directory) }()
	partialPath := filepath.Join(directory, "poster.jpg")
	if err := store.generateVideoPoster(ctx, contents, partialPath); err != nil {
		return "", previewFailure(err)
	}
	partial, err := os.Open(partialPath)
	if err != nil {
		return "", previewFailure(err)
	}
	encoded, readErr := readBounded(ctx, partial, store.options.Limits.PreviewMaxBytes)
	closeErr := partial.Close()
	if readErr != nil || closeErr != nil {
		return "", previewFailure(errors.Join(readErr, closeErr))
	}
	if _, decodedFormat, err := image.DecodeConfig(bytes.NewReader(encoded)); err != nil || decodedFormat != "jpeg" {
		return "", previewFailure(errors.New("video poster is not a JPEG image"))
	}
	if err := store.writeCacheAtomic(ctx, path, encoded, nil); err != nil {
		return "", previewFailure(err)
	}
	return path, nil
}

func encodePreviewWithinBudget(ctx context.Context, source image.Image, alpha bool, maxBytes int64) ([]byte, image.Image, string, string, error) {
	candidate := source
	var encoded bytes.Buffer
	if !alpha {
		opaque, err := flattenOpaque(ctx, candidate)
		if err != nil {
			return nil, nil, "", "", err
		}
		candidate = opaque
	}
	for {
		if err := ctx.Err(); err != nil {
			return nil, nil, "", "", err
		}
		if alpha {
			if err := encodePNGPreviewInto(ctx, candidate, &encoded); err != nil {
				return nil, nil, "", "", err
			}
			if int64(encoded.Len()) <= maxBytes {
				return encoded.Bytes(), candidate, "image/png", "png", nil
			}
		} else {
			for quality := previewJPEGMaxQuality; quality >= previewJPEGMinQuality; quality -= previewJPEGQualityStep {
				if err := encodeJPEGPreviewInto(ctx, candidate, quality, &encoded); err != nil {
					return nil, nil, "", "", err
				}
				if int64(encoded.Len()) <= maxBytes {
					return encoded.Bytes(), candidate, "image/jpeg", "jpg", nil
				}
			}
		}

		bounds := candidate.Bounds()
		longestEdge := max(bounds.Dx(), bounds.Dy())
		if longestEdge <= 1 {
			return nil, nil, "", "", domain.NewError(domain.CodeMediaPreviewFailed, "preview cannot fit within the byte budget", map[string]any{"max_bytes": maxBytes})
		}
		nextEdge := max(1, longestEdge*previewScaleNumerator/previewScaleDenominator)
		resized, err := resize(ctx, candidate, uint32(nextEdge))
		if err != nil {
			return nil, nil, "", "", err
		}
		candidate = resized
	}
}

func encodePNGPreviewInto(ctx context.Context, source image.Image, encoded *bytes.Buffer) error {
	encoded.Reset()
	output := &contextWriter{ctx: ctx, writer: encoded}
	encoder := png.Encoder{CompressionLevel: png.BestCompression}
	if err := encoder.Encode(output, source); err != nil {
		return previewFailure(err)
	}
	return nil
}

func encodeJPEGPreview(ctx context.Context, source image.Image, quality int) ([]byte, error) {
	var encoded bytes.Buffer
	if err := encodeJPEGPreviewInto(ctx, source, quality, &encoded); err != nil {
		return nil, err
	}
	return encoded.Bytes(), nil
}

func encodeJPEGPreviewInto(ctx context.Context, source image.Image, quality int, encoded *bytes.Buffer) error {
	encoded.Reset()
	output := &contextWriter{ctx: ctx, writer: encoded}
	if err := jpeg.Encode(output, source, &jpeg.Options{Quality: quality}); err != nil {
		return previewFailure(err)
	}
	return nil
}

func decodeFull(ctx context.Context, inspection inspected, contents []byte) (image.Image, error) {
	// Standard library and x/image decoders can still spend CPU between reader
	// calls. The context reader stops at the next compressed-input read, while
	// the weighted semaphore bounds all such in-flight pixel work.
	reader := &contextReader{ctx: ctx, reader: bytes.NewReader(contents)}
	switch inspection.format {
	case formatJPEG:
		return jpeg.Decode(reader)
	case formatPNG:
		return png.Decode(reader)
	case formatWebP:
		return webp.Decode(reader)
	case formatGIF:
		frame, err := gif.Decode(reader)
		if err != nil {
			return nil, err
		}
		return composeGIFFirstFrame(ctx, inspection, frame)
	default:
		return nil, errors.New("unsupported image decoder")
	}
}

func (store *Store) existingPreview(sha string) (Preview, bool) {
	for _, candidate := range []struct {
		extension     string
		decodedFormat string
		mimeType      string
	}{{"png", "png", "image/png"}, {"jpg", "jpeg", "image/jpeg"}} {
		path := store.previewPath(sha, candidate.extension)
		if store.rootAuthority != nil {
			relative := filepath.Join("cache", "previews", RecipeVersion, sha[:2], sha+"."+candidate.extension)
			info, err := store.rootAuthority.Lstat(relative)
			if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 {
				continue
			}
			file, err := store.rootAuthority.Open(relative)
			if err != nil {
				continue
			}
			config, decodedFormat, decodeErr := image.DecodeConfig(file)
			closeErr := file.Close()
			if decodeErr == nil && closeErr == nil && info.Size() <= store.options.Limits.PreviewMaxBytes && decodedFormat == candidate.decodedFormat && config.Width > 0 && config.Height > 0 && config.Width <= int(store.options.Limits.PreviewMaxEdge) && config.Height <= int(store.options.Limits.PreviewMaxEdge) {
				return Preview{Path: path, RecipeVersion: RecipeVersion, SHA256: sha, MIMEType: candidate.mimeType, Width: uint32(config.Width), Height: uint32(config.Height)}, true
			}
			continue
		}
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > store.options.Limits.PreviewMaxBytes {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		config, decodedFormat, decodeErr := image.DecodeConfig(file)
		closeErr := file.Close()
		if decodeErr != nil || closeErr != nil || decodedFormat != candidate.decodedFormat || config.Width <= 0 || config.Height <= 0 || config.Width > int(store.options.Limits.PreviewMaxEdge) || config.Height > int(store.options.Limits.PreviewMaxEdge) {
			continue
		}
		return Preview{
			Path:          path,
			RecipeVersion: RecipeVersion,
			SHA256:        sha,
			MIMEType:      candidate.mimeType,
			Width:         uint32(config.Width),
			Height:        uint32(config.Height),
		}, true
	}
	return Preview{}, false
}

func (store *Store) existingVideoPreview(sha string) bool {
	path := store.previewPath(sha, "mp4")
	var (
		info os.FileInfo
		file *os.File
		err  error
	)
	if store.rootAuthority != nil {
		relative := filepath.Join("cache", "previews", RecipeVersion, sha[:2], sha+".mp4")
		info, err = store.rootAuthority.Lstat(relative)
		if err == nil {
			file, err = store.rootAuthority.Open(relative)
		}
	} else {
		info, err = os.Lstat(path)
		if err == nil {
			file, err = os.Open(path)
		}
	}
	if err != nil || info == nil || file == nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > store.options.Limits.PreviewMaxBytes {
		if file != nil {
			_ = file.Close()
		}
		return false
	}
	header, readErr := io.ReadAll(io.LimitReader(file, 64))
	closeErr := file.Close()
	return readErr == nil && closeErr == nil && detectExtendedFormat(header) == formatMP4
}

func (store *Store) writeCacheAtomic(ctx context.Context, final string, contents []byte, beforeCommit func() error) error {
	if store.rootAuthority == nil {
		return writeCacheAtomic(ctx, final, contents, beforeCommit)
	}
	relative, err := filepath.Rel(store.storageRoot, final)
	if err != nil || filepath.IsAbs(relative) || strings.HasPrefix(relative, "..") {
		return errors.New("rooted preview path escaped authority")
	}
	directory := filepath.Dir(relative)
	if err := store.rootAuthority.MkdirAll(directory, 0o700); err != nil {
		return err
	}
	temporary := relative + ".partial"
	file, err := store.rootAuthority.OpenFile(temporary, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	published := false
	defer func() {
		_ = file.Close()
		if !published {
			_ = store.rootAuthority.Remove(temporary)
		}
	}()
	if _, err := file.Write(contents); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			return err
		}
	}
	if err := store.rootAuthority.Rename(temporary, relative); err != nil {
		return err
	}
	published = true
	return nil
}

func (store *Store) previewPath(sha, extension string) string {
	return filepath.Join(store.storageRoot, "cache", "previews", RecipeVersion, sha[:2], sha+"."+extension)
}

func resize(ctx context.Context, source image.Image, maxEdge uint32) (image.Image, error) {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= int(maxEdge) && height <= int(maxEdge) {
		return source, ctx.Err()
	}
	var targetWidth, targetHeight int
	if width >= height {
		targetWidth = int(maxEdge)
		targetHeight = maxInt(1, int((int64(height)*int64(maxEdge)+int64(width)/2)/int64(width)))
	} else {
		targetHeight = int(maxEdge)
		targetWidth = maxInt(1, int((int64(width)*int64(maxEdge)+int64(height)/2)/int64(height)))
	}
	destination := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	// ApproxBiLinear clips against the destination bounds while preserving the
	// full destination transform. Scaling in strips therefore makes long work
	// cancellable without changing the recipe's geometry.
	const rowsPerStrip = 32
	for top := 0; top < targetHeight; top += rowsPerStrip {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		bottom := min(targetHeight, top+rowsPerStrip)
		strip := destination.SubImage(image.Rect(0, top, targetWidth, bottom)).(*image.NRGBA)
		xdraw.ApproxBiLinear.Scale(strip, destination.Bounds(), source, bounds, xdraw.Src, nil)
	}
	return destination, nil
}

func orient(ctx context.Context, source image.Image, orientation uint8) (image.Image, error) {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if orientation < 2 || orientation > 8 {
		return source, ctx.Err()
	}
	targetWidth, targetHeight := width, height
	if orientation >= 5 {
		targetWidth, targetHeight = height, width
	}
	target := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < targetWidth; x++ {
			var sourceX, sourceY int
			switch orientation {
			case 2:
				sourceX, sourceY = width-1-x, y
			case 3:
				sourceX, sourceY = width-1-x, height-1-y
			case 4:
				sourceX, sourceY = x, height-1-y
			case 5:
				sourceX, sourceY = y, x
			case 6:
				sourceX, sourceY = y, height-1-x
			case 7:
				sourceX, sourceY = width-1-y, height-1-x
			case 8:
				sourceX, sourceY = width-1-y, x
			}
			target.Set(x, y, source.At(bounds.Min.X+sourceX, bounds.Min.Y+sourceY))
		}
	}
	return target, nil
}

func imageHasTransparency(ctx context.Context, source image.Image) (bool, error) {
	bounds := source.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, alpha := source.At(x, y).RGBA()
			if alpha != 0xffff {
				return true, nil
			}
		}
	}
	return false, nil
}

func flattenOpaque(ctx context.Context, source image.Image) (*image.RGBA, error) {
	bounds := source.Bounds()
	target := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := 0; x < bounds.Dx(); x++ {
			value := color.RGBAModel.Convert(source.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.RGBA)
			value.A = 0xff
			target.SetRGBA(x, y, value)
		}
	}
	return target, nil
}

func composeGIFFirstFrame(ctx context.Context, inspection inspected, frame image.Image) (image.Image, error) {
	canvas := image.NewNRGBA(image.Rect(0, 0, int(inspection.width), int(inspection.height)))
	if inspection.gifBackgroundSet && !inspection.gifFirstTransparent {
		for y := 0; y < canvas.Bounds().Dy(); y++ {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			for x := 0; x < canvas.Bounds().Dx(); x++ {
				canvas.SetNRGBA(x, y, inspection.gifBackground)
			}
		}
	}
	frameBounds := frame.Bounds().Intersect(canvas.Bounds())
	for y := frameBounds.Min.Y; y < frameBounds.Max.Y; y++ {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		for x := frameBounds.Min.X; x < frameBounds.Max.X; x++ {
			value := color.NRGBAModel.Convert(frame.At(x, y)).(color.NRGBA)
			if value.A == 0 {
				continue
			}
			canvas.SetNRGBA(x, y, value)
		}
	}
	return canvas, nil
}

func previewMemoryWeight(pixels uint64) int64 {
	maximumPixels := uint64((math.MaxInt64 - previewMemoryFixedBytes) / previewMemoryBytesPerPixel)
	if pixels > maximumPixels {
		return math.MaxInt64
	}
	return previewMemoryFixedBytes + int64(pixels)*previewMemoryBytesPerPixel
}

type contextWriter struct {
	ctx    context.Context
	writer io.Writer
}

func (writer *contextWriter) Write(data []byte) (int, error) {
	if err := writer.ctx.Err(); err != nil {
		return 0, err
	}
	return writer.writer.Write(data)
}

func writeCacheAtomic(ctx context.Context, final string, contents []byte, beforeCommit func() error) error {
	directory := filepath.Dir(final)
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return fmt.Errorf("create preview cache directory: %w", err)
	}
	if info, err := os.Lstat(directory); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("preview cache directory is not safe")
	}
	partial, err := os.CreateTemp(directory, ".partial-*")
	if err != nil {
		return err
	}
	partialPath := partial.Name()
	removePartial := true
	defer func() {
		_ = partial.Close()
		if removePartial {
			_ = os.Remove(partialPath)
		}
	}()
	if err := partial.Chmod(0o600); err != nil {
		return err
	}
	if _, err := partial.Write(contents); err != nil {
		return err
	}
	if err := partial.Sync(); err != nil {
		return err
	}
	if err := partial.Close(); err != nil {
		return err
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if beforeCommit != nil {
		if err := beforeCommit(); err != nil {
			return err
		}
	}
	if err := runtimefs.PublishPreparedFile(partialPath, final); err != nil {
		return err
	}
	removePartial = false
	return nil
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
