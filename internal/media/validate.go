package media

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"hash/crc32"
	"image"
	"image/color"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"
	"strings"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/mediatype"
)

// VerifyPortableOriginal validates authoritative metadata and immutable bytes
// without consulting derived previews or cache state. Export uses this bounded
// verifier before copying an original into a portable archive.
func VerifyPortableOriginal(ctx context.Context, input io.Reader, value Media) error {
	return verifyPortableOriginal(ctx, input, value, true)
}

// VerifyPortableArchiveOriginal validates one v1 media member using only the
// portable metadata carried by the archive. StoragePath is intentionally not
// part of that protocol and is therefore not consulted.
func VerifyPortableArchiveOriginal(ctx context.Context, input io.Reader, value Media) error {
	return verifyPortableOriginal(ctx, input, value, false)
}

func verifyPortableOriginal(ctx context.Context, input io.Reader, value Media, requireStoragePath bool) error {
	if input == nil {
		return domain.NewError(domain.CodeMediaCorrupt, "immutable original is unavailable", nil)
	}
	if _, err := domain.ParseID(value.ID.String()); err != nil {
		return err
	}
	name, err := normalizeOriginalName(value.OriginalName)
	if err != nil || name != value.OriginalName {
		return domain.NewError(domain.CodeMediaCorrupt, "media original name is not canonical", map[string]any{"media_id": value.ID.String()})
	}
	expectedPath, ok := expectedStoragePath(value.SHA256, value.MIMEType)
	if !ok || requireStoragePath && value.StoragePath != expectedPath {
		return domain.NewError(domain.CodeMediaCorrupt, "media storage path is not canonical", map[string]any{"media_id": value.ID.String()})
	}
	maxBytes, supported := mediatype.MaxBytes(value.MIMEType)
	if !supported || value.ByteSize < 1 || value.ByteSize > maxBytes || value.Width == 0 || value.Height == 0 ||
		value.Width > DefaultMaxDimension || value.Height > DefaultMaxDimension || uint64(value.Width)*uint64(value.Height) > DefaultMaxPixels ||
		value.CreatedAt.IsZero() {
		return domain.NewError(domain.CodeMediaCorrupt, "media metadata exceeds portable limits", map[string]any{"media_id": value.ID.String()})
	}
	contents, err := readBounded(ctx, input, maxBytes)
	if err != nil {
		if contextErr := ctx.Err(); contextErr != nil {
			return contextErr
		}
		return domain.NewError(domain.CodeMediaCorrupt, "immutable original cannot be read", map[string]any{"media_id": value.ID.String()})
	}
	if int64(len(contents)) != value.ByteSize {
		return domain.NewError(domain.CodeMediaCorrupt, "immutable original size does not match metadata", map[string]any{"media_id": value.ID.String()})
	}
	digest := sha256.Sum256(contents)
	if hex.EncodeToString(digest[:]) != strings.ToLower(value.SHA256) || value.SHA256 != strings.ToLower(value.SHA256) {
		return domain.NewError(domain.CodeMediaCorrupt, "immutable original checksum does not match metadata", map[string]any{"media_id": value.ID.String()})
	}
	limits, err := (Limits{}).normalized()
	if err != nil {
		return err
	}
	inspection, err := inspectImage(contents, limits)
	if err != nil {
		if extendedErr := verifyExtendedSignature(contents, value); extendedErr == nil {
			return nil
		}
		return domain.NewError(domain.CodeMediaCorrupt, "immutable original media structure is invalid", map[string]any{"media_id": value.ID.String()})
	}
	width, height := displayDimensions(inspection)
	if inspection.format.mimeType() != value.MIMEType || width != value.Width || height != value.Height {
		return domain.NewError(domain.CodeMediaCorrupt, "immutable original type or dimensions do not match metadata", map[string]any{"media_id": value.ID.String()})
	}
	return nil
}

var (
	pngSignature  = []byte("\x89PNG\r\n\x1a\n")
	jpegSignature = []byte("\xff\xd8\xff")
)

func inspectImage(contents []byte, limits Limits) (inspected, error) {
	var result inspected
	var err error
	switch {
	case bytes.HasPrefix(contents, jpegSignature):
		result, err = inspectJPEG(contents)
	case bytes.HasPrefix(contents, pngSignature):
		result, err = inspectPNG(contents, limits)
	case len(contents) >= 12 && string(contents[:4]) == "RIFF" && string(contents[8:12]) == "WEBP":
		result, err = inspectWebP(contents)
	case len(contents) >= 6 && (string(contents[:6]) == "GIF87a" || string(contents[:6]) == "GIF89a"):
		result, err = inspectGIF(contents, limits)
	default:
		return inspected{}, domain.NewError(domain.CodeMediaTypeUnsupported, "image signature is not supported", nil)
	}
	if err != nil {
		return inspected{}, err
	}
	if err := validateDimensions(result.width, result.height, limits); err != nil {
		return inspected{}, err
	}
	// Keep WebP container parsing independent from the full decoder. The scanner
	// validates canvas and frame headers itself so pixel-sized work cannot move
	// ahead of the preview resource budget as decoder internals evolve.
	if result.format != formatWebP {
		config, err := decodeConfig(result.format, contents)
		if err != nil {
			return inspected{}, corrupt("image header is corrupt")
		}
		if config.Width <= 0 || config.Height <= 0 || uint32(config.Width) != result.width || uint32(config.Height) != result.height {
			return inspected{}, corrupt("image header dimensions are inconsistent")
		}
	}
	return result, nil
}

func decodeConfig(imageFormat format, contents []byte) (image.Config, error) {
	reader := bytes.NewReader(contents)
	switch imageFormat {
	case formatJPEG:
		return jpeg.DecodeConfig(reader)
	case formatPNG:
		return png.DecodeConfig(reader)
	case formatGIF:
		return gif.DecodeConfig(reader)
	default:
		return image.Config{}, errors.New("unsupported image decoder")
	}
}

func validateDimensions(width, height uint32, limits Limits) error {
	if width == 0 || height == 0 {
		return corrupt("image dimensions are missing")
	}
	pixels := uint64(width) * uint64(height)
	if width > limits.MaxDimension || height > limits.MaxDimension || pixels > limits.MaxPixels {
		return domain.NewError(domain.CodeMediaDimensionsExceeded, "image dimensions exceed the configured budget", map[string]any{
			"max_dimension": limits.MaxDimension,
			"max_pixels":    limits.MaxPixels,
		})
	}
	return nil
}

func inspectPNG(contents []byte, limits Limits) (inspected, error) {
	if len(contents) < 33 || string(contents[12:16]) != "IHDR" || binary.BigEndian.Uint32(contents[8:12]) != 13 {
		return inspected{}, corrupt("PNG header is corrupt")
	}
	result := inspected{
		format:      formatPNG,
		width:       binary.BigEndian.Uint32(contents[16:20]),
		height:      binary.BigEndian.Uint32(contents[20:24]),
		orientation: 1,
	}
	if err := validateDimensions(result.width, result.height, limits); err != nil {
		return inspected{}, err
	}
	seenEXIF := false
	seenEnd := false
	for offset := 8; offset < len(contents); {
		if len(contents)-offset < 12 {
			return inspected{}, corrupt("PNG chunk header is truncated")
		}
		length := uint64(binary.BigEndian.Uint32(contents[offset : offset+4]))
		dataStart := uint64(offset) + 8
		dataEnd := dataStart + length
		chunkEnd := dataEnd + 4
		if dataEnd < dataStart || chunkEnd < dataEnd || chunkEnd > uint64(len(contents)) {
			return inspected{}, corrupt("PNG chunk is truncated")
		}
		kind := string(contents[offset+4 : offset+8])
		data := contents[dataStart:dataEnd]
		checksum := crc32.NewIEEE()
		_, _ = checksum.Write(contents[offset+4 : offset+8])
		_, _ = checksum.Write(data)
		if checksum.Sum32() != binary.BigEndian.Uint32(contents[dataEnd:chunkEnd]) {
			return inspected{}, corrupt("PNG chunk checksum is invalid")
		}
		switch kind {
		case "IHDR":
			if offset != 8 || length != 13 {
				return inspected{}, corrupt("PNG IHDR ordering is invalid")
			}
		case "eXIf":
			if seenEXIF {
				return inspected{}, corrupt("PNG contains duplicate eXIf chunks")
			}
			seenEXIF = true
			if orientation, ok := orientationFromEXIF(data); ok {
				result.orientation = orientation
			}
		case "IEND":
			if length != 0 || chunkEnd != uint64(len(contents)) {
				return inspected{}, corrupt("PNG IEND is invalid")
			}
			seenEnd = true
		}
		offset = int(chunkEnd)
		if seenEnd {
			break
		}
	}
	if !seenEnd {
		return inspected{}, corrupt("PNG IEND is missing")
	}
	return result, nil
}

func inspectJPEG(contents []byte) (inspected, error) {
	result := inspected{format: formatJPEG, orientation: 1}
	for offset := 2; offset < len(contents); {
		if contents[offset] != 0xff {
			return inspected{}, corrupt("JPEG marker stream is corrupt")
		}
		for offset < len(contents) && contents[offset] == 0xff {
			offset++
		}
		if offset >= len(contents) {
			return inspected{}, corrupt("JPEG marker is truncated")
		}
		marker := contents[offset]
		offset++
		if marker == 0xd9 {
			break
		}
		if marker == 0x01 || marker >= 0xd0 && marker <= 0xd7 {
			continue
		}
		if offset+2 > len(contents) {
			return inspected{}, corrupt("JPEG segment length is truncated")
		}
		length := int(binary.BigEndian.Uint16(contents[offset : offset+2]))
		if length < 2 || offset+length > len(contents) {
			return inspected{}, corrupt("JPEG segment is truncated")
		}
		segment := contents[offset+2 : offset+length]
		if isSOF(marker) {
			if len(segment) < 6 {
				return inspected{}, corrupt("JPEG frame header is corrupt")
			}
			result.height = uint32(binary.BigEndian.Uint16(segment[1:3]))
			result.width = uint32(binary.BigEndian.Uint16(segment[3:5]))
		}
		if marker == 0xe1 && len(segment) >= 6 && string(segment[:6]) == "Exif\x00\x00" {
			if orientation, ok := orientationFromEXIF(segment); ok {
				result.orientation = orientation
			}
		}
		offset += length
		if marker == 0xda {
			break
		}
	}
	if result.width == 0 || result.height == 0 {
		return inspected{}, corrupt("JPEG frame dimensions are missing")
	}
	return result, nil
}

func isSOF(marker byte) bool {
	switch marker {
	case 0xc0, 0xc1, 0xc2, 0xc3, 0xc5, 0xc6, 0xc7, 0xc9, 0xca, 0xcb, 0xcd, 0xce, 0xcf:
		return true
	default:
		return false
	}
}

func orientationFromEXIF(data []byte) (uint8, bool) {
	if len(data) >= 6 && string(data[:6]) == "Exif\x00\x00" {
		data = data[6:]
	}
	return exifOrientation(data)
}

func exifOrientation(tiff []byte) (uint8, bool) {
	if len(tiff) < 8 {
		return 0, false
	}
	var order binary.ByteOrder
	switch string(tiff[:2]) {
	case "II":
		order = binary.LittleEndian
	case "MM":
		order = binary.BigEndian
	default:
		return 0, false
	}
	if order.Uint16(tiff[2:4]) != 42 {
		return 0, false
	}
	offset := uint64(order.Uint32(tiff[4:8]))
	if offset+2 > uint64(len(tiff)) {
		return 0, false
	}
	count := uint64(order.Uint16(tiff[offset : offset+2]))
	start := offset + 2
	if count > (uint64(len(tiff))-start)/12 {
		return 0, false
	}
	for index := uint64(0); index < count; index++ {
		entry := tiff[start+index*12 : start+(index+1)*12]
		if order.Uint16(entry[:2]) != 0x0112 || order.Uint16(entry[2:4]) != 3 || order.Uint32(entry[4:8]) != 1 {
			continue
		}
		orientation := order.Uint16(entry[8:10])
		if orientation >= 1 && orientation <= 8 {
			return uint8(orientation), true
		}
	}
	return 0, false
}

func inspectWebP(contents []byte) (inspected, error) {
	if len(contents) < 20 {
		return inspected{}, corrupt("WebP header is truncated")
	}
	declared := uint64(binary.LittleEndian.Uint32(contents[4:8])) + 8
	if declared != uint64(len(contents)) || declared < 20 {
		return inspected{}, corrupt("WebP RIFF length is invalid")
	}
	result := inspected{format: formatWebP, orientation: 1}
	var frameWidth, frameHeight uint32
	var extendedFlags byte
	seenExtended := false
	seenImage := false
	seenEXIF := false
	seenAlpha := false
	frameAlpha := false
	for offset := uint64(12); offset < declared; {
		if declared-offset < 8 {
			return inspected{}, corrupt("WebP chunk header is truncated")
		}
		kind := string(contents[offset : offset+4])
		length := uint64(binary.LittleEndian.Uint32(contents[offset+4 : offset+8]))
		start := offset + 8
		end := start + length
		paddedEnd := end + length%2
		if end < start || paddedEnd < end || paddedEnd > declared {
			return inspected{}, corrupt("WebP chunk is truncated")
		}
		chunk := contents[start:end]
		switch kind {
		case "VP8X":
			if seenExtended || offset != 12 || len(chunk) != 10 {
				return inspected{}, corrupt("WebP extended header is invalid")
			}
			seenExtended = true
			extendedFlags = chunk[0]
			if extendedFlags&0x02 != 0 {
				return inspected{}, domain.NewError(domain.CodeMediaTypeUnsupported, "animated WebP is not supported", nil)
			}
			if extendedFlags&0xc1 != 0 || chunk[1] != 0 || chunk[2] != 0 || chunk[3] != 0 {
				return inspected{}, corrupt("WebP extended header reserved bits are set")
			}
			result.width = readUint24LE(chunk[4:7]) + 1
			result.height = readUint24LE(chunk[7:10]) + 1
		case "VP8 ":
			if seenImage || len(chunk) < 10 || chunk[0]&0x01 != 0 || !bytes.Equal(chunk[3:6], []byte{0x9d, 0x01, 0x2a}) {
				return inspected{}, corrupt("WebP lossy frame header is corrupt")
			}
			seenImage = true
			frameWidth = uint32(binary.LittleEndian.Uint16(chunk[6:8]) & 0x3fff)
			frameHeight = uint32(binary.LittleEndian.Uint16(chunk[8:10]) & 0x3fff)
		case "VP8L":
			if seenImage || len(chunk) < 5 || chunk[0] != 0x2f {
				return inspected{}, corrupt("WebP lossless frame header is corrupt")
			}
			seenImage = true
			bits := binary.LittleEndian.Uint32(chunk[1:5])
			if bits>>29 != 0 {
				return inspected{}, corrupt("WebP lossless frame version is invalid")
			}
			frameWidth = bits&0x3fff + 1
			frameHeight = bits>>14&0x3fff + 1
			frameAlpha = bits&(1<<28) != 0
		case "ALPH":
			if seenAlpha || seenImage || len(chunk) < 1 {
				return inspected{}, corrupt("WebP alpha chunk is invalid")
			}
			seenAlpha = true
		case "EXIF":
			if seenEXIF {
				return inspected{}, corrupt("WebP contains duplicate EXIF chunks")
			}
			seenEXIF = true
			if orientation, ok := orientationFromEXIF(chunk); ok {
				result.orientation = orientation
			}
		case "ANIM", "ANMF":
			return inspected{}, domain.NewError(domain.CodeMediaTypeUnsupported, "animated WebP is not supported", nil)
		}
		offset = paddedEnd
	}
	if !seenImage || frameWidth == 0 || frameHeight == 0 {
		return inspected{}, corrupt("WebP frame dimensions are missing")
	}
	if seenExtended {
		if result.width != frameWidth || result.height != frameHeight {
			return inspected{}, corrupt("WebP canvas and frame dimensions are inconsistent")
		}
		if seenEXIF != (extendedFlags&0x08 != 0) {
			return inspected{}, corrupt("WebP EXIF feature flag is inconsistent")
		}
		if (seenAlpha || frameAlpha) != (extendedFlags&0x10 != 0) {
			return inspected{}, corrupt("WebP alpha feature flag is inconsistent")
		}
	} else {
		if seenEXIF || seenAlpha {
			return inspected{}, corrupt("WebP extended metadata requires an extended header")
		}
		result.width, result.height = frameWidth, frameHeight
	}
	return result, nil
}

func readUint24LE(value []byte) uint32 {
	return uint32(value[0]) | uint32(value[1])<<8 | uint32(value[2])<<16
}

func inspectGIF(contents []byte, limits Limits) (inspected, error) {
	if len(contents) < 13 {
		return inspected{}, corrupt("GIF logical screen descriptor is truncated")
	}
	width := uint32(binary.LittleEndian.Uint16(contents[6:8]))
	height := uint32(binary.LittleEndian.Uint16(contents[8:10]))
	if err := validateDimensions(width, height, limits); err != nil {
		return inspected{}, err
	}
	result := inspected{format: formatGIF, width: width, height: height, orientation: 1}
	offset := 13
	if contents[10]&0x80 != 0 {
		colors := 1 << (uint(contents[10]&0x07) + 1)
		tableBytes := 3 * colors
		if offset+tableBytes > len(contents) {
			return inspected{}, corrupt("GIF global color table is truncated")
		}
		backgroundIndex := int(contents[11])
		if backgroundIndex >= colors {
			return inspected{}, corrupt("GIF background color index is invalid")
		}
		colorOffset := offset + backgroundIndex*3
		result.gifBackground = color.NRGBA{
			R: contents[colorOffset], G: contents[colorOffset+1], B: contents[colorOffset+2], A: 0xff,
		}
		result.gifBackgroundSet = true
		offset += tableBytes
	}
	if offset > len(contents) {
		return inspected{}, corrupt("GIF global color table is truncated")
	}
	var frames uint32
	var framePixels uint64
	pendingTransparency := false
	for offset < len(contents) {
		switch contents[offset] {
		case 0x3b:
			if frames == 0 {
				return inspected{}, corrupt("GIF contains no image frames")
			}
			result.animated = frames > 1
			return result, nil
		case 0x21:
			if offset+2 > len(contents) {
				return inspected{}, corrupt("GIF extension label is truncated")
			}
			if contents[offset+1] == 0xf9 {
				if offset+8 > len(contents) || contents[offset+2] != 4 || contents[offset+7] != 0 {
					return inspected{}, corrupt("GIF graphic control extension is corrupt")
				}
				pendingTransparency = contents[offset+3]&0x01 != 0
			}
			offset += 2
			var err error
			offset, err = skipGIFSubBlocks(contents, offset)
			if err != nil {
				return inspected{}, err
			}
		case 0x2c:
			if offset+10 > len(contents) {
				return inspected{}, corrupt("GIF image descriptor is truncated")
			}
			left := uint32(binary.LittleEndian.Uint16(contents[offset+1 : offset+3]))
			top := uint32(binary.LittleEndian.Uint16(contents[offset+3 : offset+5]))
			frameWidth := uint32(binary.LittleEndian.Uint16(contents[offset+5 : offset+7]))
			frameHeight := uint32(binary.LittleEndian.Uint16(contents[offset+7 : offset+9]))
			if frameWidth == 0 || frameHeight == 0 || uint64(left)+uint64(frameWidth) > uint64(width) || uint64(top)+uint64(frameHeight) > uint64(height) {
				return inspected{}, corrupt("GIF frame bounds are invalid")
			}
			frames++
			if frames == 1 {
				result.gifFirstTransparent = pendingTransparency
			}
			pendingTransparency = false
			framePixels += uint64(frameWidth) * uint64(frameHeight)
			if frames > limits.MaxGIFFrames || framePixels > limits.MaxGIFFramePixels {
				return inspected{}, domain.NewError(domain.CodeMediaDimensionsExceeded, "GIF frame budget is exceeded", map[string]any{
					"max_frames":       limits.MaxGIFFrames,
					"max_frame_pixels": limits.MaxGIFFramePixels,
				})
			}
			packed := contents[offset+9]
			offset += 10
			if packed&0x80 != 0 {
				offset += 3 * (1 << (uint(packed&0x07) + 1))
			}
			if offset >= len(contents) {
				return inspected{}, corrupt("GIF image data is truncated")
			}
			offset++ // LZW minimum code size.
			var err error
			offset, err = skipGIFSubBlocks(contents, offset)
			if err != nil {
				return inspected{}, err
			}
		default:
			return inspected{}, corrupt("GIF block stream is corrupt")
		}
	}
	return inspected{}, corrupt("GIF trailer is missing")
}

func skipGIFSubBlocks(contents []byte, offset int) (int, error) {
	for {
		if offset >= len(contents) {
			return 0, corrupt("GIF data sub-block is truncated")
		}
		length := int(contents[offset])
		offset++
		if length == 0 {
			return offset, nil
		}
		if offset+length > len(contents) {
			return 0, corrupt("GIF data sub-block is truncated")
		}
		offset += length
	}
}

func corrupt(message string) error {
	return domain.NewError(domain.CodeMediaCorrupt, message, nil)
}

func previewFailure(err error) error {
	if err == nil {
		return nil
	}
	if errors.Is(err, io.ErrUnexpectedEOF) || errors.Is(err, io.EOF) {
		return domain.NewError(domain.CodeMediaPreviewFailed, "preview generation failed", nil)
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	var domainError *domain.Error
	if errors.As(err, &domainError) {
		return err
	}
	return domain.NewError(domain.CodeMediaPreviewFailed, "preview generation failed", nil)
}
