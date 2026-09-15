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
	swallowed := map[*ast.Text]bool{}
	for _, node := range nodes {
		if swallowed[node] {
			continue
		}
		for cursor := node.Segment.Start; cursor < node.Segment.Stop; {
			relative := bytes.Index(source[cursor:node.Segment.Stop], []byte("http"))
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
			// The inline parser has already split this text at every "_" it
			// weighed as an emphasis delimiter, and a query string is full of
			// them. Let the URL parser see the whole run of plain text that
			// follows, then take the nodes it swallows; it never crosses a
			// line, and it stops short of anything that is not plain text.
			part := text.NewReader(source[:plainTextRunEnd(node)])
			part.Advance(candidate)
			link := p.Parse(ast.NewParagraph(), part, parser.NewContext())
			if link == nil {
				continue
			}
			_, position := part.Position()
			end := position.Start
			parent := node.Parent()
			parent.InsertBefore(parent, node, ast.NewTextSegment(text.NewSegment(node.Segment.Start, candidate)))
			parent.InsertBefore(parent, node, link)
			for end >= node.Segment.Stop {
				if node.SoftLineBreak() || node.HardLineBreak() {
					// The line's last text carries its break, and the URL
					// parser never crosses a line: an empty text keeps the
					// break where it was.
					node.Segment.Start = node.Segment.Stop
					node = nil
					break
				}
				next, _ := node.NextSibling().(*ast.Text)
				parent.RemoveChild(parent, node)
				node = nil
				if next == nil || end <= next.Segment.Start {
					break
				}
				swallowed[next] = true
				node = next
			}
			if node == nil {
				break
			}
			node.Segment.Start = end
			cursor = end
		}
	}
}

// plainTextRunEnd returns where the run of contiguous plain text starting at
// node stops: the end of the last following sibling that is ordinary text
// picking up exactly where the previous one ended.
func plainTextRunEnd(node *ast.Text) int {
	end := node.Segment.Stop
	for sibling := node.NextSibling(); sibling != nil; sibling = sibling.NextSibling() {
		next, ok := sibling.(*ast.Text)
		if !ok || next.IsRaw() || next.Segment.Start != end {
			break
		}
		end = next.Segment.Stop
	}
	return end
}
