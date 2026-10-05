package domain

import (
	"html"
	"strconv"
	"strings"
)

// Static delivery of the editor TOC: the same classes, anchors and normalized
// heading hierarchy, without editor plugins in feeds or CLI output.
type articleTocEntry struct {
	id, text string
	level    int
}

func articleHasToc(nodes []ArticleNode) bool {
	for _, node := range nodes {
		if node.Type == ArticleNodeToc || articleHasToc(node.Content) {
			return true
		}
	}
	return false
}

func articleTocHeadings(nodes []ArticleNode, index *int, seen map[string]bool, entries *[]articleTocEntry) []ArticleNode {
	result := append([]ArticleNode(nil), nodes...)
	for i := range result {
		node := &result[i]
		if node.Type == ArticleNodeHeading {
			var text strings.Builder
			articlePlainText(&text, node.Content)
			if text.Len() > 0 {
				id, _ := node.Presentation["data-toc-id"].(string)
				if id == "" || seen[id] {
					*index++
					id = "article-heading-" + strconv.Itoa(*index)
				}
				seen[id] = true
				attrs := make(map[string]any, len(node.Presentation)+1)
				for key, value := range node.Presentation {
					attrs[key] = value
				}
				attrs["id"] = id
				node.Presentation = attrs
				*entries = append(*entries, articleTocEntry{id, text.String(), node.Level})
			}
		}
		node.Content = articleTocHeadings(node.Content, index, seen, entries)
	}
	return result
}

func articlePlainText(output *strings.Builder, nodes []ArticleNode) {
	for _, node := range nodes {
		if node.Type == ArticleNodeText {
			output.WriteString(node.Text)
		}
		articlePlainText(output, node.Content)
	}
}

func renderArticleToc(output *strings.Builder, node ArticleNode, entries []articleTocEntry) {
	max := 20
	if value, ok := node.Presentation["maxShowCount"].(int); ok {
		max = value
	}
	if len(entries) > max {
		entries = entries[:max]
	}
	output.WriteString(`<div data-type="toc-node" class="tiptap-table-of-contents-node"`)
	renderArticlePresentation(output, node)
	output.WriteString(">")
	if len(entries) == 0 {
		output.WriteString(`<div class="tiptap-table-of-contents-empty">添加标题后，这里会显示目录。</div></div>`)
		return
	}
	if flag, ok := node.Presentation["showTitle"].(bool); !ok || flag {
		output.WriteString(`<div class="tiptap-table-of-contents-title">目录</div>`)
	}
	output.WriteString(`<nav aria-label="目录" class="tiptap-table-of-contents-list">`)
	depths := make([]int, len(entries))
	for i, item := range entries {
		depths[i] = 1
		for j := i - 1; j >= 0; j-- {
			if entries[j].level < item.level {
				depths[i] = depths[j] + 1
				break
			}
		}
		depth := strconv.Itoa(depths[i])
		output.WriteString(`<a class="tiptap-table-of-contents-item notranslate" href="#` + html.EscapeString(item.id) + `" data-depth="` + depth + `" style="--toc-depth:` + depth + `">` + html.EscapeString(item.text) + `</a>`)
	}
	output.WriteString("</nav></div>")
}
