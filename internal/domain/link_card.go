package domain

import (
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	// MaxLinkCardURLCodePoints bounds every stored URL on a link card.
	MaxLinkCardURLCodePoints = 2048
	// MaxLinkCardTitleCodePoints bounds the captured page title.
	MaxLinkCardTitleCodePoints = 300
	// MaxLinkCardDescriptionCodePoints bounds the captured summary.
	MaxLinkCardDescriptionCodePoints = 1000
	// MaxLinkCardNameCodePoints bounds the captured site and author names.
	MaxLinkCardNameCodePoints = 160
	// MaxLinkCardDurationSeconds bounds a captured media duration.
	MaxLinkCardDurationSeconds = 86_400
	// MaxLinkCardsPerEntry is the storage ceiling for one entry. The Moment
	// aggregate additionally restricts published cards to MaxMomentLinkCards.
	MaxLinkCardsPerEntry = 9
	// MaxMomentLinkCards is the v0.0.1 product rule: one card per Moment. The
	// schema already models an ordered list so a future long-form entry kind
	// can raise this without a primary-key migration.
	MaxMomentLinkCards = 1
)

// LinkCardProvider names the recognizer that produced a card. It is provenance
// only: rendering must branch on card data, never on this label, so removing a
// provider can never invalidate historical entries.
type LinkCardProvider string

// Recognized link card providers.
const (
	// LinkProviderGeneric marks a card built purely from Open Graph or oEmbed
	// metadata. It is both the default and the fallback path.
	LinkProviderGeneric LinkCardProvider = "generic"
	// LinkProviderYouTube marks a recognized YouTube video.
	LinkProviderYouTube LinkCardProvider = "youtube"
	// LinkProviderBilibili marks a recognized Bilibili video.
	LinkProviderBilibili LinkCardProvider = "bilibili"
)

// LinkCardKind separates a plain link snapshot from an embeddable video.
type LinkCardKind string

// Recognized link card kinds.
const (
	// LinkKindLink is a static snapshot with no inline player.
	LinkKindLink LinkCardKind = "link"
	// LinkKindVideo carries an embed URL an allowlisted player can load.
	LinkKindVideo LinkCardKind = "video"
)

// LinkCard is the immutable snapshot attached to an entry when an owner
// publishes a recognized URL. Rendering a stored card never requires a network
// request: the thumbnail is a managed media reference and every text field was
// captured at publish time.
type LinkCard struct {
	Position         int
	SourceURL        string
	CanonicalURL     string
	Provider         LinkCardProvider
	Kind             LinkCardKind
	Title            string
	Description      string
	SiteName         string
	AuthorName       string
	DurationSeconds  *int
	ThumbnailMediaID string
	EmbedURL         string
	CapturedAt       int64
}

// Validate enforces the canonical link card contract. It runs on the write
// path even when the payload came from Moment's own unfurl service, because a
// client may submit any card body an owner session can reach.
func (card LinkCard) Validate() error {
	if card.Position < 0 || card.Position >= MaxLinkCardsPerEntry {
		return NewError(CodeInvalidArgument, "link card position is out of range", map[string]any{"field": "position", "limit": MaxLinkCardsPerEntry})
	}
	if err := validateLinkCardURL("source_url", card.SourceURL, true); err != nil {
		return err
	}
	if err := validateLinkCardURL("canonical_url", card.CanonicalURL, true); err != nil {
		return err
	}
	switch card.Provider {
	case LinkProviderGeneric, LinkProviderYouTube, LinkProviderBilibili:
	default:
		return NewError(CodeInvalidArgument, "link card provider is not supported", map[string]any{"field": "provider"})
	}
	switch card.Kind {
	case LinkKindLink, LinkKindVideo:
	default:
		return NewError(CodeInvalidArgument, "link card kind is not supported", map[string]any{"field": "card_kind"})
	}
	fields := []struct {
		field string
		value string
		limit int
	}{
		{"title", card.Title, MaxLinkCardTitleCodePoints},
		{"description", card.Description, MaxLinkCardDescriptionCodePoints},
		{"site_name", card.SiteName, MaxLinkCardNameCodePoints},
		{"author_name", card.AuthorName, MaxLinkCardNameCodePoints},
	}
	for _, field := range fields {
		if err := validateLocationText(field.field, field.value, field.limit, false); err != nil {
			return err
		}
	}
	if card.DurationSeconds != nil && (*card.DurationSeconds <= 0 || *card.DurationSeconds > MaxLinkCardDurationSeconds) {
		return NewError(CodeInvalidArgument, "link card duration is out of range", map[string]any{"field": "duration_seconds", "limit": MaxLinkCardDurationSeconds})
	}
	if card.ThumbnailMediaID != "" {
		if _, err := ParseID(card.ThumbnailMediaID); err != nil {
			return NewError(CodeInvalidArgument, "link card thumbnail must reference managed media", map[string]any{"field": "thumbnail_media_id"})
		}
	}
	if card.Kind == LinkKindLink && card.EmbedURL != "" {
		return NewError(CodeInvalidArgument, "only video link cards can carry an embed URL", map[string]any{"field": "embed_url"})
	}
	if card.EmbedURL != "" {
		if err := validateLinkCardURL("embed_url", card.EmbedURL, true); err != nil {
			return err
		}
	}
	if card.CapturedAt <= 0 {
		return NewError(CodeInvalidArgument, "link card capture time is required", map[string]any{"field": "captured_at"})
	}
	return nil
}

// ValidateLinkCards enforces the ordered-list contract for one entry.
func ValidateLinkCards(cards []LinkCard) error {
	if len(cards) > MaxMomentLinkCards {
		return NewError(CodeInvalidArgument, "a Moment supports one link card", map[string]any{"field": "link_cards", "limit": MaxMomentLinkCards})
	}
	for index, card := range cards {
		if card.Position != index {
			return NewError(CodeInvalidArgument, "link card positions must start at zero and increase by one", map[string]any{"field": "link_cards", "index": index})
		}
		if err := card.Validate(); err != nil {
			return err
		}
	}
	return nil
}

// CloneLinkCards returns a deep copy suitable for immutable projections.
func CloneLinkCards(cards []LinkCard) []LinkCard {
	if cards == nil {
		return nil
	}
	clones := make([]LinkCard, len(cards))
	for index, card := range cards {
		clone := card
		if card.DurationSeconds != nil {
			duration := *card.DurationSeconds
			clone.DurationSeconds = &duration
		}
		clones[index] = clone
	}
	return clones
}

// NormalizeLinkURL applies the shared URL grammar every link card field obeys:
// an absolute http(s) URL with no credentials and no fragment.
func NormalizeLinkURL(raw string) (string, error) {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return "", NewError(CodeInvalidArgument, "url is required", map[string]any{"field": "url"})
	}
	if utf8.RuneCountInString(trimmed) > MaxLinkCardURLCodePoints {
		return "", NewError(CodeInvalidArgument, "url exceeds its code-point limit", map[string]any{"field": "url", "limit": MaxLinkCardURLCodePoints})
	}
	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", NewError(CodeInvalidArgument, "url is not parseable", map[string]any{"field": "url"})
	}
	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return "", NewError(CodeInvalidArgument, "url must use http or https", map[string]any{"field": "url"})
	}
	if parsed.User != nil {
		return "", NewError(CodeInvalidArgument, "url must not contain credentials", map[string]any{"field": "url"})
	}
	if parsed.Host == "" {
		return "", NewError(CodeInvalidArgument, "url must contain a host", map[string]any{"field": "url"})
	}
	parsed.Scheme = scheme
	parsed.Fragment = ""
	parsed.RawFragment = ""
	return parsed.String(), nil
}

func validateLinkCardURL(field, value string, required bool) error {
	if value == "" {
		if required {
			return NewError(CodeInvalidArgument, field+" is required", map[string]any{"field": field})
		}
		return nil
	}
	normalized, err := NormalizeLinkURL(value)
	if err != nil {
		return NewError(CodeInvalidArgument, field+" must be an absolute http or https URL", map[string]any{"field": field})
	}
	if normalized != value {
		return NewError(CodeInvalidArgument, field+" must already be normalized", map[string]any{"field": field})
	}
	return nil
}

// LinkCardSearchText renders the searchable half of a card set.
//
// Only high-signal fields participate: the captured title, the site and author
// names, and the canonical URL's host. The description is platform marketing
// copy, and a full URL is mostly an opaque identifier that would dilute a
// trigram index without making anything findable by a word a person would type.
//
// This is what makes a wordless post findable: a Moment whose body is just a
// link can still be recalled months later by the video's own title.
func LinkCardSearchText(cards []LinkCard) string {
	if len(cards) == 0 {
		return ""
	}
	var parts []string
	for _, card := range cards {
		for _, value := range []string{card.Title, card.SiteName, card.AuthorName} {
			if trimmed := strings.TrimSpace(value); trimmed != "" {
				parts = append(parts, trimmed)
			}
		}
		if parsed, err := url.Parse(card.CanonicalURL); err == nil {
			if host := strings.TrimPrefix(strings.ToLower(parsed.Hostname()), "www."); host != "" {
				parts = append(parts, host)
			}
		}
	}
	return strings.Join(parts, " ")
}
