package ginhttp

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHiddenPostsAndTheirMediaAnswerOnlyToTheAuthorOverHTTP(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	asAuthor := func(request *http.Request) *http.Request {
		request.Header.Set("Authorization", "Bearer "+fixture.token)
		return request
	}

	upload := asAuthor(mediaUploadRequest(t, testGIF(t)))
	uploaded := performRequest(fixture.engine, upload)
	if uploaded.Code != http.StatusCreated {
		t.Fatalf("upload = %d %s", uploaded.Code, uploaded.Body.String())
	}
	mediaID := decodeDataMap(t, uploaded)["media"].(map[string]any)["id"].(string)

	create := asAuthor(jsonRequest(http.MethodPost, "/api/v1/posts", `{"content_md":"shoreline secret","visibility":"only_me","media":[{"media_id":"`+mediaID+`","position":0,"alt_text":null}]}`))
	created := performRequest(fixture.engine, create)
	if created.Code != http.StatusCreated {
		t.Fatalf("create = %d %s", created.Code, created.Body.String())
	}
	createdData := decodeDataMap(t, created)
	id := createdData["id"].(string)
	if createdData["visibility"] != "only_me" {
		t.Fatalf("created visibility = %v", createdData["visibility"])
	}
	public := asAuthor(jsonRequest(http.MethodPost, "/api/v1/posts", `{"content_md":"shoreline public"}`))
	if response := performRequest(fixture.engine, public); response.Code != http.StatusCreated || decodeDataMap(t, response)["visibility"] != "everyone" {
		t.Fatalf("public create = %d %s", response.Code, response.Body.String())
	}
	for _, body := range []string{`{"content_md":"x","visibility":"friends"}`, `{"content_md":"x","visibility":null}`, `{"content_md":"x","visibility":1}`} {
		if response := performRequest(fixture.engine, asAuthor(jsonRequest(http.MethodPost, "/api/v1/posts", body))); response.Code != http.StatusBadRequest || decodeError(t, response.Body).Error.Code != "INVALID_ARGUMENT" {
			t.Fatalf("%s = %d %s", body, response.Code, response.Body.String())
		}
	}

	// A visitor: the post, its thread, its place in every list, and its file
	// are all absent.
	if response := performRequest(fixture.engine, jsonRequest(http.MethodGet, "/api/v1/posts/"+id, "")); response.Code != http.StatusNotFound || decodeError(t, response.Body).Error.Code != "POST_NOT_FOUND" {
		t.Fatalf("visitor detail = %d %s", response.Code, response.Body.String())
	}
	if items := decodeDataMap(t, performRequest(fixture.engine, jsonRequest(http.MethodGet, "/api/v1/posts?limit=10", "")))["items"].([]any); len(items) != 1 || items[0].(map[string]any)["visibility"] != "everyone" {
		t.Fatalf("visitor timeline = %#v", items)
	}
	search := decodeDataMap(t, performRequest(fixture.engine, jsonRequest(http.MethodGet, "/api/v1/search?q=shoreline", "")))
	if items := search["items"].([]any); len(items) != 1 || search["year_counts"].([]any)[0].(map[string]any)["count"].(float64) != 1 {
		t.Fatalf("visitor search = %#v", search)
	}
	if response := performRequest(fixture.engine, jsonRequest(http.MethodGet, "/api/v1/archive/months", "")); response.Code != http.StatusUnauthorized {
		t.Fatalf("visitor archive = %d", response.Code)
	}

	for _, path := range []string{"/api/v1/media/" + mediaID, "/api/v1/media/" + mediaID + "/preview"} {
		if response := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, path, nil)); response.Code != http.StatusNotFound || decodeError(t, response.Body).Error.Code != "MEDIA_NOT_FOUND" {
			t.Fatalf("visitor %s = %d %s", path, response.Code, response.Body.String())
		}
	}

	detail := performRequest(fixture.engine, asAuthor(jsonRequest(http.MethodGet, "/api/v1/posts/"+id, "")))
	if detail.Code != http.StatusOK || decodeDataMap(t, detail)["visibility"] != "only_me" {
		t.Fatalf("owner detail = %d %s", detail.Code, detail.Body.String())
	}
	if items := decodeDataMap(t, performRequest(fixture.engine, asAuthor(jsonRequest(http.MethodGet, "/api/v1/posts?limit=10", ""))))["items"].([]any); len(items) != 2 {
		t.Fatalf("owner timeline = %#v", items)
	}
	authorSearch := decodeDataMap(t, performRequest(fixture.engine, asAuthor(jsonRequest(http.MethodGet, "/api/v1/search?q=shoreline", ""))))
	if items := authorSearch["items"].([]any); len(items) != 2 || authorSearch["year_counts"].([]any)[0].(map[string]any)["count"].(float64) != 2 {
		t.Fatalf("owner search = %#v", authorSearch)
	}
	if months := decodeDataMap(t, performRequest(fixture.engine, asAuthor(jsonRequest(http.MethodGet, "/api/v1/archive/months", ""))))["items"].([]any); months[0].(map[string]any)["count"].(float64) != 2 {
		t.Fatalf("owner months = %#v", months)
	}
	original := performRequest(fixture.engine, asAuthor(httptest.NewRequest(http.MethodGet, "/api/v1/media/"+mediaID, nil)))
	if original.Code != http.StatusOK || original.Header().Get("Cache-Control") != "private, max-age=0, must-revalidate" {
		t.Fatalf("owner media = %d cache=%q", original.Code, original.Header().Get("Cache-Control"))
	}

	// Revealing the post is one PATCH with nothing else in it, and it takes
	// the file with it.
	reveal := asAuthor(jsonRequest(http.MethodPatch, "/api/v1/posts/"+id, `{"visibility":"everyone"}`))
	revealed := performRequest(fixture.engine, reveal)
	revealedData := decodeDataMap(t, revealed)
	if revealed.Code != http.StatusOK || revealedData["visibility"] != "everyone" || revealedData["edited_at"] != nil {
		t.Fatalf("reveal = %d %s", revealed.Code, revealed.Body.String())
	}
	if response := performRequest(fixture.engine, jsonRequest(http.MethodGet, "/api/v1/posts/"+id, "")); response.Code != http.StatusOK {
		t.Fatalf("visitor detail after reveal = %d %s", response.Code, response.Body.String())
	}
	shown := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/media/"+mediaID, nil))
	if shown.Code != http.StatusOK || shown.Header().Get("Cache-Control") != "public, max-age=0, must-revalidate" {
		t.Fatalf("visitor media after reveal = %d cache=%q", shown.Code, shown.Header().Get("Cache-Control"))
	}
	// A previously public file must be revalidated, and authorization must run
	// before an ETag can produce a cache hit after the post becomes private.
	hiddenAgain := performRequest(fixture.engine, asAuthor(jsonRequest(http.MethodPatch, "/api/v1/posts/"+id, `{"visibility":"only_me"}`)))
	if hiddenAgain.Code != http.StatusOK {
		t.Fatal(hiddenAgain.Body)
	}
	cached := jsonRequest(http.MethodGet, "/api/v1/media/"+mediaID, "")
	cached.Header.Set("If-None-Match", shown.Header().Get("ETag"))
	if response := performRequest(fixture.engine, cached); response.Code != http.StatusNotFound {
		t.Fatalf("stale public ETag bypassed private visibility: %d %s", response.Code, response.Body)
	}
	if response := performRequest(fixture.engine, asAuthor(cached)); response.Code != http.StatusNotModified || response.Header().Get("Cache-Control") != "private, max-age=0, must-revalidate" {
		t.Fatalf("author revalidation=%d cache=%s", response.Code, response.Header().Get("Cache-Control"))
	}
	if response := performRequest(fixture.engine, asAuthor(jsonRequest(http.MethodPatch, "/api/v1/posts/"+id, `{"visibility":"only_you"}`))); response.Code != http.StatusBadRequest {
		t.Fatalf("bad PATCH visibility = %d %s", response.Code, response.Body.String())
	}
}

// A member is a visitor as far as reading goes: a session widens what you may
// write, not what you may see.
func TestAMemberReadsHiddenPostsLikeAVisitor(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	member := approvedMemberSession(t, fixture)
	id := fixture.postID.String()
	if response := performRequest(fixture.engine, memberCommentRequest(member, http.MethodGet, "/api/v1/posts/"+id, "")); response.Code != http.StatusOK {
		t.Fatalf("member detail before hiding = %d %s", response.Code, response.Body.String())
	}
	hidden := performRequest(fixture.engine, authenticatedCommentRequest(fixture, http.MethodPatch, "/api/v1/posts/"+id, `{"visibility":"only_me"}`))
	if hidden.Code != http.StatusOK || decodeDataMap(t, hidden)["visibility"] != "only_me" {
		t.Fatalf("hide = %d %s", hidden.Code, hidden.Body.String())
	}
	for _, request := range []*http.Request{
		memberCommentRequest(member, http.MethodGet, "/api/v1/posts/"+id, ""),
		memberCommentRequest(member, http.MethodGet, "/api/v1/posts/"+id+"/comments", ""),
		memberCommentRequest(member, http.MethodPost, "/api/v1/posts/"+id+"/comments", `{"content_md":"hello?"}`),
	} {
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusNotFound || decodeError(t, response.Body).Error.Code != "POST_NOT_FOUND" {
			t.Fatalf("member %s %s = %d %s", request.Method, request.URL.Path, response.Code, response.Body.String())
		}
	}
	if items := decodeDataMap(t, performRequest(fixture.engine, memberCommentRequest(member, http.MethodGet, "/api/v1/posts?limit=10", "")))["items"].([]any); len(items) != 0 {
		t.Fatalf("member timeline = %#v", items)
	}
	note := performRequest(fixture.engine, authenticatedCommentRequest(fixture, http.MethodPost, "/api/v1/posts/"+id+"/comments", `{"content_md":"note to self"}`))
	if note.Code != http.StatusCreated {
		t.Fatalf("owner comment = %d %s", note.Code, note.Body.String())
	}
	thread := performRequest(fixture.engine, authenticatedCommentRequest(fixture, http.MethodGet, "/api/v1/posts/"+id+"/comments", ""))
	if thread.Code != http.StatusOK || len(decodeDataMap(t, thread)["items"].([]any)) != 1 {
		t.Fatalf("owner thread = %d %s", thread.Code, thread.Body.String())
	}
}
