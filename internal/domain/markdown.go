package domain

import (
	"bytes"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/parser"
	goldmarkhtml "github.com/yuin/goldmark/renderer/html"
	"github.com/yuin/goldmark/text"
)

// MaxContentCodePoints is the v0 Unicode code-point limit for an Entry body.
const MaxContentCodePoints = 20_000

// MaxCommentContentCodePoints is the v0 Unicode code-point limit for a
// comment. Comments use the same safe Markdown grammar as posts.
const MaxCommentContentCodePoints = 2_000

var markdownEngine = goldmark.New(
	goldmark.WithParserOptions(parser.WithAutoHeadingID()),
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

func renderValidatedMarkdown(source string) (string, error) {
	var output bytes.Buffer
	if err := markdownEngine.Convert([]byte(source), &output); err != nil {
		return "", NewError(CodeInternal, "could not render content", nil)
	}
	html := bytes.ReplaceAll(output.Bytes(), []byte("<a href="), []byte(`<a rel="nofollow noopener noreferrer" href=`))
	return string(html), nil
}
