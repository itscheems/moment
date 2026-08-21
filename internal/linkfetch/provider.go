package linkfetch

import (
	"context"
	"net/url"
	"regexp"
	"strings"

	"github.com/moment/moment/internal/domain"
)

// Recognition is what a provider can say about a URL without touching the
// network. It is pure string work, which is why every provider is allowed to
// implement it.
type Recognition struct {
	Provider     domain.LinkCardProvider
	CanonicalURL string
	ResourceID   string
	EmbedURL     string
	Kind         domain.LinkCardKind
}

// Provider recognizes platform URLs and may optionally add metadata.
//
// Recognize is a pure function. Enrich receives the shared Fetcher rather than
// owning a client, so a provider can never open a connection the shared
// transport would have refused. Neither method is a required path: the generic
// Open Graph route is both the default and the fallback, and a provider that
// declines, errors, or returns half a result never blocks a card.
type Provider interface {
	Recognize(target *url.URL) (Recognition, bool)
}

// Enricher is the optional second half of a provider. No provider implements
// it today: YouTube and Bilibili both serve usable Open Graph metadata to an
// anonymous fetcher, so the generic path already covers them.
type Enricher interface {
	Enrich(ctx context.Context, fetcher *Fetcher, recognition Recognition) (Metadata, error)
}

// Embeddable origins. This list is the whole inline-player surface: only a URL
// whose origin appears here may ever be framed. It is deliberately not derived
// from a page's declared twitter:player, because that would reopen the
// unbounded surface, and because YouTube itself serves twitter:card
// summary_large_image rather than player.
//
// Entries are only ever added. A stored embed URL whose origin is no longer
// listed degrades to a static card instead of rendering a frame the Content
// Security Policy would refuse.
var embeddableOrigins = map[string]struct{}{
	"https://www.youtube-nocookie.com": {},
	"https://player.bilibili.com":      {},
}

// EmbeddableOrigins lists every allowlisted embed origin in a stable order.
// The Content Security Policy frame-src directive is generated from it, so the
// header and the recognizers can never disagree.
func EmbeddableOrigins() []string {
	origins := make([]string, 0, len(embeddableOrigins))
	for origin := range embeddableOrigins {
		origins = append(origins, origin)
	}
	// Small fixed set; keep a deterministic order for the CSP header.
	for outer := 1; outer < len(origins); outer++ {
		for inner := outer; inner > 0 && origins[inner] < origins[inner-1]; inner-- {
			origins[inner], origins[inner-1] = origins[inner-1], origins[inner]
		}
	}
	return origins
}

// IsEmbeddable reports whether a stored embed URL may still be framed.
func IsEmbeddable(rawURL string) bool {
	if rawURL == "" {
		return false
	}
	parsed, err := url.Parse(rawURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return false
	}
	origin := strings.ToLower(parsed.Scheme) + "://" + strings.ToLower(parsed.Host)
	_, allowed := embeddableOrigins[origin]
	return allowed
}

// DefaultProviders returns the recognizers Moment ships.
func DefaultProviders() []Provider {
	return []Provider{youTubeProvider{}, bilibiliProvider{}}
}

// Recognize asks each provider in turn. Exactly one provider may claim a URL;
// the first match wins and the rest are not consulted.
func Recognize(providers []Provider, rawURL string) (Recognition, bool) {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return Recognition{}, false
	}
	for _, provider := range providers {
		if recognition, ok := provider.Recognize(parsed); ok {
			return recognition, true
		}
	}
	return Recognition{}, false
}

var youTubeIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

type youTubeProvider struct{}

func (youTubeProvider) Recognize(target *url.URL) (Recognition, bool) {
	host := strings.ToLower(strings.TrimPrefix(target.Hostname(), "www."))
	var id string
	switch host {
	case "youtu.be":
		id = strings.Trim(target.Path, "/")
	case "youtube.com", "m.youtube.com", "music.youtube.com", "youtube-nocookie.com":
		segments := strings.Split(strings.Trim(target.Path, "/"), "/")
		switch {
		case target.Path == "/watch":
			id = target.Query().Get("v")
		case len(segments) == 2 && (segments[0] == "shorts" || segments[0] == "embed" || segments[0] == "live" || segments[0] == "v"):
			id = segments[1]
		}
	default:
		return Recognition{}, false
	}
	if !youTubeIDPattern.MatchString(id) {
		return Recognition{}, false
	}
	return Recognition{
		Provider:     domain.LinkProviderYouTube,
		CanonicalURL: "https://www.youtube.com/watch?v=" + id,
		ResourceID:   id,
		EmbedURL:     "https://www.youtube-nocookie.com/embed/" + id,
		Kind:         domain.LinkKindVideo,
	}, true
}

var bilibiliIDPattern = regexp.MustCompile(`^BV[A-Za-z0-9]{10}$`)

type bilibiliProvider struct{}

func (bilibiliProvider) Recognize(target *url.URL) (Recognition, bool) {
	host := strings.ToLower(strings.TrimPrefix(target.Hostname(), "www."))
	if host != "bilibili.com" && host != "m.bilibili.com" {
		// b23.tv short links are intentionally not recognized here. They expand
		// during the shared fetcher's redirect walk, which already revalidates
		// every hop, so no provider needs to make a request to resolve them.
		return Recognition{}, false
	}
	segments := strings.Split(strings.Trim(target.Path, "/"), "/")
	if len(segments) < 2 || segments[0] != "video" {
		return Recognition{}, false
	}
	id := segments[1]
	if !bilibiliIDPattern.MatchString(id) {
		return Recognition{}, false
	}
	embed := "https://player.bilibili.com/player.html?bvid=" + id + "&autoplay=0&danmaku=0"
	if page := target.Query().Get("p"); page != "" && isSmallPositiveInteger(page) {
		embed += "&p=" + page
	}
	return Recognition{
		Provider:     domain.LinkProviderBilibili,
		CanonicalURL: "https://www.bilibili.com/video/" + id,
		ResourceID:   id,
		EmbedURL:     embed,
		Kind:         domain.LinkKindVideo,
	}, true
}

func isSmallPositiveInteger(value string) bool {
	if len(value) == 0 || len(value) > 4 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return value[0] != '0'
}
