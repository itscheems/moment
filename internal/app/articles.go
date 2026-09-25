package app

import (
	"fmt"
	"net/url"

	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/mediacontract"
)

// ArticleCard is what a list needs of an article: enough to draw its card
// without shipping the body. A timeline page holding twenty full bodies would
// send megabytes nobody reads.
type ArticleCard struct {
	Title        string
	Excerpt      string
	CoverMediaID *domain.ID
}

// ArticleMediaURL addresses a managed picture the way a browser reaches it.
//
// The recipe version rides in the query because it is part of the browser's
// cache key: when the derived-media recipe changes, a cached rendition must not
// be reused at the same address. The Web client builds the same URL, and the
// shared constant is what keeps the two spellings from drifting.
func ArticleMediaURL(mediaID domain.ID) string {
	return "/api/v1/media/" + url.PathEscape(mediaID.String()) +
		"/preview?v=" + url.QueryEscape(mediacontract.RecipeVersion)
}

func articleRenderOptions() domain.ArticleRenderOptions {
	return domain.ArticleRenderOptions{MediaURL: ArticleMediaURL}
}

// renderEntryHTML produces the HTML a reader receives, whichever body shape the
// entry has. An entry whose body did not come along is a read that lost half of
// itself, so it fails here rather than answering an empty page.
func renderEntryHTML(entry domain.Entry, article *domain.Article, cards []domain.LinkCard) (string, error) {
	switch entry.Kind {
	case domain.KindArticle:
		if article == nil {
			return "", fmt.Errorf("article %s was read without its body", entry.ID)
		}
		return domain.RenderArticleHTML(article.Body, articleRenderOptions())
	default:
		body, present := entry.Markdown()
		if !present {
			return "", fmt.Errorf("moment %s was read without its body", entry.ID)
		}
		return domain.RenderMarkdownWithLinkCards(body, cards)
	}
}

// validateStoredEntryBody re-checks what was read back, the way the Markdown
// path already did. An article body was validated when it was parsed, so the
// only thing left to insist on is that it is there.
func validateStoredEntryBody(entry domain.Entry, article *domain.Article, mediaCount int) error {
	if entry.Kind == domain.KindArticle {
		if article == nil {
			return fmt.Errorf("article %s was read without its body", entry.ID)
		}
		return nil
	}
	body, present := entry.Markdown()
	if !present {
		return fmt.Errorf("moment %s was read without its body", entry.ID)
	}
	return domain.ValidatePostMarkdown(body, mediaCount)
}
