// Package postdto maps the internal Entry model to Moment's public post
// language. Keeping this mapper shared prevents HTTP and CLI from drifting or
// exposing future Entry implementation fields such as kind.
package postdto

import (
	"errors"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

// Post is the stable plain-text post representation used by HTTP and CLI.
type Post struct {
	ID          string    `json:"id"`
	ContentMD   string    `json:"content_md"`
	ContentHTML string    `json:"content_html"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
	PublishedAt time.Time `json:"published_at"`
}

// Page is one stable reverse-chronological timeline page.
type Page struct {
	Items []Post `json:"items"`
}

// FromPost strips internal Entry fields while preserving the canonical
// application result.
func FromPost(post app.Post) Post {
	return Post{
		ID:          post.Entry.ID.String(),
		ContentMD:   post.Entry.ContentMD,
		ContentHTML: post.ContentHTML,
		CreatedAt:   post.Entry.CreatedAt.UTC(),
		UpdatedAt:   post.Entry.UpdatedAt.UTC(),
		PublishedAt: post.Entry.PublishedAt.UTC(),
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
