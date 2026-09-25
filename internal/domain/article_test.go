package domain

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
)

const (
	articleTestMediaOne = "01ARZ3NDEKTSV4RRFFQ69G5FB1"
	articleTestMediaTwo = "01ARZ3NDEKTSV4RRFFQ69G5FB2"
)

func articleTestOptions() ArticleRenderOptions {
	return ArticleRenderOptions{MediaURL: func(mediaID ID) string { return "/media/" + mediaID.String() }}
}

func mustParseArticle(t *testing.T, body string) ArticleDocument {
	t.Helper()
	document, err := ParseArticleDocument([]byte(body))
	if err != nil {
		t.Fatalf("parse: %v\n%s", err, body)
	}
	return document
}

func articleBody(content string) string {
	return `{"schema_version":1,"type":"doc","content":[` + content + `]}`
}

func articleParagraph(text string) string {
	return `{"type":"paragraph","content":[{"type":"text","text":` + quoteJSON(text) + `}]}`
}

func quoteJSON(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

func TestArticleDocumentRendersEveryNodeInTheGrammar(t *testing.T) {
	t.Parallel()
	body := articleBody(strings.Join([]string{
		`{"type":"heading","attrs":{"level":2},"content":[{"type":"text","text":"标题"}]}`,
		`{"type":"paragraph","content":[` +
			`{"type":"text","marks":[{"type":"bold"}],"text":"粗"},` +
			`{"type":"text","marks":[{"type":"italic"}],"text":"斜"},` +
			`{"type":"text","marks":[{"type":"strike"}],"text":"删"},` +
			`{"type":"text","marks":[{"type":"underline"}],"text":"下"},` +
			`{"type":"text","marks":[{"type":"code"}],"text":"码"},` +
			`{"type":"hardBreak"},` +
			`{"type":"text","marks":[{"type":"link","attrs":{"href":"https://example.com/x"}}],"text":"链接"}]}`,
		`{"type":"blockquote","content":[` + articleParagraph("引用") + `]}`,
		`{"type":"bulletList","content":[{"type":"listItem","content":[` + articleParagraph("一") + `]}]}`,
		`{"type":"orderedList","attrs":{"start":3},"content":[{"type":"listItem","content":[` + articleParagraph("三") + `]}]}`,
		`{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"fmt.Println(1) // <b>"}]}`,
		`{"type":"horizontalRule"}`,
		`{"type":"table","content":[` +
			`{"type":"tableRow","content":[{"type":"tableHeader","content":[` + articleParagraph("城市") + `]},{"type":"tableHeader","content":[` + articleParagraph("温度") + `]}]},` +
			`{"type":"tableRow","content":[{"type":"tableCell","attrs":{"colspan":2},"content":[` + articleParagraph("上海 30") + `]}]}]}`,
		`{"type":"image","attrs":{"media_id":"` + articleTestMediaOne + `","alt":"一张图"}}`,
	}, ","))

	html, err := RenderArticleHTML(mustParseArticle(t, body), articleTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	for _, fragment := range []string{
		"<h2>标题</h2>",
		"<strong>粗</strong>", "<em>斜</em>", "<del>删</del>", "<u>下</u>", "<code>码</code>",
		"<br>",
		`<a rel="nofollow noopener noreferrer" href="https://example.com/x">链接</a>`,
		"<blockquote><p>引用</p></blockquote>",
		"<ul><li><p>一</p></li></ul>",
		`<ol start="3"><li><p>三</p></li></ol>`,
		`<pre><code class="language-go">fmt.Println(1) // &lt;b&gt;</code></pre>`,
		"<hr>",
		"<table><thead><tr><th><p>城市</p></th><th><p>温度</p></th></tr></thead>",
		`<tbody><tr><td colspan="2"><p>上海 30</p></td></tr></tbody></table>`,
		`<img src="/media/` + articleTestMediaOne + `" alt="一张图" loading="lazy">`,
	} {
		if !strings.Contains(html, fragment) {
			t.Fatalf("missing %s in\n%s", fragment, html)
		}
	}
}

func TestArticleDocumentRefusesAnythingOutsideTheGrammar(t *testing.T) {
	t.Parallel()
	for name, body := range map[string]string{
		"an unknown node":            articleBody(`{"type":"callout","content":[]}`),
		"an unknown attribute":       articleBody(`{"type":"paragraph","attrs":{"align":"center"},"content":[]}`),
		"an unknown node field":      articleBody(`{"type":"paragraph","style":"x","content":[]}`),
		"an unknown mark":            articleBody(`{"type":"paragraph","content":[{"type":"text","marks":[{"type":"highlight"}],"text":"x"}]}`),
		"a mark on a block":          articleBody(`{"type":"paragraph","marks":[{"type":"bold"}],"content":[]}`),
		"a marked code line":         articleBody(`{"type":"codeBlock","content":[{"type":"text","marks":[{"type":"bold"}],"text":"x"}]}`),
		"a heading without level":    articleBody(`{"type":"heading","content":[{"type":"text","text":"x"}]}`),
		"a heading past three":       articleBody(`{"type":"heading","attrs":{"level":4},"content":[{"type":"text","text":"x"}]}`),
		"a list item outside a list": articleBody(`{"type":"listItem","content":[` + articleParagraph("x") + `]}`),
		"a cell outside a row":       articleBody(`{"type":"tableCell","content":[` + articleParagraph("x") + `]}`),
		"an empty text node":         articleBody(`{"type":"paragraph","content":[{"type":"text","text":""}]}`),
		"text on a block":            articleBody(`{"type":"paragraph","text":"x","content":[]}`),
		"an image with no media":     articleBody(`{"type":"image","attrs":{"alt":"x"}}`),
		"an image with a bad id":     articleBody(`{"type":"image","attrs":{"media_id":"not-a-ulid"}}`),
		"a javascript link":          articleBody(`{"type":"paragraph","content":[{"type":"text","marks":[{"type":"link","attrs":{"href":"javascript:alert(1)"}}],"text":"x"}]}`),
		"a data link":                articleBody(`{"type":"paragraph","content":[{"type":"text","marks":[{"type":"link","attrs":{"href":"data:text/html,x"}}],"text":"x"}]}`),
		"a duplicated mark":          articleBody(`{"type":"paragraph","content":[{"type":"text","marks":[{"type":"bold"},{"type":"bold"}],"text":"x"}]}`),
		"an empty blockquote":        articleBody(`{"type":"blockquote","content":[]}`),
		"a missing schema version":   `{"type":"doc","content":[` + articleParagraph("x") + `]}`,
		"a future schema version":    `{"schema_version":99,"type":"doc","content":[` + articleParagraph("x") + `]}`,
		"a document that is not one": `{"schema_version":1,"type":"paragraph","content":[]}`,
		"trailing data":              articleBody(articleParagraph("x")) + `{"type":"doc"}`,
		"nothing at all":             articleBody(""),
		"only empty paragraphs":      articleBody(`{"type":"paragraph","content":[]},{"type":"paragraph","content":[]}`),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseArticleDocument([]byte(body)); err == nil {
				t.Fatalf("%s must be refused", name)
			}
		})
	}
}

func TestArticleDocumentNamesThePathThatBrokeTheRule(t *testing.T) {
	t.Parallel()
	body := articleBody(articleParagraph("好的") + `,{"type":"blockquote","content":[{"type":"callout","content":[]}]}`)
	_, err := ParseArticleDocument([]byte(body))
	if err == nil {
		t.Fatal("expected a refusal")
	}
	var domainErr *Error
	if !errors.As(err, &domainErr) {
		t.Fatalf("expected a domain error, got %T", err)
	}
	if path, _ := domainErr.Details["path"].(string); path != "content[1].content[0]" {
		t.Fatalf("path = %v, want content[1].content[0]", domainErr.Details["path"])
	}
}

func TestArticleDocumentHoldsItsLimits(t *testing.T) {
	t.Parallel()
	images := make([]string, 0, MaxArticleInlineImages+1)
	for range MaxArticleInlineImages + 1 {
		images = append(images, `{"type":"image","attrs":{"media_id":"`+articleTestMediaOne+`"}}`)
	}
	nested := articleParagraph("深")
	for range MaxArticleDepth + 1 {
		nested = `{"type":"blockquote","content":[` + nested + `]}`
	}
	cells := make([]string, 0, MaxArticleTableColumns+1)
	for range MaxArticleTableColumns + 1 {
		cells = append(cells, `{"type":"tableCell","content":[`+articleParagraph("x")+`]}`)
	}

	for name, body := range map[string]string{
		"too many pictures": articleBody(strings.Join(images, ",")),
		"too deep":          articleBody(nested),
		"too wide a table":  articleBody(`{"type":"table","content":[{"type":"tableRow","content":[` + strings.Join(cells, ",") + `]}]}`),
		"too much text":     articleBody(articleParagraph(strings.Repeat("字", MaxArticleVisibleCodePoints+1))),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			if _, err := ParseArticleDocument([]byte(body)); err == nil {
				t.Fatalf("%s must be refused", name)
			}
		})
	}

	oversize := articleBody(articleParagraph(strings.Repeat("a", MaxArticleBodyBytes)))
	if _, err := ParseArticleDocument([]byte(oversize)); err == nil {
		t.Fatal("an oversized body must be refused before it is parsed")
	}
}

// A picture alone is an article; an empty shell is not.
func TestArticleDocumentAcceptsAPictureWithoutProse(t *testing.T) {
	t.Parallel()
	document := mustParseArticle(t, articleBody(`{"type":"image","attrs":{"media_id":"`+articleTestMediaOne+`"}}`))
	if ArticleVisibleText(document) != "" {
		t.Fatalf("a picture contributes no prose, got %q", ArticleVisibleText(document))
	}
	if ArticleExcerpt(document) != "" {
		t.Fatal("an excerpt made of nothing should be empty")
	}
}

func TestArticleTextDerivesProseExcerptAndSearchText(t *testing.T) {
	t.Parallel()
	document := mustParseArticle(t, articleBody(strings.Join([]string{
		`{"type":"heading","attrs":{"level":1},"content":[{"type":"text","text":"开头"}]}`,
		articleParagraph("第一段  有   多余空白"),
		`{"type":"image","attrs":{"media_id":"` + articleTestMediaOne + `","alt":"不该进正文的描述"}}`,
		articleParagraph("第二段"),
	}, ",")))

	visible := ArticleVisibleText(document)
	if visible != "开头 第一段 有 多余空白 第二段" {
		t.Fatalf("ArticleVisibleText() = %q", visible)
	}
	if strings.Contains(visible, "描述") {
		t.Fatal("a picture description is not the article's prose")
	}

	long := mustParseArticle(t, articleBody(articleParagraph(strings.Repeat("字", 300))))
	excerpt := ArticleExcerpt(long)
	if count := len([]rune(excerpt)); count != ArticleExcerptCodePoints {
		t.Fatalf("excerpt is %d code points, want %d", count, ArticleExcerptCodePoints)
	}
	if strings.HasSuffix(excerpt, "…") {
		t.Fatal("the excerpt is a prefix, not a rendering decision")
	}

	search := ArticleSearchText("Hello Ｗorld", document)
	if !strings.Contains(search, "hello world") {
		t.Fatalf("the title must be searchable and folded, got %q", search)
	}
	if !strings.Contains(search, "第二段") {
		t.Fatalf("the body must be searchable, got %q", search)
	}
}

func TestArticleRenderingEscapesEverythingTheAuthorTyped(t *testing.T) {
	t.Parallel()
	document := mustParseArticle(t, articleBody(strings.Join([]string{
		articleParagraph(`<script>alert("x")</script>`),
		`{"type":"image","attrs":{"media_id":"` + articleTestMediaOne + `","alt":"\" onerror=alert(1) x=\""}}`,
		`{"type":"paragraph","content":[{"type":"text","marks":[{"type":"link","attrs":{"href":"https://example.com/?a=1&b=\"2\""}}],"text":"x"}]}`,
	}, ",")))

	html, err := RenderArticleHTML(document, articleTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<script>") {
		t.Fatalf("author text reached the page as markup:\n%s", html)
	}
	// The breakout would be a raw quote closing alt= early; the escaped text
	// staying inside the attribute is the correct outcome.
	if strings.Contains(html, `" onerror=`) {
		t.Fatalf("a description escaped its attribute:\n%s", html)
	}
	if !strings.Contains(html, `alt="&#34; onerror=alert(1) x=&#34;"`) {
		t.Fatalf("expected the description escaped in place:\n%s", html)
	}
	if !strings.Contains(html, `href="https://example.com/?a=1&amp;b=&#34;2&#34;"`) {
		t.Fatalf("expected the destination escaped in place:\n%s", html)
	}
	if !strings.Contains(html, "&lt;script&gt;") {
		t.Fatalf("expected escaped text, got\n%s", html)
	}
}

func TestArticleRenderingNeedsAWayToAddressMedia(t *testing.T) {
	t.Parallel()
	document := mustParseArticle(t, articleBody(articleParagraph("x")))
	if _, err := RenderArticleHTML(document, ArticleRenderOptions{}); err == nil {
		t.Fatal("rendering without a media address must fail rather than emit a broken src")
	}
}

func TestArticleProjectsItsPicturesInDocumentOrder(t *testing.T) {
	t.Parallel()
	document := mustParseArticle(t, articleBody(strings.Join([]string{
		`{"type":"image","attrs":{"media_id":"` + articleTestMediaTwo + `","alt":"第一张"}}`,
		`{"type":"blockquote","content":[` + articleParagraph("中间") + `]}`,
		`{"type":"image","attrs":{"media_id":"` + articleTestMediaOne + `"}}`,
		`{"type":"image","attrs":{"media_id":"` + articleTestMediaTwo + `","alt":"重复用一张图"}}`,
	}, ",")))

	article, err := NewArticle("  标题  ", document, nil)
	if err != nil {
		t.Fatal(err)
	}
	if article.Title != "标题" {
		t.Fatalf("the stored title is the trimmed one, got %q", article.Title)
	}

	usages := article.InlineMedia()
	if len(usages) != 3 {
		t.Fatalf("expected three usages, got %d", len(usages))
	}
	for index, want := range []struct {
		media string
		alt   *string
	}{
		{articleTestMediaTwo, stringPointer("第一张")},
		{articleTestMediaOne, nil},
		{articleTestMediaTwo, stringPointer("重复用一张图")},
	} {
		usage := usages[index]
		if usage.MediaID.String() != want.media || usage.Position != index {
			t.Fatalf("usage %d = %s at %d", index, usage.MediaID, usage.Position)
		}
		switch {
		case want.alt == nil && usage.AltText != nil:
			t.Fatalf("usage %d should carry no description, got %q", index, *usage.AltText)
		case want.alt != nil && (usage.AltText == nil || *usage.AltText != *want.alt):
			t.Fatalf("usage %d description = %v, want %q", index, usage.AltText, *want.alt)
		}
	}
	if err := ValidateEntryMediaUsages(usages[:1]); err != nil {
		t.Fatalf("the projection must satisfy the usage contract: %v", err)
	}
}

func TestArticleRefusesTitlesOutsideTheContract(t *testing.T) {
	t.Parallel()
	document := mustParseArticle(t, articleBody(articleParagraph("x")))
	for name, title := range map[string]string{
		"an empty title": "   ",
		"a long title":   strings.Repeat("字", MaxArticleTitleCodePoints+1),
	} {
		if _, err := NewArticle(title, document, nil); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}
	bad := ID("not-a-ulid")
	if _, err := NewArticle("标题", document, &bad); err == nil {
		t.Fatal("a cover must name managed media")
	}
}

func TestArticleCoverAndInlineMediaListsEverythingItTouches(t *testing.T) {
	t.Parallel()
	document := mustParseArticle(t, articleBody(`{"type":"image","attrs":{"media_id":"`+articleTestMediaOne+`"}}`))
	cover := ID(articleTestMediaTwo)
	article, err := NewArticle("标题", document, &cover)
	if err != nil {
		t.Fatal(err)
	}
	ids := article.CoverAndInlineMedia()
	if len(ids) != 2 || ids[0] != cover || ids[1].String() != articleTestMediaOne {
		t.Fatalf("CoverAndInlineMedia() = %v", ids)
	}
}

func stringPointer(value string) *string { return &value }

// Storing the canonical serialization rather than the bytes that arrived only
// works if the two are the same document.
func TestArticleDocumentSurvivesASerializationRoundTrip(t *testing.T) {
	t.Parallel()
	body := articleBody(strings.Join([]string{
		`{"type":"heading","attrs":{"level":3},"content":[{"type":"text","text":"标题"}]}`,
		`{"type":"paragraph","content":[{"type":"text","marks":[{"type":"bold"},{"type":"link","attrs":{"href":"https://example.com/x"}}],"text":"粗链接"},{"type":"hardBreak"}]}`,
		`{"type":"orderedList","attrs":{"start":7},"content":[{"type":"listItem","content":[` + articleParagraph("七") + `]}]}`,
		`{"type":"bulletList","content":[{"type":"listItem","content":[` + articleParagraph("点") + `]}]}`,
		`{"type":"codeBlock","attrs":{"language":"rust"},"content":[{"type":"text","text":"fn main() {}"}]}`,
		`{"type":"blockquote","content":[` + articleParagraph("引") + `]}`,
		`{"type":"horizontalRule"}`,
		`{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableHeader","attrs":{"colspan":2},"content":[` + articleParagraph("头") + `]}]},{"type":"tableRow","content":[{"type":"tableCell","content":[` + articleParagraph("格") + `]},{"type":"tableCell","content":[` + articleParagraph("格") + `]}]}]}`,
		`{"type":"image","attrs":{"media_id":"` + articleTestMediaOne + `","alt":"描述"}}`,
		`{"type":"image","attrs":{"media_id":"` + articleTestMediaTwo + `"}}`,
	}, ","))

	first := mustParseArticle(t, body)
	encoded, err := MarshalArticleDocument(first)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ParseArticleDocument(encoded)
	if err != nil {
		t.Fatalf("the canonical form must parse: %v\n%s", err, encoded)
	}
	if !reflect.DeepEqual(first, second) {
		t.Fatalf("round trip changed the document:\nfirst  %+v\nsecond %+v", first, second)
	}
	again, err := MarshalArticleDocument(second)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(encoded) {
		t.Fatalf("serialization is not stable:\n%s\n%s", encoded, again)
	}

	// And the rendered page is the same either way.
	firstHTML, err := RenderArticleHTML(first, articleTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	secondHTML, err := RenderArticleHTML(second, articleTestOptions())
	if err != nil {
		t.Fatal(err)
	}
	if firstHTML != secondHTML {
		t.Fatalf("round trip changed the page:\n%s\n%s", firstHTML, secondHTML)
	}
}

// An excerpt is a sentence about the article. Code and table cells are visible
// text and belong in the index, but a card subtitle reading
// "项目 数量 驿马 三百匹 func main() {" tells a reader nothing.
func TestArticleExcerptIsProseAndTheIndexIsEverything(t *testing.T) {
	t.Parallel()
	document := mustParseArticle(t, articleBody(strings.Join([]string{
		articleParagraph("开头的一段散文"),
		`{"type":"table","content":[{"type":"tableRow","content":[{"type":"tableCell","content":[` + articleParagraph("驿马") + `]},{"type":"tableCell","content":[` + articleParagraph("三百匹") + `]}]}]}`,
		`{"type":"codeBlock","attrs":{"language":"go"},"content":[{"type":"text","text":"func main() {}"}]}`,
		articleParagraph("结尾的一段散文"),
	}, ",")))

	excerpt := ArticleExcerpt(document)
	if excerpt != "开头的一段散文 结尾的一段散文" {
		t.Fatalf("excerpt = %q", excerpt)
	}
	// The same words still have to be findable.
	search := ArticleSearchText("标题", document)
	for _, term := range []string{"驿马", "三百匹", "func main"} {
		if !strings.Contains(search, strings.ToLower(term)) {
			t.Fatalf("search text lost %q: %q", term, search)
		}
	}
}
