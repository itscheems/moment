package media

import (
	"bytes"
	"compress/zlib"
	"encoding/binary"
	"hash/crc32"
	"io"
)

// Decoders retain the source RGB values, but image.Image has no color-space
// field. Keep the RGB profile with those values through resize and encoding;
// otherwise a browser interprets Display P3 samples as sRGB. EXIF/XMP are not
// copied. Non-RGB profiles cannot describe the RGB output of our decoders.
const maxICCProfileBytes = 1 << 20

func validateICC(profile []byte) error {
	if len(profile) < 132 || len(profile) > maxICCProfileBytes ||
		uint64(binary.BigEndian.Uint32(profile[:4])) != uint64(len(profile)) ||
		string(profile[36:40]) != "acsp" {
		return corrupt("image ICC profile is invalid or exceeds the metadata budget")
	}
	count := uint64(binary.BigEndian.Uint32(profile[128:132]))
	if count > uint64(len(profile)-132)/12 {
		return corrupt("ICC tag table is truncated")
	}
	for i := uint64(0); i < count; i++ {
		tag := profile[132+i*12 : 144+i*12]
		start, size := uint64(binary.BigEndian.Uint32(tag[4:8])), uint64(binary.BigEndian.Uint32(tag[8:12]))
		if start > uint64(len(profile)) || size > uint64(len(profile))-start {
			return corrupt("ICC tag is truncated")
		}
	}
	return nil
}

type jpegICCParts struct {
	parts [256][]byte
	count byte
	bytes int
}

func (p *jpegICCParts) add(segment []byte) error {
	if len(segment) < 14 || segment[12] == 0 || segment[13] == 0 || segment[12] > segment[13] {
		return corrupt("JPEG ICC segment numbering is invalid")
	}
	index, count := segment[12], segment[13]
	if p.parts[index] != nil || (p.count != 0 && p.count != count) {
		return corrupt("JPEG ICC segments are inconsistent")
	}
	p.count = count
	p.bytes += len(segment) - 14
	if p.bytes > maxICCProfileBytes {
		return corrupt("ICC profile exceeds the metadata budget")
	}
	p.parts[index] = segment[14:]
	return nil
}

func (p *jpegICCParts) profile() ([]byte, error) {
	if p.count == 0 {
		return nil, nil
	}
	profile := make([]byte, 0, p.bytes)
	for i := 1; i <= int(p.count); i++ {
		if p.parts[i] == nil {
			return nil, corrupt("JPEG ICC profile is incomplete")
		}
		profile = append(profile, p.parts[i]...)
	}
	return profile, validateICC(profile)
}

func decodePNGICC(data []byte) ([]byte, error) {
	end := bytes.IndexByte(data, 0)
	if end < 1 || end > 79 || end+2 >= len(data) || data[end+1] != 0 || len(data) > maxICCProfileBytes {
		return nil, corrupt("PNG ICC chunk is invalid")
	}
	reader, err := zlib.NewReader(bytes.NewReader(data[end+2:]))
	if err != nil {
		return nil, corrupt("PNG ICC profile is not zlib compressed")
	}
	defer func() { _ = reader.Close() }()
	profile, err := io.ReadAll(io.LimitReader(reader, maxICCProfileBytes+1))
	if err != nil {
		return nil, corrupt("PNG ICC profile is truncated")
	}
	return profile, validateICC(profile)
}

func encodeICCMetadata(profile []byte, png bool) ([]byte, error) {
	if len(profile) == 0 {
		return nil, nil
	}
	if err := validateICC(profile); err != nil {
		return nil, err
	}
	if string(profile[16:20]) != "RGB " {
		return nil, nil
	}
	var output bytes.Buffer
	if !png {
		const partBytes = 65535 - 2 - 14
		count := (len(profile) + partBytes - 1) / partBytes
		for i, start := 1, 0; start < len(profile); i, start = i+1, start+partBytes {
			part := profile[start:min(start+partBytes, len(profile))]
			output.Write([]byte{0xff, 0xe2, byte((len(part) + 16) >> 8), byte(len(part) + 16)})
			output.WriteString("ICC_PROFILE\x00")
			output.Write([]byte{byte(i), byte(count)})
			output.Write(part)
		}
		return output.Bytes(), nil
	}
	var payload bytes.Buffer
	payload.WriteString("Color profile\x00\x00")
	writer := zlib.NewWriter(&payload)
	if _, err := writer.Write(profile); err != nil {
		return nil, err
	}
	if err := writer.Close(); err != nil {
		return nil, err
	}
	chunk := make([]byte, 12+payload.Len())
	binary.BigEndian.PutUint32(chunk[:4], uint32(payload.Len()))
	copy(chunk[4:8], "iCCP")
	copy(chunk[8:], payload.Bytes())
	binary.BigEndian.PutUint32(chunk[len(chunk)-4:], crc32.ChecksumIEEE(chunk[4:len(chunk)-4]))
	return chunk, nil
}

func insertICCMetadata(encoded, metadata []byte, png bool) []byte {
	if len(metadata) == 0 {
		return encoded
	}
	offset := 2 // JPEG SOI; PNG's complete IHDR ends at byte 33.
	if png {
		offset = 33
	}
	result := make([]byte, 0, len(encoded)+len(metadata))
	result = append(result, encoded[:offset]...)
	result = append(result, metadata...)
	return append(result, encoded[offset:]...)
}
