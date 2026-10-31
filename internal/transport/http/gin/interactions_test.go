package ginhttp

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/iotest"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	sqlitestore "github.com/moment/moment/internal/storage/sqlite"
)

func TestInteractionHTTPStreamedEmptyLikeRequests(t *testing.T) {
	f := newCommentHTTPFixture(t, nil)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength != -1 || len(r.TransferEncoding) != 1 || r.TransferEncoding[0] != "chunked" {
			t.Errorf("expected unknown-length chunked request, got length=%d encoding=%v", r.ContentLength, r.TransferEncoding)
		}
		f.engine.ServeHTTP(w, r)
	}))
	defer server.Close()
	path := "/api/v1/posts/" + f.postID.String() + "/like"
	for _, tc := range []struct {
		name, method, query, body string
		status                    int
		liked                     bool
	}{
		{"like", "PUT", "", "", 200, true},
		{"unlike", "DELETE", "", "", 200, false},
		{"whitespace body", "PUT", "", " ", 400, false},
		{"JSON body", "PUT", "", "{}", 400, false},
		{"query", "PUT", "?extra=1", "", 400, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// A streaming body forces real HTTP chunked framing, including at EOF.
			request, err := http.NewRequest(tc.method, server.URL+path+tc.query, io.NopCloser(strings.NewReader(tc.body)))
			if err != nil {
				t.Fatal(err)
			}
			request.ContentLength = -1
			request.TransferEncoding = []string{"chunked"}
			request.Header.Set("Authorization", "Bearer "+f.token)
			response, err := server.Client().Do(request)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = response.Body.Close() }()
			body, err := io.ReadAll(response.Body)
			if err != nil {
				t.Fatal(err)
			}
			if response.StatusCode != tc.status {
				t.Fatalf("status=%d want=%d body=%s", response.StatusCode, tc.status, body)
			}
			state := decodeDataMap(t, performRequest(f.engine, authenticatedCommentRequest(f, "GET", path, "")))
			if state["liked_by_me"] != tc.liked {
				t.Fatalf("like state=%v", state)
			}
		})
	}
}

func TestInteractionHTTPRejectsUnreadableLikeBody(t *testing.T) {
	f := newCommentHTTPFixture(t, nil)
	path := "/api/v1/posts/" + f.postID.String() + "/like"
	request := authenticatedCommentRequest(f, "PUT", path, "")
	request.Body = io.NopCloser(iotest.ErrReader(io.ErrUnexpectedEOF))
	request.ContentLength = -1
	response := performRequest(f.engine, request)
	if response.Code != 400 {
		t.Fatalf("unreadable body: %d %s", response.Code, response.Body.String())
	}
	state := decodeDataMap(t, performRequest(f.engine, authenticatedCommentRequest(f, "GET", path, "")))
	if state["liked_by_me"] != false || state["like_count"] != float64(0) {
		t.Fatalf("unreadable request changed like state: %v", state)
	}
}

func TestInteractionHTTPAccountViewsLatestBodyAndPermissionRedaction(t *testing.T) {
	f := newCommentHTTPFixture(t, nil)
	member := approvedMemberSession(t, f)
	path := "/api/v1/posts/" + f.postID.String() + "/like"
	call := func(method, path, body string) map[string]any {
		t.Helper()
		response := performRequest(f.engine, authenticatedCommentRequest(f, method, path, body))
		if response.Code != 200 {
			t.Fatalf("%s %s: %d %s", method, path, response.Code, response.Body.String())
		}
		return decodeDataMap(t, response)
	}
	for range 2 {
		response := performRequest(f.engine, memberCommentRequest(member, http.MethodPut, path, ""))
		if response.Code != 200 {
			t.Fatalf("like: %d %s", response.Code, response.Body.String())
		}
	}
	if state := call("GET", path, ""); state["like_count"] != float64(1) || state["liked_by_me"] != false {
		t.Fatalf("state: %+v", state)
	}
	base := "/api/v1/account/notifications"
	page := call("GET", base, "")
	items := page["items"].([]any)
	if len(items) != 1 || page["new_count"] != float64(1) {
		t.Fatalf("page: %+v", page)
	}
	item := items[0].(map[string]any)
	id := item["id"].(string)
	hidden := performRequest(f.engine, memberCommentRequest(member, "GET", base+"/"+id, ""))
	if hidden.Code != 404 {
		t.Fatalf("cross account: %d", hidden.Code)
	}
	// A list/body read and incremental consumer do not acknowledge human viewing.
	call("GET", base+"/"+id, "")
	call("GET", base+"?after="+page["through_cursor"].(string), "")
	if state := call("GET", base+"/state", ""); state["new_count"] != float64(1) {
		t.Fatal("GET cleared unread")
	}
	repo, _ := sqlitestore.NewPostRepository(f.database, f.runtime.Locks)
	posts, _ := app.NewPostService(repo)
	owner, _ := f.accountService.ProfileAccount(context.Background())
	ctx := app.WithContentActor(context.Background(), owner)
	body := "Latest message"
	if _, err := posts.Update(ctx, app.UpdatePostInput{ID: f.postID, ContentMD: body}); err != nil {
		t.Fatal(err)
	}
	if detail := call("GET", base+"/"+id, ""); detail["post"].(map[string]any)["content_md"] != body {
		t.Fatalf("body stale: %+v", detail)
	}
	visibility := domain.VisibilityOnlyMe
	if _, err := posts.Update(ctx, app.UpdatePostInput{ID: f.postID, PreserveContent: true, Visibility: &visibility}); err != nil {
		t.Fatal(err)
	}
	// The owner may still read their own private source; the notifying member may not like it again.
	denied := performRequest(f.engine, memberCommentRequest(member, "PUT", path, ""))
	if denied.Code != 404 {
		t.Fatalf("private like = %d", denied.Code)
	}
	if _, err := posts.Delete(ctx, f.postID); err != nil {
		t.Fatal(err)
	}
	detail := call("GET", base+"/"+id, "")
	if detail["available"] != false || detail["excerpt"] != "" || detail["post"] != nil || detail["comment"] != nil || detail["reply_to_comment_id"] != nil {
		t.Fatalf("unavailable content leaked: %+v", detail)
	}
	for _, target := range []string{base + "?scope=all&scope=mentions", base + "?unknown=1", base + "?limit=0", base + "/state?token=secret", base + "/stream?token=secret"} {
		response := performRequest(f.engine, authenticatedCommentRequest(f, "GET", target, ""))
		if response.Code != 400 {
			t.Fatalf("query accepted %s: %d", target, response.Code)
		}
	}
	response := performRequest(f.engine, httptest.NewRequest("GET", base, nil))
	if response.Code != 401 {
		t.Fatalf("anonymous inbox = %d", response.Code)
	}
}

func readNotificationEvent(t *testing.T, reader *bufio.Reader) (string, map[string]any) {
	t.Helper()
	event := ""
	var data map[string]any
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			t.Fatal(err)
		}
		if strings.HasPrefix(line, "event: ") {
			event = strings.TrimSpace(strings.TrimPrefix(line, "event: "))
		}
		if strings.HasPrefix(line, "data: ") {
			if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &data); err != nil {
				t.Fatal(err)
			}
		}
		if line == "\n" && event != "" {
			return event, data
		}
	}
}

func TestInteractionSSEObservesExternalWritesAndRevocationBeyondGlobalDeadline(t *testing.T) {
	f := newCommentHTTPFixture(t, nil)
	member := approvedMemberSession(t, f)
	server := httptest.NewUnstartedServer(f.engine)
	server.Config.WriteTimeout = 50 * time.Millisecond
	server.Start()
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Second)
	defer cancel()
	request, _ := http.NewRequestWithContext(ctx, "GET", server.URL+"/api/v1/account/notifications/stream", nil)
	request.Header.Set("Authorization", "Bearer "+f.token)
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != 200 || response.Header.Get("Content-Type") != "text/event-stream" {
		t.Fatalf("stream: %d", response.StatusCode)
	}
	reader := bufio.NewReader(response.Body)
	event, state := readNotificationEvent(t, reader)
	if event != "sync" || state["new_count"] != float64(0) {
		t.Fatalf("initial: %s %+v", event, state)
	}
	// Independent database connection, with no publisher reference to this HTTP bundle.
	db, err := sqlitestore.Open(ctx, f.runtime, sqlitestore.Options{})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	pr, _ := sqlitestore.NewPostRepository(db, f.runtime.Locks)
	posts, _ := app.NewPostService(pr)
	cr, _ := sqlitestore.NewCommentRepository(db, f.runtime.Locks)
	comments, _ := app.NewCommentService(cr)
	ir, _ := sqlitestore.NewInteractionRepository(db, f.runtime.Locks, f.runtime.Current.ID)
	service, _ := app.NewInteractionService(ir, posts, comments)
	started := time.Now()
	if _, err := service.Like(app.WithContentActor(ctx, member.user), domain.ContentRef{Kind: "post", ID: f.postID}, true); err != nil {
		t.Fatal(err)
	}
	event, state = readNotificationEvent(t, reader)
	if event != "sync" || state["new_count"] != float64(1) || time.Since(started) > 3*time.Second {
		t.Fatalf("update: %s %+v after %v", event, state, time.Since(started))
	}
	// Token revocation is rechecked while the stream is open.
	verified, err := f.accountService.ResolveAPIToken(ctx, f.token)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.accountService.RevokeAPIToken(app.WithContentActor(ctx, verified.User), verified.User.ID, verified.Token.ID); err != nil {
		t.Fatal(err)
	}
	event, _ = readNotificationEvent(t, reader)
	if event != "closed" {
		t.Fatalf("revoked stream stayed open: %s", event)
	}
}
