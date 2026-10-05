package domain

import "regexp"

// These are the persisted attributes produced by the editor's native nodes.
var articlePresentationAttrs = map[string][]string{
	ArticleNodeParagraph:   {"id", "textAlign", "backgroundColor", "nodeTextAlign", "nodeVerticalAlign"},
	ArticleNodeHeading:     {"id", "data-toc-id", "textAlign", "backgroundColor", "nodeTextAlign", "nodeVerticalAlign"},
	ArticleNodeBlockquote:  {"id", "backgroundColor", "nodeTextAlign", "nodeVerticalAlign"},
	ArticleNodeBulletList:  {"id", "backgroundColor"},
	ArticleNodeOrderedList: {"id", "backgroundColor", "type"},
	ArticleNodeTaskList:    {"id", "backgroundColor"},
	ArticleNodeTaskItem:    {},
	ArticleNodeCodeBlock:   {"id"},
	ArticleNodeTable:       {"id"},
	ArticleNodeTableCell:   {"colwidth", "align", "backgroundColor", "nodeTextAlign", "nodeVerticalAlign"},
	ArticleNodeTableHeader: {"colwidth", "align", "backgroundColor", "nodeTextAlign", "nodeVerticalAlign"},
	ArticleNodeImage:       {"title", "width", "height", "data-align", "showCaption"},
	ArticleNodeToc:         {"id", "backgroundColor", "topOffset", "maxShowCount", "showTitle"},
}

var articleColorPattern = regexp.MustCompile(`^(#[0-9a-fA-F]{3,8}|(?:rgb|rgba|hsl|hsla)\([0-9.,%+\- /]+\)|var\(--tt-[a-z0-9-]+\)|[a-zA-Z]{1,24})$`)
var articleAnchorPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,128}$`)

func init() {
	for name, attrs := range articlePresentationAttrs {
		spec := articleNodeSpecs[name]
		spec.attributes = append(spec.attributes, attrs...)
		articleNodeSpecs[name] = spec
	}
}

func applyArticlePresentation(node *ArticleNode, attrs map[string]any, path string) error {
	names := articlePresentationAttrs[node.Type]
	if node.Type == ArticleNodeTaskItem {
		names = []string{"checked"}
	}
	result := map[string]any{}
	for _, key := range names {
		value, present := attrs[key]
		if !present || value == nil {
			continue
		}
		switch key {
		case "checked", "showCaption", "showTitle":
			flag, ok := value.(bool)
			if !ok {
				return articleError(path, key+" must be a boolean")
			}
			if key == "showCaption" && !flag || key == "showTitle" && flag {
				continue
			}
			result[key] = flag
		case "width", "height", "topOffset", "maxShowCount":
			low := 1
			if key == "topOffset" {
				low = 0
			}
			number, err := articleIntAttribute(attrs, key, path, low, 100_000)
			if err != nil {
				return err
			}
			result[key] = number
		case "colwidth":
			array, ok := value.([]any)
			if !ok || len(array) > MaxArticleTableColumns {
				return articleError(path, "invalid column widths")
			}
			widths := make([]int, len(array))
			for index, item := range array {
				width, err := articleIntAttribute(map[string]any{"width": item}, "width", path, 0, 100_000)
				if err != nil {
					return err
				}
				widths[index] = width
			}
			result[key] = widths
		default:
			text, err := articleOptionalStringAttribute(attrs, key, path)
			if err != nil {
				return err
			}
			if text == "" {
				continue
			}
			switch key {
			case "id", "data-toc-id":
				if !articleAnchorPattern.MatchString(text) {
					return articleError(path, "invalid heading anchor")
				}
			case "backgroundColor":
				if !articleColorPattern.MatchString(text) {
					return articleError(path, "invalid background color")
				}
			case "textAlign", "nodeTextAlign", "align":
				if !articleAllows([]string{"left", "center", "right", "justify"}, text) {
					return articleError(path, "invalid text alignment")
				}
			case "nodeVerticalAlign":
				if !articleAllows([]string{"top", "middle", "bottom"}, text) {
					return articleError(path, "invalid vertical alignment")
				}
			case "data-align":
				if !articleAllows([]string{"left", "center", "right"}, text) {
					return articleError(path, "invalid image alignment")
				}
			case "type":
				if !articleAllows([]string{"1", "a", "A", "i", "I"}, text) {
					return articleError(path, "invalid list marker")
				}
			case "title":
				if len(text) > 4000 {
					return articleError(path, "image title is too long")
				}
			}
			result[key] = text
		}
	}
	if len(result) > 0 {
		node.Presentation = result
	}
	return nil
}
