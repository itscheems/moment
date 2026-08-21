package domain

import (
	"strings"
	"testing"
)

func cardFor(url string) []LinkCard {
	return []LinkCard{{SourceURL: url, CanonicalURL: url}}
}

func TestLinkifyMakesBareURLsClickable(t *testing.T) {
	html, err := RenderMarkdown("看看 https://example.com/a 这个")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `<a rel="nofollow noopener noreferrer" href="https://example.com/a"`) {
		t.Fatalf("bare URL was not linkified: %s", html)
	}
}

func TestLinkifyLeavesOrdinaryProseAlone(t *testing.T) {
	// Without an explicit scheme these are prose, not links.
	html, err := RenderMarkdown("联系 someone@example.com 或看 www.example.com")
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "<a ") {
		t.Fatalf("prose was turned into links: %s", html)
	}
}

func TestTrailingBareURLIsHiddenOnlyWhenACardAttached(t *testing.T) {
	const body = "看看这个视频\n\nhttps://example.com/a"
	withCard, err := RenderMarkdownWithLinkCards(body, cardFor("https://example.com/a"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(withCard, "example.com") {
		t.Fatalf("expected the trailing URL to be hidden: %s", withCard)
	}
	if !strings.Contains(withCard, "看看这个视频") {
		t.Fatalf("expected the body text to survive: %s", withCard)
	}
	withoutCard, err := RenderMarkdownWithLinkCards(body, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(withoutCard, `href="https://example.com/a"`) {
		t.Fatalf("expected an un-carded URL to stay visible: %s", withoutCard)
	}
}

func TestURLInMidSentenceStaysVisibleEvenWithACard(t *testing.T) {
	html, err := RenderMarkdownWithLinkCards("看 https://example.com/a 这个视频", cardFor("https://example.com/a"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `href="https://example.com/a"`) {
		t.Fatalf("a mid-sentence URL must stay visible: %s", html)
	}
}

func TestSentenceFinalURLStaysVisible(t *testing.T) {
	// The URL ends the body but does not stand alone; removing it would leave
	// the sentence dangling.
	html, err := RenderMarkdownWithLinkCards("推荐一下 https://example.com/a", cardFor("https://example.com/a"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, `href="https://example.com/a"`) {
		t.Fatalf("a sentence-final URL must stay visible: %s", html)
	}
}

func TestLabelledLinkIsNeverHidden(t *testing.T) {
	html, err := RenderMarkdownWithLinkCards("看看\n\n[标题](https://example.com/a)", cardFor("https://example.com/a"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(html, "标题") || !strings.Contains(html, `href="https://example.com/a"`) {
		t.Fatalf("a labelled link must never be hidden: %s", html)
	}
}

func TestBodyOfOnlyACardURLRendersEmpty(t *testing.T) {
	html, err := RenderMarkdownWithLinkCards("https://example.com/a", cardFor("https://example.com/a"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(html, "example.com") {
		t.Fatalf("expected an empty body: %s", html)
	}
}
