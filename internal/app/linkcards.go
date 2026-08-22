package app

import (
	"bytes"
	"context"
	"errors"
	"io"
	"time"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/linkfetch"
)

// LinkPreviewResolver unfurls one owner-supplied URL. The application layer
// depends on this port, never on an HTTP client, so the guarded transport is
// the only way out to the network.
type LinkPreviewResolver interface {
	Resolve(context.Context, string) (linkfetch.Preview, error)
	Fetcher() *linkfetch.Fetcher
}

// LinkThumbnailStore ingests a downloaded thumbnail as managed media. It is the
// same seam an owner upload uses, so a remote image gets exactly the same
// signature sniffing, MIME allowlist, dimension caps, and content-hash dedup.
type LinkThumbnailStore interface {
	Ingest(ctx context.Context, originalName string, input io.Reader) (domain.ID, string, uint32, uint32, error)
}

// LinkPreview is the application projection of one unfurled URL, after any
// thumbnail has been captured into managed media.
type LinkPreview struct {
	Card              domain.LinkCard
	ThumbnailMIMEType string
	ThumbnailWidth    uint32
	ThumbnailHeight   uint32
	FaviconMIMEType   string
}

// LinkPreviewService turns a URL into a storable card snapshot.
type LinkPreviewService struct {
	resolver   LinkPreviewResolver
	thumbnails LinkThumbnailStore
	clock      func() time.Time
}

// LinkPreviewOptions configures a LinkPreviewService.
type LinkPreviewOptions struct {
	Clock func() time.Time
}

// NewLinkPreviewService builds the service. A nil thumbnail store is allowed:
// the service then produces text-only cards rather than failing, which keeps a
// preview useful on a deployment without media processors.
func NewLinkPreviewService(resolver LinkPreviewResolver, thumbnails LinkThumbnailStore, options LinkPreviewOptions) (*LinkPreviewService, error) {
	if resolver == nil {
		return nil, errors.New("link preview resolver is required")
	}
	clock := options.Clock
	if clock == nil {
		clock = time.Now
	}
	return &LinkPreviewService{resolver: resolver, thumbnails: thumbnails, clock: clock}, nil
}

// Resolve unfurls one URL and captures its thumbnail.
//
// A failure to capture the image is never a failure to preview: the card
// degrades to text rather than blocking publication, which mirrors how a
// missing alt text does not block a post.
func (service *LinkPreviewService) Resolve(ctx context.Context, rawURL string) (LinkPreview, error) {
	if err := contextError(ctx); err != nil {
		return LinkPreview{}, err
	}
	normalized, err := domain.NormalizeLinkURL(rawURL)
	if err != nil {
		return LinkPreview{}, err
	}
	preview, err := service.resolver.Resolve(ctx, normalized)
	if err != nil {
		return LinkPreview{}, normalizeLinkPreviewError(err)
	}

	card := domain.LinkCard{
		Position:        0,
		SourceURL:       preview.SourceURL,
		CanonicalURL:    preview.CanonicalURL,
		Provider:        preview.Provider,
		Kind:            preview.Kind,
		Title:           preview.Title,
		Description:     preview.Description,
		SiteName:        preview.SiteName,
		AuthorName:      preview.AuthorName,
		DurationSeconds: preview.DurationSeconds,
		ViewCount:       preview.ViewCount,
		CommentCount:    preview.CommentCount,
		LikeCount:       preview.LikeCount,
		EmbedURL:        preview.EmbedURL,
		CapturedAt:      domain.TimeToMillis(domain.NormalizeTime(service.clock())),
	}

	result := LinkPreview{}
	if preview.ImageURL != "" && service.thumbnails != nil {
		if id, mime, width, height, err := service.captureImage(ctx, preview.ImageURL, "link-preview"); err == nil {
			card.ThumbnailMediaID = id.String()
			result.ThumbnailMIMEType = mime
			result.ThumbnailWidth = width
			result.ThumbnailHeight = height
		}
	}
	// A site mark is optional in exactly the same way. Many sites publish only
	// an ICO, which managed media refuses, and the card then shows no mark
	// rather than reaching out to the origin from the reader's browser.
	if preview.IconURL != "" && service.thumbnails != nil {
		if id, mime, _, _, err := service.captureImage(ctx, preview.IconURL, "link-favicon"); err == nil {
			card.FaviconMediaID = id.String()
			result.FaviconMIMEType = mime
		}
	}

	if err := card.Validate(); err != nil {
		return LinkPreview{}, err
	}
	result.Card = card
	return result, nil
}

// captureImage downloads a remote image through the guarded fetcher and hands
// the bytes to the managed media store. The remote image receives no exemption
// from the owner-upload rules: an SVG or an unsupported codec is refused here
// exactly as it would be on upload, and the card simply renders without it.
func (service *LinkPreviewService) captureImage(ctx context.Context, imageURL, name string) (domain.ID, string, uint32, uint32, error) {
	response, err := service.resolver.Fetcher().Get(ctx, imageURL, []string{"image/*"}, linkfetch.DefaultMaxImageBytes)
	if err != nil {
		return "", "", 0, 0, err
	}
	id, mime, width, height, err := service.thumbnails.Ingest(ctx, name, bytes.NewReader(response.Body))
	if err != nil {
		return "", "", 0, 0, err
	}
	return id, mime, width, height, nil
}

func normalizeLinkPreviewError(err error) error {
	var domainError *domain.Error
	if errors.As(err, &domainError) {
		return domainError
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return err
	}
	// Every transport failure folds into one stable code so a private failure
	// mode of the fetched origin is never mirrored back to the client.
	return domain.NewError(domain.CodeLinkPreviewUnavailable, "link preview is unavailable for this URL", nil)
}
