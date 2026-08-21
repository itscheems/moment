package linkfetch

import (
	"context"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/moment/moment/internal/domain"
)

// Preview is one unfurled link, before a thumbnail has been ingested.
type Preview struct {
	SourceURL       string
	CanonicalURL    string
	Provider        domain.LinkCardProvider
	Kind            domain.LinkCardKind
	Title           string
	Description     string
	SiteName        string
	AuthorName      string
	DurationSeconds *int
	ImageURL        string
	EmbedURL        string
}

// Resolver turns an owner-supplied URL into a Preview.
type Resolver struct {
	fetcher   *Fetcher
	providers []Provider
	cache     *previewCache
	clock     func() time.Time
}

// ResolverOptions configures a Resolver.
type ResolverOptions struct {
	Fetcher   *Fetcher
	Providers []Provider
	CacheTTL  time.Duration
	Clock     func() time.Time
}

// DefaultCacheTTL bounds how long an unfurl result is reused. The cache exists
// to keep a composer that re-requests the same URL from repeatedly hitting an
// origin, not to be an authority: a published card is always a stored snapshot.
const DefaultCacheTTL = 5 * time.Minute

// NewResolver builds a Resolver over a guarded Fetcher.
func NewResolver(options ResolverOptions) *Resolver {
	fetcher := options.Fetcher
	if fetcher == nil {
		fetcher = New(Options{})
	}
	providers := options.Providers
	if providers == nil {
		providers = DefaultProviders()
	}
	clock := options.Clock
	if clock == nil {
		clock = time.Now
	}
	ttl := options.CacheTTL
	if ttl <= 0 {
		ttl = DefaultCacheTTL
	}
	return &Resolver{
		fetcher:   fetcher,
		providers: providers,
		cache:     newPreviewCache(ttl, clock),
		clock:     clock,
	}
}

// Fetcher exposes the guarded fetcher so a caller that must download a
// thumbnail uses the same transport and the same address filter.
func (resolver *Resolver) Fetcher() *Fetcher { return resolver.fetcher }

// Resolve unfurls one URL. A provider only ever adds to the generic result: it
// contributes a canonical URL, a resource identifier, and an embed URL, while
// the page's own Open Graph metadata supplies the text and image.
func (resolver *Resolver) Resolve(ctx context.Context, rawURL string) (Preview, error) {
	source, err := domain.NormalizeLinkURL(rawURL)
	if err != nil {
		return Preview{}, err
	}
	if cached, ok := resolver.cache.get(source); ok {
		return cached, nil
	}

	recognition, recognized := Recognize(resolver.providers, source)

	// The generic document fetch is the trunk. It follows redirects, which is
	// also how a short link such as b23.tv resolves to its real target.
	metadata, documentErr := resolver.fetcher.FetchDocument(ctx, source)
	if documentErr != nil && ctx.Err() != nil {
		return Preview{}, ctx.Err()
	}

	// A recognizer that did not match the submitted URL may still match the URL
	// the redirect chain landed on.
	if !recognized && metadata.CanonicalURL != "" {
		recognition, recognized = Recognize(resolver.providers, metadata.CanonicalURL)
	}

	if recognized {
		if enricher, ok := any(resolver.providerFor(recognition.Provider)).(Enricher); ok {
			if extra, err := enricher.Enrich(ctx, resolver.fetcher, recognition); err == nil {
				metadata.Merge(extra)
			}
		}
	}

	if documentErr != nil && metadata.Title == "" && !recognized {
		return Preview{}, documentErr
	}

	preview := Preview{
		SourceURL:       source,
		CanonicalURL:    firstNonEmpty(recognition.CanonicalURL, metadata.CanonicalURL, source),
		Provider:        domain.LinkProviderGeneric,
		Kind:            domain.LinkKindLink,
		Title:           metadata.Title,
		Description:     metadata.Description,
		SiteName:        metadata.SiteName,
		AuthorName:      metadata.AuthorName,
		DurationSeconds: metadata.DurationSeconds,
		ImageURL:        metadata.ImageURL,
	}
	if recognized {
		preview.Provider = recognition.Provider
		if recognition.Kind != "" {
			preview.Kind = recognition.Kind
		}
		if IsEmbeddable(recognition.EmbedURL) {
			preview.EmbedURL = recognition.EmbedURL
		}
	} else if metadata.Kind != "" {
		// An unrecognized page may describe itself as a video, but without an
		// allowlisted embed origin it stays a static card.
		preview.Kind = domain.LinkKindLink
	}
	if preview.EmbedURL == "" {
		preview.Kind = domain.LinkKindLink
	}
	if preview.SiteName == "" {
		preview.SiteName = hostLabel(preview.CanonicalURL)
	}
	preview.Title = clampText(preview.Title, domain.MaxLinkCardTitleCodePoints)
	preview.Description = clampText(preview.Description, domain.MaxLinkCardDescriptionCodePoints)
	preview.SiteName = clampText(preview.SiteName, domain.MaxLinkCardNameCodePoints)
	preview.AuthorName = clampText(preview.AuthorName, domain.MaxLinkCardNameCodePoints)

	resolver.cache.put(source, preview)
	return preview, nil
}

func (resolver *Resolver) providerFor(name domain.LinkCardProvider) Provider {
	for _, provider := range resolver.providers {
		if recognizer, ok := provider.(interface {
			providerName() domain.LinkCardProvider
		}); ok && recognizer.providerName() == name {
			return provider
		}
	}
	return nil
}

func (youTubeProvider) providerName() domain.LinkCardProvider  { return domain.LinkProviderYouTube }
func (bilibiliProvider) providerName() domain.LinkCardProvider { return domain.LinkProviderBilibili }

func hostLabel(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www.")
}

func clampText(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) <= limit {
		return value
	}
	return strings.TrimSpace(string(runes[:limit]))
}

type previewCache struct {
	mutex   sync.Mutex
	entries map[string]cachedPreview
	ttl     time.Duration
	clock   func() time.Time
}

type cachedPreview struct {
	preview   Preview
	expiresAt time.Time
}

const maxCachedPreviews = 128

func newPreviewCache(ttl time.Duration, clock func() time.Time) *previewCache {
	return &previewCache{entries: map[string]cachedPreview{}, ttl: ttl, clock: clock}
}

func (cache *previewCache) get(key string) (Preview, bool) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	entry, exists := cache.entries[key]
	if !exists || cache.clock().After(entry.expiresAt) {
		delete(cache.entries, key)
		return Preview{}, false
	}
	return entry.preview, true
}

func (cache *previewCache) put(key string, preview Preview) {
	cache.mutex.Lock()
	defer cache.mutex.Unlock()
	if len(cache.entries) >= maxCachedPreviews {
		now := cache.clock()
		for candidate, entry := range cache.entries {
			if now.After(entry.expiresAt) {
				delete(cache.entries, candidate)
			}
		}
		if len(cache.entries) >= maxCachedPreviews {
			for candidate := range cache.entries {
				delete(cache.entries, candidate)
				break
			}
		}
	}
	cache.entries[key] = cachedPreview{preview: preview, expiresAt: cache.clock().Add(cache.ttl)}
}
