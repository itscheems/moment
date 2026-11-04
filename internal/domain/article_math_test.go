package domain

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

func TestArticleMathRoundtripAndProjections(t *testing.T) {
	source := []byte(`{"schema_version":4,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"Formula: "},{"type":"inlineMath","attrs":{"latex":"a^2+b^2=c^2"}}]},{"type":"blockMath","attrs":{"latex":"\\int_0^1 x^2\\,dx = \\frac{1}{3}"}}]}`)
	body, err := ParseArticleDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	canonical, err := MarshalArticleDocument(body)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := ParseArticleDocument(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if reloaded.Content[1].Latex != `\int_0^1 x^2\,dx = \frac{1}{3}` {
		t.Fatalf("formula changed: %s", canonical)
	}
	if !strings.Contains(ArticleSearchText("", reloaded), "int_0^1") || !strings.Contains(ArticleExcerpt(reloaded), "a^2+b^2=c^2") {
		t.Fatal("formula missing from search or inline prose")
	}
	if strings.Contains(ArticlePreviewText(reloaded), "int_0^1") {
		t.Fatal("display formula displaced the prose preview")
	}
	for version := 1; version < 4; version++ {
		old := bytes.Replace(source, []byte(`"schema_version":4`), []byte(fmt.Sprintf(`"schema_version":%d`, version)), 1)
		if _, err := ParseArticleDraftDocument(old); err == nil {
			t.Fatalf("schema %d accepted math", version)
		}
	}
	standalone, err := ParseArticleDocument([]byte(`{"schema_version":4,"type":"doc","content":[{"type":"blockMath","attrs":{"latex":"x^2"}}]}`))
	if err != nil || ArticleVisibleText(standalone) != "x^2" {
		t.Fatalf("formula-only article: %v", err)
	}
}

func TestArticleMathValidationAndEscapedHTML(t *testing.T) {
	for _, node := range []string{
		`{"type":"inlineMath","attrs":{"latex":"x"}}`,
		`{"type":"paragraph","content":[{"type":"blockMath","attrs":{"latex":"x"}}]}`,
		`{"type":"codeBlock","content":[{"type":"inlineMath","attrs":{"latex":"x"}}]}`,
		`{"type":"blockMath","attrs":{}}`,
		`{"type":"blockMath","attrs":{"latex":4}}`,
		`{"type":"blockMath","attrs":{"latex":"x","onclick":"alert(1)"}}`,
		`{"type":"blockMath","attrs":{"latex":"x"},"content":[{"type":"text","text":"extra"}]}`,
		`{"type":"blockMath","attrs":{"latex":"` + strings.Repeat("x", MaxArticleMathCodePoints+1) + `"}}`,
	} {
		if _, err := ParseArticleDraftDocument([]byte(`{"schema_version":4,"type":"doc","content":[` + node + `]}`)); err == nil {
			t.Fatalf("accepted invalid math: %.100s", node)
		}
	}
	body, err := ParseArticleDocument([]byte(`{"schema_version":4,"type":"doc","content":[{"type":"blockMath","attrs":{"latex":"<img src=x onerror=alert(1)> & \\notACommand"}}]}`))
	if err != nil {
		t.Fatal(err)
	}
	rendered, err := RenderArticleHTML(body, ArticleRenderOptions{MediaURL: func(id ID) string { return "/media/" + id.String() }})
	if err != nil || !strings.Contains(rendered, `data-type="block-math"`) || strings.Contains(rendered, "<img") || !strings.Contains(rendered, "&lt;img") {
		t.Fatalf("unsafe or unreadable math HTML: %q, %v", rendered, err)
	}
}
