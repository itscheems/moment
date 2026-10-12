package domain

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestArticlePreviewTextPreservesReadingStructure(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, content, want string
	}{
		{"empty", "", ""},
		{"blank paragraph", articleParagraph(" \t\n "), ""},
		{"short", articleParagraph(" first article. "), "first article...."},
		{"paragraphs", articleParagraph("first") + "," + articleParagraph("second"), "first\nsecond..."},
		{"empty paragraph", articleParagraph("first") + `,{"type":"paragraph"},` + articleParagraph("second"), "first\n\nsecond..."},
		{"hard break and marks", `{"type":"paragraph","content":[{"type":"text","text":"multi"},{"type":"text","marks":[{"type":"bold"}],"text":"ple"},{"type":"hardBreak"},{"type":"text","marks":[{"type":"link","attrs":{"href":"https://example.com"}}],"text":"runs"}]}`, "multiple\nruns..."},
		{"containers", `{"type":"blockquote","content":[` + articleParagraph("first") + "," + articleParagraph("second") + `]},{"type":"bulletList","content":[{"type":"listItem","content":[` + articleParagraph("one") + `]},{"type":"listItem","content":[` + articleParagraph("two") + `]}]}`, "first\nsecond\none\ntwo..."},
		{"prose only", articleParagraph("before") + `,{"type":"codeBlock","content":[{"type":"text","text":"code"}]},{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","content":[` + articleParagraph("cell") + `]}]}]},` + articleParagraph("after"), "before\nafter..."},
		{"picture only", `{"type":"image","attrs":{"media_id":"` + articleTestMediaOne + `","alt":"picture description"}}`, ""},
		{"authored spacing", articleParagraph(" first  \tsecond 👨‍👩‍👧‍👦 "), "first  \tsecond 👨‍👩‍👧‍👦..."},
	} {
		t.Run(test.name, func(t *testing.T) {
			document, err := ParseArticleDraftDocument([]byte(articleBody(test.content)))
			if err != nil {
				t.Fatal(err)
			}
			if got := ArticlePreviewText(document); got != test.want {
				t.Fatalf("preview = %q, want %q", got, test.want)
			}
		})
	}
}

func TestArticlePreviewTextBoundsCodePointsWithoutChangingLegacyDerivations(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, text, want string }{
		{"above legacy bound", strings.Repeat("字", 180), strings.Repeat("字", 180) + "..."},
		{"exactly 200", strings.Repeat("字", 200), strings.Repeat("字", 200) + "..."},
		{"trim after prefix", strings.Repeat("A", 199) + " tail", strings.Repeat("A", 199) + "..."},
		{"unicode", "  " + strings.Repeat("🙂字", 101) + "  ", strings.Repeat("🙂字", 100) + "..."},
	} {
		t.Run(test.name, func(t *testing.T) {
			document := mustParseArticle(t, articleBody(articleParagraph(test.text)))
			if got := ArticlePreviewText(document); got != test.want || !utf8.ValidString(got) {
				t.Fatalf("preview = %q, want %q", got, test.want)
			}
			if legacy := ArticleExcerpt(document); utf8.RuneCountInString(legacy) > ArticleExcerptCodePoints || strings.HasSuffix(legacy, "...") {
				t.Fatalf("legacy stored excerpt changed: %q", legacy)
			}
		})
	}
}

// Measure the extra work of a preview read, including parsing the stored body.
// Transports omit the body; requested records are parsed at read time.
func BenchmarkArticlePreviewReadProjection(b *testing.B) {
	for _, test := range []struct{ name, text string }{
		{"typical", strings.Repeat("沿着河流走一段路。", 100)},
		{"visible limit", strings.Repeat("🙂", MaxArticleVisibleCodePoints)},
	} {
		body := []byte(articleBody(articleParagraph(test.text)))
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(body)))
			for b.Loop() {
				document, err := ParseArticleDocument(body)
				if err != nil {
					b.Fatal(err)
				}
				if ArticlePreviewText(document) == "" {
					b.Fatal("lost preview")
				}
			}
		})
	}
}
