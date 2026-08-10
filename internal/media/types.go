// Package media validates hostile image input, stores immutable originals, and
// builds deterministic, replaceable previews.
package media

import (
	"crypto/rand"
	"image/color"
	"io"
	"time"

	"github.com/moment/moment/internal/domain"
)

// Product ceilings and recipe identity are exported for transport diagnostics
// and deterministic maintenance tooling.
const (
	DefaultMaxBytes           int64  = 10 << 20
	DefaultMaxDimension       uint32 = 16_384
	DefaultMaxPixels          uint64 = 80_000_000
	DefaultMaxGIFFrames       uint32 = 300
	DefaultMaxGIFFramePixels  uint64 = 160_000_000
	DefaultPreviewMaxEdge     uint32 = 1_600
	DefaultPreviewConcurrency        = 2

	// RecipeVersion changes whenever any output byte semantics change.
	RecipeVersion = "v2-max1600-orient-all-jpeg82-alpha-png-gif-canvas-first"

	// Nine bytes cover the largest supported 16-bit RGBA decode plus bounded
	// preview intermediates; fixed bytes cover compressed input and encoders.
	previewMemoryBytesPerPixel int64 = 9
	previewMemoryFixedBytes    int64 = 64 << 20
)

// Limits are hard resource ceilings. Zero values select the bounded defaults.
// Values may be lowered by callers but cannot exceed the product ceilings.
type Limits struct {
	MaxBytes          int64
	MaxDimension      uint32
	MaxPixels         uint64
	MaxGIFFrames      uint32
	MaxGIFFramePixels uint64
	PreviewMaxEdge    uint32
	PreviewWorkers    int
}

func (limits Limits) normalized() (Limits, error) {
	defaults := Limits{
		MaxBytes:          DefaultMaxBytes,
		MaxDimension:      DefaultMaxDimension,
		MaxPixels:         DefaultMaxPixels,
		MaxGIFFrames:      DefaultMaxGIFFrames,
		MaxGIFFramePixels: DefaultMaxGIFFramePixels,
		PreviewMaxEdge:    DefaultPreviewMaxEdge,
		PreviewWorkers:    DefaultPreviewConcurrency,
	}
	if limits.MaxBytes == 0 {
		limits.MaxBytes = defaults.MaxBytes
	}
	if limits.MaxDimension == 0 {
		limits.MaxDimension = defaults.MaxDimension
	}
	if limits.MaxPixels == 0 {
		limits.MaxPixels = defaults.MaxPixels
	}
	if limits.MaxGIFFrames == 0 {
		limits.MaxGIFFrames = defaults.MaxGIFFrames
	}
	if limits.MaxGIFFramePixels == 0 {
		limits.MaxGIFFramePixels = defaults.MaxGIFFramePixels
	}
	if limits.PreviewMaxEdge == 0 {
		limits.PreviewMaxEdge = defaults.PreviewMaxEdge
	}
	if limits.PreviewWorkers == 0 {
		limits.PreviewWorkers = defaults.PreviewWorkers
	}
	if limits.MaxBytes < 1 || limits.MaxBytes > defaults.MaxBytes ||
		limits.MaxDimension < 1 || limits.MaxDimension > defaults.MaxDimension ||
		limits.MaxPixels < 1 || limits.MaxPixels > defaults.MaxPixels ||
		limits.MaxGIFFrames < 1 || limits.MaxGIFFrames > defaults.MaxGIFFrames ||
		limits.MaxGIFFramePixels < 1 || limits.MaxGIFFramePixels > defaults.MaxGIFFramePixels ||
		limits.PreviewMaxEdge < 1 || limits.PreviewMaxEdge > defaults.PreviewMaxEdge ||
		limits.PreviewWorkers < 1 || limits.PreviewWorkers > 8 {
		return Limits{}, domain.NewError(domain.CodeInvalidArgument, "media limits must be positive and no greater than product ceilings", nil)
	}
	return limits, nil
}

type format uint8

const (
	formatJPEG format = iota + 1
	formatPNG
	formatWebP
	formatGIF
)

func (f format) mimeType() string {
	switch f {
	case formatJPEG:
		return "image/jpeg"
	case formatPNG:
		return "image/png"
	case formatWebP:
		return "image/webp"
	case formatGIF:
		return "image/gif"
	default:
		return ""
	}
}

func (f format) extension() string {
	switch f {
	case formatJPEG:
		return "jpg"
	case formatPNG:
		return "png"
	case formatWebP:
		return "webp"
	case formatGIF:
		return "gif"
	default:
		return ""
	}
}

type inspected struct {
	format              format
	width               uint32
	height              uint32
	orientation         uint8
	animated            bool
	gifBackground       color.NRGBA
	gifBackgroundSet    bool
	gifFirstTransparent bool
}

// Media is the authoritative metadata persisted beside an immutable original.
type Media struct {
	ID                domain.ID  `json:"id"`
	SHA256            string     `json:"sha256"`
	StoragePath       string     `json:"storage_path"`
	OriginalName      string     `json:"original_name"`
	MIMEType          string     `json:"mime_type"`
	ByteSize          int64      `json:"byte_size"`
	Width             uint32     `json:"width"`
	Height            uint32     `json:"height"`
	CreatedAt         time.Time  `json:"created_at"`
	UnreferencedSince *time.Time `json:"unreferenced_since"`
	AnimatedOriginal  bool       `json:"animated_original"`
}

// Preview describes a complete cache artifact. The path is an internal local
// path and is deliberately omitted from JSON transport contracts.
type Preview struct {
	Path             string `json:"-"`
	RecipeVersion    string `json:"recipe_version"`
	SHA256           string `json:"sha256"`
	MIMEType         string `json:"mime_type"`
	Width            uint32 `json:"width"`
	Height           uint32 `json:"height"`
	AnimatedOriginal bool   `json:"animated_original"`
}

// Options supplies deterministic test seams and bounded production resources.
type Options struct {
	Limits  Limits
	Clock   func() time.Time
	Entropy io.Reader
}

func normalizeOptions(options Options) (Options, error) {
	limits, err := options.Limits.normalized()
	if err != nil {
		return Options{}, err
	}
	options.Limits = limits
	if options.Clock == nil {
		options.Clock = time.Now
	}
	if options.Entropy == nil {
		options.Entropy = rand.Reader
	}
	return options, nil
}
