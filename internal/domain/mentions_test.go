package domain

import (
	"reflect"
	"testing"
)

func TestMentionsFollowVisibleProseBoundaries(t *testing.T) {
	source := "@Cedar @cedar 中文@maple @a @ab @bad-name you@cedar.test @" + "abcdefghijklmnopqrstuvwxyzaaaaaa" + "\n" +
		"`@hidden` [@linked](https://example.com) https://example.com/@pathuser\n\n```go\n@fenced\n```\n\n**@birch** @account @elm_2 &commat;willow"
	want := []string{"cedar", "maple", "birch", "elm_2", "willow"}
	if got := MarkdownMentions(source); !reflect.DeepEqual(got, want) {
		t.Fatalf("mentions = %#v, want %#v", got, want)
	}
}

func TestArticleMentionsIgnoreCodeLinksAndAttributes(t *testing.T) {
	doc := ArticleDocument{Content: []ArticleNode{
		{Type: ArticleNodeParagraph, Content: []ArticleNode{{Type: ArticleNodeText, Text: "@cedar @cedar"}, {Type: ArticleNodeText, Text: "@linked", Marks: []ArticleMark{{Type: ArticleMarkLink}}}, {Type: ArticleNodeText, Text: "@inline", Marks: []ArticleMark{{Type: ArticleMarkCode}}}}},
		{Type: ArticleNodeCodeBlock, Content: []ArticleNode{{Type: ArticleNodeText, Text: "@fenced"}}},
		{Type: ArticleNodeImage, Alt: "@altuser"},
	}}
	if got := ArticleMentions(doc); !reflect.DeepEqual(got, []string{"cedar"}) {
		t.Fatalf("mentions = %#v", got)
	}
}
