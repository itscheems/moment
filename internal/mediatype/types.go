// Package mediatype defines Moment's authoritative media capabilities without
// depending on storage, codecs, or transports. Signature inspection remains in
// package media; this package is the shared contract for accepted MIME types,
// canonical extensions, media kinds, and original-byte budgets.
package mediatype

const (
	// JPEG is the JPEG image MIME type.
	JPEG = "image/jpeg"
	// PNG is the PNG image MIME type.
	PNG = "image/png"
	// WebP is the WebP image MIME type.
	WebP = "image/webp"
	// GIF is the GIF image MIME type.
	GIF = "image/gif"
	// HEIC is the HEIC image MIME type.
	HEIC = "image/heic"
	// MP4 is the MP4 video MIME type.
	MP4 = "video/mp4"
	// QuickTime is the QuickTime video MIME type.
	QuickTime = "video/quicktime"

	// MaxImageBytes is the accepted image original-byte budget.
	MaxImageBytes int64 = 30 << 20
	// MaxVideoBytes is the accepted video original-byte budget.
	MaxVideoBytes int64 = 300 << 20
	// MaxInputBytes is the largest supported original-byte budget.
	MaxInputBytes int64 = MaxVideoBytes
)

// Kind identifies whether a media capability represents an image or video.
type Kind uint8

const (
	// Image identifies image media.
	Image Kind = iota + 1
	// Video identifies video media.
	Video
)

// Capability describes the canonical storage and size contract for a MIME type.
type Capability struct {
	MIMEType  string
	Extension string
	Kind      Kind
	MaxBytes  int64
}

var capabilities = [...]Capability{
	{MIMEType: JPEG, Extension: "jpg", Kind: Image, MaxBytes: MaxImageBytes},
	{MIMEType: PNG, Extension: "png", Kind: Image, MaxBytes: MaxImageBytes},
	{MIMEType: WebP, Extension: "webp", Kind: Image, MaxBytes: MaxImageBytes},
	{MIMEType: GIF, Extension: "gif", Kind: Image, MaxBytes: MaxImageBytes},
	{MIMEType: HEIC, Extension: "heic", Kind: Image, MaxBytes: MaxImageBytes},
	{MIMEType: MP4, Extension: "mp4", Kind: Video, MaxBytes: MaxVideoBytes},
	{MIMEType: QuickTime, Extension: "mov", Kind: Video, MaxBytes: MaxVideoBytes},
}

// Lookup returns the capability registered for mimeType.
func Lookup(mimeType string) (Capability, bool) {
	for _, capability := range capabilities {
		if capability.MIMEType == mimeType {
			return capability, true
		}
	}
	return Capability{}, false
}

// IsSupported reports whether mimeType is accepted by Moment.
func IsSupported(mimeType string) bool {
	_, ok := Lookup(mimeType)
	return ok
}

// IsVideo reports whether mimeType is a supported video type.
func IsVideo(mimeType string) bool {
	capability, ok := Lookup(mimeType)
	return ok && capability.Kind == Video
}

// Extension returns the canonical filename extension for mimeType.
func Extension(mimeType string) (string, bool) {
	capability, ok := Lookup(mimeType)
	return capability.Extension, ok
}

// MaxBytes returns the accepted original-byte budget for mimeType.
func MaxBytes(mimeType string) (int64, bool) {
	capability, ok := Lookup(mimeType)
	return capability.MaxBytes, ok
}

// All returns a copy of the authoritative accepted media capabilities.
func All() []Capability { return append([]Capability(nil), capabilities[:]...) }
