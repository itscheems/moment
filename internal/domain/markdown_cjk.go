package domain

import (
	"bytes"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	"github.com/yuin/goldmark/text"
)

// cjkLinkify transforms ordinary text after Markdown has identified code and
// explicit links. Goldmark's inline dispatch only recognizes ASCII punctuation;
// merely registering the UTF-8 prefix bytes cannot make it visit a CJK mark.
// The existing Linkify parser still owns URL recognition and trimming.
type cjkLinkify struct{ parser.InlineParser }

func (p cjkLinkify) Transform(document *ast.Document, reader text.Reader, _ parser.Context) {
	source := reader.Source()
	var nodes []*ast.Text
	_ = ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch node.(type) {
		case *ast.Link, *ast.AutoLink, *ast.CodeSpan, *ast.Image:
			return ast.WalkSkipChildren, nil
		}
		if node, ok := node.(*ast.Text); ok && !node.IsRaw() {
			nodes = append(nodes, node)
		}
		return ast.WalkContinue, nil
	})
	for _, node := range nodes {
		start, stop := node.Segment.Start, node.Segment.Stop
		for cursor := start; cursor < stop; {
			relative := bytes.Index(source[cursor:stop], []byte("http"))
			if relative < 0 {
				break
			}
			candidate := cursor + relative
			cursor = candidate + 4
			if candidate <= node.Segment.Start {
				continue
			}
			previous, _ := utf8.DecodeLastRune(source[node.Segment.Start:candidate])
			if !strings.ContainsRune("：、。，；！？（）「」『』【】《》〈〉", previous) {
				continue
			}
			part := text.NewReader(source[:stop])
			part.Advance(candidate)
			link := p.Parse(ast.NewParagraph(), part, parser.NewContext())
			if link == nil {
				continue
			}
			_, position := part.Position()
			parent := node.Parent()
			parent.InsertBefore(parent, node, ast.NewTextSegment(text.NewSegment(node.Segment.Start, candidate)))
			parent.InsertBefore(parent, node, link)
			node.Segment.Start = position.Start
			cursor = position.Start
		}
	}
}
