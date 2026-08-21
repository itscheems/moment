package linkfetch

import (
	"context"
	"encoding/json"
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
	if metadata.ImageURL != "" {
		if normalized, err := domain.NormalizeLinkURL(metadata.ImageURL); err == nil {
			metadata.ImageURL = normalized
		} else {
			metadata.ImageURL = ""
		}
	}
	return metadata, nil
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
