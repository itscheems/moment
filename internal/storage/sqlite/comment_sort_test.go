package sqlite

import (
	"slices"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestCommentTimeSortPagesAndScopes(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	service := newTestCommentService(t, db, runtimeState, func() time.Time { return time.UnixMilli(1700000000000) }, commentIDs(commentOne, commentTwo, commentThree, commentFour, "01ARZ3NDEKTSV4RRFFQ69G5FB4", "01ARZ3NDEKTSV4RRFFQ69G5FB5"))
	author := testCommentAuthor(t, db, runtimeState)
	postID, _ := domain.ParseID(entryOne)
	roots := []domain.ID{}
	for _, body := range []string{"first", "second", "third"} {
		roots = append(roots, mustAccountComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: body}).Comment.Comment.ID)
	}
	replies := []domain.ID{}
	for _, body := range []string{"reply one", "reply two", "reply three"} {
		replies = append(replies, mustAccountComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &roots[0], ReplyToCommentID: &roots[0], ContentMD: body}).Comment.Comment.ID)
	}
	if _, err := service.Pin(testAccountContext(), postID, roots[0]); err != nil {
		t.Fatal(err)
	}
	for _, sort := range []app.CommentSort{app.CommentOldest, app.CommentNewest} {
		t.Run(string(sort), func(t *testing.T) {
			wantRoots, wantReplies := slices.Clone(roots), slices.Clone(replies)
			if sort == app.CommentNewest {
				slices.Reverse(wantRoots)
				slices.Reverse(wantReplies)
			}
			cursor := ""
			for i, want := range wantRoots {
				page, err := service.ListPage(testAccountContext(), app.ListCommentsInput{EntryID: postID, Sort: sort, Limit: 1, Cursor: cursor})
				if err != nil {
					t.Fatal(err)
				}
				if page.Items[0].Comment.ID != want {
					t.Fatalf("root %d = %s, want %s", i, page.Items[0].Comment.ID, want)
				}
				if len(page.Pinned) != 2 || page.Pinned[1].Comment.ID != wantReplies[0] {
					t.Fatalf("pinned preview = %+v", page.Pinned)
				}
				if page.Items[0].Comment.ID == roots[0] && page.Items[1].Comment.ID != wantReplies[0] {
					t.Fatal("inline preview uses another sort")
				}
				if i == 0 {
					other := app.CommentNewest
					if sort == other {
						other = app.CommentOldest
					}
					if _, err := service.ListPage(testAccountContext(), app.ListCommentsInput{EntryID: postID, Sort: other, Cursor: page.NextCursor}); !domain.HasCode(err, domain.CodeInvalidArgument) {
						t.Fatalf("cross-sort root cursor: %v", err)
					}
				}
				cursor = page.NextCursor
			}
			if cursor != "" {
				t.Fatal("root cursor did not end")
			}
			for _, scope := range []app.CommentReplyScope{app.CommentRepliesThread, app.CommentRepliesDirect} {
				cursor = ""
				for i, want := range wantReplies {
					page, err := service.ListReplies(testAccountContext(), app.ListRepliesInput{EntryID: postID, RootID: roots[0], Scope: scope, Sort: sort, Limit: 1, Cursor: cursor})
					if err != nil {
						t.Fatal(err)
					}
					if len(page.Items) != 1 || page.Items[0].Comment.ID != want {
						t.Fatalf("reply page %d = %+v", i, page)
					}
					if i == 0 {
						other := app.CommentNewest
						if sort == other {
							other = app.CommentOldest
						}
						if _, err := service.ListReplies(testAccountContext(), app.ListRepliesInput{EntryID: postID, RootID: roots[0], Scope: scope, Sort: other, Cursor: page.NextCursor}); !domain.HasCode(err, domain.CodeInvalidArgument) {
							t.Fatalf("cross-sort reply cursor: %v", err)
						}
					}
					cursor = page.NextCursor
				}
				if cursor != "" {
					t.Fatal("reply cursor did not end")
				}
			}
		})
	}
	for _, sort := range []app.CommentSort{"likes", "relevant"} {
		if _, err := service.ListPage(testAccountContext(), app.ListCommentsInput{EntryID: postID, Sort: sort}); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("unsupported sort: %v", err)
		}
	}
}
