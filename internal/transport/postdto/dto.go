// Package postdto maps the internal Entry model to Moment's public post
// language. Keeping this mapper shared prevents HTTP and CLI from drifting or
// exposing future Entry implementation fields such as kind.
package postdto

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"strings"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/linkfetch"
	"github.com/moment/moment/internal/mediatype"
)

// Post is the stable plain-text post representation used by HTTP and CLI.
//
// Visibility is who may read the post, "everyone" or "only_me". A reader only
// ever receives posts they may read, so for anyone but the owner it is always
// "everyone".
type Post struct {
	AuthorInfo *Author `json:"author,omitempty"`
	ID         string  `json:"id"`
	// Kind says which body shape this post has: "moment" or "article". It is
	// on the wire because a reader has to be able to tell them apart, and
	// because ContentMD being absent is a consequence of it rather than a
	// signal of its own.
	Kind string `json:"kind"`
	// ContentMD is null for an article.
	ContentMD *string `json:"content_md"`
	// Article carries the parts a moment does not have. It is null for a
	// moment, and its Body is null in a list, where a card needs the title,
	// the excerpt and the cover and nothing else.
	Article      *Article          `json:"article"`
	ContentHTML  string            `json:"content_html"`
	CreatedAt    time.Time         `json:"created_at"`
	UpdatedAt    time.Time         `json:"updated_at"`
	PublishedAt  time.Time         `json:"published_at"`
	RepostedAt   *time.Time        `json:"reposted_at"`
	EditedAt     *time.Time        `json:"edited_at"`
	Visibility   string            `json:"visibility"`
	TimelineKind string            `json:"timeline_item_kind"`
	CommentCount int               `json:"comment_count"`
	Media        []MediaProjection `json:"media"`
	Location     *Location         `json:"location"`
	LinkCards    []LinkCard        `json:"link_cards"`
	QuotedPost   *QuotedPost       `json:"quoted_post"`
}

func fromArticle(post app.Post) *Article {
	mapped := fromArticleCard(post.ArticleCard, post.Article)
	if mapped == nil || post.Article == nil {
		return mapped
	}
	seen := make(map[domain.ID]bool)
	for _, media := range post.Media {
		if seen[media.MediaID] {
			continue
		}
		seen[media.MediaID] = true
		mapped.BodyMedia = append(mapped.BodyMedia, ArticleCover{
			MediaID: media.MediaID.String(), MIMEType: media.MIMEType,
			Width: media.Width, Height: media.Height,
		})
	}
	return mapped
}

// fromArticleCard maps a card, adding the body only when the caller has one.
// A list read and a quote both stop at the card.
func fromArticleCard(card *app.ArticleCard, body *domain.Article) *Article {
	if card == nil {
		return nil
	}
	mapped := &Article{Title: card.Title, Excerpt: card.Excerpt}
	if cover := card.Cover; cover != nil {
		mapped.Cover = &ArticleCover{
			MediaID: cover.MediaID.String(), MIMEType: cover.MIMEType,
			Width: cover.Width, Height: cover.Height,
		}
	}
	if body != nil {
		encoded, err := domain.MarshalArticleDocument(body.Body)
		if err == nil {
			version := body.Body.SchemaVersion
			mapped.Body, mapped.BodySchemaVersion = encoded, &version
		}
	}
	return mapped
}

// cloneOptionalText copies an optional body so the projection cannot be used
// to reach back and change the entry it was built from, the same reason media
// usages are deep-copied on their way across this boundary.
func cloneOptionalText(value *string) *string {
	if value == nil {
		return nil
	}
	copied := *value
	return &copied
}

// Article is what a reader receives of an article.
//
// Body is present on a detail read and null in a list. A page of twenty
// articles carrying twenty full bodies would be megabytes nobody on that page
// reads, and the card needs none of it.
type Article struct {
	Title             string          `json:"title"`
	Excerpt           string          `json:"excerpt"`
	Cover             *ArticleCover   `json:"cover"`
	Body              json.RawMessage `json:"body"`
	BodySchemaVersion *int            `json:"body_schema_version"`
	// Geometry is a read projection, separate from the stored document and
	// attachment grid. It lets structured renderers reserve inline image space.
	BodyMedia []ArticleCover `json:"body_media,omitempty"`
}

// ArticleCover carries geometry for the same reason every media projection
// does: the browser reserves the space before the bytes arrive.
type ArticleCover struct {
	MediaID  string `json:"media_id"`
	MIMEType string `json:"mime_type"`
	Width    uint32 `json:"width"`
	Height   uint32 `json:"height"`
}

// QuotedPost is either a complete embedded target or an explicit unavailable tombstone.
type QuotedPost struct {
	Unavailable bool    `json:"unavailable"`
	ID          *string `json:"id"`
	// Kind is set whenever the target is available. Unavailable is what says a
	// target is missing; a null ContentMD only says the target is an article.
	Kind *string `json:"kind"`
	// Article carries the quoted target's card when it is an article, and is
	// null otherwise. A quote shows a card; embedding a whole long post inside
	// a short one is not a quote.
	Article     *Article          `json:"article"`
	ContentMD   *string           `json:"content_md"`
	ContentHTML *string           `json:"content_html"`
	PublishedAt *time.Time        `json:"published_at"`
	Media       []MediaProjection `json:"media"`
	Location    *Location         `json:"location"`
}

// LinkCard is the deliberately narrow public card projection. It carries only
// what a renderer needs. The originally typed source URL, the capture time, and
// the recognizer label stay out of the public shape and survive only in the
// portable archive, so removing a recognizer can never change what a stored
// card renders as.
type LinkCard struct {
	Position          int     `json:"position"`
	CanonicalURL      string  `json:"canonical_url"`
	Kind              string  `json:"card_kind"`
	Title             string  `json:"title"`
	Description       string  `json:"description"`
	SiteName          string  `json:"site_name"`
	AuthorName        string  `json:"author_name"`
	DurationSeconds   *int    `json:"duration_seconds"`
	ViewCount         *int64  `json:"view_count"`
	CommentCount      *int64  `json:"comment_count"`
	LikeCount         *int64  `json:"like_count"`
	FaviconMediaID    *string `json:"favicon_media_id"`
	ThumbnailMediaID  *string `json:"thumbnail_media_id"`
	ThumbnailMIMEType *string `json:"thumbnail_mime_type"`
	ThumbnailWidth    *uint32 `json:"thumbnail_width"`
	ThumbnailHeight   *uint32 `json:"thumbnail_height"`
	// EmbedURL is emitted only while its origin is still allowlisted. A card
	// whose embed origin has since been withdrawn renders as a static card
	// instead of a frame the Content Security Policy would refuse.
	EmbedURL *string `json:"embed_url"`
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
	MIMEType string  `json:"mime_type"`
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
		case "mime_type":
			if err := decoder.Decode(&usage.MIMEType); err != nil {
				return errors.New("mime_type must be a string")
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
		!seen["media_id"] || !seen["position"] || !seen["alt_text"] || !seen["mime_type"] || !seen["width"] || !seen["height"] {
		return errors.New("media projection requires media_id, position, alt_text, mime_type, width, and height")
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
// linkCardProjections narrows stored cards to the public shape. The embed URL
// is re-checked against the allowlist here rather than trusted from storage, so
// withdrawing an embed origin downgrades historical cards to static rather than
// emitting a frame the Content Security Policy would refuse.
func linkCardProjections(cards []app.LinkCardProjection) []LinkCard {
	projections := make([]LinkCard, 0, len(cards))
	for _, projection := range cards {
		card := projection.Card
		mapped := LinkCard{
			Position:        card.Position,
			CanonicalURL:    card.CanonicalURL,
			Kind:            string(card.Kind),
			Title:           card.Title,
			Description:     card.Description,
			SiteName:        card.SiteName,
			AuthorName:      card.AuthorName,
			DurationSeconds: card.DurationSeconds,
			ViewCount:       card.ViewCount,
			CommentCount:    card.CommentCount,
			LikeCount:       card.LikeCount,
		}
		if card.FaviconMediaID != "" && projection.FaviconMIMEType != "" {
			favicon := card.FaviconMediaID
			mapped.FaviconMediaID = &favicon
		}
		if card.ThumbnailMediaID != "" {
			id := card.ThumbnailMediaID
			mapped.ThumbnailMediaID = &id
			if projection.ThumbnailMIMEType != "" {
				mimeType := projection.ThumbnailMIMEType
				width := projection.ThumbnailWidth
				height := projection.ThumbnailHeight
				mapped.ThumbnailMIMEType = &mimeType
				mapped.ThumbnailWidth = &width
				mapped.ThumbnailHeight = &height
			}
		}
		if card.EmbedURL != "" && linkfetch.IsEmbeddable(card.EmbedURL) {
			embed := card.EmbedURL
			mapped.EmbedURL = &embed
		} else {
			mapped.Kind = string(domain.LinkKindLink)
		}
		projections = append(projections, mapped)
	}
	return projections
}

// articleAttachments answers the usages a reader should see as attachments.
func articleAttachments(post app.Post) []app.MediaUsageProjection {
	if post.Entry.Kind == domain.KindArticle {
		return nil
	}
	return post.Media
}

// FromPost maps one application post onto the stable public shape.
func FromPost(post app.Post) Post {
	// media is the attachment grid. An article has none: the pictures it
	// places live inside its body, and entry_media holds them only so that
	// reference counting and reclamation stay in SQL. Publishing that
	// bookkeeping as attachments would draw every article twice.
	media := make([]MediaProjection, 0, len(post.Media))
	for _, usage := range articleAttachments(post) {
		mapped := MediaProjection{MediaID: usage.MediaID.String(), Position: usage.Position, MIMEType: usage.MIMEType, Width: usage.Width, Height: usage.Height}
		if usage.AltText != nil {
			alt := *usage.AltText
			mapped.AltText = &alt
		}
		media = append(media, mapped)
	}
	var repostedAt *time.Time
	if post.Entry.RepostedAt != nil {
		value := post.Entry.RepostedAt.UTC()
		repostedAt = &value
	}
	var editedAt *time.Time
	if post.Entry.EditedAt != nil {
		value := post.Entry.EditedAt.UTC()
		editedAt = &value
	}
	timelineKind := post.TimelineKind
	if timelineKind == "" {
		timelineKind = app.TimelineItemOriginal
	}
	mapped := Post{
		AuthorInfo:   fromAuthor(post.Author),
		ID:           post.Entry.ID.String(),
		Kind:         string(post.Entry.Kind),
		ContentMD:    cloneOptionalText(post.Entry.ContentMD),
		Article:      fromArticle(post),
		ContentHTML:  post.ContentHTML,
		CreatedAt:    post.Entry.CreatedAt.UTC(),
		UpdatedAt:    post.Entry.UpdatedAt.UTC(),
		PublishedAt:  post.Entry.PublishedAt.UTC(),
		RepostedAt:   repostedAt,
		EditedAt:     editedAt,
		Visibility:   string(post.Entry.Visibility),
		TimelineKind: string(timelineKind),
		CommentCount: post.CommentCount,
		Media:        media,
		Location: func() *Location {
			if post.Location == nil {
				return nil
			}
			return &Location{Name: post.Location.Name, DisplayName: post.Location.DisplayName}
		}(),
		LinkCards: linkCardProjections(post.LinkCards),
	}
	if post.Quoted != nil {
		if post.Quoted.Unavailable {
			mapped.QuotedPost = &QuotedPost{Unavailable: true}
			return mapped
		}
		quotedMedia := make([]MediaProjection, 0, len(post.Quoted.Media))
		for _, usage := range post.Quoted.Media {
			item := MediaProjection{MediaID: usage.MediaID.String(), Position: usage.Position, AltText: usage.AltText, MIMEType: usage.MIMEType, Width: usage.Width, Height: usage.Height}
			quotedMedia = append(quotedMedia, item)
		}
		id := post.Quoted.Entry.ID.String()
		kind := string(post.Quoted.Entry.Kind)
		contentHTML := post.Quoted.ContentHTML
		publishedAt := post.Quoted.Entry.PublishedAt.UTC()
		mapped.QuotedPost = &QuotedPost{
			ID: &id, Kind: &kind, Article: fromArticleCard(post.Quoted.ArticleCard, nil),
			ContentMD: cloneOptionalText(post.Quoted.Entry.ContentMD), ContentHTML: &contentHTML,
			PublishedAt: &publishedAt, Media: quotedMedia,
		}
		if post.Quoted.Location != nil {
			mapped.QuotedPost.Location = &Location{Name: post.Quoted.Location.Name, DisplayName: post.Quoted.Location.DisplayName}
		}
	}
	return mapped
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

// PrevCursor maps the newer-direction continuation the same way.
func PrevCursor(page app.PostPage) *string {
	if page.PrevCursor == "" {
		return nil
	}
	value := page.PrevCursor
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
	if post.EditedAt != nil && (!wireInstant(*post.EditedAt) || post.EditedAt.Before(post.CreatedAt) || post.EditedAt.After(post.UpdatedAt)) {
		return errors.New("post edited_at is not canonical")
	}
	if post.RepostedAt != nil && (!wireInstant(*post.RepostedAt) || post.RepostedAt.Before(post.PublishedAt) || post.UpdatedAt.Before(*post.RepostedAt)) {
		return errors.New("post reposted_at is not canonical")
	}
	if post.TimelineKind != string(app.TimelineItemOriginal) && post.TimelineKind != string(app.TimelineItemRepost) {
		return errors.New("post timeline_item_kind is invalid")
	}
	if !domain.Visibility(post.Visibility).Valid() {
		return errors.New("post visibility is invalid")
	}
	if post.TimelineKind == string(app.TimelineItemRepost) && post.RepostedAt == nil {
		return errors.New("repost timeline item requires reposted_at")
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
		if usage.Width == 0 || usage.Height == 0 || !validProjectionMIMEType(usage.MIMEType) {
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
	kind, err := domain.ParseEntryKind(post.Kind)
	if err != nil {
		return err
	}
	// A moment's HTML is derivable from its Markdown, so the two are held to
	// each other here. An article's is derived from a body this projection
	// does not carry, so the only thing to insist on is that it exists.
	if kind == domain.KindArticle {
		if post.ContentMD != nil {
			return errors.New("an article carries no Markdown body")
		}
		if post.Article == nil {
			return errors.New("an article must carry its title and excerpt")
		}
		// A list read leaves the body behind, and leaves the rendering behind
		// with it; a detail read carries both or neither.
		if hasArticleBody(post.Article.Body) != (post.ContentHTML != "") {
			return errors.New("an article carries its body and its rendered content together")
		}
		if len(post.Article.BodyMedia) > 0 {
			if !hasArticleBody(post.Article.Body) {
				return errors.New("article body media requires a document")
			}
			seen := make(map[string]bool)
			for _, media := range post.Article.BodyMedia {
				if _, err := domain.ParseID(media.MediaID); err != nil {
					return err
				}
				if seen[media.MediaID] || media.Width == 0 || media.Height == 0 || !strings.HasPrefix(media.MIMEType, "image/") || !validProjectionMIMEType(media.MIMEType) {
					return errors.New("article body media is invalid")
				}
				seen[media.MediaID] = true
			}
		}
	} else {
		if post.Article != nil {
			return errors.New("a moment carries no article")
		}
		if post.ContentMD == nil {
			return errors.New("a moment must carry a Markdown body")
		}
		if err := domain.ValidatePostMarkdown(*post.ContentMD, len(usages)); err != nil {
			return err
		}
		cards := make([]domain.LinkCard, 0, len(post.LinkCards))
		for _, card := range post.LinkCards {
			cards = append(cards, domain.LinkCard{SourceURL: card.CanonicalURL, CanonicalURL: card.CanonicalURL})
		}
		html, renderErr := domain.RenderMarkdownWithLinkCards(*post.ContentMD, cards)
		if renderErr != nil {
			return renderErr
		}
		if html != post.ContentHTML {
			return errors.New("post content_html does not match content_md")
		}
	}
	if post.QuotedPost != nil {
		if post.QuotedPost.Unavailable {
			if post.QuotedPost.ID != nil || post.QuotedPost.Kind != nil || post.QuotedPost.Article != nil || post.QuotedPost.ContentMD != nil || post.QuotedPost.ContentHTML != nil || post.QuotedPost.PublishedAt != nil || post.QuotedPost.Media != nil || post.QuotedPost.Location != nil {
				return errors.New("unavailable quoted post must not contain target data")
			}
			return nil
		}
		if post.QuotedPost.ID == nil || post.QuotedPost.Kind == nil || post.QuotedPost.ContentHTML == nil || post.QuotedPost.PublishedAt == nil || post.QuotedPost.Media == nil {
			return errors.New("available quoted post requires complete target data")
		}
		if _, err := domain.ParseID(*post.QuotedPost.ID); err != nil {
			return err
		}
		if *post.QuotedPost.ID == post.ID {
			return errors.New("post cannot quote itself")
		}
		if !wireInstant(*post.QuotedPost.PublishedAt) {
			return errors.New("quoted post is not canonical")
		}
		if err := validateQuotedPost(*post.QuotedPost); err != nil {
			return err
		}
	}
	return nil
}

func validateQuotedPost(post QuotedPost) error {
	if post.ID == nil || post.Kind == nil || post.ContentHTML == nil || post.PublishedAt == nil {
		return errors.New("quoted post target data is incomplete")
	}
	kind, err := domain.ParseEntryKind(*post.Kind)
	if err != nil {
		return err
	}
	if kind == domain.KindArticle {
		if post.ContentMD != nil {
			return errors.New("a quoted article carries no Markdown body")
		}
		if post.Article == nil {
			return errors.New("a quoted article must carry its card")
		}
		if hasArticleBody(post.Article.Body) {
			return errors.New("a quote shows an article's card, not its body")
		}
	} else {
		if post.ContentMD == nil {
			return errors.New("quoted post target data is incomplete")
		}
		if err := domain.ValidateMarkdown(*post.ContentMD); err != nil {
			return err
		}
		html, renderErr := domain.RenderMarkdown(*post.ContentMD)
		if renderErr != nil || html != *post.ContentHTML {
			return errors.New("quoted post content_html does not match content_md")
		}
	}
	if post.Location != nil {
		location := domain.Location{Name: post.Location.Name, DisplayName: post.Location.DisplayName, Provider: "manual"}
		if err := location.Validate(); err != nil {
			return err
		}
	}
	usages := make([]domain.EntryMediaUsage, 0, len(post.Media))
	for _, usage := range post.Media {
		if usage.Width == 0 || usage.Height == 0 || !validProjectionMIMEType(usage.MIMEType) {
			return errors.New("quoted post media is not canonical")
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
	return nil
}

// json.RawMessage receives the literal bytes "null" on an API read, whereas
// a direct projection has a nil slice. Both mean the list left its body behind.
func hasArticleBody(body json.RawMessage) bool {
	return body != nil && !bytes.Equal(bytes.TrimSpace(body), []byte("null"))
}

func validProjectionMIMEType(value string) bool {
	return mediatype.IsSupported(value)
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

// ArticleDraft is one unfinished article on the wire.
//
// UpdatedAt is not decoration: a client sends it back when it saves, and a save
// based on a revision somebody else has already replaced is refused rather than
// applied. PublishedEntryID names the article this draft is a working copy of.
type ArticleDraft struct {
	AuthorUserID      string          `json:"author_user_id"`
	SourceState       string          `json:"source_state"`
	SourceVisibility  string          `json:"source_visibility"`
	ID                string          `json:"id"`
	Title             string          `json:"title"`
	Excerpt           string          `json:"excerpt"`
	Body              json.RawMessage `json:"body"`
	BodySchemaVersion int             `json:"body_schema_version"`
	CoverMediaID      *string         `json:"cover_media_id"`
	PublishedEntryID  *string         `json:"published_entry_id"`
	CreatedAt         time.Time       `json:"created_at"`
	UpdatedAt         time.Time       `json:"updated_at"`
}

// FromArticleDraft maps one stored draft onto the stable public shape.
func FromArticleDraft(record app.ArticleDraftRecord) ArticleDraft {
	state := record.SourceState
	if state == "" {
		state = "draft"
		if record.PublishedEntryID != nil {
			state = "working_copy"
		}
	}
	visibility := record.SourceVisibility
	if visibility == "" {
		visibility = domain.VisibilityEveryone
	}
	mapped := ArticleDraft{
		AuthorUserID: record.AuthorUserID, SourceState: state, SourceVisibility: string(visibility),
		ID:                record.ID.String(),
		Title:             record.Draft.Title,
		Excerpt:           domain.ArticlePreviewText(record.Draft.Body),
		BodySchemaVersion: record.Draft.Body.SchemaVersion,
		CreatedAt:         record.CreatedAt.UTC(),
		UpdatedAt:         record.UpdatedAt.UTC(),
	}
	if body, err := domain.MarshalArticleDocument(record.Draft.Body); err == nil {
		mapped.Body = body
	}
	if record.Draft.CoverID != nil {
		cover := record.Draft.CoverID.String()
		mapped.CoverMediaID = &cover
	}
	if record.PublishedEntryID != nil {
		entryID := record.PublishedEntryID.String()
		mapped.PublishedEntryID = &entryID
	}
	return mapped
}

// FromArticleDrafts maps a whole list, newest first as it arrived.
func FromArticleDrafts(records []app.ArticleDraftRecord) []ArticleDraft {
	mapped := make([]ArticleDraft, 0, len(records))
	for _, record := range records {
		mapped = append(mapped, FromArticleDraft(record))
	}
	return mapped
}

// RefreshCursor is the persistent newer watermark, distinct from pagination.
func RefreshCursor(page app.PostPage) *string {
	if page.RefreshCursor == "" {
		return nil
	}
	cursor := page.RefreshCursor
	return &cursor
}

// Author is the public identity of the account that authored a post.
type Author struct {
	UserID        string  `json:"user_id"`
	Username      string  `json:"username"`
	DisplayName   string  `json:"display_name"`
	AvatarMediaID *string `json:"avatar_media_id"`
	ProfileURL    string  `json:"profile_url"`
}

func fromAuthor(author *app.CommentAuthor) *Author {
	if author == nil {
		return nil
	}
	return &Author{UserID: author.UserID, Username: author.Username, DisplayName: author.DisplayName, AvatarMediaID: author.AvatarMediaID, ProfileURL: author.ProfileURL}
}

// ArticleDraftSummary is the body-free management list shape.
type ArticleDraftSummary struct {
	ID               string    `json:"id"`
	AuthorUserID     string    `json:"author_user_id"`
	Title            string    `json:"title"`
	Excerpt          string    `json:"excerpt"`
	PublishedEntryID *string   `json:"published_entry_id"`
	SourceState      string    `json:"source_state"`
	SourceVisibility string    `json:"source_visibility"`
	CreatedAt        time.Time `json:"created_at"`
	UpdatedAt        time.Time `json:"updated_at"`
}

// FromArticleDraftSummaries maps the current author's list metadata.
func FromArticleDraftSummaries(records []app.ArticleDraftSummary) []ArticleDraftSummary {
	result := make([]ArticleDraftSummary, 0, len(records))
	for _, record := range records {
		var target *string
		if record.PublishedEntryID != nil {
			v := record.PublishedEntryID.String()
			target = &v
		}
		result = append(result, ArticleDraftSummary{ID: record.ID.String(), AuthorUserID: record.AuthorUserID, Title: record.Title, Excerpt: record.Excerpt, PublishedEntryID: target, SourceState: record.SourceState, SourceVisibility: string(record.SourceVisibility), CreatedAt: record.CreatedAt, UpdatedAt: record.UpdatedAt})
	}
	return result
}
