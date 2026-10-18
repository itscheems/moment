package ginhttp

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestApprovedAuthorManagesArticlesWithoutAdministratorOverride(t *testing.T) {
	fixture := newCommentHTTPFixture(t, nil)
	// This fixture uses one deterministic post id. Remove its initial comment target through the API before publishing another entry.
	remove := jsonRequest(http.MethodDelete, "/api/v1/posts/"+fixture.postID.String(), "")
	remove.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, remove); response.Code != http.StatusOK {
		t.Fatalf("prepare fixture=%s", response.Body.String())
	}
	member := approvedMemberSession(t, fixture)
	create := func(key string) *httptest.ResponseRecorder {
		request := memberCommentRequest(member, http.MethodPost, "/api/v1/articles/drafts", `{"title":"member article","body":`+articleDraftBodyJSON+`}`)
		request.Header.Set("Idempotency-Key", key)
		return performRequest(fixture.engine, request)
	}
	first := create("create-once")
	if first.Code != http.StatusCreated {
		t.Fatalf("member create = %d %s", first.Code, first.Body.String())
	}
	draft := decodeDataMap(t, first)
	id := draft["id"].(string)
	replay := create("create-once")
	if replay.Code != http.StatusCreated || decodeDataMap(t, replay)["id"] != id {
		t.Fatal("create replay duplicated a draft")
	}
	for _, prefix := range []string{"/api/v1", "/api/v1"} {
		authorRead := jsonRequest(http.MethodGet, prefix+"/articles/drafts/"+id, "")
		authorRead.Header.Set("Authorization", "Bearer "+fixture.token)
		if response := performRequest(fixture.engine, authorRead); response.Code == http.StatusOK {
			t.Fatal("non-author admin read member draft")
		}
		response := performRequest(fixture.engine, memberCommentRequest(member, http.MethodGet, prefix+"/articles/drafts", ""))
		var envelope struct {
			Data []map[string]any `json:"data"`
		}
		if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
			t.Fatal(err)
		}
		if len(envelope.Data) != 1 {
			t.Fatalf("author summary=%s", response.Body.String())
		}
		if _, present := envelope.Data[0]["body"]; present {
			t.Fatal("summary shipped the full body")
		}
		if _, present := envelope.Data[0]["excerpt"]; !present {
			t.Fatal("summary omitted its cached text preview")
		}
	}
	publish := performRequest(fixture.engine, memberCommentRequest(member, http.MethodPost, "/api/v1/articles/drafts/"+id+"/publish", ""))
	if publish.Code != http.StatusCreated {
		t.Fatalf("publish=%d %s", publish.Code, publish.Body.String())
	}
	post := decodeDataMap(t, publish)
	postID := post["id"].(string)
	adminEdit := jsonRequest(http.MethodPost, "/api/v1/articles/"+postID+"/draft", "")
	adminEdit.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, adminEdit); response.Code == http.StatusOK {
		t.Fatal("administrator opened another author's working copy")
	}
	withdrawn := performRequest(fixture.engine, memberCommentRequest(member, http.MethodPost, "/api/v1/articles/"+postID+"/unpublish", ""))
	if withdrawn.Code != http.StatusOK {
		t.Fatalf("withdraw=%d %s", withdrawn.Code, withdrawn.Body.String())
	}
	if response := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/posts/"+postID, nil)); response.Code != http.StatusNotFound {
		t.Fatal("withdrawn public detail remained readable")
	}
	if response := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/posts", nil)); strings.Contains(response.Body.String(), postID) {
		t.Fatal("withdrawn article remained in timeline")
	}
	republished := performRequest(fixture.engine, memberCommentRequest(member, http.MethodPost, "/api/v1/articles/drafts/"+id+"/publish", ""))
	if republished.Code != http.StatusOK && republished.Code != http.StatusCreated {
		t.Fatalf("republish=%d %s", republished.Code, republished.Body.String())
	}
	moderation := jsonRequest(http.MethodDelete, "/api/v1/posts/"+postID, "")
	moderation.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, moderation); response.Code != http.StatusOK {
		t.Fatalf("administrator could not moderate published article: %d %s", response.Code, response.Body.String())
	}
}
