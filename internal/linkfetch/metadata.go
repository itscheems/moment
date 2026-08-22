package linkfetch

import (
	"context"
	"encoding/json"
	"net/url"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"github.com/moment/moment/internal/domain"
)

// Metadata is the provider-independent result of reading a page. Every field is
// optional: a card is still useful with nothing but a canonical URL and a host.
type Metadata struct {
	CanonicalURL    string
	Title           string
	Description     string
	SiteName        string
	AuthorName      string
	ImageURL        string
	IconURL         string
	DurationSeconds *int
	Kind            domain.LinkCardKind
	ViewCount       *int64
	CommentCount    *int64
	LikeCount       *int64
}

// Merge fills empty fields from other. The generic Open Graph result is always
// the trunk; a provider result is only ever an addition to it.
func (metadata *Metadata) Merge(other Metadata) {
	if metadata.CanonicalURL == "" {
		metadata.CanonicalURL = other.CanonicalURL
	}
	if metadata.Title == "" {
		metadata.Title = other.Title
	}
	if metadata.Description == "" {
		metadata.Description = other.Description
	}
	if metadata.SiteName == "" {
		metadata.SiteName = other.SiteName
	}
	if metadata.AuthorName == "" {
		metadata.AuthorName = other.AuthorName
	}
	if metadata.ImageURL == "" {
		metadata.ImageURL = other.ImageURL
	}
	if metadata.IconURL == "" {
		metadata.IconURL = other.IconURL
	}
	if metadata.DurationSeconds == nil {
		metadata.DurationSeconds = other.DurationSeconds
	}
	if metadata.Kind == "" {
		metadata.Kind = other.Kind
	}
	if metadata.ViewCount == nil {
		metadata.ViewCount = other.ViewCount
	}
	if metadata.CommentCount == nil {
		metadata.CommentCount = other.CommentCount
	}
	if metadata.LikeCount == nil {
		metadata.LikeCount = other.LikeCount
	}
}

const (
	contentTypeHTML  = "text/html"
	contentTypeXHTML = "application/xhtml+xml"
	contentTypeJSON  = "application/json"
)

// FetchDocument retrieves an HTML page and extracts its metadata.
func (fetcher *Fetcher) FetchDocument(ctx context.Context, rawURL string) (Metadata, error) {
	response, err := fetcher.Get(ctx, rawURL, []string{contentTypeHTML, contentTypeXHTML}, DefaultMaxDocumentBytes)
	if err != nil {
		return Metadata{}, err
	}
	metadata := parseHTMLMetadata(response.Body)
	if metadata.CanonicalURL == "" {
		metadata.CanonicalURL = response.FinalURL
	}
	if normalized, err := domain.NormalizeLinkURL(metadata.CanonicalURL); err == nil {
		metadata.CanonicalURL = normalized
	} else {
		metadata.CanonicalURL = response.FinalURL
	}
	metadata.ImageURL = absoluteLinkURL(metadata.ImageURL, response.FinalURL)
	metadata.IconURL = absoluteLinkURL(metadata.IconURL, response.FinalURL)
	if metadata.IconURL == "" {
		metadata.IconURL = defaultFaviconURL(response.FinalURL)
	}
	return metadata, nil
}

// absoluteLinkURL resolves a possibly relative reference against the document
// it was found in. Sites routinely declare icons as "/favicon.png".
func absoluteLinkURL(reference, documentURL string) string {
	if strings.TrimSpace(reference) == "" {
		return ""
	}
	if normalized, err := domain.NormalizeLinkURL(reference); err == nil {
		return normalized
	}
	base, err := url.Parse(documentURL)
	if err != nil {
		return ""
	}
	resolved, err := base.Parse(reference)
	if err != nil {
		return ""
	}
	normalized, err := domain.NormalizeLinkURL(resolved.String())
	if err != nil {
		return ""
	}
	return normalized
}

// defaultFaviconURL is the conventional location a site serves its mark from
// when it declares none. It is frequently an ICO, which managed media refuses,
// in which case the card simply shows no mark.
func defaultFaviconURL(documentURL string) string {
	base, err := url.Parse(documentURL)
	if err != nil || base.Host == "" {
		return ""
	}
	return base.Scheme + "://" + base.Host + "/favicon.ico"
}

// FetchOEmbed retrieves a JSON oEmbed document.
func (fetcher *Fetcher) FetchOEmbed(ctx context.Context, endpoint string) (Metadata, error) {
	response, err := fetcher.Get(ctx, endpoint, []string{contentTypeJSON}, DefaultMaxDocumentBytes)
	if err != nil {
		return Metadata{}, err
	}
	var document struct {
		Title        string `json:"title"`
		AuthorName   string `json:"author_name"`
		ProviderName string `json:"provider_name"`
		ThumbnailURL string `json:"thumbnail_url"`
		Type         string `json:"type"`
	}
	if err := json.Unmarshal(response.Body, &document); err != nil {
		return Metadata{}, ErrUnavailable
	}
	metadata := Metadata{
		Title:      strings.TrimSpace(document.Title),
		AuthorName: strings.TrimSpace(document.AuthorName),
		SiteName:   strings.TrimSpace(document.ProviderName),
	}
	if document.Type == "video" {
		metadata.Kind = domain.LinkKindVideo
	}
	if normalized, err := domain.NormalizeLinkURL(document.ThumbnailURL); err == nil {
		metadata.ImageURL = normalized
	}
	return metadata, nil
}

// parseHTMLMetadata walks a bounded HTML document and collects the head
// metadata Moment renders. Only <head> is inspected: body content is never
// promoted into a card.
func parseHTMLMetadata(body []byte) Metadata {
	document, err := html.Parse(strings.NewReader(string(body)))
	if err != nil {
		return Metadata{}
	}
	properties := map[string]string{}
	icons := map[string]string{}
	var canonical, documentTitle string
	var walk func(*html.Node)
	walk = func(node *html.Node) {
		if node.Type == html.ElementNode {
			switch node.Data {
			case "meta":
				var key, content string
				for _, attribute := range node.Attr {
					switch strings.ToLower(attribute.Key) {
					case "property", "name":
						if key == "" {
							key = strings.ToLower(strings.TrimSpace(attribute.Val))
						}
					case "content":
						content = attribute.Val
					}
				}
				if key != "" && content != "" {
					if _, exists := properties[key]; !exists {
						properties[key] = strings.TrimSpace(content)
					}
				}
			case "link":
				var rel, href string
				for _, attribute := range node.Attr {
					switch strings.ToLower(attribute.Key) {
					case "rel":
						rel = strings.ToLower(strings.TrimSpace(attribute.Val))
					case "href":
						href = strings.TrimSpace(attribute.Val)
					}
				}
				if rel == "canonical" && canonical == "" {
					canonical = href
				}
				// A site may declare several marks. Keep the best of each kind
				// and choose between them after the walk.
				for _, kind := range strings.Fields(rel) {
					switch kind {
					case "apple-touch-icon", "apple-touch-icon-precomposed", "icon", "shortcut":
						if href != "" && icons[kind] == "" {
							icons[kind] = href
						}
					}
				}
			case "title":
				if documentTitle == "" && node.FirstChild != nil && node.FirstChild.Type == html.TextNode {
					documentTitle = strings.TrimSpace(node.FirstChild.Data)
				}
			case "body":
				// Stop before the body: card text comes from head metadata only.
				return
			}
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)

	metadata := Metadata{
		CanonicalURL: firstNonEmpty(canonical, properties["og:url"]),
		Title:        firstNonEmpty(properties["og:title"], properties["twitter:title"], documentTitle),
		Description:  firstNonEmpty(properties["og:description"], properties["twitter:description"], properties["description"]),
		SiteName:     firstNonEmpty(properties["og:site_name"], properties["application-name"]),
		AuthorName:   firstNonEmpty(properties["author"], properties["article:author"]),
		ImageURL:     firstNonEmpty(properties["og:image:secure_url"], properties["og:image"], properties["twitter:image"]),
		// Prefer the declared marks in descending quality. A bare
		// /favicon.ico is usually an ICO, which managed media does not accept,
		// so it is the last resort rather than the first choice.
		IconURL: firstNonEmpty(
			icons["apple-touch-icon"],
			icons["apple-touch-icon-precomposed"],
			icons["icon"],
			icons["shortcut"],
		),
	}
	if strings.HasPrefix(properties["og:type"], "video") {
		metadata.Kind = domain.LinkKindVideo
	}
	if seconds, err := strconv.Atoi(properties["og:video:duration"]); err == nil && seconds > 0 {
		metadata.DurationSeconds = &seconds
	} else if seconds, err := strconv.Atoi(properties["video:duration"]); err == nil && seconds > 0 {
		metadata.DurationSeconds = &seconds
	}
	return metadata
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
