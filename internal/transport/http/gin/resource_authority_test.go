package ginhttp

import (
	"context"
	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestResourceRoutesLetActiveMembersWriteUnderTheirOwnIdentity(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	ctx := context.Background()
	outcome, err := fixture.accounts.SignIn(ctx, app.ExternalIdentity{Provider: "google", ProviderUserID: "writer", DisplayName: "Member Writer"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := fixture.accounts.SetStatus(ctx, outcome.User.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	issued, err := fixture.accounts.CreateAPIToken(ctx, member.ID, "writer client")
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, token string) *httptest.ResponseRecorder {
		request := jsonRequest(method, path, body)
		request.Header.Set("Authorization", "Bearer "+token)
		return performRequest(fixture.engine, request)
	}
	created := call(http.MethodPost, "/api/v1/posts", `{"content_md":"member writes"}`, issued.Value)
	if created.Code != http.StatusCreated {
		t.Fatalf("member post=%d %s", created.Code, created.Body.String())
	}
	post := decodeDataMap(t, created)
	if post["author"].(map[string]any)["user_id"] != member.ID {
		t.Fatal("post borrowed the display account identity")
	}
	id := post["id"].(string)
	rewritten := call(http.MethodPatch, "/api/v1/posts/"+id, `{"content_md":"administrator rewrite"}`, fixture.token)
	if rewritten.Code != http.StatusForbidden || decodeError(t, rewritten.Body).Error.Code != "UNAUTHORIZED" {
		t.Fatalf("administrator rewrote another author=%d %s", rewritten.Code, rewritten.Body.String())
	}
	edited := call(http.MethodPatch, "/api/v1/posts/"+id, `{"content_md":"author edits"}`, issued.Value)
	if edited.Code != http.StatusOK {
		t.Fatalf("author edit=%d %s", edited.Code, edited.Body.String())
	}
	private := call(http.MethodPost, "/api/v1/posts", `{"content_md":"private member post","visibility":"only_me"}`, issued.Value)
	if private.Code != http.StatusCreated {
		t.Fatalf("private post=%d %s", private.Code, private.Body.String())
	}
	privateID := decodeDataMap(t, private)["id"].(string)
	hidden := call(http.MethodGet, "/api/v1/posts/"+privateID, "", fixture.token)
	if hidden.Code != http.StatusNotFound {
		t.Fatalf("administrator read another author's private post=%d %s", hidden.Code, hidden.Body.String())
	}
	draft := call(http.MethodPost, "/api/v1/articles/drafts", `{"title":"member article","body":`+articleDraftBodyJSON+`}`, issued.Value)
	if draft.Code != http.StatusCreated {
		t.Fatalf("member article=%d %s", draft.Code, draft.Body.String())
	}
	draftID := decodeDataMap(t, draft)["id"].(string)
	otherDraft := call(http.MethodGet, "/api/v1/articles/drafts/"+draftID, "", fixture.token)
	if otherDraft.Code != http.StatusForbidden {
		t.Fatalf("administrator read another author's draft=%d %s", otherDraft.Code, otherDraft.Body.String())
	}
	deleted := call(http.MethodDelete, "/api/v1/posts/"+id, "", fixture.token)
	if deleted.Code != http.StatusOK {
		t.Fatalf("public moderation=%d %s", deleted.Code, deleted.Body.String())
	}
}

func TestRetiredIdentityNamespaceHasNoAPIRoutes(t *testing.T) {
	fixture := newFullEngineFixture(t)
	token := issueTestAPIToken(t, fixture.accounts)
	for _, probe := range []struct{ method, path string }{
		{http.MethodPost, "/api/v1/owner/posts"},
		{http.MethodGet, "/api/v1/owner/articles/drafts"},
		{http.MethodPost, "/api/v1/owner/media"},
		{http.MethodPatch, "/api/v1/owner/site"},
		{http.MethodGet, "/api/v1/owner/accounts"},
		{http.MethodGet, "/api/v1/owner/export"},
		{http.MethodGet, "/api/v1/owner/doctor"},
	} {
		request := jsonRequest(probe.method, probe.path, `{}`)
		request.Header.Set("Authorization", "Bearer "+token)
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusNotFound || decodeError(t, response.Body).Error.Code != "API_ROUTE_NOT_FOUND" {
			t.Fatalf("retired route %s=%d %s", probe.path, response.Code, response.Body.String())
		}
	}
}

func TestAdministrativeAccessFollowsCurrentRoleAndNotDisplaySelection(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	ctx := context.Background()
	initial, err := fixture.accounts.ProfileAccount(ctx)
	if err != nil {
		t.Fatal(err)
	}
	outcome, err := fixture.accounts.SignIn(ctx, app.ExternalIdentity{Provider: "google", ProviderUserID: "profile-member", DisplayName: "Profile Member"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := fixture.accounts.SetStatus(ctx, outcome.User.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	token, err := fixture.accounts.CreateAPIToken(ctx, member.ID, "member session")
	if err != nil {
		t.Fatal(err)
	}
	call := func(method, path, body, credential string) *httptest.ResponseRecorder {
		request := jsonRequest(method, path, body)
		request.Header.Set("Authorization", "Bearer "+credential)
		return performRequest(fixture.engine, request)
	}
	selected := call(http.MethodPut, "/api/v1/site/profile", `{"user_id":"`+member.ID+`"}`, fixture.token)
	if selected.Code != http.StatusOK || decodeDataMap(t, selected)["profile_user_id"] != member.ID {
		t.Fatalf("display selection=%d %s", selected.Code, selected.Body.String())
	}
	for _, probe := range []struct{ path, body string }{
		{"/api/v1/accounts/" + member.ID + "/role", `{"role":"admin"}`},
		{"/api/v1/site/profile", `{"user_id":"` + initial.ID + `"}`},
	} {
		response := call(http.MethodPut, probe.path, probe.body, token.Value)
		if response.Code != http.StatusForbidden || decodeError(t, response.Body).Error.Code != "UNAUTHORIZED" {
			t.Fatalf("display identity gained administrative access=%d %s", response.Code, response.Body.String())
		}
	}
	promoted := call(http.MethodPut, "/api/v1/accounts/"+member.ID+"/role", `{"role":"admin"}`, fixture.token)
	if promoted.Code != http.StatusOK {
		t.Fatalf("promotion=%d %s", promoted.Code, promoted.Body.String())
	}
	demoted := call(http.MethodPut, "/api/v1/accounts/"+initial.ID+"/role", `{"role":"member"}`, fixture.token)
	if demoted.Code != http.StatusOK {
		t.Fatalf("demotion=%d %s", demoted.Code, demoted.Body.String())
	}
	refused := call(http.MethodPatch, "/api/v1/site", `{"title":"stale role"}`, fixture.token)
	if refused.Code != http.StatusForbidden {
		t.Fatalf("old credential retained administrative rights=%d %s", refused.Code, refused.Body.String())
	}
	accepted := call(http.MethodPatch, "/api/v1/site", `{"title":"current administrator"}`, token.Value)
	if accepted.Code != http.StatusOK {
		t.Fatalf("existing member token did not acquire its current role=%d %s", accepted.Code, accepted.Body.String())
	}
	public := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/users/"+member.Username, nil))
	if public.Code != http.StatusOK || decodeDataMap(t, public)["role"] != nil {
		t.Fatalf("public role leaked=%d %s", public.Code, public.Body.String())
	}
}

func TestMissingAdministrativeTargetDoesNotInvalidateTheCaller(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	const missing = "ffffffffffffffffffffffffffffffff"
	for _, probe := range []struct{ path, body string }{
		{"/api/v1/accounts/" + missing + "/role", `{"role":"member"}`},
		{"/api/v1/site/profile", `{"user_id":"` + missing + `"}`},
	} {
		request := jsonRequest(http.MethodPut, probe.path, probe.body)
		request.Header.Set("Authorization", "Bearer "+fixture.token)
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusNotFound || decodeError(t, response.Body).Error.Code != "ACCOUNT_NOT_FOUND" {
			t.Fatalf("missing target=%d %s", response.Code, response.Body.String())
		}
	}
	request := jsonRequest(http.MethodGet, "/api/v1/accounts", "")
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	if response := performRequest(fixture.engine, request); response.Code != http.StatusOK {
		t.Fatal("caller lost administrative access after selecting a missing account")
	}
}

func TestPublicAuthorFeedIsIndependentOfAdministrationAndPrivateVisibility(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	ctx := context.Background()
	outcome, err := fixture.accounts.SignIn(ctx, app.ExternalIdentity{Provider: "google", ProviderUserID: "feed-writer", DisplayName: "Feed Writer"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := fixture.accounts.SetStatus(ctx, outcome.User.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	memberCtx := app.WithContentActor(ctx, member)
	if _, err := fixture.posts.Create(fixture.ctx, app.CreatePostInput{ContentMD: "another author"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.posts.Create(memberCtx, app.CreatePostInput{ContentMD: "member public"}); err != nil {
		t.Fatal(err)
	}
	if _, err := fixture.posts.Create(memberCtx, app.CreatePostInput{ContentMD: "member private", Visibility: domain.VisibilityOnlyMe}); err != nil {
		t.Fatal(err)
	}
	issued, err := fixture.accounts.CreateAPIToken(ctx, member.ID, "author feed")
	if err != nil {
		t.Fatal(err)
	}
	for _, probe := range []struct {
		token string
		count int
	}{{"", 1}, {fixture.token, 1}, {issued.Value, 2}} {
		request := httptest.NewRequest(http.MethodGet, "/api/v1/posts?author_user_id="+member.ID, nil)
		if probe.token != "" {
			request.Header.Set("Authorization", "Bearer "+probe.token)
		}
		response := performRequest(fixture.engine, request)
		if response.Code != http.StatusOK {
			t.Fatalf("author feed=%d %s", response.Code, response.Body.String())
		}
		data := decodeDataMap(t, response)
		items := data["items"].([]any)
		if len(items) != probe.count {
			t.Fatalf("author feed count=%d want%d", len(items), probe.count)
		}
		for _, raw := range items {
			if raw.(map[string]any)["author"].(map[string]any)["user_id"] != member.ID {
				t.Fatal("author feed included another account's content")
			}
		}
	}
}

func TestQuotedPostKeepsItsAuthorSeparateFromTheQuotingAccount(t *testing.T) {
	fixture := newPostHTTPFixture(t, 0)
	ctx := context.Background()
	outcome, err := fixture.accounts.SignIn(ctx, app.ExternalIdentity{Provider: "google", ProviderUserID: "quoted-author", DisplayName: "Original Writer"})
	if err != nil {
		t.Fatal(err)
	}
	member, err := fixture.accounts.SetStatus(ctx, outcome.User.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	original, err := fixture.posts.Create(app.WithContentActor(ctx, member), app.CreatePostInput{ContentMD: "original text"})
	if err != nil {
		t.Fatal(err)
	}
	request := jsonRequest(http.MethodPost, "/api/v1/posts", `{"content_md":"another account comments","quoted_post_id":"`+original.Post.Entry.ID.String()+`"}`)
	request.Header.Set("Authorization", "Bearer "+fixture.token)
	response := performRequest(fixture.engine, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("quote=%d %s", response.Code, response.Body.String())
	}
	data := decodeDataMap(t, response)
	author := data["author"].(map[string]any)
	quoted := data["quoted_post"].(map[string]any)
	quotedAuthor := quoted["author"].(map[string]any)
	if author["user_id"] == member.ID || quotedAuthor["user_id"] != member.ID || quotedAuthor["display_name"] != "Original Writer" {
		t.Fatalf("quote identity=%v", data)
	}
	read := performRequest(fixture.engine, httptest.NewRequest(http.MethodGet, "/api/v1/posts/"+data["id"].(string), nil))
	if read.Code != http.StatusOK || decodeDataMap(t, read)["quoted_post"].(map[string]any)["author"].(map[string]any)["user_id"] != member.ID {
		t.Fatalf("read quote=%d %s", read.Code, read.Body.String())
	}
}
