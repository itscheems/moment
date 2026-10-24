package ginhttp

import (
	"net/http"
	"testing"
)

func TestCommentContextHTTPAndScopeValidation(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	root := postAccountComment(t, fixture, `{"content_md":"root"}`)["id"].(string)
	target := postAccountComment(t, fixture, `{"content_md":"reply","parent_id":"`+root+`","reply_to_comment_id":"`+root+`"}`)["id"].(string)
	base := "/api/v1/posts/" + fixture.postID.String() + "/comments/"
	response := performRequest(fixture.engine, commentRequest(http.MethodGet, base+target, ""))
	if response.Code != http.StatusOK {
		t.Fatalf("context=%d %s", response.Code, response.Body.String())
	}
	context := commentData(t, response)
	if context["root_comment_id"] != root || context["reply_target"].(map[string]any)["id"] != root || context["direct_reply_count"] != float64(0) {
		t.Fatalf("context=%#v", context)
	}
	for _, query := range []string{"scope=wrong", "scope=", "scope=direct&scope=thread", "scope=direct&extra=1"} {
		bad := performRequest(fixture.engine, commentRequest(http.MethodGet, base+root+"/replies?"+query, ""))
		if bad.Code != http.StatusBadRequest {
			t.Fatalf("%s=%d %s", query, bad.Code, bad.Body.String())
		}
	}
	empty := performRequest(fixture.engine, commentRequest(http.MethodGet, base+target+"/replies?scope=direct", ""))
	if empty.Code != http.StatusOK || len(decodeCommentItems(t, empty)) != 0 {
		t.Fatalf("direct=%d %s", empty.Code, empty.Body.String())
	}
}
