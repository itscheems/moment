// Package media validates hostile image input, stores immutable originals, and
// builds deterministic, replaceable previews.
package media

import (
	"crypto/rand"
	"image/color"
	"io"
	"sync"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/mediatype"
)

// Product ceilings and recipe identity are exported for transport diagnostics
// and deterministic maintenance tooling.
const (
	// Images and videos have separate immutable-original budgets. Previews have
	// a smaller delivery budget so accepting a large source never makes timeline
	// rendering load the same bytes implicitly.
	DefaultMaxImageBytes      int64  = mediatype.MaxImageBytes
	DefaultMaxVideoBytes      int64  = mediatype.MaxVideoBytes
	DefaultMaxInputBytes      int64  = mediatype.MaxInputBytes
	DefaultMaxBytes           int64  = DefaultMaxImageBytes // Deprecated: use the kind-specific ceiling.
	DefaultMaxDimension       uint32 = 16_384
	DefaultMaxPixels          uint64 = 80_000_000
	DefaultMaxGIFFrames       uint32 = 300
	DefaultMaxGIFFramePixels  uint64 = 160_000_000
	DefaultPreviewMaxBytes    int64  = 10 << 20
	DefaultPreviewMaxEdge     uint32 = 1_600
	DefaultPreviewConcurrency        = 2
	DefaultVideoConcurrency          = 1
	DefaultMaxVideoDuration          = 5 * time.Minute

	// RecipeVersion changes whenever any output byte semantics change.
	RecipeVersion = "v5-max1600-display10m-heic-libheif-mp4-h264-crf-faststart"

	// Nine bytes cover the largest supported 16-bit RGBA decode plus bounded
	// preview intermediates; fixed bytes cover compressed input and encoders.
	previewMemoryBytesPerPixel int64 = 9
	previewMemoryFixedBytes    int64 = 64 << 20
)

// Limits are hard resource ceilings. Zero values select the bounded defaults.
// Values may be lowered by callers but cannot exceed the product ceilings.
type Limits struct {
	// MaxBytes is a compatibility override that lowers both kind-specific
	// ceilings. New callers should configure MaxImageBytes and MaxVideoBytes.
	MaxBytes          int64
	MaxImageBytes     int64
	MaxVideoBytes     int64
	MaxDimension      uint32
	MaxPixels         uint64
	MaxGIFFrames      uint32
	MaxGIFFramePixels uint64
	// PreviewMaxBytes is the hard cap for every derived display artifact.
	PreviewMaxBytes int64
	PreviewMaxEdge  uint32
	// PreviewWorkers bounds image decode/encode work, including HEIC conversion.
	PreviewWorkers int
	// VideoWorkers independently bounds ffprobe and FFmpeg work.
	VideoWorkers int
}

func (limits Limits) normalized() (Limits, error) {
	defaults := Limits{
		MaxImageBytes:     DefaultMaxImageBytes,
		MaxVideoBytes:     DefaultMaxVideoBytes,
		MaxDimension:      DefaultMaxDimension,
		MaxPixels:         DefaultMaxPixels,
		MaxGIFFrames:      DefaultMaxGIFFrames,
		MaxGIFFramePixels: DefaultMaxGIFFramePixels,
		PreviewMaxBytes:   DefaultPreviewMaxBytes,
		PreviewMaxEdge:    DefaultPreviewMaxEdge,
		PreviewWorkers:    DefaultPreviewConcurrency,
		VideoWorkers:      DefaultVideoConcurrency,
	}
	if limits.MaxBytes != 0 {
		if limits.MaxImageBytes == 0 {
			limits.MaxImageBytes = limits.MaxBytes
		}
		if limits.MaxVideoBytes == 0 {
			limits.MaxVideoBytes = limits.MaxBytes
		}
	}
	if limits.MaxImageBytes == 0 {
		limits.MaxImageBytes = defaults.MaxImageBytes
	}
	if limits.MaxVideoBytes == 0 {
		limits.MaxVideoBytes = defaults.MaxVideoBytes
	}
	limits.MaxBytes = max(limits.MaxImageBytes, limits.MaxVideoBytes)
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
	if limits.PreviewMaxBytes == 0 {
		limits.PreviewMaxBytes = defaults.PreviewMaxBytes
	}
	if limits.PreviewMaxEdge == 0 {
		limits.PreviewMaxEdge = defaults.PreviewMaxEdge
	}
	if limits.PreviewWorkers == 0 {
		limits.PreviewWorkers = defaults.PreviewWorkers
	}
	if limits.VideoWorkers == 0 {
		limits.VideoWorkers = defaults.VideoWorkers
	}
	if limits.MaxImageBytes < 1 || limits.MaxImageBytes > defaults.MaxImageBytes ||
		limits.MaxVideoBytes < 1 || limits.MaxVideoBytes > defaults.MaxVideoBytes ||
		limits.MaxDimension < 1 || limits.MaxDimension > defaults.MaxDimension ||
		limits.MaxPixels < 1 || limits.MaxPixels > defaults.MaxPixels ||
		limits.MaxGIFFrames < 1 || limits.MaxGIFFrames > defaults.MaxGIFFrames ||
		limits.MaxGIFFramePixels < 1 || limits.MaxGIFFramePixels > defaults.MaxGIFFramePixels ||
		limits.PreviewMaxBytes < 1 || limits.PreviewMaxBytes > defaults.PreviewMaxBytes ||
		limits.PreviewMaxEdge < 1 || limits.PreviewMaxEdge > defaults.PreviewMaxEdge ||
		limits.PreviewWorkers < 1 || limits.PreviewWorkers > 8 ||
		limits.VideoWorkers < 1 || limits.VideoWorkers > 4 {
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
	formatHEIC
	formatMP4
	formatQuickTime
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
	case formatHEIC:
		return "image/heic"
	case formatMP4:
		return mediatype.MP4
	case formatQuickTime:
		return mediatype.QuickTime
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
	case formatHEIC:
		return "heic"
	case formatMP4:
		return "mp4"
	case formatQuickTime:
		return "mov"
	default:
		return ""
	}
}

func (f format) isVideo() bool {
	return f == formatMP4 || f == formatQuickTime
}

func (f format) maxOriginalBytes(limits Limits) int64 {
	if f.isVideo() {
		return limits.MaxVideoBytes
	}
	return limits.MaxImageBytes
}

func formatFromMIMEType(mimeType string) (format, bool) {
	switch mimeType {
	case mediatype.JPEG:
		return formatJPEG, true
	case mediatype.PNG:
		return formatPNG, true
	case mediatype.WebP:
		return formatWebP, true
	case mediatype.GIF:
		return formatGIF, true
	case mediatype.HEIC:
		return formatHEIC, true
	case mediatype.MP4:
		return formatMP4, true
	case mediatype.QuickTime:
		return formatQuickTime, true
	default:
		return 0, false
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
	durationSeconds     float64
	decodedSource       []byte
	codec               string
	pixelFormat         string
}

// Media is the authoritative metadata persisted beside an immutable original.
type Media struct {
	ID                domain.ID  `json:"id"`
	SHA256            string     `json:"sha256"`
	StoragePath       string     `json:"-"`
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

// UploadResult is the transport-neutral result of managed ingestion.
type UploadResult struct {
	Media       Media   `json:"media"`
	Preview     Preview `json:"preview"`
	OriginalURL string  `json:"original_url"`
	PreviewURL  string  `json:"preview_url"`
}

// IngestObserver receives bounded derived artifacts while an ingest is still
// running. Callbacks must return quickly; they are advisory and never become
// part of the media authority transaction.
type IngestObserver struct {
	VideoPosterReady func([]byte)
}

// NewUploadResult adds the explicit public original and preview routes.
func NewUploadResult(value Media, preview Preview) UploadResult {
	id := value.ID.String()
	return UploadResult{
		Media:       value,
		Preview:     preview,
		OriginalURL: "/api/v1/media/" + id,
		PreviewURL:  "/api/v1/media/" + id + "/preview",
	}
}

// Asset is one bounded immutable response body and its controlled metadata.
type Asset struct {
	Media    Media
	Contents []byte
	MIMEType string
	ETag     string
}

// StreamAsset keeps a safe seekable file and its media lease alive for the
// complete delivery operation. Callers must close it on every response path.
// The close operation is idempotent so transport cleanup can be defensive.
type StreamAsset struct {
	Media    Media
	Reader   io.ReadSeekCloser
	MIMEType string
	ETag     string

	closeOnce sync.Once
	closeErr  error
	lease     releasableLease
}

// Close releases the file before the shared media lease. This order prevents
// prune or import from replacing authority while the file descriptor is live.
func (asset *StreamAsset) Close() error {
	if asset == nil {
		return nil
	}
	asset.closeOnce.Do(func() {
		if asset.Reader != nil {
			asset.closeErr = asset.Reader.Close()
		}
		if asset.lease != nil {
			if err := asset.lease.Release(); asset.closeErr == nil {
				asset.closeErr = err
			}
			asset.lease = nil
		}
	})
	return asset.closeErr
}

// ListItem joins media metadata with the one authoritative resolver state and
// current derived-cache status.
type ListItem struct {
	Media               Media  `json:"media"`
	Referenced          bool   `json:"referenced"`
	ReferenceConsistent bool   `json:"reference_consistent"`
	PreviewStatus       string `json:"preview_status"`
}

// ListResult reports stable lifecycle state for all managed media.
type ListResult struct {
	RecipeVersion string     `json:"recipe_version"`
	Items         []ListItem `json:"items"`
}

// VerifyItem reports one immutable original and reference check.
type VerifyItem struct {
	MediaID string `json:"media_id"`
	Status  string `json:"status"`
	Code    string `json:"code,omitempty"`
}

// VerifyResult is a complete checksum and resolver-consistency diagnostic.
type VerifyResult struct {
	Total                 int          `json:"total"`
	Healthy               int          `json:"healthy"`
	Corrupt               int          `json:"corrupt"`
	ReferenceInconsistent int          `json:"reference_inconsistent"`
	Items                 []VerifyItem `json:"items"`
}

// RebuildResult reports one derived preview inspection or repair pass.
type RebuildResult struct {
	RecipeVersion            string `json:"recipe_version"`
	Total                    int    `json:"total"`
	Ready                    int    `json:"ready"`
	Missing                  int    `json:"missing"`
	Stale                    int    `json:"stale"`
	Rebuilt                  int    `json:"rebuilt"`
	EstimatedAdditionalBytes int64  `json:"estimated_additional_bytes"`
	DryRun                   bool   `json:"dry_run"`
}

// PruneItem identifies one exact continuously unreferenced candidate.
type PruneItem struct {
	MediaID           string    `json:"media_id"`
	SHA256            string    `json:"sha256"`
	ByteSize          int64     `json:"byte_size"`
	UnreferencedSince time.Time `json:"unreferenced_since"`
}

// PruneResult reports a dry-run plan or completed destructive pass.
type PruneResult struct {
	Eligible        int         `json:"eligible"`
	Deleted         int         `json:"deleted"`
	CleanupFailures int         `json:"cleanup_failures"`
	BytesReclaimed  int64       `json:"bytes_reclaimed"`
	DryRun          bool        `json:"dry_run"`
	Items           []PruneItem `json:"items"`
}

// Options supplies deterministic test seams and bounded production resources.
type Options struct {
	Limits           Limits
	Clock            func() time.Time
	Entropy          io.Reader
	HEIFConvertPath  string
	FFmpegPath       string
	FFprobePath      string
	ProcessorTimeout time.Duration
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
	if options.ProcessorTimeout == 0 {
		options.ProcessorTimeout = 2 * time.Minute
	}
	if options.ProcessorTimeout < time.Second || options.ProcessorTimeout > 10*time.Minute {
		return Options{}, domain.NewError(domain.CodeInvalidArgument, "media processor timeout must be between one second and ten minutes", nil)
	}
	return options, nil
}
