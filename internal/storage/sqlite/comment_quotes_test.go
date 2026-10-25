package sqlite

import (
	"context"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/postdto"
)

func TestCommentQuotesKeepIdentityVisibilityRetryAndDeletion(t *testing.T) {
	db, runtime := openTestDatabase(t, Options{})
	now := func() time.Time { return time.UnixMilli(1700000000000) }
	posts := newTestPostService(t, db, now, nil)
	comments := newTestCommentService(t, db, runtime, now, commentIDs(commentOne, commentTwo))
	alice := addContentTestAccount(t, db, "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "alice")
	bob := addContentTestAccount(t, db, "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", "bob")
	a := app.WithContentActor(context.Background(), alice)
	b := app.WithContentActor(context.Background(), bob)
	original, err := posts.Create(a, app.CreatePostInput{ContentMD: "original"})
	if err != nil {
		t.Fatal(err)
	}
	reply := mustAccountComment(t, comments, bob, app.CreateCommentInput{EntryID: original.Post.Entry.ID, ContentMD: "Bob **reply**"})
	target := reply.Comment.Comment.ID
	input := app.CreatePostInput{QuotedCommentID: &target, IdempotencyKey: "comment-quote-retry"}
	quote, err := posts.Create(a, input)
	if err != nil {
		t.Fatal(err)
	}
	assertQuote := func(post app.Post) {
		t.Helper()
		q := post.Quoted
		if q == nil || q.Unavailable || q.Comment == nil || q.Comment.ID != target || q.Author == nil || q.Author.UserID != bob.ID || q.Entry.ID != original.Post.Entry.ID || q.ContentHTML != reply.Comment.ContentHTML {
			t.Fatalf("quote = %+v", q)
		}
		if err := postdto.ValidatePost(postdto.FromPost(post)); err != nil {
			t.Fatal(err)
		}
	}
	assertQuote(quote.Post)
	replay, err := posts.Create(a, input)
	if err != nil || !replay.Replayed || replay.Post.Entry.ID != quote.Post.Entry.ID {
		t.Fatalf("replay=%+v %v", replay, err)
	}
	assertQuote(replay.Post)
	input.ContentMD = "changed"
	if _, err := posts.Create(a, input); !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("retry conflict=%v", err)
	}
	if _, err := posts.Create(a, app.CreatePostInput{ContentMD: "x", QuotedPostID: &original.Post.Entry.ID, QuotedCommentID: &target}); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("mixed targets=%v", err)
	}
	if _, err := posts.Update(a, app.UpdatePostInput{ID: original.Post.Entry.ID, PreserveContent: true, Visibility: ptrVisibility(domain.VisibilityOnlyMe)}); err != nil {
		t.Fatal(err)
	}
	if _, err := posts.Create(b, app.CreatePostInput{ContentMD: "x", QuotedCommentID: &target}); !domain.HasCode(err, domain.CodeCommentNotFound) {
		t.Fatalf("private quote=%v", err)
	}
	hidden, err := posts.Get(context.Background(), quote.Post.Entry.ID, app.PublicViewer())
	if err != nil || hidden.Quoted == nil || !hidden.Quoted.Unavailable {
		t.Fatalf("hidden=%+v %v", hidden, err)
	}
	assertQuoteMustRead := func() {
		p, err := posts.Get(a, quote.Post.Entry.ID, app.AccountViewer(alice.ID))
		if err != nil {
			t.Fatal(err)
		}
		assertQuote(p)
	}
	assertQuoteMustRead()
	if _, err := posts.Update(a, app.UpdatePostInput{ID: original.Post.Entry.ID, PreserveContent: true, Visibility: ptrVisibility(domain.VisibilityEveryone)}); err != nil {
		t.Fatal(err)
	}
	if _, err := comments.Delete(b, target, false); err != nil {
		t.Fatal(err)
	}
	tombstone, err := posts.Get(a, quote.Post.Entry.ID, app.AccountViewer(alice.ID))
	if err != nil || tombstone.Quoted == nil || !tombstone.Quoted.Unavailable {
		t.Fatalf("deleted=%+v %v", tombstone, err)
	}
	if err := postdto.ValidatePost(postdto.FromPost(tombstone)); err != nil {
		t.Fatal(err)
	}
	// Replaying a committed request after deletion returns its post and a tombstone.
	input.ContentMD = ""
	if p, err := posts.Create(a, input); err != nil || !p.Replayed || p.Post.Quoted == nil || !p.Post.Quoted.Unavailable {
		t.Fatalf("deleted replay=%+v %v", p, err)
	}
	second := mustAccountComment(t, comments, bob, app.CreateCommentInput{EntryID: original.Post.Entry.ID, ContentMD: "second"})
	q2, err := posts.Create(a, app.CreatePostInput{ContentMD: "with text", QuotedCommentID: &second.Comment.Comment.ID})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := posts.Delete(a, original.Post.Entry.ID); err != nil {
		t.Fatal(err)
	}
	p, err := posts.Get(a, q2.Post.Entry.ID, app.PublicViewer())
	if err != nil || p.Quoted == nil || !p.Quoted.Unavailable {
		t.Fatalf("deleted original=%+v %v", p, err)
	}
}

func ptrVisibility(value domain.Visibility) *domain.Visibility { return &value }

func TestLegacyCommentQuotesKeepTheirStoredSignature(t *testing.T) {
	db, _ := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "host")
	_, err := db.write.ExecContext(context.Background(), `INSERT INTO comments (id,entry_id,parent_id,author_name,content_md,created_at_ms) VALUES (?,?,NULL,?,?,?)`, commentOne, entryOne, "Former reader", "legacy words", 1700000000000)
	if err != nil {
		t.Fatal(err)
	}
	posts := newTestPostService(t, db, func() time.Time { return time.UnixMilli(1700000000000) }, nil)
	target := domain.ID(commentOne)
	quoted, err := posts.Create(testAccountContext(), app.CreatePostInput{ContentMD: "reader discussion", QuotedCommentID: &target})
	if err != nil {
		t.Fatal(err)
	}
	wire := postdto.FromPost(quoted.Post)
	if err := postdto.ValidatePost(wire); err != nil {
		t.Fatal(err)
	}
	if wire.QuotedPost.AuthorInfo != nil || wire.QuotedPost.CommentAuthorName == nil || *wire.QuotedPost.CommentAuthorName != "Former reader" {
		t.Fatalf("legacy=%+v", wire.QuotedPost)
	}
}
