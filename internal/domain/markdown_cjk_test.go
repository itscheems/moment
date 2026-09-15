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

// The inline parser splits a text run at every "_" it weighed as emphasis,
// and a query string is full of them. A URL after a CJK mark must still be
// linked to its end, exactly as the same URL after a space is.
func TestCJKPunctuationLinksRunPastEmphasisDelimiters(t *testing.T) {
	long := "https://example.com/very/long/path?utm_source=moment&utm_medium=timeline&session=0123456789abcdef"
	for _, tc := range []struct{ source, href, after string }{
		{"留个链接：" + long, long, ""},
		{"留个链接：" + long + "。然后", long, "。然后"},
		{"看：https://example.com/a_b *强调*", "https://example.com/a_b", " <em>强调</em>"},
		{"看：https://example.com/a_b_", "https://example.com/a_b", "_"},
		{"一：https://a.example/x_y，二：https://b.example/p_q", "https://a.example/x_y", "，二：<a"},
		{"看：https://example.com/a_b\n下一行", "https://example.com/a_b", "\n下一行"},
		{"看：https://example.com/a_b  \n下一行", "https://example.com/a_b", "<br>\n下一行"},
		{"看：https://example.com/a\\_b", "https://example.com/a", "_b"},
		{"看：https://example.com/a_b，再看：https://example.com/c_d。", "https://example.com/c_d", "。"},
	} {
		rendered, err := RenderMarkdown(tc.source)
		if err != nil {
			t.Fatalf("%q: %v", tc.source, err)
		}
		href := strings.ReplaceAll(tc.href, "&", "&amp;")
		want := `<a rel="nofollow noopener noreferrer" href="` + href + `">` + href + `</a>` + tc.after
		if !strings.Contains(rendered, want) {
			t.Fatalf("%q:\n got %q\nwant %q", tc.source, rendered, want)
		}
		if strings.Count(rendered, "<a ") != strings.Count(tc.source, "https://") {
			t.Fatalf("%q: %q", tc.source, rendered)
		}
	}
	spaced, err := RenderMarkdown("留个链接 " + long)
	if err != nil {
		t.Fatal(err)
	}
	marked, err := RenderMarkdown("留个链接：" + long)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimPrefix(spaced, "<p>留个链接 ") != strings.TrimPrefix(marked, "<p>留个链接：") {
		t.Fatalf("space: %q\nmark:  %q", spaced, marked)
	}
}

// Search text drops bare URLs, so a URL after a CJK mark must vanish from it
// whole, not leave the part after an underscore behind; that is what moved
// the normalizer version.
func TestCJKPunctuationLinksLeaveNoTailInSearchText(t *testing.T) {
	actual, err := NormalizeContentV2("留个链接：https://example.com/path?utm_source=moment&utm_medium=timeline 好")
	if err != nil {
		t.Fatal(err)
	}
	if actual != "留个链接: 好" {
		t.Fatalf("NormalizeContentV2() = %q", actual)
	}
}
