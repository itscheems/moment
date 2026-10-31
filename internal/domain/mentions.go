package domain

import (
	"strings"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// MarkdownMentions inspects visible prose, excluding code and existing links.
func MarkdownMentions(source string) []string {
	data := []byte(source)
	doc := markdownEngine.Parser().Parse(text.NewReader(data))
	var handles []string
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node.Kind() {
		case ast.KindCodeSpan, ast.KindCodeBlock, ast.KindFencedCodeBlock, ast.KindLink, ast.KindAutoLink, ast.KindImage:
			return ast.WalkSkipChildren, nil
		}
		if run, ok := node.(*ast.Text); ok {
			if _, continuation := run.PreviousSibling().(*ast.Text); continuation {
				return ast.WalkContinue, nil
			}
			var prose strings.Builder
			for sibling := ast.Node(run); sibling != nil; sibling = sibling.NextSibling() {
				part, ok := sibling.(*ast.Text)
				if !ok {
					break
				}
				value := util.UnescapePunctuations(part.Value(data))
				value = util.ResolveNumericReferences(value)
				prose.Write(util.ResolveEntityNames(value))
				if part.SoftLineBreak() || part.HardLineBreak() {
					prose.WriteByte('\n')
				}
			}
			handles = append(handles, textMentions(prose.String())...)
		}
		return ast.WalkContinue, nil
	})
	return uniqueHandles(handles)
}

// ArticleMentions traverses prose nodes only; titles, media attributes and
// embedded quote targets are not implicit notification recipients.
func ArticleMentions(doc ArticleDocument) []string {
	var handles []string
	var walk func([]ArticleNode)
	walk = func(nodes []ArticleNode) {
		for _, node := range nodes {
			if node.Type == ArticleNodeCodeBlock {
				continue
			}
			if node.Type == ArticleNodeText {
				blocked := false
				for _, mark := range node.Marks {
					if mark.Type == ArticleMarkCode || mark.Type == ArticleMarkLink {
						blocked = true
						break
					}
				}
				if !blocked {
					handles = append(handles, textMentions(node.Text)...)
				}
			}
			walk(node.Content)
		}
	}
	walk(doc.Content)
	return uniqueHandles(handles)
}

func textMentions(value string) []string {
	var found []string
	word := func(b byte) bool {
		return b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9' || b == '_'
	}
	for i := 0; i < len(value); i++ {
		if value[i] != '@' || i > 0 && word(value[i-1]) {
			continue
		}
		end := i + 1
		for end < len(value) && (word(value[end]) || value[end] == '-') {
			end++
		}
		handle, err := NormalizeUsername(value[i+1 : end])
		if err == nil {
			found = append(found, handle)
		}
		i = end - 1
	}
	return found
}

func uniqueHandles(values []string) []string {
	result := make([]string, 0, len(values))
	seen := make(map[string]bool)
	for _, value := range values {
		value = strings.ToLower(value)
		if !seen[value] {
			seen[value] = true
			result = append(result, value)
		}
	}
	return result
}
