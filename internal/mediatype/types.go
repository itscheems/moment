// Package mediatype defines Moment's authoritative media capabilities without
// depending on storage, codecs, or transports. Signature inspection remains in
// package media; this package is the shared contract for accepted MIME types,
// canonical extensions, media kinds, and original-byte budgets.
package mediatype

const (
	JPEG      = "image/jpeg"
	PNG       = "image/png"
	WebP      = "image/webp"
	GIF       = "image/gif"
	HEIC      = "image/heic"
	MP4       = "video/mp4"
	QuickTime = "video/quicktime"

	MaxImageBytes int64 = 30 << 20
	MaxVideoBytes int64 = 100 << 20
	MaxInputBytes int64 = MaxVideoBytes
)

type Kind uint8

const (
	Image Kind = iota + 1
	Video
)

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

func Lookup(mimeType string) (Capability, bool) {
	for _, capability := range capabilities {
		if capability.MIMEType == mimeType {
			return capability, true
		}
	}
	return Capability{}, false
}

func IsSupported(mimeType string) bool {
	_, ok := Lookup(mimeType)
	return ok
}

func IsVideo(mimeType string) bool {
	capability, ok := Lookup(mimeType)
	return ok && capability.Kind == Video
}

func Extension(mimeType string) (string, bool) {
	capability, ok := Lookup(mimeType)
	return capability.Extension, ok
}

func MaxBytes(mimeType string) (int64, bool) {
	capability, ok := Lookup(mimeType)
	return capability.MaxBytes, ok
}
