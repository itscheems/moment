package media

import (
	"bytes"
	"encoding/binary"
	"errors"
	"image"
	"image/gif"
	"image/jpeg"
	"image/png"
	"io"

	"github.com/moment/moment/internal/domain"
	"golang.org/x/image/webp"
)

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
		result, err = inspectPNG(contents)
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
	// DecodeConfig is still cheap, but proves the selected decoder agrees with
	// the hand-scanned header before any full pixel allocation is permitted.
	config, err := decodeConfig(result.format, contents)
	if err != nil {
		return inspected{}, corrupt("image header is corrupt")
	}
	if config.Width <= 0 || config.Height <= 0 || uint32(config.Width) != result.width || uint32(config.Height) != result.height {
		return inspected{}, corrupt("image header dimensions are inconsistent")
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
	case formatWebP:
		return webp.DecodeConfig(reader)
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

func inspectPNG(contents []byte) (inspected, error) {
	if len(contents) < 33 || string(contents[12:16]) != "IHDR" || binary.BigEndian.Uint32(contents[8:12]) != 13 {
		return inspected{}, corrupt("PNG header is corrupt")
	}
	return inspected{
		format:      formatPNG,
		width:       binary.BigEndian.Uint32(contents[16:20]),
		height:      binary.BigEndian.Uint32(contents[20:24]),
		orientation: 1,
	}, nil
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
			if orientation, ok := exifOrientation(segment[6:]); ok {
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
	for offset := 12; offset+8 <= int(declared); {
		kind := string(contents[offset : offset+4])
		length := int(binary.LittleEndian.Uint32(contents[offset+4 : offset+8]))
		start := offset + 8
		end := start + length
		if length < 0 || end > int(declared) {
			return inspected{}, corrupt("WebP chunk is truncated")
		}
		chunk := contents[start:end]
		switch kind {
		case "VP8X":
			if len(chunk) < 10 {
				return inspected{}, corrupt("WebP extended header is truncated")
			}
			if chunk[0]&0x02 != 0 {
				return inspected{}, domain.NewError(domain.CodeMediaTypeUnsupported, "animated WebP is not supported", nil)
			}
			result.width = readUint24LE(chunk[4:7]) + 1
			result.height = readUint24LE(chunk[7:10]) + 1
		case "VP8 ":
			if len(chunk) < 10 || !bytes.Equal(chunk[3:6], []byte{0x9d, 0x01, 0x2a}) {
				return inspected{}, corrupt("WebP lossy frame header is corrupt")
			}
			if result.width == 0 {
				result.width = uint32(binary.LittleEndian.Uint16(chunk[6:8]) & 0x3fff)
				result.height = uint32(binary.LittleEndian.Uint16(chunk[8:10]) & 0x3fff)
			}
		case "VP8L":
			if len(chunk) < 5 || chunk[0] != 0x2f {
				return inspected{}, corrupt("WebP lossless frame header is corrupt")
			}
			if result.width == 0 {
				bits := binary.LittleEndian.Uint32(chunk[1:5])
				result.width = bits&0x3fff + 1
				result.height = bits>>14&0x3fff + 1
			}
		}
		offset = end + length%2
	}
	if result.width == 0 || result.height == 0 {
		return inspected{}, corrupt("WebP frame dimensions are missing")
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
	offset := 13
	if contents[10]&0x80 != 0 {
		offset += 3 * (1 << (uint(contents[10]&0x07) + 1))
	}
	if offset > len(contents) {
		return inspected{}, corrupt("GIF global color table is truncated")
	}
	var frames uint32
	var framePixels uint64
	for offset < len(contents) {
		switch contents[offset] {
		case 0x3b:
			if frames == 0 {
				return inspected{}, corrupt("GIF contains no image frames")
			}
			return inspected{format: formatGIF, width: width, height: height, orientation: 1, animated: frames > 1}, nil
		case 0x21:
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
	var domainError *domain.Error
	if errors.As(err, &domainError) {
		return err
	}
	return domain.NewError(domain.CodeMediaPreviewFailed, "preview generation failed", nil)
}
