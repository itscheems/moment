package domain

import (
	"html"
	"strconv"
	"strings"
)

// ArticleRenderOptions carries the one thing this package deliberately refuses
// to know: how this deployment addresses a managed media object. Keeping the
// URL shape out of the domain is what stops it from having an opinion about
// /api/v1, and it makes the renderer testable without a server.
type ArticleRenderOptions struct {
	MediaURL func(mediaID ID) string
	// MediaGeometry answers a picture's stored size when the caller knows it.
	// Without it an article body is the only place on this site whose pictures
	// do not reserve their space, and the page jumps as they arrive.
	MediaGeometry func(mediaID ID) (width, height uint32, known bool)
}

// Marks nest in a fixed order so the same document always renders to the same
// bytes. Code sits innermost because its content is literal.
var articleMarkOrder = []string{
	ArticleMarkLink, ArticleMarkBold, ArticleMarkItalic,
	ArticleMarkUnderline, ArticleMarkStrike, ArticleMarkCode,
}

var articleMarkTags = map[string][2]string{
	ArticleMarkBold:      {"<strong>", "</strong>"},
	ArticleMarkItalic:    {"<em>", "</em>"},
	ArticleMarkUnderline: {"<u>", "</u>"},
	ArticleMarkStrike:    {"<del>", "</del>"},
	ArticleMarkCode:      {"<code>", "</code>"},
}

// RenderArticleHTML turns a validated body into the HTML a reader receives.
//
// Everything it emits comes from the allowlist the parser enforced, and every
// piece of author text is escaped on the way out, so the output carries no
// markup the author did not choose from that list. External images cannot
// appear because an image node names managed media and nothing else.
func RenderArticleHTML(document ArticleDocument, options ArticleRenderOptions) (string, error) {
	if options.MediaURL == nil {
		return "", NewError(CodeInternal, "rendering an article needs a way to address media", nil)
	}
	var output strings.Builder
	renderArticleNodes(&output, document.Content, options)
	return output.String(), nil
}

func renderArticleNodes(output *strings.Builder, nodes []ArticleNode, options ArticleRenderOptions) {
	for _, node := range nodes {
		renderArticleNode(output, node, options)
	}
}

func renderArticleNode(output *strings.Builder, node ArticleNode, options ArticleRenderOptions) {
	switch node.Type {
	case ArticleNodeText:
		renderArticleText(output, node)
	case ArticleNodeHardBreak:
		output.WriteString("<br>")
	case ArticleNodeHorizontalRule:
		output.WriteString("<hr>")
	case ArticleNodeParagraph:
		renderArticleWrapped(output, "p", node, options)
	case ArticleNodeHeading:
		renderArticleWrapped(output, "h"+strconv.Itoa(node.Level), node, options)
	case ArticleNodeBlockquote:
		renderArticleWrapped(output, "blockquote", node, options)
	case ArticleNodeBulletList:
		renderArticleWrapped(output, "ul", node, options)
	case ArticleNodeListItem:
		renderArticleWrapped(output, "li", node, options)
	case ArticleNodeOrderedList:
		output.WriteString("<ol")
		if node.Start > 1 {
			output.WriteString(` start="` + strconv.Itoa(node.Start) + `"`)
		}
		output.WriteString(">")
		renderArticleNodes(output, node.Content, options)
		output.WriteString("</ol>")
	case ArticleNodeCodeBlock:
		output.WriteString("<pre><code")
		if node.Language != "" {
			output.WriteString(` class="language-` + html.EscapeString(node.Language) + `"`)
		}
		output.WriteString(">")
		for _, child := range node.Content {
			output.WriteString(html.EscapeString(child.Text))
		}
		output.WriteString("</code></pre>")
	case ArticleNodeImage:
		output.WriteString(`<img src="` + html.EscapeString(options.MediaURL(node.MediaID)) + `"`)
		output.WriteString(` alt="` + html.EscapeString(node.Alt) + `"`)
		if options.MediaGeometry != nil {
			if width, height, known := options.MediaGeometry(node.MediaID); known && width > 0 && height > 0 {
				output.WriteString(` width="` + strconv.FormatUint(uint64(width), 10) + `"`)
				output.WriteString(` height="` + strconv.FormatUint(uint64(height), 10) + `"`)
			}
		}
		output.WriteString(` loading="lazy">`)
	case ArticleNodeTable:
		renderArticleTable(output, node, options)
	case ArticleNodeTableRow:
		renderArticleWrapped(output, "tr", node, options)
	case ArticleNodeTableHeader:
		renderArticleCell(output, "th", node, options)
	case ArticleNodeTableCell:
		renderArticleCell(output, "td", node, options)
	}
}

// The first row becomes a header only when every cell in it is one, which is
// the shape a Markdown table already renders to.
func renderArticleTable(output *strings.Builder, node ArticleNode, options ArticleRenderOptions) {
	output.WriteString("<table>")
	rows := node.Content
	if len(rows) > 0 && articleRowIsHeader(rows[0]) {
		output.WriteString("<thead>")
		renderArticleNode(output, rows[0], options)
		output.WriteString("</thead>")
		rows = rows[1:]
	}
	if len(rows) > 0 {
		output.WriteString("<tbody>")
		renderArticleNodes(output, rows, options)
		output.WriteString("</tbody>")
	}
	output.WriteString("</table>")
}

func articleRowIsHeader(row ArticleNode) bool {
	if len(row.Content) == 0 {
		return false
	}
	for _, cell := range row.Content {
		if cell.Type != ArticleNodeTableHeader {
			return false
		}
	}
	return true
}

func renderArticleCell(output *strings.Builder, tag string, node ArticleNode, options ArticleRenderOptions) {
	output.WriteString("<" + tag)
	if node.ColSpan > 1 {
		output.WriteString(` colspan="` + strconv.Itoa(node.ColSpan) + `"`)
	}
	if node.RowSpan > 1 {
		output.WriteString(` rowspan="` + strconv.Itoa(node.RowSpan) + `"`)
	}
	output.WriteString(">")
	renderArticleNodes(output, node.Content, options)
	output.WriteString("</" + tag + ">")
}

func renderArticleWrapped(output *strings.Builder, tag string, node ArticleNode, options ArticleRenderOptions) {
	output.WriteString("<" + tag + ">")
	renderArticleNodes(output, node.Content, options)
	output.WriteString("</" + tag + ">")
}

func renderArticleText(output *strings.Builder, node ArticleNode) {
	closers := make([]string, 0, len(node.Marks))
	for _, name := range articleMarkOrder {
		mark, present := findArticleMark(node.Marks, name)
		if !present {
			continue
		}
		if name == ArticleMarkLink {
			// The same fixed rel policy the Markdown renderer applies.
			output.WriteString(`<a rel="nofollow noopener noreferrer" href="` + html.EscapeString(mark.Href) + `">`)
			closers = append(closers, "</a>")
			continue
		}
		tags := articleMarkTags[name]
		output.WriteString(tags[0])
		closers = append(closers, tags[1])
	}
	output.WriteString(html.EscapeString(node.Text))
	for index := len(closers) - 1; index >= 0; index-- {
		output.WriteString(closers[index])
	}
}

func findArticleMark(marks []ArticleMark, name string) (ArticleMark, bool) {
	for _, mark := range marks {
		if mark.Type == name {
			return mark, true
		}
	}
	return ArticleMark{}, false
}
