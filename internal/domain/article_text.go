package domain

import (
	"strings"

	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// ArticleVisibleText is the prose of an article with every structure removed:
// what a reader would have if the page were read aloud.
//
// Picture descriptions stay out of it, the same way a moment's media
// descriptions stay out of its derived text. They describe an image, not the
// article, and an excerpt made of them would read as nonsense.
func ArticleVisibleText(document ArticleDocument) string {
	var output strings.Builder
	appendArticleVisibleText(&output, document.Content)
	return cleanVisibleText(output.String())
}

// ArticleExcerpt derives the legacy bounded summary stored beside the body.
// Display previews use ArticlePreviewText without changing this projection.
//
// It is prose and only prose. Code and table cells are visible text and belong
// in the index, but read as noise in a one-line summary: a card whose subtitle
// is "项目 数量 驿马 三百匹 func main() {" tells a reader nothing about the
// article. Headings stay, because a heading is a sentence about what follows.
func ArticleExcerpt(document ArticleDocument) string {
	var output strings.Builder
	appendArticleProse(&output, document.Content)
	prose := cleanVisibleText(output.String())
	runes := []rune(prose)
	if len(runes) <= ArticleExcerptCodePoints {
		return prose
	}
	return strings.TrimSpace(string(runes[:ArticleExcerptCodePoints]))
}

// ArticlePreviewText is the reading preview shown by article cards. It keeps
// authored line breaks and inline runs, unlike the legacy storage excerpt and
// the search projection. Its limit is presentation-only: changing a card must
// not rewrite the body or the bounded excerpt stored beside it.
func ArticlePreviewText(document ArticleDocument) string {
	const previewCodePoints = 200
	var output strings.Builder
	appendArticlePreviewText(&output, document.Content)
	prose := strings.TrimSpace(output.String())
	if prose == "" {
		return ""
	}
	count := 0
	for offset := range prose {
		if count == previewCodePoints {
			prose = prose[:offset]
			break
		}
		count++
	}
	return strings.TrimSpace(prose) + "..."
}

func appendArticlePreviewText(output *strings.Builder, nodes []ArticleNode) {
	for _, node := range nodes {
		switch node.Type {
		case ArticleNodeMapCard:
			output.WriteString(node.MapCard.Text())
			output.WriteByte('\n')
		case ArticleNodeCodeBlock, ArticleNodeTable, ArticleNodeImage, ArticleNodeHorizontalRule:
			continue
		case ArticleNodeText:
			output.WriteString(node.Text)
		case ArticleNodeHardBreak:
			output.WriteByte('\n')
		case ArticleNodeParagraph, ArticleNodeHeading:
			appendArticlePreviewText(output, node.Content)
			output.WriteByte('\n')
		default:
			appendArticlePreviewText(output, node.Content)
		}
	}
}

func appendArticleProse(output *strings.Builder, nodes []ArticleNode) {
	for _, node := range nodes {
		switch node.Type {
		case ArticleNodeMapCard:
			output.WriteString(node.MapCard.Text())
			output.WriteByte('\n')
		case ArticleNodeCodeBlock, ArticleNodeTable:
			continue
		case ArticleNodeText:
			output.WriteString(node.Text)
		case ArticleNodeHardBreak, ArticleNodeHorizontalRule, ArticleNodeImage:
			output.WriteByte(' ')
		default:
			appendArticleProse(output, node.Content)
			output.WriteByte(' ')
		}
	}
}

// ArticleSearchText derives the indexed text of an article. The title is part
// of it because the title is how anybody looks for an article again.
//
// The folding matches NormalizeContentV2 exactly, since a query normalized
// there has to meet this on the same terms.
func ArticleSearchText(title string, document ArticleDocument) string {
	combined := strings.TrimSpace(title) + " " + ArticleVisibleText(document)
	return cleanVisibleText(cases.Fold().String(norm.NFKC.String(combined)))
}

func appendArticleVisibleText(output *strings.Builder, nodes []ArticleNode) {
	for _, node := range nodes {
		switch node.Type {
		case ArticleNodeMapCard:
			output.WriteString(node.MapCard.Text())
			output.WriteByte('\n')
		case ArticleNodeText:
			output.WriteString(node.Text)
		case ArticleNodeHardBreak, ArticleNodeHorizontalRule, ArticleNodeImage:
			output.WriteByte(' ')
		default:
			appendArticleVisibleText(output, node.Content)
			output.WriteByte(' ')
		}
	}
}
