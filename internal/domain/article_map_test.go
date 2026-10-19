package domain

import (
	"bytes"
	"fmt"
	"strings"
	"testing"
)

const articleMapJSON = `{"places":[{"name":"銀座 <trip>","display_name":"東京都中央区","latitude":35.6712345,"longitude":139.7654321,"coordinate_system":"wgs84"},{"name":"静安寺","display_name":"上海","latitude":31.2231861,"longitude":121.4450667,"coordinate_system":"gcj02"}],"connect_points":true}`

func TestArticleMapSnapshotGrammarRenderingAndRoundtrip(t *testing.T) {
	source := []byte(`{"schema_version":3,"type":"doc","content":[{"type":"mapCard","attrs":{"id":"map-1","map_card":` + articleMapJSON + `}}]}`)
	body, err := ParseArticleDocument(source)
	if err != nil {
		t.Fatal(err)
	}
	mapCard := body.Content[0].MapCard
	if mapCard.Places[0].Point.LatitudeE7 != 356712345 || mapCard.Places[1].Point.System != CoordinateGCJ02 {
		t.Fatalf("map = %#v", mapCard)
	}
	canonical, err := MarshalArticleDocument(body)
	if err != nil {
		t.Fatal(err)
	}
	reloaded, err := ParseArticleDocument(canonical)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(ArticleSearchText("旅程", reloaded), "静安寺") || !strings.Contains(ArticlePreviewText(reloaded), "銀座") {
		t.Fatal("map missing from search/summary")
	}
	html, err := RenderArticleHTML(reloaded, ArticleRenderOptions{MediaURL: func(id ID) string { return "/media/" + id.String() }})
	if err != nil || !strings.Contains(html, "&lt;trip&gt;") || strings.Contains(html, "<trip>") {
		t.Fatalf("html=%s err=%v", html, err)
	}
	for _, version := range []int{1, 2} {
		old := bytes.Replace(source, []byte(`"schema_version":3`), []byte(fmt.Sprintf(`"schema_version":%d`, version)), 1)
		if _, err := ParseArticleDraftDocument(old); err == nil {
			t.Fatalf("schema %d accepted map", version)
		}
	}
	duplicate := bytes.Replace(source, []byte(`"latitude":35.6712345`), []byte(`"latitude":35.6712345,"latitude":35`), 1)
	if _, err := ParseArticleDocument(duplicate); err == nil {
		t.Fatal("duplicate nested map coordinate accepted")
	}
	invalid := bytes.Replace(source, []byte(`"coordinate_system":"gcj02"`), []byte(`"provider":"amap","coordinate_system":"gcj02"`), 1)
	if _, err := ParseArticleDocument(invalid); err == nil {
		t.Fatal("accepted provider metadata in public map")
	}
}
