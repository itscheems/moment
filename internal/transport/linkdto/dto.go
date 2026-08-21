// Package linkdto carries the owner-facing link card write shape. It is
// deliberately separate from the narrow public read projection in postdto: a
// write must name the originally typed source URL and the recognizer that
// produced the card, while a reader never needs either.
package linkdto

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/linkfetch"
)

// Card is the canonical wire shape for one link card. Every field is required,
// including explicit nulls, so a client cannot silently omit part of a snapshot.
type Card struct {
	SourceURL         string  `json:"source_url"`
	CanonicalURL      string  `json:"canonical_url"`
	Provider          string  `json:"provider"`
	Kind              string  `json:"card_kind"`
	Title             string  `json:"title"`
	Description       string  `json:"description"`
	SiteName          string  `json:"site_name"`
	AuthorName        string  `json:"author_name"`
	DurationSeconds   *int    `json:"duration_seconds"`
	ThumbnailMediaID  *string `json:"thumbnail_media_id"`
	ThumbnailMIMEType *string `json:"thumbnail_mime_type"`
	ThumbnailWidth    *uint32 `json:"thumbnail_width"`
	ThumbnailHeight   *uint32 `json:"thumbnail_height"`
	EmbedURL          *string `json:"embed_url"`
	CapturedAt        *int64  `json:"captured_at_ms"`
}

var cardFields = []string{
	"source_url", "canonical_url", "provider", "card_kind", "title",
	"description", "site_name", "author_name", "duration_seconds",
	"thumbnail_media_id", "thumbnail_mime_type", "thumbnail_width",
	"thumbnail_height", "embed_url", "captured_at_ms",
}

// UnmarshalJSON rejects unknown, duplicated, and missing keys so an API-mode
// client and the browser stay bound to one shape.
func (card *Card) UnmarshalJSON(data []byte) error {
	*card = Card{}
	type alias Card
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var value alias
	if err := decoder.Decode(&value); err != nil {
		return errors.New("link card must be one canonical JSON object")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("link card must be exactly one JSON document")
	}
	var present map[string]json.RawMessage
	if err := json.Unmarshal(data, &present); err != nil {
		return errors.New("link card must be one JSON object")
	}
	for _, field := range cardFields {
		if _, exists := present[field]; !exists {
			return errors.New("link card is missing required field " + field)
		}
	}
	if len(present) != len(cardFields) {
		return errors.New("link card contains an unsupported field")
	}
	*card = Card(value)
	return nil
}

// Domain converts the wire shape into the validated snapshot at a position.
//
// The embed URL is re-checked against the allowlist here rather than trusted:
// the card body arrives from a client, and an owner session must not be able to
// name an arbitrary frame source.
func (card Card) Domain(position int) (domain.LinkCard, error) {
	value := domain.LinkCard{
		Position:     position,
		SourceURL:    card.SourceURL,
		CanonicalURL: card.CanonicalURL,
		Provider:     domain.LinkCardProvider(card.Provider),
		Kind:         domain.LinkCardKind(card.Kind),
		Title:        card.Title,
		Description:  card.Description,
		SiteName:     card.SiteName,
		AuthorName:   card.AuthorName,
	}
	if card.DurationSeconds != nil {
		seconds := *card.DurationSeconds
		value.DurationSeconds = &seconds
	}
	if card.ThumbnailMediaID != nil {
		value.ThumbnailMediaID = *card.ThumbnailMediaID
	}
	if card.EmbedURL != nil && *card.EmbedURL != "" {
		if !linkfetch.IsEmbeddable(*card.EmbedURL) {
			return domain.LinkCard{}, domain.NewError(domain.CodeInvalidArgument, "embed_url origin is not permitted", map[string]any{"field": "embed_url"})
		}
		value.EmbedURL = *card.EmbedURL
	}
	if value.EmbedURL == "" {
		value.Kind = domain.LinkKindLink
	}
	if card.CapturedAt != nil {
		value.CapturedAt = *card.CapturedAt
	}
	if err := value.Validate(); err != nil {
		return domain.LinkCard{}, err
	}
	return value, nil
}

// FromPreview renders one resolved preview for an owner client.
func FromPreview(preview app.LinkPreview) Card {
	card := Card{
		SourceURL:    preview.Card.SourceURL,
		CanonicalURL: preview.Card.CanonicalURL,
		Provider:     string(preview.Card.Provider),
		Kind:         string(preview.Card.Kind),
		Title:        preview.Card.Title,
		Description:  preview.Card.Description,
		SiteName:     preview.Card.SiteName,
		AuthorName:   preview.Card.AuthorName,
	}
	if preview.Card.DurationSeconds != nil {
		seconds := *preview.Card.DurationSeconds
		card.DurationSeconds = &seconds
	}
	if preview.Card.ThumbnailMediaID != "" {
		id := preview.Card.ThumbnailMediaID
		card.ThumbnailMediaID = &id
		if preview.ThumbnailMIMEType != "" {
			mimeType := preview.ThumbnailMIMEType
			width := preview.ThumbnailWidth
			height := preview.ThumbnailHeight
			card.ThumbnailMIMEType = &mimeType
			card.ThumbnailWidth = &width
			card.ThumbnailHeight = &height
		}
	}
	if preview.Card.EmbedURL != "" {
		embed := preview.Card.EmbedURL
		card.EmbedURL = &embed
	}
	captured := preview.Card.CapturedAt
	card.CapturedAt = &captured
	return card
}

// FromDomainCard renders a stored snapshot back onto the wire, so a CLI client
// can round-trip a card between unfurl and post write without losing a field.
func FromDomainCard(card domain.LinkCard) Card {
	wire := Card{
		SourceURL:    card.SourceURL,
		CanonicalURL: card.CanonicalURL,
		Provider:     string(card.Provider),
		Kind:         string(card.Kind),
		Title:        card.Title,
		Description:  card.Description,
		SiteName:     card.SiteName,
		AuthorName:   card.AuthorName,
	}
	if card.DurationSeconds != nil {
		seconds := *card.DurationSeconds
		wire.DurationSeconds = &seconds
	}
	if card.ThumbnailMediaID != "" {
		id := card.ThumbnailMediaID
		wire.ThumbnailMediaID = &id
	}
	if card.EmbedURL != "" {
		embed := card.EmbedURL
		wire.EmbedURL = &embed
	}
	captured := card.CapturedAt
	wire.CapturedAt = &captured
	return wire
}
