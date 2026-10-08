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
	remove := ownerRequest(http.MethodDelete, "/api/v1/owner/posts/"+fixture.postID.String(), "")
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
	for _, prefix := range []string{"/api/v1", "/api/v1/owner"} {
		ownerRead := ownerRequest(http.MethodGet, prefix+"/articles/drafts/"+id, "")
		ownerRead.Header.Set("Authorization", "Bearer "+fixture.token)
		if response := performRequest(fixture.engine, ownerRead); response.Code == http.StatusOK {
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
	for _, path := range []string{"/api/v1/articles/" + postID, "/api/v1/owner/posts/" + postID} {
		request := ownerRequest(http.MethodDelete, path, "")
		request.Header.Set("Authorization", "Bearer "+fixture.token)
		if response := performRequest(fixture.engine, request); response.Code == http.StatusOK {
			t.Fatal("admin generic delete bypassed authorship")
		}
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
}
