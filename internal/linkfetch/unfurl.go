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
	IconURL         string
	EmbedURL        string
	ViewCount       *int64
	CommentCount    *int64
	LikeCount       *int64
}

// Resolver turns supplied URLs into bounded immutable previews.
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

const (
	challengeRetries = 3
	challengeBackoff = 500 * time.Millisecond
)

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
	var refiner Refiner

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
		provider := resolver.providerFor(recognition.Provider)
		if enricher, ok := any(provider).(Enricher); ok {
			if extra, err := enricher.Enrich(ctx, resolver.fetcher, recognition); err == nil {
				metadata.Merge(extra)
			}
		}
		// Refinement is pure and runs after the challenge retry below decides
		// whether the document is trustworthy at all.
		refiner, _ = any(provider).(Refiner)
	}

	if documentErr != nil && metadata.Title == "" && !recognized {
		return Preview{}, documentErr
	}

	// A recognized video page always carries a thumbnail. When one does not,
	// the response was not really the resource that was asked for -- an
	// anti-bot interstitial or a consent wall, which still returns 200 and a
	// title such as "verification required".
	//
	// Some origins serve that challenge intermittently to the same client, so
	// one bounded retry recovers most of them. This is ordinary resilience
	// against a flaky response, not an attempt to defeat the check: if the
	// retry is challenged too, the untrustworthy text is dropped and only the
	// recognizer's own contribution survives. The card stays useful either
	// way, because it still links and embeds.
	// An origin that answers a bot with something other than the real page does
	// not always answer with an obvious challenge: it may also return a partial
	// document that has a title and a cover but none of the figures the page
	// normally carries. Both shapes are detected the same way -- the response
	// is missing something the resource always has -- and both are worth one
	// bounded second look.
	for attempt := 0; attempt < challengeRetries && recognized && ctx.Err() == nil; attempt++ {
		if complete(metadata, refiner) {
			break
		}
		select {
		case <-ctx.Done():
			return Preview{}, ctx.Err()
		case <-time.After(challengeBackoff * time.Duration(attempt+1)):
		}
		retry, err := resolver.fetcher.FetchDocument(ctx, source)
		if err != nil {
			break
		}
		if complete(retry, refiner) {
			metadata = retry
			break
		}
		if metadata.ImageURL == "" && retry.ImageURL != "" {
			metadata = retry
		}
	}
	if recognized && metadata.ImageURL == "" {
		metadata.Title = ""
		metadata.Description = ""
		metadata.AuthorName = ""
		metadata.SiteName = ""
	} else if refiner != nil {
		metadata = refiner.Refine(metadata)
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
		IconURL:         metadata.IconURL,
		ViewCount:       metadata.ViewCount,
		CommentCount:    metadata.CommentCount,
		LikeCount:       metadata.LikeCount,
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

// complete reports whether a fetched document carries everything the resource
// is expected to publish. A cover is required of every recognized video page;
// a provider that refines statistics additionally expects to find them.
func complete(metadata Metadata, refiner Refiner) bool {
	if metadata.ImageURL == "" {
		return false
	}
	if refiner == nil {
		return true
	}
	return refiner.HasStatistics(refiner.Refine(metadata))
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
