package domain

import (
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
	"golang.org/x/text/cases"
	"golang.org/x/text/unicode/norm"
)

// ContentNormalizerVersion identifies the derived search text contract. The
// version is persisted with the index so future normalizer changes require an
// explicit rebuild rather than silently mixing representations.
const ContentNormalizerVersion = 2

// NormalizeContentV2 converts Markdown-shaped input into deterministic search
// text. It keeps visible link labels and code, drops destinations and invisible
// structures, applies Unicode compatibility normalization and case folding,
// and collapses every whitespace run to one ASCII space.
//
// Both entry ingestion and search queries call this exact function.
func NormalizeContentV2(source string) (string, error) {
	if !utf8.ValidString(source) {
		return "", NewError(CodeInvalidArgument, "content must be valid UTF-8", map[string]any{"field": "content"})
	}
	data := []byte(source)
	document := markdownEngine.Parser().Parse(text.NewReader(data))
	var output strings.Builder
	appendVisible := func(value []byte) {
		value = util.UnescapePunctuations(value)
		value = util.ResolveNumericReferences(value)
		value = util.ResolveEntityNames(value)
		output.Write(value)
	}
	if err := ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			if node.Type() == ast.TypeBlock {
				output.WriteByte(' ')
			}
			return ast.WalkContinue, nil
		}
		switch typed := node.(type) {
		case *ast.AutoLink, *ast.Image, *ast.RawHTML, *ast.HTMLBlock:
			return ast.WalkSkipChildren, nil
		case *ast.CodeBlock:
			appendLines(&output, typed.Lines(), data)
			return ast.WalkSkipChildren, nil
		case *ast.FencedCodeBlock:
			appendLines(&output, typed.Lines(), data)
			return ast.WalkSkipChildren, nil
		case *ast.Text:
			value := typed.Value(data)
			if !hasAncestor(node, ast.KindLink) && !hasAncestor(node, ast.KindCodeSpan) {
				value = stripPlainURLs(value)
			}
			appendVisible(value)
			if typed.SoftLineBreak() || typed.HardLineBreak() {
				output.WriteByte(' ')
			}
		case *ast.String:
			appendVisible(typed.Value)
		}
		return ast.WalkContinue, nil
	}); err != nil {
		return "", NewError(CodeInternal, "could not normalize content", nil)
	}

	normalized := cases.Fold().String(norm.NFKC.String(output.String()))
	var clean strings.Builder
	spacePending := false
	for _, character := range normalized {
		switch {
		case unicode.IsSpace(character):
			spacePending = clean.Len() > 0
		case unicode.Is(unicode.Cf, character), unicode.IsControl(character):
			// Formatting controls and zero-width structures are not visible text.
		default:
			if spacePending {
				clean.WriteByte(' ')
				spacePending = false
			}
			clean.WriteRune(character)
		}
	}
	return strings.TrimSpace(clean.String()), nil
}

func hasAncestor(node ast.Node, kind ast.NodeKind) bool {
	for parent := node.Parent(); parent != nil; parent = parent.Parent() {
		if parent.Kind() == kind {
			return true
		}
	}
	return false
}

func stripPlainURLs(value []byte) []byte {
	var output strings.Builder
	for index := 0; index < len(value); {
		prefix := plainURLPrefix(value[index:])
		if prefix > 0 && (index == 0 || isURLBoundary(value[index-1])) {
			end := index + prefix
			for end < len(value) && value[end] < utf8.RuneSelf && !isURLBoundary(value[end]) {
				end++
			}
			if end > index+prefix {
				output.WriteByte(' ')
				index = end
				continue
			}
		}
		character, size := utf8.DecodeRune(value[index:])
		output.WriteRune(character)
		index += size
	}
	return []byte(output.String())
}

func plainURLPrefix(value []byte) int {
	lower := strings.ToLower(string(value[:min(len(value), len("https://"))]))
	for _, prefix := range []string{"https://", "http://", "mailto:", "www."} {
		if strings.HasPrefix(lower, prefix) {
			return len(prefix)
		}
	}
	return 0
}

func isURLBoundary(character byte) bool {
	if character >= utf8.RuneSelf {
		return true
	}
	switch character {
	case ' ', '\t', '\r', '\n', '<', '>', '"', '\'', '`', '(', ')', '[', ']', '{', '}', ',', ';':
		return true
	default:
		return false
	}
}

func appendLines(output *strings.Builder, lines *text.Segments, source []byte) {
	for index := 0; index < lines.Len(); index++ {
		segment := lines.At(index)
		output.Write(segment.Value(source))
		output.WriteByte(' ')
	}
}
