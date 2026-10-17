package ginhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const articleDraftBodyJSON = `{"schema_version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"文章正文"}]}]}`

// ownerCall signs an owner request the way every other owner test does.
func ownerCall(fixture postHTTPFixture, method, path, body string) *http.Request {
	request := jsonRequest(method, path, body)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	return request
}

func createDraft(t *testing.T, fixture postHTTPFixture, title string) map[string]any {
	t.Helper()
	response := performRequest(fixture.engine, ownerCall(fixture, http.MethodPost, "/api/v1/owner/articles/drafts",
		`{"title":"`+title+`","body":`+articleDraftBodyJSON+`}`))
	if response.Code != http.StatusCreated {
		t.Fatalf("create draft = %d %s", response.Code, response.Body.String())
	}
	return decodeDataMap(t, response)
}

func TestArticleDraftsAreOwnerOnlyAndRoundTrip(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)

	// Nothing here is public: a draft is by definition not published.
	for _, request := range []*http.Request{
		httptest.NewRequest(http.MethodGet, "/api/v1/owner/articles/drafts", nil),
		httptest.NewRequest(http.MethodPost, "/api/v1/owner/articles/drafts", strings.NewReader(`{"title":"x","body":`+articleDraftBodyJSON+`}`)),
	} {
		if response := performRequest(fixture.engine, request); response.Code != http.StatusUnauthorized {
			t.Fatalf("anonymous access = %d %s", response.Code, response.Body.String())
		}
	}

	draft := createDraft(t, fixture, "草稿标题")
	id, _ := draft["id"].(string)
	if id == "" || draft["title"] != "草稿标题" || draft["published_entry_id"] != nil {
		t.Fatalf("draft = %#v", draft)
	}
	if draft["body"] == nil || draft["body_schema_version"] != float64(1) {
		t.Fatalf("a draft carries its body: %#v", draft)
	}

	listed := performRequest(fixture.engine, ownerCall(fixture, http.MethodGet, "/api/v1/owner/articles/drafts", ""))
	var envelope struct {
		Data []map[string]any `json:"data"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	if len(envelope.Data) != 1 || envelope.Data[0]["id"] != id {
		t.Fatalf("listed = %#v", envelope.Data)
	}
}

// A save carries the revision it was based on, and one that is behind is
// refused rather than allowed to erase the other tab's words.
func TestReplacingADraftRefusesAStaleRevision(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	draft := createDraft(t, fixture, "第一版")
	id, _ := draft["id"].(string)
	stale, _ := draft["updated_at"].(string)

	first := performRequest(fixture.engine, ownerCall(fixture, http.MethodPut, "/api/v1/owner/articles/drafts/"+id,
		`{"title":"第二版","body":`+articleDraftBodyJSON+`,"expected_updated_at":"`+stale+`"}`))
	if first.Code != http.StatusOK {
		t.Fatalf("current write = %d %s", first.Code, first.Body.String())
	}
	second := performRequest(fixture.engine, ownerCall(fixture, http.MethodPut, "/api/v1/owner/articles/drafts/"+id,
		`{"title":"另一个页签","body":`+articleDraftBodyJSON+`,"expected_updated_at":"`+stale+`"}`))
	if second.Code != http.StatusConflict {
		t.Fatalf("stale write = %d %s", second.Code, second.Body.String())
	}
	if code := decodeError(t, second.Body).Error.Code; code != "ARTICLE_DRAFT_STALE" {
		t.Fatalf("error code = %s", code)
	}
}

func TestPublishingADraftPutsAnArticleOnTheTimeline(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	draft := createDraft(t, fixture, "发布的文章")
	id, _ := draft["id"].(string)

	published := performRequest(fixture.engine, ownerCall(fixture, http.MethodPost, "/api/v1/owner/articles/drafts/"+id+"/publish", ""))
	if published.Code != http.StatusCreated {
		t.Fatalf("publish = %d %s", published.Code, published.Body.String())
	}
	post := decodeDataMap(t, published)
	postID, _ := post["id"].(string)
	if post["kind"] != "article" || post["content_md"] != nil {
		t.Fatalf("published post = %#v", post)
	}
	article, ok := post["article"].(map[string]any)
	if !ok || article["title"] != "发布的文章" {
		t.Fatalf("article = %#v", post["article"])
	}
	// A detail read carries the body and the rendering together.
	if article["body"] == nil || post["content_html"] == "" {
		t.Fatalf("a published article carries its body and rendering: %#v", post)
	}

	// The draft is consumed.
	if gone := performRequest(fixture.engine, ownerCall(fixture, http.MethodGet, "/api/v1/owner/articles/drafts/"+id, "")); gone.Code != http.StatusNotFound {
		t.Fatalf("draft after publish = %d", gone.Code)
	}

	// A list leaves the body behind; the detail read has it.
	page := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil))
	if !strings.Contains(page.Body.String(), postID) {
		t.Fatalf("the article must appear on the timeline: %s", page.Body.String())
	}
	var listEnvelope struct {
		Data struct {
			Items []map[string]any `json:"items"`
		} `json:"data"`
	}
	if err := json.Unmarshal(page.Body.Bytes(), &listEnvelope); err != nil {
		t.Fatal(err)
	}
	item := listEnvelope.Data.Items[0]
	listArticle, _ := item["article"].(map[string]any)
	if listArticle == nil || listArticle["body"] != nil || item["content_html"] != "" {
		t.Fatalf("a list carries the card and not the body: %#v", item)
	}
	if listArticle["title"] != "发布的文章" {
		t.Fatalf("list card = %#v", listArticle)
	}
}

func TestTimelineCanBeNarrowedToOneKind(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	if moment := performRequest(fixture.engine, ownerCall(fixture, http.MethodPost, "/api/v1/owner/posts", `{"content_md":"一条普通帖子"}`)); moment.Code != http.StatusCreated {
		t.Fatalf("create moment = %d %s", moment.Code, moment.Body.String())
	}
	draft := createDraft(t, fixture, "一篇文章")
	id, _ := draft["id"].(string)
	if published := performRequest(fixture.engine, ownerCall(fixture, http.MethodPost, "/api/v1/owner/articles/drafts/"+id+"/publish", "")); published.Code != http.StatusCreated {
		t.Fatalf("publish = %d %s", published.Code, published.Body.String())
	}

	count := func(query string) int {
		t.Helper()
		response := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/posts"+query, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("list%s = %d %s", query, response.Code, response.Body.String())
		}
		var envelope struct {
			Data struct {
				Items []map[string]any `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		return len(envelope.Data.Items)
	}
	if got := count(""); got != 2 {
		t.Fatalf("both kinds share one timeline, got %d", got)
	}
	if got := count("?kind=article"); got != 1 {
		t.Fatalf("narrowed to articles, got %d", got)
	}
	if got := count("?kind=moment"); got != 1 {
		t.Fatalf("narrowed to moments, got %d", got)
	}
	bad := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/posts?kind=note", nil))
	if bad.Code != http.StatusBadRequest {
		t.Fatalf("an unknown kind = %d %s", bad.Code, bad.Body.String())
	}
}

// Editing opens a working copy, and publishing it replaces the article in
// place rather than adding a second one.
func TestEditingAPublishedArticleUsesAWorkingCopy(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	draft := createDraft(t, fixture, "初版")
	draftID, _ := draft["id"].(string)
	published := performRequest(fixture.engine, ownerCall(fixture, http.MethodPost, "/api/v1/owner/articles/drafts/"+draftID+"/publish", ""))
	post := decodeDataMap(t, published)
	postID, _ := post["id"].(string)

	opened := performRequest(fixture.engine, ownerCall(fixture, http.MethodPost, "/api/v1/owner/articles/"+postID+"/draft", ""))
	if opened.Code != http.StatusOK {
		t.Fatalf("open working copy = %d %s", opened.Code, opened.Body.String())
	}
	working := decodeDataMap(t, opened)
	if working["published_entry_id"] != postID || working["title"] != "初版" {
		t.Fatalf("working copy = %#v", working)
	}
	workingID, _ := working["id"].(string)
	updatedAt, _ := working["updated_at"].(string)

	saved := performRequest(fixture.engine, ownerCall(fixture, http.MethodPut, "/api/v1/owner/articles/drafts/"+workingID,
		`{"title":"改过了","body":`+articleDraftBodyJSON+`,"expected_updated_at":"`+updatedAt+`"}`))
	if saved.Code != http.StatusOK {
		t.Fatalf("save working copy = %d %s", saved.Code, saved.Body.String())
	}
	// The live article has not moved.
	live := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/posts/"+postID, nil))
	liveArticle, _ := decodeDataMap(t, live)["article"].(map[string]any)
	if liveArticle["title"] != "初版" {
		t.Fatalf("editing changed what readers see: %#v", liveArticle)
	}

	republished := performRequest(fixture.engine, ownerCall(fixture, http.MethodPost, "/api/v1/owner/articles/drafts/"+workingID+"/publish", ""))
	if republished.Code != http.StatusCreated {
		t.Fatalf("republish = %d %s", republished.Code, republished.Body.String())
	}
	again := decodeDataMap(t, republished)
	if again["id"] != postID {
		t.Fatalf("republishing must not create a second article: %v", again["id"])
	}
	if article, _ := again["article"].(map[string]any); article["title"] != "改过了" {
		t.Fatalf("republished article = %#v", again["article"])
	}
	if again["edited_at"] == nil {
		t.Fatal("an edited article must say so")
	}
}

// An article is found by its title, which is how anybody looks for one again,
// and a result carries the card rather than the body.
func TestSearchFindsAnArticleAndReturnsItsCard(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	draft := createDraft(t, fixture, "长安的荔枝")
	id, _ := draft["id"].(string)
	if published := performRequest(fixture.engine, ownerCall(fixture, http.MethodPost, "/api/v1/owner/articles/drafts/"+id+"/publish", "")); published.Code != http.StatusCreated {
		t.Fatalf("publish = %d %s", published.Code, published.Body.String())
	}

	for _, term := range []string{"荔枝", "文章正文"} {
		response := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/search?q="+term, nil))
		if response.Code != http.StatusOK {
			t.Fatalf("search %q = %d %s", term, response.Code, response.Body.String())
		}
		var envelope struct {
			Data struct {
				Items []map[string]any `json:"items"`
			} `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Data.Items) != 1 {
			t.Fatalf("searching %q found %d results", term, len(envelope.Data.Items))
		}
		article, _ := envelope.Data.Items[0]["article"].(map[string]any)
		if article == nil || article["title"] != "长安的荔枝" {
			t.Fatalf("a result must carry the card: %#v", envelope.Data.Items[0])
		}
		if article["body"] != nil {
			t.Fatalf("a result must not carry the body: %#v", article)
		}
	}
}

func TestArticleStructuredReadCarriesInlineGeometryWithoutAttachments(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	insertPostHTTPMedia(t, fixture.database, postHTTPMediaOne, "a")
	body := `{"schema_version":1,"type":"doc","content":[{"type":"image","attrs":{"media_id":"` + postHTTPMediaOne + `","alt":"河流"}},{"type":"image","attrs":{"media_id":"` + postHTTPMediaOne + `","alt":"同一河流"}}]}`
	created := performRequest(fixture.engine, ownerCall(fixture, http.MethodPost, "/api/v1/owner/articles/drafts", `{"title":"图文","body":`+body+`}`))
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	draft := decodeDataMap(t, created)
	published := performRequest(fixture.engine, ownerCall(fixture, http.MethodPost, "/api/v1/owner/articles/drafts/"+draft["id"].(string)+"/publish", `{"visibility":"only_me"}`))
	if published.Code != http.StatusCreated {
		t.Fatalf("publish = %d %s", published.Code, published.Body.String())
	}
	post := decodeDataMap(t, published)
	id := post["id"].(string)
	read := performRequest(fixture.engine, ownerCall(fixture, http.MethodGet, "/api/v1/posts/"+id, ""))
	if read.Code != http.StatusOK {
		t.Fatalf("read = %d %s", read.Code, read.Body.String())
	}
	post = decodeDataMap(t, read)
	media := post["article"].(map[string]any)["body_media"].([]any)
	if len(media) != 1 || len(post["media"].([]any)) != 0 {
		t.Fatalf("geometry is deduplicated outside the attachment grid: %#v", post)
	}
	item := media[0].(map[string]any)
	if item["media_id"] != postHTTPMediaOne || item["width"] != float64(8) || item["height"] != float64(8) || item["mime_type"] != "image/jpeg" {
		t.Fatalf("geometry = %#v", item)
	}
	visitor := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/posts/"+id, nil))
	if visitor.Code != http.StatusNotFound {
		t.Fatalf("private body leaked: %d %s", visitor.Code, visitor.Body.String())
	}
	page := performRequest(fixture.engine, ownerCall(fixture, http.MethodGet, "/api/v1/posts?kind=article", ""))
	if strings.Contains(page.Body.String(), "body_media") {
		t.Fatal("list leaked body geometry")
	}
}
