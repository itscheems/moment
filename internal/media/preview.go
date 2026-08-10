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
	"os"
	"path/filepath"

	xdraw "golang.org/x/image/draw"
	"golang.org/x/image/webp"
)

func (store *Store) ensurePreviewBytes(ctx context.Context, contents []byte, inspection inspected, sha string) (Preview, error) {
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
	if store.generateHook != nil {
		store.generateHook()
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	decoded, err := decodeFull(inspection.format, contents)
	if err != nil {
		return Preview{}, previewFailure(err)
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	oriented := orient(decoded, inspection.orientation)
	alpha := imageHasTransparency(oriented)
	resized := resize(oriented, store.options.Limits.PreviewMaxEdge)
	var encoded bytes.Buffer
	mimeType := "image/jpeg"
	extension := "jpg"
	if alpha {
		mimeType = "image/png"
		extension = "png"
		encoder := png.Encoder{CompressionLevel: png.BestCompression}
		if err := encoder.Encode(&encoded, resized); err != nil {
			return Preview{}, previewFailure(err)
		}
	} else if err := jpeg.Encode(&encoded, flattenOpaque(resized), &jpeg.Options{Quality: 82}); err != nil {
		return Preview{}, previewFailure(err)
	}
	if err := ctx.Err(); err != nil {
		return Preview{}, err
	}
	path := store.previewPath(sha, extension)
	if err := writeCacheAtomic(ctx, path, encoded.Bytes(), store.beforePreviewCommit); err != nil {
		return Preview{}, previewFailure(err)
	}
	bounds := resized.Bounds()
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

func decodeFull(imageFormat format, contents []byte) (image.Image, error) {
	reader := bytes.NewReader(contents)
	switch imageFormat {
	case formatJPEG:
		return jpeg.Decode(reader)
	case formatPNG:
		return png.Decode(reader)
	case formatWebP:
		return webp.Decode(reader)
	case formatGIF:
		return gif.Decode(reader)
	default:
		return nil, errors.New("unsupported image decoder")
	}
}

func (store *Store) existingPreview(sha string) (Preview, bool) {
	for _, candidate := range []struct {
		extension string
		mimeType  string
	}{{"png", "image/png"}, {"jpg", "image/jpeg"}} {
		path := store.previewPath(sha, candidate.extension)
		info, err := os.Lstat(path)
		if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 {
			continue
		}
		file, err := os.Open(path)
		if err != nil {
			continue
		}
		config, decodedFormat, decodeErr := image.DecodeConfig(file)
		closeErr := file.Close()
		if decodeErr != nil || closeErr != nil || decodedFormat != candidate.extension || config.Width <= 0 || config.Height <= 0 || config.Width > int(store.options.Limits.PreviewMaxEdge) || config.Height > int(store.options.Limits.PreviewMaxEdge) {
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

func (store *Store) previewPath(sha, extension string) string {
	return filepath.Join(store.runtime.Root, "cache", "previews", RecipeVersion, sha[:2], sha+"."+extension)
}

func resize(source image.Image, maxEdge uint32) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if width <= int(maxEdge) && height <= int(maxEdge) {
		return source
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
	xdraw.ApproxBiLinear.Scale(destination, destination.Bounds(), source, bounds, xdraw.Over, nil)
	return destination
}

func orient(source image.Image, orientation uint8) image.Image {
	bounds := source.Bounds()
	width, height := bounds.Dx(), bounds.Dy()
	if orientation < 2 || orientation > 8 {
		return source
	}
	targetWidth, targetHeight := width, height
	if orientation >= 5 {
		targetWidth, targetHeight = height, width
	}
	target := image.NewNRGBA(image.Rect(0, 0, targetWidth, targetHeight))
	for y := 0; y < targetHeight; y++ {
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
	return target
}

func imageHasTransparency(source image.Image) bool {
	bounds := source.Bounds()
	for y := bounds.Min.Y; y < bounds.Max.Y; y++ {
		for x := bounds.Min.X; x < bounds.Max.X; x++ {
			_, _, _, alpha := source.At(x, y).RGBA()
			if alpha != 0xffff {
				return true
			}
		}
	}
	return false
}

func flattenOpaque(source image.Image) *image.RGBA {
	bounds := source.Bounds()
	target := image.NewRGBA(image.Rect(0, 0, bounds.Dx(), bounds.Dy()))
	for y := 0; y < bounds.Dy(); y++ {
		for x := 0; x < bounds.Dx(); x++ {
			value := color.RGBAModel.Convert(source.At(bounds.Min.X+x, bounds.Min.Y+y)).(color.RGBA)
			value.A = 0xff
			target.SetRGBA(x, y, value)
		}
	}
	return target
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
	if err := os.Rename(partialPath, final); err != nil {
		return err
	}
	removePartial = false
	return syncDirectory(directory)
}

func maxInt(left, right int) int {
	if left > right {
		return left
	}
	return right
}
