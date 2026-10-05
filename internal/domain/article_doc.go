package domain

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"
)

// ArticleDocSchemaVersion is the version of the article body grammar, and it
// travels inside every stored body.
//
// It is not the migration number and not the archive format version. Those say
// what the table and the backup file look like; this says which grammar one
// particular body was written against. Adding a node type raises it, and a body
// stored under an older number keeps that number and keeps rendering.
const ArticleDocSchemaVersion = 2

// Bounds on one article. They exist so a constructed body cannot make the
// renderer, the search index or a page unbounded, not because anybody writes
// against them.
const (
	// MaxArticleTitleCodePoints keeps a title inside a card and an OG tag.
	MaxArticleTitleCodePoints = 200
	// MaxArticleBodyBytes bounds the serialized document.
	MaxArticleBodyBytes = 512 << 10
	// MaxArticleVisibleCodePoints is five times what a moment may carry.
	MaxArticleVisibleCodePoints = 100_000
	// MaxArticleNodes stops ten thousand empty paragraphs.
	MaxArticleNodes = 10_000
	// MaxArticleDepth lets the renderer recurse without guarding the stack.
	MaxArticleDepth = 10
	// MaxArticleInlineImages matches the position ceiling on entry_media.
	MaxArticleInlineImages = 50
	// MaxArticleTableColumns and MaxArticleTableRows bound one table.
	MaxArticleTableColumns = 20
	MaxArticleTableRows    = 200
	// ArticleExcerptCodePoints is how much of the body a card shows.
	ArticleExcerptCodePoints = 140
)

// Node types. The set is a allowlist: anything absent is refused, because a
// renderer that passes through what it does not understand is a renderer that
// trusts its input.
const (
	ArticleNodeParagraph      = "paragraph"
	ArticleNodeHeading        = "heading"
	ArticleNodeBlockquote     = "blockquote"
	ArticleNodeBulletList     = "bulletList"
	ArticleNodeOrderedList    = "orderedList"
	ArticleNodeListItem       = "listItem"
	ArticleNodeCodeBlock      = "codeBlock"
	ArticleNodeHorizontalRule = "horizontalRule"
	ArticleNodeTable          = "table"
	ArticleNodeTableRow       = "tableRow"
	ArticleNodeTableHeader    = "tableHeader"
	ArticleNodeTableCell      = "tableCell"
	ArticleNodeImage          = "image"
	ArticleNodeHardBreak      = "hardBreak"
	ArticleNodeText           = "text"
	ArticleNodeTaskList       = "taskList"
	ArticleNodeTaskItem       = "taskItem"
	ArticleNodeToc            = "tocNode"
)

// Mark types.
const (
	ArticleMarkBold        = "bold"
	ArticleMarkItalic      = "italic"
	ArticleMarkStrike      = "strike"
	ArticleMarkCode        = "code"
	ArticleMarkUnderline   = "underline"
	ArticleMarkLink        = "link"
	ArticleMarkTextStyle   = "textStyle"
	ArticleMarkHighlight   = "highlight"
	ArticleMarkSuperscript = "superscript"
	ArticleMarkSubscript   = "subscript"
)

// ArticleDocument is a body that has passed the grammar. Nothing in this
// package produces one without validating it first.
type ArticleDocument struct {
	SchemaVersion int
	Content       []ArticleNode
}

// ArticleNode is one validated node. Presentation retains only attributes
// checked against the native document contract.
type ArticleNode struct {
	Type         string
	Presentation map[string]any
	Level        int
	Start        int
	Language     string
	MediaID      ID
	Alt          string
	ColSpan      int
	RowSpan      int
	Text         string
	Marks        []ArticleMark
	Content      []ArticleNode
}

// ArticleMark is one validated inline mark.
type ArticleMark struct {
	Type  string
	Href  string
	Color string
}

type articleContentModel int

const (
	articleContentNone articleContentModel = iota
	articleContentInline
	articleContentTopBlocks
	articleContentNestedBlocks
	articleContentCellBlocks
	articleContentListItems
	articleContentTableRows
	articleContentRowCells
	articleContentPlainText
	articleContentTaskItems
)

type articleNodeSpec struct {
	attributes  []string
	content     articleContentModel
	needsChild  bool
	allowsMarks bool
}

var articleNodeSpecs = map[string]articleNodeSpec{
	ArticleNodeParagraph:      {content: articleContentInline},
	ArticleNodeHeading:        {attributes: []string{"level"}, content: articleContentInline, needsChild: true},
	ArticleNodeBlockquote:     {content: articleContentNestedBlocks, needsChild: true},
	ArticleNodeBulletList:     {content: articleContentListItems, needsChild: true},
	ArticleNodeOrderedList:    {attributes: []string{"start"}, content: articleContentListItems, needsChild: true},
	ArticleNodeListItem:       {content: articleContentNestedBlocks, needsChild: true},
	ArticleNodeCodeBlock:      {attributes: []string{"language"}, content: articleContentPlainText},
	ArticleNodeHorizontalRule: {content: articleContentNone},
	ArticleNodeTable:          {content: articleContentTableRows, needsChild: true},
	ArticleNodeTableRow:       {content: articleContentRowCells, needsChild: true},
	ArticleNodeTableHeader:    {attributes: []string{"colspan", "rowspan"}, content: articleContentCellBlocks, needsChild: true},
	ArticleNodeTableCell:      {attributes: []string{"colspan", "rowspan"}, content: articleContentCellBlocks, needsChild: true},
	ArticleNodeImage:          {attributes: []string{"media_id", "alt"}, content: articleContentInline},
	ArticleNodeHardBreak:      {content: articleContentNone},
	ArticleNodeText:           {content: articleContentNone, allowsMarks: true},
	ArticleNodeTaskList:       {content: articleContentTaskItems, needsChild: true},
	ArticleNodeTaskItem:       {attributes: []string{"checked"}, content: articleContentNestedBlocks, needsChild: true},
	ArticleNodeToc:            {content: articleContentNone},
}

var articleContentSets = map[articleContentModel][]string{
	articleContentInline: {ArticleNodeText, ArticleNodeHardBreak},
	articleContentTopBlocks: {
		ArticleNodeParagraph, ArticleNodeHeading, ArticleNodeBlockquote,
		ArticleNodeBulletList, ArticleNodeOrderedList, ArticleNodeCodeBlock,
		ArticleNodeHorizontalRule, ArticleNodeTable, ArticleNodeImage, ArticleNodeTaskList, ArticleNodeToc,
	},
	articleContentNestedBlocks: {
		ArticleNodeParagraph, ArticleNodeHeading, ArticleNodeBlockquote,
		ArticleNodeBulletList, ArticleNodeOrderedList, ArticleNodeCodeBlock, ArticleNodeHorizontalRule, ArticleNodeImage, ArticleNodeTable, ArticleNodeTaskList, ArticleNodeToc,
	},
	articleContentCellBlocks: {
		ArticleNodeParagraph, ArticleNodeHeading, ArticleNodeBlockquote, ArticleNodeBulletList, ArticleNodeOrderedList, ArticleNodeCodeBlock, ArticleNodeHorizontalRule, ArticleNodeImage, ArticleNodeTable, ArticleNodeTaskList, ArticleNodeToc,
	},
	articleContentTaskItems: {ArticleNodeTaskItem},
	articleContentListItems: {ArticleNodeListItem},
	articleContentTableRows: {ArticleNodeTableRow},
	articleContentRowCells:  {ArticleNodeTableHeader, ArticleNodeTableCell},
	articleContentPlainText: {ArticleNodeText},
}

// Fenced code keeps a language label only when it is one this site highlights.
// An unknown label is dropped rather than refused: it is a hint, and refusing
// a whole article over one would be out of proportion.
var articleCodeLanguages = map[string]struct{}{
	"bash": {}, "c": {}, "cpp": {}, "csharp": {}, "css": {}, "diff": {},
	"go": {}, "graphql": {}, "html": {}, "java": {}, "javascript": {},
	"json": {}, "kotlin": {}, "lua": {}, "makefile": {}, "markdown": {},
	"nginx": {}, "objectivec": {}, "php": {}, "plaintext": {}, "python": {},
	"ruby": {}, "rust": {}, "scss": {}, "shell": {}, "sql": {}, "swift": {},
	"toml": {}, "typescript": {}, "xml": {}, "yaml": {},
}

type rawArticleDocument struct {
	SchemaVersion *int             `json:"schema_version"`
	Type          string           `json:"type"`
	Content       []rawArticleNode `json:"content"`
}

type rawArticleNode struct {
	Type    string           `json:"type"`
	Attrs   map[string]any   `json:"attrs"`
	Content []rawArticleNode `json:"content"`
	Text    *string          `json:"text"`
	Marks   []rawArticleMark `json:"marks"`
}

type rawArticleMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs"`
}

type articleWalk struct {
	nodes  int
	images int
	text   int
}

// ParseArticleDocument validates one serialized article body and returns the
// typed tree. Every refusal names the path of the first node that broke the
// rule, because a body is deep enough that "invalid" on its own is useless.
func ParseArticleDocument(source []byte) (ArticleDocument, error) {
	return parseArticleDocument(source, true)
}

// ParseArticleDraftDocument applies the same grammar without insisting the
// document says anything. A draft is allowed to be empty -- that is most of
// what being a draft means -- while publishing one is not.
func ParseArticleDraftDocument(source []byte) (ArticleDocument, error) {
	return parseArticleDocument(source, false)
}

func parseArticleDocument(source []byte, requireSubstance bool) (ArticleDocument, error) {
	if length := len(source); length > MaxArticleBodyBytes {
		return ArticleDocument{}, NewError(CodeContentTooLarge, "article body exceeds the byte limit",
			map[string]any{"field": "body", "limit": MaxArticleBodyBytes, "actual": length})
	}
	if !utf8.Valid(source) {
		return ArticleDocument{}, articleError("", "article body must be valid UTF-8")
	}

	decoder := json.NewDecoder(bytes.NewReader(source))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	var raw rawArticleDocument
	if err := decoder.Decode(&raw); err != nil {
		return ArticleDocument{}, articleError("", fmt.Sprintf("article body is not a valid document: %v", err))
	}
	if decoder.More() {
		return ArticleDocument{}, articleError("", "article body carries trailing data")
	}

	if raw.SchemaVersion == nil {
		return ArticleDocument{}, articleError("", "article body must name its schema_version")
	}
	if *raw.SchemaVersion < 1 || *raw.SchemaVersion > ArticleDocSchemaVersion {
		return ArticleDocument{}, articleError("", fmt.Sprintf("article body schema_version %d is not supported", *raw.SchemaVersion))
	}
	if raw.Type != "doc" {
		return ArticleDocument{}, articleError("", "article body must be a doc")
	}

	walk := &articleWalk{}
	content, err := validateArticleChildren(raw.Content, articleContentTopBlocks, "content", 1, walk)
	if err != nil {
		return ArticleDocument{}, err
	}
	document := ArticleDocument{SchemaVersion: *raw.SchemaVersion, Content: content}
	if requireSubstance {
		if err := articleHasSubstance(document, walk); err != nil {
			return ArticleDocument{}, err
		}
	}
	return document, nil
}

// An article with no words, no picture and no table is not an article. The
// rule mirrors the one a media-only moment already lives under.
func articleHasSubstance(document ArticleDocument, walk *articleWalk) error {
	if walk != nil && walk.images > 0 {
		return nil
	}
	if walk == nil && len(Article{Body: document}.InlineMedia()) > 0 {
		return nil
	}
	if strings.TrimSpace(ArticleVisibleText(document)) != "" {
		return nil
	}
	if articleContainsTable(document.Content) {
		return nil
	}
	return NewError(CodeContentEmpty, "an article must contain text, a picture or a table", nil)
}

func articleContainsTable(nodes []ArticleNode) bool {
	for _, node := range nodes {
		if node.Type == ArticleNodeTable || articleContainsTable(node.Content) {
			return true
		}
	}
	return false
}

func validateArticleChildren(raw []rawArticleNode, model articleContentModel, path string, depth int, walk *articleWalk) ([]ArticleNode, error) {
	if model == articleContentNone {
		if len(raw) > 0 {
			return nil, articleError(path, "this node cannot contain anything")
		}
		return nil, nil
	}
	if depth > MaxArticleDepth {
		return nil, articleError(path, fmt.Sprintf("article body is nested deeper than %d", MaxArticleDepth))
	}
	allowed := articleContentSets[model]
	nodes := make([]ArticleNode, 0, len(raw))
	for index, child := range raw {
		childPath := fmt.Sprintf("%s[%d]", path, index)
		if !articleAllows(allowed, child.Type) {
			return nil, articleError(childPath, fmt.Sprintf("%q cannot appear here", child.Type))
		}
		node, err := validateArticleNode(child, childPath, depth, walk)
		if err != nil {
			return nil, err
		}
		nodes = append(nodes, node)
	}
	return nodes, nil
}

func validateArticleNode(raw rawArticleNode, path string, depth int, walk *articleWalk) (ArticleNode, error) {
	spec, known := articleNodeSpecs[raw.Type]
	if !known {
		return ArticleNode{}, articleError(path, fmt.Sprintf("%q is not a node this site stores", raw.Type))
	}
	walk.nodes++
	if walk.nodes > MaxArticleNodes {
		return ArticleNode{}, articleError(path, fmt.Sprintf("article body holds more than %d nodes", MaxArticleNodes))
	}

	node := ArticleNode{Type: raw.Type}
	if err := articleAttributesAllowed(raw.Attrs, spec.attributes, path); err != nil {
		return ArticleNode{}, err
	}
	if err := applyArticlePresentation(&node, raw.Attrs, path); err != nil {
		return ArticleNode{}, err
	}
	if err := applyArticleAttributes(&node, raw.Attrs, path); err != nil {
		return ArticleNode{}, err
	}

	switch raw.Type {
	case ArticleNodeText:
		if raw.Text == nil || *raw.Text == "" {
			return ArticleNode{}, articleError(path, "a text node must carry text")
		}
		node.Text = *raw.Text
		walk.text += utf8.RuneCountInString(node.Text)
		if walk.text > MaxArticleVisibleCodePoints {
			return ArticleNode{}, NewError(CodeContentTooLarge, "article text exceeds the code-point limit",
				map[string]any{"field": "body", "limit": MaxArticleVisibleCodePoints})
		}
	case ArticleNodeImage:
		if node.MediaID == "" {
			return ArticleNode{}, articleError(path, "an image must name the managed media it shows")
		}
		walk.images++
		if walk.images > MaxArticleInlineImages {
			return ArticleNode{}, articleError(path, fmt.Sprintf("an article may place at most %d pictures", MaxArticleInlineImages))
		}
	default:
		if raw.Text != nil {
			return ArticleNode{}, articleError(path, "only a text node carries text")
		}
	}

	if len(raw.Marks) > 0 {
		if !spec.allowsMarks {
			return ArticleNode{}, articleError(path, "only a text node carries marks")
		}
		marks, err := validateArticleMarks(raw.Marks, path)
		if err != nil {
			return ArticleNode{}, err
		}
		node.Marks = marks
	}

	content := spec.content
	if raw.Type == ArticleNodeCodeBlock {
		// Code is literal: a mark inside it would be rendered, not escaped.
		content = articleContentPlainText
	}
	children, err := validateArticleChildren(raw.Content, content, path+".content", depth+1, walk)
	if err != nil {
		return ArticleNode{}, err
	}
	if content == articleContentPlainText {
		for index, child := range children {
			if len(child.Marks) > 0 {
				return ArticleNode{}, articleError(fmt.Sprintf("%s.content[%d]", path, index), "code cannot carry marks")
			}
		}
	}
	if spec.needsChild && len(children) == 0 {
		return ArticleNode{}, articleError(path, fmt.Sprintf("%q cannot be empty", raw.Type))
	}
	node.Content = children

	if raw.Type == ArticleNodeTable {
		if err := validateArticleTable(node, path); err != nil {
			return ArticleNode{}, err
		}
	}
	return node, nil
}

func validateArticleTable(table ArticleNode, path string) error {
	if len(table.Content) > MaxArticleTableRows {
		return articleError(path, fmt.Sprintf("a table may have at most %d rows", MaxArticleTableRows))
	}
	for index, row := range table.Content {
		columns := 0
		for _, cell := range row.Content {
			columns += cell.ColSpan
		}
		if columns > MaxArticleTableColumns {
			return articleError(fmt.Sprintf("%s.content[%d]", path, index),
				fmt.Sprintf("a table row may span at most %d columns", MaxArticleTableColumns))
		}
	}
	return nil
}

func validateArticleMarks(raw []rawArticleMark, path string) ([]ArticleMark, error) {
	marks := make([]ArticleMark, 0, len(raw))
	seen := make(map[string]struct{}, len(raw))
	for index, item := range raw {
		markPath := fmt.Sprintf("%s.marks[%d]", path, index)
		switch item.Type {
		case ArticleMarkBold, ArticleMarkItalic, ArticleMarkStrike, ArticleMarkCode, ArticleMarkUnderline, ArticleMarkSuperscript, ArticleMarkSubscript:
			if err := articleAttributesAllowed(item.Attrs, nil, markPath); err != nil {
				return nil, err
			}
			marks = append(marks, ArticleMark{Type: item.Type})
		case ArticleMarkTextStyle, ArticleMarkHighlight:
			if err := articleAttributesAllowed(item.Attrs, []string{"color"}, markPath); err != nil {
				return nil, err
			}
			color, err := articleStringAttribute(item.Attrs, "color", markPath)
			if err != nil {
				return nil, err
			}
			if !articleColorPattern.MatchString(color) {
				return nil, articleError(markPath, "invalid text color")
			}
			marks = append(marks, ArticleMark{Type: item.Type, Color: color})
		case ArticleMarkLink:
			if err := articleAttributesAllowed(item.Attrs, []string{"href"}, markPath); err != nil {
				return nil, err
			}
			href, err := articleStringAttribute(item.Attrs, "href", markPath)
			if err != nil {
				return nil, err
			}
			// The same validator the Markdown grammar uses. One allowlist.
			if err := validateLink([]byte(href)); err != nil {
				return nil, err
			}
			marks = append(marks, ArticleMark{Type: item.Type, Href: href})
		default:
			return nil, articleError(markPath, fmt.Sprintf("%q is not a mark this site stores", item.Type))
		}
		if _, duplicate := seen[item.Type]; duplicate {
			return nil, articleError(markPath, fmt.Sprintf("%q is applied twice", item.Type))
		}
		seen[item.Type] = struct{}{}
	}
	return marks, nil
}

func applyArticleAttributes(node *ArticleNode, attrs map[string]any, path string) error {
	switch node.Type {
	case ArticleNodeHeading:
		level, err := articleIntAttribute(attrs, "level", path, 1, 3)
		if err != nil {
			return err
		}
		if level == 0 {
			return articleError(path, "a heading must name its level")
		}
		node.Level = level
	case ArticleNodeOrderedList:
		start, err := articleIntAttribute(attrs, "start", path, 1, 1_000_000)
		if err != nil {
			return err
		}
		if start == 0 {
			start = 1
		}
		node.Start = start
	case ArticleNodeCodeBlock:
		language, err := articleOptionalStringAttribute(attrs, "language", path)
		if err != nil {
			return err
		}
		language = strings.ToLower(strings.TrimSpace(language))
		if _, known := articleCodeLanguages[language]; known {
			node.Language = language
		}
	case ArticleNodeImage:
		value, err := articleOptionalStringAttribute(attrs, "media_id", path)
		if err != nil {
			return err
		}
		if value != "" {
			id, parseErr := ParseID(value)
			if parseErr != nil {
				return articleError(path, "an image must name a managed media id")
			}
			node.MediaID = id
		}
		alt, err := articleOptionalStringAttribute(attrs, "alt", path)
		if err != nil {
			return err
		}
		if utf8.RuneCountInString(alt) > MaxMediaAltCodePoints {
			return articleError(path, fmt.Sprintf("a description may be at most %d code points", MaxMediaAltCodePoints))
		}
		node.Alt = alt
	case ArticleNodeTableHeader, ArticleNodeTableCell:
		colspan, err := articleIntAttribute(attrs, "colspan", path, 1, MaxArticleTableColumns)
		if err != nil {
			return err
		}
		rowspan, err := articleIntAttribute(attrs, "rowspan", path, 1, MaxArticleTableRows)
		if err != nil {
			return err
		}
		// An absent span means one. Normalizing here rather than at each
		// reader is what makes serializing and parsing an exact round trip.
		if colspan == 0 {
			colspan = 1
		}
		if rowspan == 0 {
			rowspan = 1
		}
		node.ColSpan, node.RowSpan = colspan, rowspan
	}
	return nil
}

// Attributes are an allowlist, and an unknown one is refused rather than
// dropped. Dropping would let the editor and the store disagree in silence.
func articleAttributesAllowed(attrs map[string]any, allowed []string, path string) error {
	for name := range attrs {
		if !articleAllows(allowed, name) {
			return articleError(path, fmt.Sprintf("%q is not an attribute this site stores", name))
		}
	}
	return nil
}

func articleAllows(allowed []string, name string) bool {
	for _, candidate := range allowed {
		if candidate == name {
			return true
		}
	}
	return false
}

func articleStringAttribute(attrs map[string]any, name, path string) (string, error) {
	value, present := attrs[name]
	if !present || value == nil {
		return "", articleError(path, fmt.Sprintf("%q is required here", name))
	}
	text, ok := value.(string)
	if !ok {
		return "", articleError(path, fmt.Sprintf("%q must be a string", name))
	}
	return text, nil
}

func articleOptionalStringAttribute(attrs map[string]any, name, path string) (string, error) {
	value, present := attrs[name]
	if !present || value == nil {
		return "", nil
	}
	text, ok := value.(string)
	if !ok {
		return "", articleError(path, fmt.Sprintf("%q must be a string", name))
	}
	return text, nil
}

// A missing or null numeric attribute answers zero, which each caller reads as
// "use the default". Tiptap writes null for an unset colspan, so refusing null
// here would refuse ordinary editor output.
func articleIntAttribute(attrs map[string]any, name, path string, low, high int) (int, error) {
	value, present := attrs[name]
	if !present || value == nil {
		return 0, nil
	}
	number, ok := value.(json.Number)
	if !ok {
		return 0, articleError(path, fmt.Sprintf("%q must be a number", name))
	}
	parsed, err := number.Int64()
	if err != nil {
		return 0, articleError(path, fmt.Sprintf("%q must be a whole number", name))
	}
	if parsed < int64(low) || parsed > int64(high) {
		return 0, articleError(path, fmt.Sprintf("%q must be between %d and %d", name, low, high))
	}
	return int(parsed), nil
}

func articleError(path, message string) error {
	details := map[string]any{"field": "body"}
	if path != "" {
		details["path"] = path
	}
	return NewError(CodeInvalidArgument, message, details)
}

// MarshalArticleDocument serializes a validated body back to its canonical
// JSON. Storing this rather than the bytes that arrived means what is written
// is exactly what the grammar accepted: there is no room for a field that
// slipped past because nobody looked at it.
//
// Parsing the result returns an equal document, and a test holds it to that.
func MarshalArticleDocument(document ArticleDocument) ([]byte, error) {
	encoded, err := json.Marshal(marshalledArticleDocument{
		SchemaVersion: document.SchemaVersion,
		Type:          "doc",
		Content:       marshalArticleNodes(document.Content),
	})
	if err != nil {
		return nil, NewError(CodeInternal, "could not serialize the article body", nil)
	}
	return encoded, nil
}

type marshalledArticleDocument struct {
	SchemaVersion int                     `json:"schema_version"`
	Type          string                  `json:"type"`
	Content       []marshalledArticleNode `json:"content"`
}

type marshalledArticleNode struct {
	Type    string                  `json:"type"`
	Attrs   map[string]any          `json:"attrs,omitempty"`
	Content []marshalledArticleNode `json:"content,omitempty"`
	Text    *string                 `json:"text,omitempty"`
	Marks   []marshalledArticleMark `json:"marks,omitempty"`
}

type marshalledArticleMark struct {
	Type  string         `json:"type"`
	Attrs map[string]any `json:"attrs,omitempty"`
}

func marshalArticleNodes(nodes []ArticleNode) []marshalledArticleNode {
	if len(nodes) == 0 {
		return nil
	}
	encoded := make([]marshalledArticleNode, 0, len(nodes))
	for _, node := range nodes {
		encoded = append(encoded, marshalArticleNode(node))
	}
	return encoded
}

func marshalArticleNode(node ArticleNode) marshalledArticleNode {
	encoded := marshalledArticleNode{Type: node.Type, Content: marshalArticleNodes(node.Content)}
	if node.Type == ArticleNodeText {
		text := node.Text
		encoded.Text = &text
	}
	// Only attributes that carry information are written. A default written out
	// and read back in is noise that makes two equal documents differ in bytes.
	attrs := map[string]any{}
	switch node.Type {
	case ArticleNodeHeading:
		attrs["level"] = node.Level
	case ArticleNodeOrderedList:
		if node.Start > 1 {
			attrs["start"] = node.Start
		}
	case ArticleNodeCodeBlock:
		if node.Language != "" {
			attrs["language"] = node.Language
		}
	case ArticleNodeImage:
		attrs["media_id"] = node.MediaID.String()
		if node.Alt != "" {
			attrs["alt"] = node.Alt
		}
	case ArticleNodeTableHeader, ArticleNodeTableCell:
		if node.ColSpan > 1 {
			attrs["colspan"] = node.ColSpan
		}
		if node.RowSpan > 1 {
			attrs["rowspan"] = node.RowSpan
		}
	}
	for key, value := range node.Presentation {
		attrs[key] = value
	}
	if len(attrs) > 0 {
		encoded.Attrs = attrs
	}
	for _, mark := range node.Marks {
		item := marshalledArticleMark{Type: mark.Type}
		if mark.Color != "" {
			item.Attrs = map[string]any{"color": mark.Color}
		}
		if mark.Type == ArticleMarkLink {
			item.Attrs = map[string]any{"href": mark.Href}
		}
		encoded.Marks = append(encoded.Marks, item)
	}
	return encoded
}
