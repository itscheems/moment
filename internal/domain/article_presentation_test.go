package domain

import (
	"reflect"
	"strings"
	"testing"
)

func TestNativeArticlePresentationSurvivesSaveAndRender(t *testing.T) {
	body := `{"schema_version":2,"type":"doc","content":[
 {"type":"tocNode","attrs":{"maxShowCount":10}},
 {"type":"heading","attrs":{"level":1,"id":"chapter-a","data-toc-id":"chapter-a"},"content":[{"type":"text","text":"章 <一>"}]},
 {"type":"heading","attrs":{"level":3},"content":[{"type":"text","text":"小节"}]},
 {"type":"paragraph","attrs":{"textAlign":"center","backgroundColor":"var(--tt-bg-color-yellow)"},"content":[{"type":"text","text":"彩色","marks":[{"type":"textStyle","attrs":{"color":"#cc3300"}},{"type":"highlight","attrs":{"color":"#ffff00"}},{"type":"superscript"}]}]},
 {"type":"taskList","content":[{"type":"taskItem","attrs":{"checked":true},"content":[{"type":"paragraph","content":[{"type":"text","text":"已完成"}]}]}]},
 {"type":"image","attrs":{"media_id":"` + articleTestMediaOne + `","width":320,"height":180,"data-align":"center","showCaption":true},"content":[{"type":"text","text":"图注"}]},
 {"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","attrs":{"colwidth":[240],"backgroundColor":"#eff1f5","nodeTextAlign":"right","nodeVerticalAlign":"middle"},"content":[{"type":"paragraph","content":[{"type":"text","text":"单元格"}]}]}]}]}
 ]}`
	first := mustParseArticle(t, body)
	encoded, err := MarshalArticleDocument(first)
	if err != nil {
		t.Fatal(err)
	}
	reopened := mustParseArticle(t, string(encoded))
	if !reflect.DeepEqual(first, reopened) {
		t.Fatalf("presentation lost after save: %s", encoded)
	}
	output, err := RenderArticleHTML(reopened, articleTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{`href="#chapter-a"`, `id="article-heading-1"`, `data-depth="2"`, `章 &lt;一&gt;`, `text-align:center`, `background-color:var(--tt-bg-color-yellow)`, `color:#cc3300`, `background-color:#ffff00`, `<sup>`, `type="checkbox" disabled checked`, `data-align="center"`, `width="320" height="180"`, `<figcaption>图注</figcaption>`, `vertical-align:middle`, `width:240px`} {
		if !strings.Contains(output, fragment) {
			t.Fatalf("missing %s in %s", fragment, output)
		}
	}
	// Generating fallback anchors for delivery never mutates stored documents.
	after, _ := MarshalArticleDocument(reopened)
	if string(encoded) != string(after) {
		t.Fatal("render mutated article")
	}
	// Native table cells use block+, including headings and quotations.
	mustParseArticle(t, articleBody(`{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","content":[{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"单元格标题"}]},{"type":"blockquote","content":[`+articleParagraph("单元格引用")+`]}]}]}]}`))
}

func TestNativePresentationRefusesInvalidValues(t *testing.T) {
	for name, attrs := range map[string]string{
		"CSS declaration injection": `{"backgroundColor":"red;position:fixed"}`,
		"invalid alignment":         `{"textAlign":"diagonal"}`,
		"unsafe anchor":             `{"id":"x\" onclick=\"alert(1)"}`,
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseArticleDocument([]byte(articleBody(`{"type":"paragraph","attrs":` + attrs + `,"content":[{"type":"text","text":"value"}]}`)))
			if err == nil {
				t.Fatal("accepted invalid presentation")
			}
		})
	}
}
