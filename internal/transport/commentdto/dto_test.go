package commentdto

import (
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestCommentMediaReadProjectionCarriesPositiveDimensions(t *testing.T) {
	commentID, err := domain.ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAV")
	if err != nil {
		t.Fatal(err)
	}
	postID, err := domain.ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAW")
	if err != nil {
		t.Fatal(err)
	}
	mediaID, err := domain.ParseID("01ARZ3NDEKTSV4RRFFQ69G5FAX")
	if err != nil {
		t.Fatal(err)
	}
	wire := FromComment(app.Comment{
		Comment: domain.Comment{
			ID: commentID, EntryID: postID, AuthorKind: domain.CommentAuthorGuest,
			AuthorName: "Guest", ContentMD: "image comment", CreatedAt: time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC),
		},
		Author:      app.CommentAuthor{DisplayName: "Guest"},
		ContentHTML: "<p>image comment</p>\n",
		Media:       []app.MediaUsageProjection{{MediaID: mediaID, Position: 0, MIMEType: "image/jpeg", Width: 640, Height: 480}},
	})
	if len(wire.Media) != 1 || wire.Media[0].Width != 640 || wire.Media[0].Height != 480 {
		t.Fatalf("comment media = %#v", wire.Media)
	}
	if err := ValidateComment(wire); err != nil {
		t.Fatal(err)
	}
	wire.Media[0].Width = 0
	if err := ValidateComment(wire); err == nil {
		t.Fatal("accepted zero media width")
	}
}

// A legacy guest row has a name and nothing else; an owner row has the full
// reference set. Anything between the two would render a link to nowhere.
func TestCommentAuthorRejectsPartiallyResolvedIdentity(t *testing.T) {
	username := "cheems"
	userID := "0123456789abcdef0123456789abcdef"
	profileURL := "/cheems"
	if err := validateAuthor(Author{DisplayName: "Legacy visitor"}); err != nil {
		t.Fatalf("rejected a legacy guest identity: %v", err)
	}
	if err := validateAuthor(Author{UserID: &userID, Username: &username, DisplayName: "Cheems", ProfileURL: &profileURL}); err != nil {
		t.Fatalf("rejected a fully resolved identity: %v", err)
	}
	if err := validateAuthor(Author{Username: &username, DisplayName: "Cheems"}); err == nil {
		t.Fatal("accepted a username without a profile URL")
	}
	mismatched := "/someone-else"
	if err := validateAuthor(Author{UserID: &userID, Username: &username, DisplayName: "Cheems", ProfileURL: &mismatched}); err == nil {
		t.Fatal("accepted a profile URL that does not match the username")
	}
	if err := validateAuthor(Author{DisplayName: ""}); err == nil {
		t.Fatal("accepted an empty display name")
	}
}

// testComment builds one canonical comment so a validator test can vary the
// single field it is about.
func testComment(t *testing.T, id, postID, parentID string, createdAt time.Time) Comment {
	t.Helper()
	comment := Comment{
		ID: id, PostID: postID, AuthorKind: string(domain.CommentAuthorGuest), AuthorName: "Guest",
		Author: Author{DisplayName: "Guest"}, ContentMD: "body", ContentHTML: "<p>body</p>\n",
		CreatedAt: createdAt, Media: []MediaUsage{},
	}
	if parentID != "" {
		value := parentID
		comment.ParentID, comment.Depth = &value, 1
	}
	if err := ValidateComment(comment); err != nil {
		t.Fatalf("fixture comment %s is not canonical: %v", id, err)
	}
	return comment
}

// These validators are the CLI's only contract check in --api mode, so a
// property they miss is one the CLI prints as canonical. The browser parses
// the pinned conversation with the same expected post it parses the page with,
// which is the check missing here: the post-id loop walked items only, and a
// pinned conversation belonging to another post was accepted.
func TestValidateThreadTiesThePinnedConversationToTheThreadPost(t *testing.T) {
	postID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	otherPostID := "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	rootID := "01ARZ3NDEKTSV4RRFFQ69G5FB0"
	strangerID := "01ARZ3NDEKTSV4RRFFQ69G5FB1"
	created := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	root := testComment(t, rootID, postID, "", created)
	stranger := testComment(t, strangerID, otherPostID, "", created)

	valid := Thread{Items: []Comment{root}, PinnedCommentID: &rootID, Pinned: []Comment{root}}
	if err := ValidateThread(valid); err != nil {
		t.Fatalf("rejected a canonical thread: %v", err)
	}
	// A pinned root the server could not load leaves the selection with no
	// conversation beside it. ListPage emits exactly that, so it is legal.
	if err := ValidateThread(Thread{Items: []Comment{root}, PinnedCommentID: &strangerID}); err != nil {
		t.Fatalf("rejected an unloadable pinned selection: %v", err)
	}
	crossPost := Thread{Items: []Comment{root}, PinnedCommentID: &strangerID, Pinned: []Comment{stranger}}
	if err := ValidateThread(crossPost); err == nil {
		t.Fatal("accepted a pinned conversation from another post")
	}
	replyID := "01ARZ3NDEKTSV4RRFFQ69G5FB2"
	crossReply := Thread{
		Items:           []Comment{root},
		PinnedCommentID: &rootID,
		Pinned:          []Comment{root, testComment(t, replyID, otherPostID, rootID, created.Add(time.Second))},
	}
	if err := ValidateThread(crossReply); err == nil {
		t.Fatal("accepted a pinned reply from another post")
	}
	garbage := "not-a-comment-id"
	if err := ValidateThread(Thread{Items: []Comment{root}, PinnedCommentID: &garbage}); err == nil {
		t.Fatal("accepted a pinned_comment_id that is not a comment ID")
	}
}

// ValidateReplyPage dropped both whole-page checks its sibling performs, so a
// page mixing posts and a page repeating a reply — what concatenating two
// overlapping pages produces — were accepted while the browser rejected them.
func TestValidateReplyPageRejectsMixedPostsAndRepeatedReplies(t *testing.T) {
	postID := "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	otherPostID := "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	rootID := "01ARZ3NDEKTSV4RRFFQ69G5FB0"
	firstID := "01ARZ3NDEKTSV4RRFFQ69G5FB1"
	secondID := "01ARZ3NDEKTSV4RRFFQ69G5FB2"
	created := time.Date(2026, time.August, 10, 12, 0, 0, 0, time.UTC)
	first := testComment(t, firstID, postID, rootID, created)
	second := testComment(t, secondID, postID, rootID, created.Add(time.Second))

	if err := ValidateReplyPage(ReplyPage{Items: []Comment{first, second}}); err != nil {
		t.Fatalf("rejected a canonical reply page: %v", err)
	}
	stranger := testComment(t, secondID, otherPostID, rootID, created.Add(time.Second))
	if err := ValidateReplyPage(ReplyPage{Items: []Comment{first, stranger}}); err == nil {
		t.Fatal("accepted a reply page spanning two posts")
	}
	// Ordering alone does not catch a repeat: the second copy sorts after the
	// first, so only identity does.
	repeated := first
	repeated.CreatedAt = created.Add(2 * time.Second)
	if err := ValidateReplyPage(ReplyPage{Items: []Comment{first, second, repeated}}); err == nil {
		t.Fatal("accepted a reply page repeating one reply")
	}
}
