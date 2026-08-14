// Package postdto maps the internal Entry model to Moment's public post
// language. Keeping this mapper shared prevents HTTP and CLI from drifting or
// exposing future Entry implementation fields such as kind.
package postdto

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

// Post is the stable plain-text post representation used by HTTP and CLI.
type Post struct {
	ID           string            `json:"id"`
	ContentMD    string            `json:"content_md"`
	ContentHTML  string            `json:"content_html"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	PublishedAt  time.Time         `json:"published_at"`
	CommentCount int               `json:"comment_count"`
	Media        []MediaProjection `json:"media"`
	Location     *Location         `json:"location"`
}

// Location is the deliberately coarse public location projection. Exact
// coordinates and provider identifiers remain owner-only.
type Location struct {
	Name        string `json:"name"`
	DisplayName string `json:"display_name"`
}

// MediaUsage is the exact public ordered usage shape. AltText is always
// emitted, including JSON null for the missing state.
type MediaUsage struct {
	MediaID  string  `json:"media_id"`
	Position int     `json:"position"`
	AltText  *string `json:"alt_text"`
}

// MediaProjection is the public read shape. Width and height are authoritative
// stored image dimensions and are deliberately absent from MediaUsage writes.
type MediaProjection struct {
	MediaID  string  `json:"media_id"`
	Position int     `json:"position"`
	AltText  *string `json:"alt_text"`
	Width    uint32  `json:"width"`
	Height   uint32  `json:"height"`
}

// UnmarshalJSON keeps API-mode clients strict about the complete read shape.
func (usage *MediaProjection) UnmarshalJSON(data []byte) error {
	*usage = MediaProjection{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("media projection must be an object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok || seen[key] {
			return errors.New("media projection fields must appear exactly once")
		}
		seen[key] = true
		switch key {
		case "media_id":
			if err := decoder.Decode(&usage.MediaID); err != nil {
				return errors.New("media_id must be a string")
			}
		case "position":
			if err := decoder.Decode(&usage.Position); err != nil {
				return errors.New("position must be an integer")
			}
		case "alt_text":
			var raw json.RawMessage
			if err := decoder.Decode(&raw); err != nil {
				return errors.New("alt_text must be null or a string")
			}
			if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				var alt string
				if err := json.Unmarshal(raw, &alt); err != nil {
					return errors.New("alt_text must be null or a string")
				}
				usage.AltText = &alt
			}
		case "width":
			if err := decoder.Decode(&usage.Width); err != nil {
				return errors.New("width must be a non-negative integer")
			}
		case "height":
			if err := decoder.Decode(&usage.Height); err != nil {
				return errors.New("height must be a non-negative integer")
			}
		default:
			return errors.New("media projection contains an unknown field")
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') ||
		!seen["media_id"] || !seen["position"] || !seen["alt_text"] || !seen["width"] || !seen["height"] {
		return errors.New("media projection requires media_id, position, alt_text, width, and height")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("media projection contains trailing data")
	}
	return nil
}

// UnmarshalJSON rejects missing, duplicate, and unknown fields so API-mode
// clients never accept a superficially canonical usage.
func (usage *MediaUsage) UnmarshalJSON(data []byte) error {
	*usage = MediaUsage{}
	decoder := json.NewDecoder(bytes.NewReader(data))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return errors.New("media usage must be an object")
	}
	seen := map[string]bool{}
	for decoder.More() {
		keyToken, err := decoder.Token()
		key, ok := keyToken.(string)
		if err != nil || !ok || seen[key] {
			return errors.New("media usage fields must appear exactly once")
		}
		seen[key] = true
		switch key {
		case "media_id":
			if err := decoder.Decode(&usage.MediaID); err != nil {
				return errors.New("media_id must be a string")
			}
		case "position":
			if err := decoder.Decode(&usage.Position); err != nil {
				return errors.New("position must be an integer")
			}
		case "alt_text":
			var raw json.RawMessage
			if err := decoder.Decode(&raw); err != nil {
				return errors.New("alt_text must be null or a string")
			}
			if !bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
				var alt string
				if err := json.Unmarshal(raw, &alt); err != nil {
					return errors.New("alt_text must be null or a string")
				}
				usage.AltText = &alt
			}
		default:
			return errors.New("media usage contains an unknown field")
		}
	}
	if token, err := decoder.Token(); err != nil || token != json.Delim('}') || !seen["media_id"] || !seen["position"] || !seen["alt_text"] {
		return errors.New("media usage requires media_id, position, and alt_text")
	}
	if err := decoder.Decode(&struct{}{}); err != io.EOF {
		return errors.New("media usage contains trailing data")
	}
	return nil
}

// Page is one stable reverse-chronological timeline page.
type Page struct {
	Items []Post `json:"items"`
}

// FromPost strips internal Entry fields while preserving the canonical
// application result.
func FromPost(post app.Post) Post {
	media := make([]MediaProjection, 0, len(post.Media))
	for _, usage := range post.Media {
		mapped := MediaProjection{MediaID: usage.MediaID.String(), Position: usage.Position, Width: usage.Width, Height: usage.Height}
		if usage.AltText != nil {
			alt := *usage.AltText
			mapped.AltText = &alt
		}
		media = append(media, mapped)
	}
	return Post{
		ID:           post.Entry.ID.String(),
		ContentMD:    post.Entry.ContentMD,
		ContentHTML:  post.ContentHTML,
		CreatedAt:    post.Entry.CreatedAt.UTC(),
		UpdatedAt:    post.Entry.UpdatedAt.UTC(),
		PublishedAt:  post.Entry.PublishedAt.UTC(),
		CommentCount: post.CommentCount,
		Media:        media,
		Location: func() *Location {
			if post.Location == nil {
				return nil
			}
			return &Location{Name: post.Location.Name, DisplayName: post.Location.DisplayName}
		}(),
	}
}

// FromPage maps the canonical items; continuation belongs to envelope metadata.
func FromPage(page app.PostPage) Page {
	items := make([]Post, 0, len(page.Items))
	for _, post := range page.Items {
		items = append(items, FromPost(post))
	}
	return Page{Items: items}
}

// NextCursor maps the exhausted application cursor to a nullable wire value
// carried in envelope metadata.
func NextCursor(page app.PostPage) *string {
	if page.NextCursor == "" {
		return nil
	}
	value := page.NextCursor
	return &value
}

// ValidatePost rejects a superficially successful but non-canonical API DTO.
func ValidatePost(post Post) error {
	if _, err := domain.ParseID(post.ID); err != nil {
		return err
	}
	if post.CreatedAt.IsZero() || post.UpdatedAt.IsZero() || post.PublishedAt.IsZero() ||
		!wireInstant(post.CreatedAt) || !wireInstant(post.UpdatedAt) || !wireInstant(post.PublishedAt) {
		return errors.New("post timestamps are not canonical UTC milliseconds")
	}
	if post.UpdatedAt.Before(post.CreatedAt) {
		return errors.New("post updated_at precedes created_at")
	}
	if post.CommentCount < 0 {
		return errors.New("post comment_count must be non-negative")
	}
	if post.Media == nil {
		return errors.New("post media must be an array")
	}
	if post.Location != nil {
		location := domain.Location{
			Name: post.Location.Name, DisplayName: post.Location.DisplayName,
			Provider: "manual",
		}
		if err := location.Validate(); err != nil {
			return err
		}
	}
	usages := make([]domain.EntryMediaUsage, 0, len(post.Media))
	for _, usage := range post.Media {
		if usage.Width == 0 || usage.Height == 0 {
			return errors.New("post media dimensions must be positive")
		}
		id, err := domain.ParseID(usage.MediaID)
		if err != nil {
			return err
		}
		usages = append(usages, domain.EntryMediaUsage{MediaID: id, Position: usage.Position, AltText: usage.AltText})
	}
	if err := domain.ValidateEntryMediaUsages(usages); err != nil {
		return err
	}
	if err := domain.ValidatePostMarkdown(post.ContentMD, len(usages)); err != nil {
		return err
	}
	html, err := domain.RenderMarkdown(post.ContentMD)
	if err != nil {
		return err
	}
	if html != post.ContentHTML {
		return errors.New("post content_html does not match content_md")
	}
	return nil
}

// ValidatePage requires an array, including for the empty timeline, and
// validates every canonical post item.
func ValidatePage(page Page) error {
	if page.Items == nil {
		return errors.New("post page items must be an array")
	}
	for _, post := range page.Items {
		if err := ValidatePost(post); err != nil {
			return err
		}
	}
	return nil
}

func wireInstant(value time.Time) bool {
	_, offset := value.Zone()
	return offset == 0 && value.Nanosecond()%int(time.Millisecond) == 0
}
