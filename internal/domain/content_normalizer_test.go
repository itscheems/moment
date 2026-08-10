package domain

import "testing"

func TestNormalizeContentV2KeepsVisibleTextOnly(t *testing.T) {
	t.Parallel()
	input := "# HéLLo **Ｍｏｍｅｎｔ**\n\n[label](https://example.com/private?q=1) <https://hidden.example/x>\n\n`Code_Value`\n\n```go\nVisibleCode()\n```\nwww.hidden.example \u200b尾巴"
	actual, err := NormalizeContentV2(input)
	if err != nil {
		t.Fatal(err)
	}
	want := "héllo moment label code_value visiblecode() 尾巴"
	if actual != want {
		t.Fatalf("NormalizeContentV2() = %q, want %q", actual, want)
	}
}

func TestNormalizeContentV2IsSharedQuerySafe(t *testing.T) {
	t.Parallel()
	for _, input := range []string{`hello OR "world"*`, `%_\\`, "中文", "Straße", "ＡＢＣ"} {
		first, err := NormalizeContentV2(input)
		if err != nil {
			t.Fatalf("normalize %q: %v", input, err)
		}
		second, err := NormalizeContentV2(first)
		if err != nil {
			t.Fatalf("renormalize %q: %v", input, err)
		}
		if first != second {
			t.Fatalf("normalizer is not idempotent for %q: %q then %q", input, first, second)
		}
	}
}

func TestNormalizeContentV2KeepsAdjacentCJKAndCodeURLs(t *testing.T) {
	t.Parallel()
	input := "https://example.com之后 [https://label.example](https://destination.example) `https://inline.example`\n\n```text\nhttps://fenced.example\n```"
	actual, err := NormalizeContentV2(input)
	if err != nil {
		t.Fatal(err)
	}
	want := "之后 https://label.example https://inline.example https://fenced.example"
	if actual != want {
		t.Fatalf("NormalizeContentV2() = %q, want %q", actual, want)
	}
}

func TestVisibleContentPreservesDisplayCaseWhileRemovingMarkdownDestinations(t *testing.T) {
	t.Parallel()
	actual, err := VisibleContent("# HéLLo **Ｍｏｍｅｎｔ** [Label](https://private.example) <https://hidden.example>")
	if err != nil {
		t.Fatal(err)
	}
	if actual != "HéLLo Moment Label" {
		t.Fatalf("VisibleContent() = %q", actual)
	}
}
