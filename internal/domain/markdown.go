package domain

import (
	"bytes"
	"net/url"
	"strings"
	"unicode/utf8"

	"regexp"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
	"github.com/yuin/goldmark/util"
)

// MaxContentCodePoints is the v0 Unicode code-point limit for an Entry body.
const MaxContentCodePoints = 20_000

// MaxCommentContentCodePoints is the v0 Unicode code-point limit for a
// comment. Comments use the same safe Markdown grammar as posts.
const MaxCommentContentCodePoints = 2_000

// markdownEngine linkifies bare URLs so a pasted link is clickable without
// Markdown syntax. Only URLs carrying an explicit scheme are linkified: a
// bare "www." string or an email address would otherwise turn ordinary prose
// into links. Every generated href still passes the protocol allowlist below.
var markdownEngine = goldmark.New(
	goldmark.WithParserOptions(parser.WithAutoHeadingID(), parser.WithASTTransformers(
		util.Prioritized(cjkLinkify{extension.NewLinkifyParser(
			extension.WithLinkifyAllowedProtocols([][]byte{[]byte("http:"), []byte("https:")}),
			extension.WithLinkifyEmailRegexp(regexp.MustCompile(`a^`)),
			extension.WithLinkifyWWWRegexp(regexp.MustCompile(`a^`)),
		)}, 999),
	)),
	// Strikethrough and Table are added one by one rather than through
	// extension.GFM: that bundle carries its own Linkify, which would fight
	// the CJK-aware one configured above. Task lists stay out -- the composer
	// has no checkbox control, so enabling them would turn "- [ ] x" in an
	// existing post into a checkbox nobody asked for.
	goldmark.WithExtensions(extension.Strikethrough, extension.Table, extension.NewLinkify(
		extension.WithLinkifyAllowedProtocols([][]byte{[]byte("http:"), []byte("https:")}),
		extension.WithLinkifyEmailRegexp(regexp.MustCompile(`a^`)),
		extension.WithLinkifyWWWRegexp(regexp.MustCompile(`a^`)),
	)),
)

// ValidateMarkdown applies the authoritative Entry Markdown grammar. An Entry
// body may be empty because the Post aggregate can be media-only; callers that
// own the media usages must additionally call ValidatePostMarkdown.
func ValidateMarkdown(source string) error {
	return validateMarkdown(source, MaxContentCodePoints, true)
}

// ValidatePostMarkdown enforces the aggregate rule that a Moment contains
// visible Markdown or at least one managed media usage.
func ValidatePostMarkdown(source string, mediaCount int) error {
	if err := ValidateMarkdown(source); err != nil {
		return err
	}
	if strings.TrimSpace(source) == "" && mediaCount <= 0 {
		return NewError(CodeContentEmpty, "post must contain Markdown or managed media", nil)
	}
	return nil
}

// ValidateCommentMarkdown applies the authoritative comment Markdown policy.
func ValidateCommentMarkdown(source string) error {
	return validateMarkdown(source, MaxCommentContentCodePoints, false)
}

func validateMarkdown(source string, limit int, allowEmpty bool) error {
	if !utf8.ValidString(source) {
		return NewError(CodeInvalidArgument, "content must be valid UTF-8", map[string]any{"field": "content"})
	}
	if !allowEmpty && strings.TrimSpace(source) == "" {
		return NewError(CodeContentEmpty, "content must contain a non-whitespace character", nil)
	}
	if count := utf8.RuneCountInString(source); count > limit {
		return NewError(CodeContentTooLarge, "content exceeds the code-point limit", map[string]any{"limit": limit, "actual": count})
	}

	data := []byte(source)
	document := markdownEngine.Parser().Parse(text.NewReader(data))
	return ast.Walk(document, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering {
			return ast.WalkContinue, nil
		}
		switch typed := node.(type) {
		case *ast.Image:
			return ast.WalkStop, NewError(CodeExternalImageNotAllowed, "Markdown images are not allowed; attach managed media instead", nil)
		case *ast.RawHTML, *ast.HTMLBlock:
			return ast.WalkStop, NewError(CodeInvalidArgument, "raw HTML is not allowed", map[string]any{"field": "content"})
		case *ast.Link:
			if err := validateLink(typed.Destination); err != nil {
				return ast.WalkStop, err
			}
		case *ast.AutoLink:
			if err := validateLink(typed.URL(data)); err != nil {
				return ast.WalkStop, err
			}
		}
		return ast.WalkContinue, nil
	})
}

// RenderCommentMarkdown returns safe HTML after applying the smaller comment
// content budget.
func RenderCommentMarkdown(source string) (string, error) {
	if err := ValidateCommentMarkdown(source); err != nil {
		return "", err
	}
	return renderValidatedMarkdown(source)
}

func validateLink(destination []byte) error {
	value := strings.TrimSpace(string(destination))
	if value == "" {
		return NewError(CodeInvalidArgument, "link destination cannot be empty", map[string]any{"field": "content"})
	}
	if goldmarkhtml.IsDangerousURL(destination) || strings.HasPrefix(value, "//") {
		return NewError(CodeInvalidArgument, "link protocol is not allowed", map[string]any{"field": "content"})
	}
	parsed, err := url.Parse(value)
	if err != nil {
		return NewError(CodeInvalidArgument, "link destination is invalid", map[string]any{"field": "content"})
	}
	if parsed.Scheme == "" {
		return nil
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https", "mailto":
		return nil
	default:
		return NewError(CodeInvalidArgument, "link protocol is not allowed", map[string]any{"field": "content"})
	}
}

// RenderMarkdown returns safe HTML only after applying the save/import policy.
// Goldmark's unsafe HTML mode remains disabled; generated external links gain a
// fixed rel policy without accepting user-authored attributes.
func RenderMarkdown(source string) (string, error) {
	if err := ValidateMarkdown(source); err != nil {
		return "", err
	}
	return renderValidatedMarkdown(source)
}

// RenderMarkdownWithLinkCards renders a body whose trailing bare URL is already
// represented by an attached card.
//
// The body itself is never rewritten: content_md keeps the complete URL, and
// this is purely a presentation transform, so changing the rule later cannot
// damage stored data. Hiding applies only when the URL is a bare autolink
// standing alone at the very end of the body and a card actually attached. A
// labelled link, a URL in mid-sentence, and a URL with no card all render
// normally, because in those cases the text is doing work the card does not
// replace.
func RenderMarkdownWithLinkCards(source string, cards []LinkCard) (string, error) {
	if err := ValidateMarkdown(source); err != nil {
		return "", err
	}
	return renderValidatedMarkdown(hideTrailingCardURL(source, cards))
}

// hideTrailingCardURL removes a trailing bare URL from the source that is about
// to be rendered. It operates on a copy; the caller's authoritative body is
// untouched.
func hideTrailingCardURL(source string, cards []LinkCard) string {
	if len(cards) == 0 {
		return source
	}
	trimmed := strings.TrimRight(source, " \t\r\n")
	for _, card := range cards {
		for _, candidate := range []string{card.SourceURL, card.CanonicalURL} {
			if candidate == "" || !strings.HasSuffix(trimmed, candidate) {
				continue
			}
			head := trimmed[:len(trimmed)-len(candidate)]
			// The URL must stand alone: preceded by nothing, or by a line
			// break. A URL that ends a sentence stays visible because removing
			// it would leave the sentence dangling.
			if head == "" || strings.HasSuffix(head, "\n") {
				return strings.TrimRight(head, " \t\r\n")
			}
		}
	}
	return source
}

func renderValidatedMarkdown(source string) (string, error) {
	var output bytes.Buffer
	if err := markdownEngine.Convert([]byte(source), &output); err != nil {
		return "", NewError(CodeInternal, "could not render content", nil)
	}
	html := bytes.ReplaceAll(output.Bytes(), []byte("<a href="), []byte(`<a rel="nofollow noopener noreferrer" href=`))
	return string(html), nil
}
