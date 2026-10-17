package domain

import (
	"strings"
	"unicode/utf8"
)

// Article is a published long post: a title, an optional cover picture and a
// structured body. The timeline envelope is the Entry stored beside it, whose
// ContentMD is nil precisely because the body lives here.
type Article struct {
	Title   string
	Body    ArticleDocument
	CoverID *ID
}

// NewArticle validates the parts around the body. The body arrives already
// parsed, because the only way to hold an ArticleDocument is to have passed it
// through the grammar.
func NewArticle(title string, body ArticleDocument, coverID *ID) (Article, error) {
	normalized, err := NormalizeArticleTitle(title)
	if err != nil {
		return Article{}, err
	}
	if coverID != nil {
		if _, err := ParseID(coverID.String()); err != nil {
			return Article{}, NewError(CodeInvalidArgument, "an article cover must name managed media",
				map[string]any{"field": "cover_media_id"})
		}
	}
	return Article{Title: normalized, Body: body, CoverID: coverID}, nil
}

// NormalizeArticleTitle returns the stored spelling of a title, which is the
// trimmed one: the database holds the same rule as a check, so a title with
// surrounding space would be accepted here and refused there.
func NormalizeArticleTitle(title string) (string, error) {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" {
		return "", NewError(CodeContentEmpty, "an article must have a title", map[string]any{"field": "title"})
	}
	if count := utf8.RuneCountInString(trimmed); count > MaxArticleTitleCodePoints {
		return "", NewError(CodeContentTooLarge, "article title exceeds the code-point limit",
			map[string]any{"field": "title", "limit": MaxArticleTitleCodePoints, "actual": count})
	}
	return trimmed, nil
}

// Excerpt is the legacy bounded prose summary persisted beside the body.
func (article Article) Excerpt() string {
	return ArticleExcerpt(article.Body)
}

// SearchText is what the derived index stores for this article.
func (article Article) SearchText() string {
	return ArticleSearchText(article.Title, article.Body)
}

// InlineMedia projects the pictures the body places, in the order they appear.
//
// The body is where a picture's position lives; nothing else knows it. This
// projection exists so that reference counting, reclamation and media
// visibility keep working the way they already do -- as one SQL predicate over
// rows -- instead of having to parse a document in three more places.
func (article Article) InlineMedia() []EntryMediaUsage {
	usages := make([]EntryMediaUsage, 0, 4)
	appendArticleMedia(&usages, article.Body.Content)
	for index := range usages {
		usages[index].Position = index
	}
	return usages
}

// CoverAndInlineMedia is every managed media object the article refers to,
// cover first. Callers that only need to know "which media does this touch"
// should use this rather than assembling it themselves and forgetting one.
func (article Article) CoverAndInlineMedia() []ID {
	ids := make([]ID, 0, 5)
	if article.CoverID != nil {
		ids = append(ids, *article.CoverID)
	}
	for _, usage := range article.InlineMedia() {
		ids = append(ids, usage.MediaID)
	}
	return ids
}

func appendArticleMedia(usages *[]EntryMediaUsage, nodes []ArticleNode) {
	for _, node := range nodes {
		if node.Type == ArticleNodeImage {
			usage := EntryMediaUsage{MediaID: node.MediaID}
			if node.Alt != "" {
				alt := node.Alt
				usage.AltText = &alt
			}
			*usages = append(*usages, usage)
			continue
		}
		appendArticleMedia(usages, node.Content)
	}
}

// ArticleDraft is an article that is not finished.
//
// It is a separate type from Article because it is allowed to break the one
// rule a published article cannot: it may have no title yet, and it may say
// nothing at all. Publishing is where those become requirements, which is why
// Publish is the only way to turn one into the other.
type ArticleDraft struct {
	Title   string
	Body    ArticleDocument
	CoverID *ID
}

// NewArticleDraft validates what a draft still has to get right: a title that
// is not absurdly long, and a cover that names managed media.
func NewArticleDraft(title string, body ArticleDocument, coverID *ID) (ArticleDraft, error) {
	trimmed := strings.TrimSpace(title)
	if count := utf8.RuneCountInString(trimmed); count > MaxArticleTitleCodePoints {
		return ArticleDraft{}, NewError(CodeContentTooLarge, "article title exceeds the code-point limit",
			map[string]any{"field": "title", "limit": MaxArticleTitleCodePoints, "actual": count})
	}
	if coverID != nil {
		if _, err := ParseID(coverID.String()); err != nil {
			return ArticleDraft{}, NewError(CodeInvalidArgument, "an article cover must name managed media",
				map[string]any{"field": "cover_media_id"})
		}
	}
	return ArticleDraft{Title: trimmed, Body: body, CoverID: coverID}, nil
}

// Publish promotes a draft into an article, applying the rules a draft was
// exempt from. A draft that says nothing cannot become a post.
func (draft ArticleDraft) Publish() (Article, error) {
	if err := articleHasSubstance(draft.Body, nil); err != nil {
		return Article{}, err
	}
	return NewArticle(draft.Title, draft.Body, draft.CoverID)
}

// InlineMedia projects the pictures a draft's body places, so an unpublished
// draft keeps its uploads from being reclaimed.
func (draft ArticleDraft) InlineMedia() []EntryMediaUsage {
	return Article{Body: draft.Body}.InlineMedia()
}

// CoverAndInlineMedia is every managed media object a draft refers to, for the
// same reason the published article has one: a caller asking "which media does
// this touch" must not have to assemble the answer and forget the cover.
func (draft ArticleDraft) CoverAndInlineMedia() []ID {
	return Article{Body: draft.Body, CoverID: draft.CoverID}.CoverAndInlineMedia()
}
