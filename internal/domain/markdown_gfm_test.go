package domain

import (
	"strings"
	"testing"
)

// The Markdown grammar gained strikethrough and tables. These pin both what
// readers now see and what the search index now stores, because the same
// stored bodies used to render as literal tildes and pipes.

func TestStrikethroughRendersAndIndexesWithoutDelimiters(t *testing.T) {
	t.Parallel()
	const source = "这个 ~~不要了~~ 换成新的"
	if err := ValidateMarkdown(source); err != nil {
		t.Fatalf("validate: %v", err)
	}
	html, err := RenderMarkdown(source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "<del>不要了</del>") {
		t.Fatalf("expected a del element, got %s", html)
	}
	normalized, err := NormalizeContentV2(source)
	if err != nil {
		t.Fatal(err)
	}
	if normalized != "这个 不要了 换成新的" {
		t.Fatalf("NormalizeContentV2() = %q", normalized)
	}
}

func TestTableRendersAndIndexesAsCellText(t *testing.T) {
	t.Parallel()
	const source = "| 城市 | 温度 |\n| --- | --- |\n| 上海 | 30 |\n| 北京 | 25 |"
	if err := ValidateMarkdown(source); err != nil {
		t.Fatalf("validate: %v", err)
	}
	html, err := RenderMarkdown(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{"<table>", "<th>城市</th>", "<td>上海</td>", "<td>25</td>"} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("expected %s in %s", fragment, html)
		}
	}
	normalized, err := NormalizeContentV2(source)
	if err != nil {
		t.Fatal(err)
	}
	if normalized != "城市 温度 上海 30 北京 25" {
		t.Fatalf("NormalizeContentV2() = %q", normalized)
	}
}

// A cell is not a hole in the link policy: the validator walks into table rows
// like anywhere else.
func TestTableCellLinksObeyTheProtocolAllowlist(t *testing.T) {
	t.Parallel()
	const dangerous = "| a | b |\n| - | - |\n| [标签](javascript:alert(1)) | x |"
	if err := ValidateMarkdown(dangerous); err == nil {
		t.Fatal("expected a javascript: link inside a table cell to be rejected")
	}
	if _, err := RenderMarkdown(dangerous); err == nil {
		t.Fatal("expected rendering to refuse the same body")
	}

	const safe = "| a | b |\n| - | - |\n| [标签](https://example.com/x) | x |"
	html, err := RenderMarkdown(safe)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !strings.Contains(html, `rel="nofollow noopener noreferrer" href="https://example.com/x"`) {
		t.Fatalf("expected the table link to carry the fixed rel policy, got %s", html)
	}
}

func TestTildesThatWereNeverStrikethroughAreUnchanged(t *testing.T) {
	t.Parallel()
	for source, want := range map[string]string{
		"`~~不是删除线~~`": "~~不是删除线~~",
		"价格 ~100 元":     "价格 ~100 元",
	} {
		normalized, err := NormalizeContentV2(source)
		if err != nil {
			t.Fatalf("normalize %q: %v", source, err)
		}
		if normalized != want {
			t.Fatalf("NormalizeContentV2(%q) = %q, want %q", source, normalized, want)
		}
	}
}

// Task lists stay off on purpose: the composer has no checkbox control, so
// turning an existing "- [ ] x" into one would change posts nobody edited.
func TestTaskListSyntaxStaysLiteral(t *testing.T) {
	t.Parallel()
	html, err := RenderMarkdown("- [ ] todo\n- [x] done")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<input") {
		t.Fatalf("task lists must stay literal text, got %s", html)
	}
	if !strings.Contains(html, "[ ] todo") {
		t.Fatalf("expected the literal marker, got %s", html)
	}
}
