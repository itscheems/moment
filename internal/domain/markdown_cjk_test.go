package domain

import (
	"strings"
	"testing"
)

func TestCJKPunctuationLinksUseSafeMarkdownGrammar(t *testing.T) {
	for _, mark := range "：、。，；！？（）「」『』【】《》〈〉" {
		source := "看这个" + string(mark) + "https://example.com/a。"
		rendered, err := RenderMarkdown(source)
		if err != nil || !strings.Contains(rendered, `href="https://example.com/a"`) || !strings.Contains(rendered, `</a>。`) {
			t.Fatalf("%q: %q %v", source, rendered, err)
		}
	}
	for _, source := range []string{"`看：https://example.com/a`", "```\n看：https://example.com/a\n```", "[看：https://example.com/a](https://example.org)", "看：javascript:alert(1)", "看：www.example.com"} {
		rendered, err := RenderMarkdown(source)
		if err != nil || strings.Contains(rendered, `href="https://example.com/a"`) {
			t.Fatalf("%q: %q %v", source, rendered, err)
		}
	}
	rendered, err := RenderCommentMarkdown("评论：https://example.com/a")
	if err != nil || !strings.Contains(rendered, `href="https://example.com/a"`) {
		t.Fatalf("comment: %q %v", rendered, err)
	}
}
