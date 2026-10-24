package sqlite

import (
	"context"
	"crypto/rand"
	"fmt"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestCommentContextAndDirectReplyPaging(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	insertEntry(t, db, entryTwo, "other")
	tick := int64(0)
	service := newTestCommentService(t, db, runtimeState, func() time.Time { tick++; return time.UnixMilli(1_700_000_000_000 + tick/2) }, func(now time.Time) (domain.ID, error) { return domain.NewID(now, rand.Reader) })
	author := testCommentAuthor(t, db, runtimeState)
	postID := mustID(t, entryOne)
	root := mustAccountComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "root"}).Comment.Comment.ID
	target := mustAccountComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &root, ReplyToCommentID: &root, ContentMD: "target"}).Comment.Comment.ID
	legacy := mustAccountComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &root, ContentMD: "unaddressed"}).Comment.Comment.ID
	expected := map[domain.ID]bool{}
	for i := 0; i < 103; i++ {
		reply := mustAccountComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &root, ReplyToCommentID: &target, ContentMD: fmt.Sprintf("answer %d", i)})
		expected[reply.Comment.Comment.ID] = true
	}
	detail, err := service.GetContext(testAccountContext(), postID, target, app.AccountViewer(author.ID))
	if err != nil || detail.DirectReplyCount != 103 || detail.RootCommentID != root || detail.ReplyTarget == nil || detail.ReplyTarget.Comment.ID != root || detail.Comment.Comment.ID != target {
		t.Fatalf("context=%+v err=%v", detail, err)
	}
	cursor := ""
	seen := map[domain.ID]bool{}
	for {
		page, err := service.ListReplies(testAccountContext(), app.ListRepliesInput{EntryID: postID, RootID: target, Scope: app.CommentRepliesDirect, Cursor: cursor, Limit: 10})
		if err != nil {
			t.Fatal(err)
		}
		for _, item := range page.Items {
			if !expected[item.Comment.ID] || seen[item.Comment.ID] {
				t.Fatalf("unexpected/repeated reply %s", item.Comment.ID)
			}
			seen[item.Comment.ID] = true
		}
		if page.NextCursor == "" {
			break
		}
		for _, scope := range []app.CommentReplyScope{app.CommentRepliesThread} {
			if _, err := service.ListReplies(testAccountContext(), app.ListRepliesInput{EntryID: postID, RootID: root, Scope: scope, Cursor: page.NextCursor}); !domain.HasCode(err, domain.CodeInvalidArgument) {
				t.Fatalf("cursor scope: %v", err)
			}
		}
		if _, err := service.ListReplies(testAccountContext(), app.ListRepliesInput{EntryID: postID, RootID: root, Scope: app.CommentRepliesDirect, Cursor: page.NextCursor}); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("cursor target: %v", err)
		}
		cursor = page.NextCursor
	}
	if len(seen) != len(expected) {
		t.Fatalf("seen %d expected %d", len(seen), len(expected))
	}
	roots, err := service.ListReplies(testAccountContext(), app.ListRepliesInput{EntryID: postID, RootID: root, Scope: app.CommentRepliesDirect})
	if err != nil || len(roots.Items) != 2 {
		t.Fatalf("root direct=%+v %v", roots, err)
	}
	rootContext, err := service.GetContext(testAccountContext(), postID, root, app.AccountViewer(author.ID))
	if err != nil || rootContext.DirectReplyCount != 2 || rootContext.Comment.ReplyCount != 105 {
		t.Fatalf("root direct/whole counts: %+v %v", rootContext, err)
	}
	for _, item := range roots.Items {
		if item.Comment.ID != target && item.Comment.ID != legacy {
			t.Fatalf("wrong root answer %s", item.Comment.ID)
		}
	}
	thread, err := service.ListReplies(testAccountContext(), app.ListRepliesInput{EntryID: postID, RootID: root, Limit: 100})
	if err != nil || len(thread.Items) != 100 || thread.NextCursor == "" {
		t.Fatalf("thread: %d %v", len(thread.Items), err)
	}
	if _, err := service.ListReplies(testAccountContext(), app.ListRepliesInput{EntryID: postID, RootID: root, Scope: app.CommentRepliesDirect, Cursor: thread.NextCursor}); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("thread cursor accepted by direct: %v", err)
	}
	if _, err := service.GetContext(testAccountContext(), mustID(t, entryTwo), target, app.AccountViewer(author.ID)); !domain.HasCode(err, domain.CodeCommentNotFound) {
		t.Fatalf("cross post context: %v", err)
	}
	ctx, cancel := context.WithCancel(testAccountContext())
	cancel()
	if _, err := service.GetContext(ctx, postID, target, app.AccountViewer(author.ID)); err == nil {
		t.Fatal("cancelled read succeeded")
	}
	if _, err := service.Delete(testAccountContext(), target, false); err != nil {
		t.Fatal(err)
	}
	for id := range expected {
		child, err := service.GetContext(testAccountContext(), postID, id, app.AccountViewer(author.ID))
		if err != nil || child.ReplyTarget != nil || child.Comment.ReplyTo != nil || child.RootCommentID != root {
			t.Fatalf("deleted target context=%+v %v", child, err)
		}
		break
	}
	directAfterDelete, err := service.ListReplies(testAccountContext(), app.ListRepliesInput{EntryID: postID, RootID: root, Scope: app.CommentRepliesDirect, Limit: 100})
	if err != nil || len(directAfterDelete.Items) != 100 || directAfterDelete.NextCursor == "" {
		t.Fatalf("unaddressed replies missing: %d %v", len(directAfterDelete.Items), err)
	}
	mustWrite(t, db, `UPDATE entries SET visibility='only_me' WHERE id=?`, postID.String())
	if _, err := service.GetContext(context.Background(), postID, root, app.Viewer{}); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("private context: %v", err)
	}
}
