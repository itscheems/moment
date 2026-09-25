package app

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"path"
	"strings"
	"unicode"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/mediacontract"
)

const (
	shareTitleLimit       = 100
	shareDescriptionLimit = 200
	sharePostLeadLimit    = 72
)

// SharePage identifies public SPA routes with stable metadata semantics.
type SharePage uint8

const (
	// SharePageTimeline is the public chronological home route.
	SharePageTimeline SharePage = iota + 1
	// SharePageSearch is the public memory search route.
	SharePageSearch
	// SharePageArchive is the public calendar archive route.
	SharePageArchive
)

// ShareMetadata is a bounded, transport-independent description of one SPA
// shell. Empty absolute URLs are intentionally omitted by the HTTP renderer.
type ShareMetadata struct {
	Title        string
	Description  string
	Type         string
	CanonicalURL string
	ImageURL     string
	NoIndex      bool
	PostFound    bool
}

// ShareSiteReader exposes only the portable public site identity.
type ShareSiteReader interface {
	Get(context.Context) (SiteSettings, error)
}

// SharePostReader exposes the same published post query used by public APIs.
type SharePostReader interface {
	Get(context.Context, domain.ID, Viewer) (Post, error)
}

// ShareMetadataService prepares route metadata without depending on HTTP
// request hosts, Gin, or React rendering.
type ShareMetadataService struct {
	site  ShareSiteReader
	posts SharePostReader
}

// NewShareMetadataService composes the read-only site and post authorities.
func NewShareMetadataService(site ShareSiteReader, posts SharePostReader) (*ShareMetadataService, error) {
	if site == nil || posts == nil {
		return nil, errors.New("share metadata requires site and post readers")
	}
	return &ShareMetadataService{site: site, posts: posts}, nil
}

// Page returns metadata for one known public collection route.
func (service *ShareMetadataService) Page(ctx context.Context, page SharePage) (ShareMetadata, error) {
	settings, err := service.site.Get(ctx)
	if err != nil {
		return ShareMetadata{}, err
	}
	metadata := genericShareMetadata(settings)
	switch page {
	case SharePageTimeline:
		metadata.CanonicalURL = publicShareURL(settings.PublicBaseURL)
	case SharePageSearch:
		metadata.Title = boundedShareText("搜索 · "+metadata.Title, shareTitleLimit)
		metadata.CanonicalURL = publicShareURL(settings.PublicBaseURL, "search")
	case SharePageArchive:
		metadata.Title = boundedShareText("归档 · "+metadata.Title, shareTitleLimit)
		metadata.CanonicalURL = publicShareURL(settings.PublicBaseURL, "archive")
	default:
		metadata.NoIndex = true
	}
	return metadata, nil
}

// Fallback returns safe generic metadata for private, malformed, or unknown
// SPA routes. It never canonicalizes the unrecognized request path.
func (service *ShareMetadataService) Fallback(ctx context.Context) (ShareMetadata, error) {
	settings, err := service.site.Get(ctx)
	if err != nil {
		return ShareMetadata{}, err
	}
	metadata := genericShareMetadata(settings)
	metadata.CanonicalURL = publicShareURL(settings.PublicBaseURL)
	metadata.NoIndex = true
	return metadata, nil
}

// Post returns article metadata for an existing post and generic noindex
// metadata for a canonical but missing post ID.
func (service *ShareMetadataService) Post(ctx context.Context, id domain.ID) (ShareMetadata, error) {
	settings, err := service.site.Get(ctx)
	if err != nil {
		return ShareMetadata{}, err
	}
	// Share metadata is written for whoever follows a link -- a crawler, a
	// chat preview -- so it reads as a visitor even when the owner's own
	// browser asked for the page. A hidden post shares nothing.
	post, err := service.posts.Get(ctx, id, PublicViewer())
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return ShareMetadata{}, err
		}
		metadata := genericShareMetadata(settings)
		metadata.CanonicalURL = publicShareURL(settings.PublicBaseURL)
		metadata.NoIndex = true
		return metadata, nil
	}
	// A moment has no title, so its first visible line stands in for one and
	// the whole body becomes the description. An article has both already, and
	// using them is the whole reason it has a title.
	description, lead, err := shareTextOf(post)
	if err != nil {
		return ShareMetadata{}, err
	}
	description = boundedShareText(description, shareDescriptionLimit)
	if description == "" {
		description = genericShareDescription(settings)
	}
	siteTitle := genericShareTitle(settings)
	lead = boundedShareText(lead, sharePostLeadLimit)
	if lead == "" {
		lead = boundedShareText(description, sharePostLeadLimit)
	}
	if lead == "" {
		lead = "Moment"
	}
	metadata := ShareMetadata{
		Title:        boundedShareText(lead+" · "+siteTitle, shareTitleLimit),
		Description:  description,
		Type:         "article",
		CanonicalURL: publicShareURL(settings.PublicBaseURL, "m", id.String()),
		PostFound:    true,
	}
	// An article leads with its cover; a moment leads with its first picture.
	switch {
	case post.ArticleCard != nil && post.ArticleCard.Cover != nil:
		metadata.ImageURL = derivedShareURL(settings.PublicBaseURL, "api", "v1", "media", post.ArticleCard.Cover.MediaID.String(), "preview")
	case post.Entry.Kind != domain.KindArticle && len(post.Media) > 0:
		metadata.ImageURL = derivedShareURL(settings.PublicBaseURL, "api", "v1", "media", post.Media[0].MediaID.String(), "preview")
	default:
		metadata.ImageURL = avatarShareURL(settings)
	}
	return metadata, nil
}

// shareTextOf answers the description and the stand-in title for one post.
func shareTextOf(post Post) (string, string, error) {
	if post.Entry.Kind == domain.KindArticle {
		if post.Article == nil {
			return "", "", fmt.Errorf("article %s was read without its body", post.Entry.ID)
		}
		return post.Article.Excerpt(), post.Article.Title, nil
	}
	body, present := post.Entry.Markdown()
	if !present {
		return "", "", fmt.Errorf("moment %s was read without its body", post.Entry.ID)
	}
	description, err := domain.NormalizeContentV2(body)
	if err != nil {
		return "", "", err
	}
	for _, line := range strings.Split(body, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		lead, lineErr := domain.VisibleContent(line)
		if lineErr != nil {
			return "", "", lineErr
		}
		if lead != "" {
			return description, lead, nil
		}
	}
	return description, "", nil
}

func genericShareMetadata(settings SiteSettings) ShareMetadata {
	return ShareMetadata{
		Title:       genericShareTitle(settings),
		Description: genericShareDescription(settings),
		Type:        "website",
		ImageURL:    avatarShareURL(settings),
	}
}

func genericShareTitle(settings SiteSettings) string {
	title := boundedShareText(settings.Title, shareTitleLimit)
	if title == "" {
		return "Moment"
	}
	return title
}

func genericShareDescription(settings SiteSettings) string {
	description := boundedShareText(settings.Bio, shareDescriptionLimit)
	if description == "" {
		return "记下此刻，找回后来。"
	}
	return description
}

func avatarShareURL(settings SiteSettings) string {
	if settings.AvatarMediaID == nil {
		return ""
	}
	id, err := domain.ParseID(*settings.AvatarMediaID)
	if err != nil {
		return ""
	}
	return derivedShareURL(settings.PublicBaseURL, "api", "v1", "media", id.String(), "preview")
}

func derivedShareURL(base *string, elements ...string) string {
	value := publicShareURL(base, elements...)
	if value == "" {
		return ""
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return ""
	}
	query := parsed.Query()
	query.Set("v", mediacontract.RecipeVersion)
	parsed.RawQuery = query.Encode()
	return parsed.String()
}

func publicShareURL(base *string, elements ...string) string {
	if base == nil || *base == "" {
		return ""
	}
	normalized, err := NormalizePublicBaseURL(*base, true)
	if err != nil || normalized != *base {
		return ""
	}
	parsed, err := url.Parse(normalized)
	if err != nil {
		return ""
	}
	if len(elements) > 0 {
		parts := make([]string, 0, len(elements)+1)
		if parsed.Path != "" {
			parts = append(parts, parsed.Path)
		}
		parts = append(parts, elements...)
		parsed.Path = path.Join(parts...)
		if !strings.HasPrefix(parsed.Path, "/") {
			parsed.Path = "/" + parsed.Path
		}
	}
	parsed.RawPath = ""
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func boundedShareText(value string, limit int) string {
	fields := strings.FieldsFunc(value, func(character rune) bool {
		return unicode.IsSpace(character) || unicode.IsControl(character) || unicode.Is(unicode.Cf, character)
	})
	runes := []rune(strings.Join(fields, " "))
	if len(runes) <= limit {
		return string(runes)
	}
	if limit < 2 {
		return string(runes[:limit])
	}
	return string(runes[:limit-1]) + "…"
}
