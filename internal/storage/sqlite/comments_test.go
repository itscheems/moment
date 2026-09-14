package sqlite

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/runtimefs"
)

const (
	commentOne   = "01ARZ3NDEKTSV4RRFFQ69G5FB0"
	commentTwo   = "01ARZ3NDEKTSV4RRFFQ69G5FB1"
	commentThree = "01ARZ3NDEKTSV4RRFFQ69G5FB2"
	commentFour  = "01ARZ3NDEKTSV4RRFFQ69G5FB3"
)

func TestCommentServiceOneLevelOrderingIdentityAndCascade(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	base := time.UnixMilli(1_700_000_000_000)
	clock := newSequenceClock(base, base.Add(3*time.Second), base.Add(time.Second), base.Add(2*time.Second))
	service := newTestCommentService(t, db, runtimeState, clock.Now, commentIDs(commentOne, commentTwo, commentThree, commentFour))
	author := testCommentAuthor(t, db, runtimeState)
	postID, _ := domain.ParseID(entryOne)

	rootOne := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, AuthorName: "  Impostor  ", ContentMD: "first **root**"})
	replyOne := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &rootOne.Comment.Comment.ID, ContentMD: "latest reply"})
	rootTwo := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "second root"})
	replyTwo := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &rootTwo.Comment.Comment.ID, ContentMD: "earlier reply"})

	thread, err := service.List(context.Background(), postID, app.OwnerViewer())
	if err != nil {
		t.Fatal(err)
	}
	want := []domain.ID{rootOne.Comment.Comment.ID, replyOne.Comment.Comment.ID, rootTwo.Comment.Comment.ID, replyTwo.Comment.Comment.ID}
	if len(thread.Items) != len(want) {
		t.Fatalf("thread = %+v", thread)
	}
	for index, item := range thread.Items {
		if item.Comment.ID != want[index] {
			t.Fatalf("item %d = %s, want %s", index, item.Comment.ID, want[index])
		}
	}
	// A caller-supplied author name is not an identity. CreateOwner overwrites
	// it, so no adapter can sign a comment as anybody but the owner.
	// A comment names the account that wrote it. Stamping the site owner here
	// was correct while the owner was the only account that could exist; the
	// moment a second one could, it meant a member's words were published
	// under someone else's name.
	if rootOne.Comment.Comment.AuthorUserID == nil || *rootOne.Comment.Comment.AuthorUserID != author.ID {
		t.Fatalf("comment author = %v, want the writing account %s", rootOne.Comment.Comment.AuthorUserID, author.ID)
	}
	if rootOne.Comment.Comment.AuthorKind != domain.CommentAuthorOwner || rootOne.Comment.Comment.AuthorName != author.DisplayName ||
		replyOne.Comment.Comment.AuthorKind != domain.CommentAuthorOwner || replyOne.Comment.Comment.AuthorName != author.DisplayName ||
		!strings.Contains(rootOne.Comment.ContentHTML, "<strong>root</strong>") {
		t.Fatalf("identity/rendering root=%+v reply=%+v", rootOne, replyOne)
	}

	preview, err := service.Delete(context.Background(), rootOne.Comment.Comment.ID, true)
	if err != nil || preview.RepliesAffected != 1 || preview.Deleted || !preview.DryRun {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	deleted, err := service.Delete(context.Background(), rootOne.Comment.Comment.ID, false)
	if err != nil || deleted.RepliesAffected != 1 || !deleted.Deleted || deleted.DryRun {
		t.Fatalf("delete = %+v, %v", deleted, err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM comments WHERE id IN (?, ?)`, 0, commentOne, commentTwo)

	postService := newTestPostService(t, db, func() time.Time { return base }, nil)
	if _, err := postService.Delete(context.Background(), postID); err != nil {
		t.Fatal(err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM comments`, 0)
	if _, err := service.List(context.Background(), postID, app.OwnerViewer()); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("deleted post list = %v", err)
	}
}

func TestCommentPinIsSingletonRootScopedAndCascadesWithDeletion(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "one")
	insertEntry(t, db, entryTwo, "two")
	base := time.UnixMilli(1_700_000_000_000)
	service := newTestCommentService(t, db, runtimeState, func() time.Time { return base }, commentIDs(commentOne, commentTwo, commentThree, commentFour))
	author := testCommentAuthor(t, db, runtimeState)
	one, _ := domain.ParseID(entryOne)
	two, _ := domain.ParseID(entryTwo)
	rootOne := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: one, ContentMD: "first"})
	reply := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: one, ParentID: &rootOne.Comment.Comment.ID, ContentMD: "reply"})
	rootTwo := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: one, ContentMD: "second"})
	otherPostRoot := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: two, ContentMD: "other"})

	pinned, err := service.Pin(context.Background(), one, rootOne.Comment.Comment.ID)
	if err != nil || pinned.PinnedCommentID == nil || *pinned.PinnedCommentID != rootOne.Comment.Comment.ID {
		t.Fatalf("pin first root = %+v, %v", pinned, err)
	}
	pinned, err = service.Pin(context.Background(), one, rootTwo.Comment.Comment.ID)
	if err != nil || pinned.PinnedCommentID == nil || *pinned.PinnedCommentID != rootTwo.Comment.Comment.ID {
		t.Fatalf("replace pinned root = %+v, %v", pinned, err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM comment_pins WHERE entry_id = ? AND comment_id = ?`, 1, entryOne, commentThree)
	thread, err := service.List(context.Background(), one, app.OwnerViewer())
	if err != nil || thread.PinnedCommentID == nil || *thread.PinnedCommentID != rootTwo.Comment.Comment.ID {
		t.Fatalf("listed pin = %+v, %v", thread, err)
	}

	if _, err := service.Pin(context.Background(), one, reply.Comment.Comment.ID); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("pin reply = %v", err)
	}
	if _, err := service.Pin(context.Background(), one, otherPostRoot.Comment.Comment.ID); !domain.HasCode(err, domain.CodeCommentNotFound) {
		t.Fatalf("pin cross-post root = %v", err)
	}
	if _, err := service.Unpin(context.Background(), one, rootOne.Comment.Comment.ID); !domain.HasCode(err, domain.CodeCommentNotFound) {
		t.Fatalf("unpin non-selected root = %v", err)
	}
	if _, err := service.Unpin(context.Background(), one, rootTwo.Comment.Comment.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Unpin(context.Background(), one, rootTwo.Comment.Comment.ID); err != nil {
		t.Fatalf("idempotent unpin = %v", err)
	}

	if _, err := service.Pin(context.Background(), one, rootOne.Comment.Comment.ID); err != nil {
		t.Fatal(err)
	}
	deleted, err := service.Delete(context.Background(), rootOne.Comment.Comment.ID, false)
	if err != nil || !deleted.Deleted || deleted.RepliesAffected != 1 {
		t.Fatalf("delete pinned conversation = %+v, %v", deleted, err)
	}
	thread, err = service.List(context.Background(), one, app.OwnerViewer())
	if err != nil || thread.PinnedCommentID != nil || len(thread.Items) != 1 || thread.Items[0].Comment.ID != rootTwo.Comment.Comment.ID {
		t.Fatalf("thread after pinned cascade = %+v, %v", thread, err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM comment_pins`, 0)
}

func TestCommentLocationOnlyRoundTripsAndParticipatesInIdempotency(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	postID, _ := domain.ParseID(entryOne)
	service := newTestCommentService(t, db, runtimeState, func() time.Time { return time.UnixMilli(1_700_000_000_000) }, commentIDs(commentOne))
	author := testCommentAuthor(t, db, runtimeState)
	location := &domain.Location{
		Name: "BANANA+香蕉攀岩", DisplayName: "天目西路218号嘉里合集B1B103B104",
		CountryCode: "CN", Country: "中国", Region: "上海市", City: "上海市",
		District: "静安区", Category: "体育场馆", Provider: "amap", ProviderPlaceID: "B0TEST",
		Point: &domain.GeoPoint{LatitudeE7: 312231860, LongitudeE7: 1214452600, System: domain.CoordinateGCJ02},
	}

	created, err := service.Create(context.Background(), app.CreateCommentInput{Author: author,
		EntryID: postID, Location: location, IdempotencyKey: "located-comment",
	})
	if err != nil || created.Comment.Comment.ContentMD != "" || created.Comment.ContentHTML != "" ||
		created.Comment.Comment.Location == nil || created.Comment.Comment.Location.DisplayName != location.DisplayName {
		t.Fatalf("created location comment = %+v, %v", created, err)
	}
	location.Name = "caller mutation"
	thread, err := service.List(context.Background(), postID, app.OwnerViewer())
	if err != nil || len(thread.Items) != 1 || thread.Items[0].Comment.Location == nil || thread.Items[0].Comment.Location.Name != "BANANA+香蕉攀岩" {
		t.Fatalf("stored location comment = %+v, %v", thread, err)
	}
	replayLocation := domain.CloneLocation(thread.Items[0].Comment.Location)
	replayed, err := service.Create(context.Background(), app.CreateCommentInput{Author: author, EntryID: postID, Location: replayLocation, IdempotencyKey: "located-comment"})
	if err != nil || !replayed.Replayed || replayed.Comment.Comment.Location == nil {
		t.Fatalf("location replay = %+v, %v", replayed, err)
	}
	replayLocation.DisplayName = "different"
	if _, err := service.Create(context.Background(), app.CreateCommentInput{Author: author, EntryID: postID, Location: replayLocation, IdempotencyKey: "located-comment"}); !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("location conflict = %v", err)
	}

	if _, err := service.Delete(context.Background(), created.Comment.Comment.ID, false); err != nil {
		t.Fatal(err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM comment_location`, 0)
}

func TestCommentServiceRejectsDepthCrossPostUnsafeContentAndDisabledWrites(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "one")
	insertEntry(t, db, entryTwo, "two")
	base := time.UnixMilli(1_700_000_000_000)
	service := newTestCommentService(t, db, runtimeState, func() time.Time { return base }, commentIDs(commentOne, commentTwo, commentThree, commentFour))
	author := testCommentAuthor(t, db, runtimeState)
	one, _ := domain.ParseID(entryOne)
	two, _ := domain.ParseID(entryTwo)
	root := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: one, ContentMD: "root"})
	reply := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: one, ParentID: &root.Comment.Comment.ID, ContentMD: "reply"})
	if _, err := service.Create(context.Background(), app.CreateCommentInput{Author: author, EntryID: one, ParentID: &reply.Comment.Comment.ID, ContentMD: "deep"}); !domain.HasCode(err, domain.CodeCommentDepthExceeded) {
		t.Fatalf("deep = %v", err)
	}
	if _, err := service.Create(context.Background(), app.CreateCommentInput{Author: author, EntryID: two, ParentID: &root.Comment.Comment.ID, ContentMD: "cross"}); !domain.HasCode(err, domain.CodeCommentDepthExceeded) {
		t.Fatalf("cross = %v", err)
	}
	missing, _ := domain.ParseID(commentFour)
	if _, err := service.Create(context.Background(), app.CreateCommentInput{Author: author, EntryID: one, ParentID: &missing, ContentMD: "missing"}); !domain.HasCode(err, domain.CodeCommentNotFound) {
		t.Fatalf("missing = %v", err)
	}
	for _, test := range []struct {
		content string
		code    domain.Code
	}{
		{content: "<b>raw</b>", code: domain.CodeInvalidArgument},
		{content: "![external](https://example.test/a.png)", code: domain.CodeExternalImageNotAllowed},
		{content: strings.Repeat("界", domain.MaxCommentContentCodePoints+1), code: domain.CodeContentTooLarge},
	} {
		if _, err := service.Create(context.Background(), app.CreateCommentInput{Author: author, EntryID: one, ContentMD: test.content}); !domain.HasCode(err, test.code) {
			t.Fatalf("unsafe input = %v, want %s", err, test.code)
		}
	}
	mustWrite(t, db, `UPDATE site_settings SET comments_enabled = 0 WHERE id = 1`)
	if _, err := service.Create(context.Background(), app.CreateCommentInput{Author: author, EntryID: one, ContentMD: "closed"}); !domain.HasCode(err, domain.CodeCommentsDisabled) {
		t.Fatalf("disabled = %v", err)
	}
	thread, err := service.List(context.Background(), one, app.OwnerViewer())
	if err != nil || len(thread.Items) != 2 {
		t.Fatalf("closed read = %+v, %v", thread, err)
	}

	assertWriteRejected(t, db, `
INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms)
VALUES (?, ?, ?, 'owner', 'Bypass', 'deep', ?)`, commentThree, entryOne, commentTwo, base.Add(time.Second).UnixMilli())
	assertWriteRejected(t, db, `
INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms)
VALUES (?, ?, ?, 'owner', 'Bypass', 'cross', ?)`, commentThree, entryTwo, commentOne, base.Add(time.Second).UnixMilli())
}

func TestCommentIdempotencyConcurrentReplayAndCancellation(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "one")
	postID, _ := domain.ParseID(entryOne)
	// One identifier is the whole budget: every later call in this test either
	// replays the first insert or refuses to write, so a second allocation
	// exhausts the sequence and fails loudly.
	service := newTestCommentService(t, db, runtimeState, func() time.Time { return time.UnixMilli(1_700_000_000_000) }, commentIDs(commentOne))
	author := testCommentAuthor(t, db, runtimeState)
	const workers = 16
	results := make(chan app.CreateCommentResult, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := service.Create(context.Background(), app.CreateCommentInput{Author: author, EntryID: postID, ContentMD: "retry", IdempotencyKey: "comment-retry"})
			if err != nil {
				errs <- err
				return
			}
			results <- result
		}()
	}
	group.Wait()
	close(results)
	close(errs)
	for err := range errs {
		t.Errorf("concurrent create: %v", err)
	}
	replays, count := 0, 0
	for result := range results {
		count++
		if result.Replayed {
			replays++
		}
		if result.Comment.Comment.ID.String() != commentOne {
			t.Errorf("id = %s", result.Comment.Comment.ID)
		}
	}
	if count != workers || replays != workers-1 {
		t.Fatalf("count=%d replay=%d", count, replays)
	}
	if _, err := service.Create(context.Background(), app.CreateCommentInput{Author: author, EntryID: postID, ContentMD: "changed", IdempotencyKey: "comment-retry"}); !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("conflict = %v", err)
	}
	if _, err := service.Create(context.Background(), app.CreateCommentInput{Author: author, EntryID: postID, ContentMD: " ", IdempotencyKey: "comment-retry"}); !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("invalid conflicting replay = %v", err)
	}
	originalID, _ := domain.ParseID(commentOne)
	if _, err := service.Delete(context.Background(), originalID, false); err != nil {
		t.Fatal(err)
	}
	replayedAfterDelete, err := service.Create(context.Background(), app.CreateCommentInput{Author: author, EntryID: postID, ContentMD: "retry", IdempotencyKey: "comment-retry"})
	if err != nil || !replayedAfterDelete.Replayed || replayedAfterDelete.Comment.Comment.ID != originalID {
		t.Fatalf("replay after delete = %+v, %v", replayedAfterDelete, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.Create(cancelled, app.CreateCommentInput{Author: author, EntryID: postID, ContentMD: "cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled create = %v", err)
	}
	// The replay reports the deleted comment without resurrecting it, and the
	// cancelled create wrote nothing, so the table stays empty.
	assertCount(t, db.read, `SELECT count(*) FROM comments`, 0)
	if _, err := service.List(cancelled, postID, app.OwnerViewer()); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled = %v", err)
	}
}

// TestCommentServiceReadsLegacyGuestRowsWrittenBeforeAccountsWereRequired keeps
// the read half of the retired anonymous path honest. Nothing writes a guest
// row any more, but every archive taken before commenting required an account
// is full of them, and an archive that will not render is a backup that does
// not exist. The row therefore has to go in through SQL, with no account
// reference, which is exactly the shape a real legacy row has.
func TestCommentServiceReadsLegacyGuestRowsWrittenBeforeAccountsWereRequired(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	base := time.UnixMilli(1_700_000_000_000)
	service := newTestCommentService(t, db, runtimeState, func() time.Time { return base.Add(time.Hour) }, commentIDs(commentTwo))
	author := testCommentAuthor(t, db, runtimeState)
	postID, _ := domain.ParseID(entryOne)
	const legacyName = "访客"
	mustWrite(t, db, `
INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms)
VALUES (?, ?, NULL, 'guest', ?, 'left **before** accounts existed', ?)`, commentOne, entryOne, legacyName, base.UnixMilli())
	legacyRootID, _ := domain.ParseID(commentOne)
	reply := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &legacyRootID, ContentMD: "answered later"})

	thread, err := service.List(context.Background(), postID, app.OwnerViewer())
	if err != nil || len(thread.Items) != 2 {
		t.Fatalf("thread = %+v, %v", thread, err)
	}
	legacy := thread.Items[0]
	if legacy.Comment.ID != legacyRootID || legacy.Comment.AuthorKind != domain.CommentAuthorGuest ||
		legacy.Comment.AuthorName != legacyName || legacy.Comment.ParentID != nil ||
		!strings.Contains(legacy.ContentHTML, "<strong>before</strong>") {
		t.Fatalf("legacy guest row = %+v", legacy)
	}
	if thread.Items[1].Comment.ID != reply.Comment.Comment.ID || thread.Items[1].Comment.AuthorKind != domain.CommentAuthorOwner ||
		thread.Items[1].Comment.ParentID == nil || *thread.Items[1].Comment.ParentID != legacyRootID {
		t.Fatalf("owner reply to a legacy root = %+v", thread.Items[1])
	}

	// A legacy root is still moderatable, and still takes its replies with it.
	deleted, err := service.Delete(context.Background(), legacyRootID, false)
	if err != nil || !deleted.Deleted || deleted.RepliesAffected != 1 || deleted.Comment.Comment.AuthorName != legacyName {
		t.Fatalf("delete legacy root = %+v, %v", deleted, err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM comments`, 0)
}

func newTestCommentService(t *testing.T, db *Database, runtimeState *runtimefs.Runtime, clock app.CommentClock, ids app.CommentIDGenerator) *app.CommentService {
	t.Helper()
	repository, err := NewCommentRepository(db, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewCommentService(repository, app.WithCommentClock(clock), app.WithCommentIDGenerator(ids))
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func commentIDs(values ...string) app.CommentIDGenerator {
	var mu sync.Mutex
	index := 0
	return func(time.Time) (domain.ID, error) {
		mu.Lock()
		defer mu.Unlock()
		if index >= len(values) {
			return "", errors.New("test comment ID sequence exhausted")
		}
		id, err := domain.ParseID(values[index])
		index++
		return id, err
	}
}

func mustOwnerComment(t *testing.T, service *app.CommentService, author domain.User, input app.CreateCommentInput) app.CreateCommentResult {
	t.Helper()
	input.Author = author
	result, err := service.Create(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return result
}

// A reply names the comment it answers so a reader can tell which of several
// sibling replies it is responding to. The annotation is deliberately separate
// from parent_id: it takes no part in depth, ordering or cascade, so the
// one-level rule and its four enforcement sites stay untouched.
func TestCommentReplyTargetStaysWithinItsConversation(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	insertEntry(t, db, entryTwo, "post")
	base := time.UnixMilli(1_700_000_000_000)
	clock := newSequenceClock(base, base.Add(time.Second), base.Add(2*time.Second), base.Add(3*time.Second), base.Add(4*time.Second))
	service := newTestCommentService(t, db, runtimeState, clock.Now,
		commentIDs(commentOne, commentTwo, commentThree, commentFour, "01ARZ3NDEKTSV4RRFFQ69G5FB4"))
	author := testCommentAuthor(t, db, runtimeState)
	postID, _ := domain.ParseID(entryOne)
	otherPostID, _ := domain.ParseID(entryTwo)

	root := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "root"})
	rootID := root.Comment.Comment.ID
	first := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &rootID, ContentMD: "first reply"})
	firstID := first.Comment.Comment.ID

	answeringSibling := mustOwnerComment(t, service, author, app.CreateCommentInput{
		EntryID: postID, ParentID: &rootID, ReplyToCommentID: &firstID, ContentMD: "answering the sibling",
	})
	if answeringSibling.Comment.ReplyTo == nil || answeringSibling.Comment.ReplyTo.CommentID != firstID {
		t.Fatalf("reply target = %#v", answeringSibling.Comment.ReplyTo)
	}
	// The target's identity is resolved server side because the target can sit
	// outside whatever page the reader has loaded.
	if answeringSibling.Comment.ReplyTo.Author.Username == "" {
		t.Fatalf("reply target author = %#v", answeringSibling.Comment.ReplyTo.Author)
	}

	otherRoot := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "unrelated root"})
	otherRootID := otherRoot.Comment.Comment.ID
	otherPostRoot := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: otherPostID, ContentMD: "other post root"})
	otherPostRootID := otherPostRoot.Comment.Comment.ID

	for name, target := range map[string]*domain.ID{
		"another root in the same post": &otherRootID,
		"a comment on another post":     &otherPostRootID,
	} {
		_, err := service.Create(context.Background(), app.CreateCommentInput{Author: author,
			EntryID: postID, ParentID: &rootID, ReplyToCommentID: target, ContentMD: "out of conversation",
		})
		if !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("%s = %v", name, err)
		}
	}
	if _, err := service.Create(context.Background(), app.CreateCommentInput{Author: author,
		EntryID: postID, ReplyToCommentID: &firstID, ContentMD: "root answering a reply",
	}); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("root with a reply target = %v", err)
	}

	// Deleting the answered comment clears the annotation without touching the
	// reply that carried it, so a non-null reference always points at a row
	// that exists and no reader has to handle a dangling target.
	if _, err := service.Delete(context.Background(), firstID, false); err != nil {
		t.Fatal(err)
	}
	thread, err := service.List(context.Background(), postID, app.OwnerViewer())
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, comment := range thread.Items {
		if comment.Comment.ID != answeringSibling.Comment.Comment.ID {
			continue
		}
		found = true
		if comment.Comment.ReplyToCommentID != nil || comment.ReplyTo != nil {
			t.Fatalf("reply target survived its deleted comment: %#v", comment.Comment.ReplyToCommentID)
		}
	}
	if !found {
		t.Fatal("the reply was deleted along with the comment it answered")
	}
}

// The reply target is part of what a create request asks for, so it has to be
// part of what the idempotency digest covers. Without it one key would replay a
// stored comment for a request naming a different target and return a
// well-formed answer to the wrong comment.
func TestCommentIdempotencyDistinguishesReplyTargets(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	base := time.UnixMilli(1_700_000_000_000)
	clock := newSequenceClock(base, base.Add(time.Second), base.Add(2*time.Second), base.Add(3*time.Second))
	service := newTestCommentService(t, db, runtimeState, clock.Now, commentIDs(commentOne, commentTwo, commentThree, commentFour))
	author := testCommentAuthor(t, db, runtimeState)
	postID, _ := domain.ParseID(entryOne)

	root := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "root"})
	rootID := root.Comment.Comment.ID
	first := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &rootID, ContentMD: "first"})
	firstID := first.Comment.Comment.ID

	original := mustOwnerComment(t, service, author, app.CreateCommentInput{
		EntryID: postID, ParentID: &rootID, ReplyToCommentID: &firstID,
		ContentMD: "same body", IdempotencyKey: "reply-target",
	})
	replayed, err := service.Create(context.Background(), app.CreateCommentInput{Author: author,
		EntryID: postID, ParentID: &rootID, ReplyToCommentID: &firstID,
		ContentMD: "same body", IdempotencyKey: "reply-target",
	})
	if err != nil || !replayed.Replayed || replayed.Comment.Comment.ID != original.Comment.Comment.ID {
		t.Fatalf("identical retry = %+v, %v", replayed, err)
	}
	if replayed.Comment.ReplyTo == nil || replayed.Comment.ReplyTo.CommentID != firstID {
		t.Fatalf("replayed reply target = %#v", replayed.Comment.ReplyTo)
	}
	if _, err := service.Create(context.Background(), app.CreateCommentInput{Author: author,
		EntryID: postID, ParentID: &rootID, ReplyToCommentID: &rootID,
		ContentMD: "same body", IdempotencyKey: "reply-target",
	}); !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("retry naming a different target = %v", err)
	}
}

// The service check is defence in depth over a database trigger, matching how
// the one-level rule is guarded. A writer that bypasses the service entirely
// still cannot record an out-of-conversation target.
func TestCommentReplyTargetTriggerRejectsRawWrites(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	base := time.UnixMilli(1_700_000_000_000)
	clock := newSequenceClock(base, base.Add(time.Second), base.Add(2*time.Second))
	service := newTestCommentService(t, db, runtimeState, clock.Now, commentIDs(commentOne, commentTwo, commentThree))
	author := testCommentAuthor(t, db, runtimeState)
	postID, _ := domain.ParseID(entryOne)
	root := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "root"})
	other := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "unrelated root"})

	err := db.WriteTx(context.Background(), func(queries Queries) error {
		_, execErr := queries.ExecContext(context.Background(), `
INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms, reply_to_comment_id)
VALUES (?, ?, ?, 'owner', 'Owner', 'raw', 9, ?)`,
			commentFour, entryOne, root.Comment.Comment.ID.String(), other.Comment.Comment.ID.String())
		return execErr
	})
	if err == nil || !strings.Contains(err.Error(), "comment reply target must be in the same conversation") {
		t.Fatalf("raw out-of-conversation target = %v", err)
	}

	selfErr := db.WriteTx(context.Background(), func(queries Queries) error {
		_, execErr := queries.ExecContext(context.Background(), `
INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms, reply_to_comment_id)
VALUES (?, ?, ?, 'owner', 'Owner', 'raw self', 9, ?)`,
			commentFour, entryOne, root.Comment.Comment.ID.String(), commentFour)
		return execErr
	})
	if selfErr == nil || !strings.Contains(selfErr.Error(), "comment reply target must be in the same conversation") {
		t.Fatalf("raw self-referencing target = %v", selfErr)
	}
}

// Comments page oldest-first, the opposite direction from the timeline, so the
// seek predicate walks forward. Two comments sharing a millisecond are what
// separate a correct predicate from one that compares only the timestamp: the
// wrong version either repeats the pair forever or skips one of them, and
// neither failure is visible with distinct timestamps.
func TestCommentRootPagingWalksForwardThroughATie(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	shared := time.UnixMilli(1_700_000_000_000)
	clock := newSequenceClock(shared, shared, shared.Add(time.Second), shared.Add(2*time.Second))
	service := newTestCommentService(t, db, runtimeState, clock.Now, commentIDs(commentOne, commentTwo, commentThree, commentFour))
	author := testCommentAuthor(t, db, runtimeState)
	postID, _ := domain.ParseID(entryOne)

	for _, content := range []string{"first", "second", "third", "fourth"} {
		mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: content})
	}

	seen := make([]string, 0, 4)
	cursor := ""
	for page := 0; page < 6; page++ {
		thread, err := service.ListPage(context.Background(), app.ListCommentsInput{EntryID: postID, Cursor: cursor, Limit: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(thread.Items) != 1 {
			t.Fatalf("page %d returned %d comments", page, len(thread.Items))
		}
		seen = append(seen, thread.Items[0].Comment.ContentMD)
		cursor = thread.NextCursor
		if cursor == "" {
			break
		}
	}
	want := []string{"first", "second", "third", "fourth"}
	if len(seen) != len(want) {
		t.Fatalf("paged comments = %v", seen)
	}
	for index, content := range want {
		if seen[index] != content {
			t.Fatalf("paged comments = %v, want %v", seen, want)
		}
	}
	if cursor != "" {
		t.Fatalf("cursor survived the final page: %q", cursor)
	}
}

// A cursor names the thread it was issued for, so one minted for a different
// post or a different root is refused rather than silently answering with
// someone else's page.
func TestCommentCursorsRefuseAnotherScope(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	insertEntry(t, db, entryTwo, "post")
	base := time.UnixMilli(1_700_000_000_000)
	clock := newSequenceClock(base, base.Add(time.Second), base.Add(2*time.Second), base.Add(3*time.Second), base.Add(4*time.Second))
	service := newTestCommentService(t, db, runtimeState, clock.Now,
		commentIDs(commentOne, commentTwo, commentThree, commentFour, "01ARZ3NDEKTSV4RRFFQ69G5FB4"))
	author := testCommentAuthor(t, db, runtimeState)
	postID, _ := domain.ParseID(entryOne)
	otherPostID, _ := domain.ParseID(entryTwo)

	rootOne := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "root one"})
	mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "root two"})
	mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: otherPostID, ContentMD: "other post root"})
	rootID := rootOne.Comment.Comment.ID
	mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &rootID, ContentMD: "reply one"})
	mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &rootID, ContentMD: "reply two"})

	page, err := service.ListPage(context.Background(), app.ListCommentsInput{EntryID: postID, Limit: 1})
	if err != nil || page.NextCursor == "" {
		t.Fatalf("first page = %+v, %v", page, err)
	}
	if _, err := service.ListPage(context.Background(), app.ListCommentsInput{EntryID: otherPostID, Cursor: page.NextCursor}); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("root cursor replayed on another post = %v", err)
	}

	replies, err := service.ListReplies(context.Background(), app.ListRepliesInput{EntryID: postID, RootID: rootID, Limit: 1})
	if err != nil || replies.NextCursor == "" {
		t.Fatalf("first reply page = %+v, %v", replies, err)
	}
	// A reply cursor names its root, so replaying it under a different one is
	// refused. The root itself is also checked against the post in the URL:
	// without that, any thread could be paged through any post's address.
	secondPage, err := service.ListPage(context.Background(), app.ListCommentsInput{EntryID: postID, Cursor: page.NextCursor, Limit: 1})
	if err != nil || len(secondPage.Items) == 0 {
		t.Fatalf("second page = %+v, %v", secondPage, err)
	}
	otherRootID := secondPage.Items[0].Comment.ID
	if _, err := service.ListReplies(context.Background(), app.ListRepliesInput{EntryID: postID, RootID: otherRootID, Cursor: replies.NextCursor}); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("reply cursor replayed under another root = %v", err)
	}
	if _, err := service.ListReplies(context.Background(), app.ListRepliesInput{EntryID: otherPostID, RootID: rootID}); !domain.HasCode(err, domain.CodeCommentNotFound) {
		t.Fatalf("replies asked for through the wrong post = %v", err)
	}
	for _, bad := range []string{"not-base64!", "eyJ2IjoxfQ"} {
		if _, err := service.ListPage(context.Background(), app.ListCommentsInput{EntryID: postID, Cursor: bad}); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("cursor %q = %v", bad, err)
		}
	}
}

// A root reports how many replies it has, not how many travelled with it, and
// the pinned conversation arrives beside the page rather than reordered into
// it so the cursor keeps meaning the same thing when the pin moves.
func TestCommentPageReportsTotalsAndCarriesThePinnedConversation(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	base := time.UnixMilli(1_700_000_000_000)
	times := make([]time.Time, 0, 9)
	ids := make([]string, 0, 9)
	for index := 0; index < 9; index++ {
		times = append(times, base.Add(time.Duration(index)*time.Second))
		ids = append(ids, "01ARZ3NDEKTSV4RRFFQ69G5F"+string(rune('A'+index))+"0")
	}
	clock := newSequenceClock(times...)
	service := newTestCommentService(t, db, runtimeState, clock.Now, commentIDs(ids...))
	author := testCommentAuthor(t, db, runtimeState)
	postID, _ := domain.ParseID(entryOne)

	root := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "busy root"})
	rootID := root.Comment.Comment.ID
	for index := 0; index < 5; index++ {
		mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &rootID, ContentMD: "reply"})
	}
	later := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "later root"})

	thread, err := service.ListPage(context.Background(), app.ListCommentsInput{EntryID: postID})
	if err != nil {
		t.Fatal(err)
	}
	inlineReplies := 0
	for _, comment := range thread.Items {
		if comment.Comment.ParentID != nil {
			inlineReplies++
			continue
		}
		if comment.Comment.ID != rootID {
			continue
		}
		if comment.ReplyCount != 5 {
			t.Fatalf("reply count = %d, want the true total 5", comment.ReplyCount)
		}
		if comment.RepliesNextCursor == "" {
			t.Fatal("a root with more replies than the preview reported no continuation")
		}
	}
	if inlineReplies != app.InlineReplyPreviewCount {
		t.Fatalf("inline replies = %d, want %d", inlineReplies, app.InlineReplyPreviewCount)
	}

	if _, err := service.Pin(context.Background(), postID, later.Comment.Comment.ID); err != nil {
		t.Fatal(err)
	}
	pinnedThread, err := service.ListPage(context.Background(), app.ListCommentsInput{EntryID: postID, Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(pinnedThread.Pinned) == 0 || pinnedThread.Pinned[0].Comment.ID != later.Comment.Comment.ID {
		t.Fatalf("pinned conversation = %#v", pinnedThread.Pinned)
	}
	// The stream itself is untouched by the pin: the first page is still the
	// oldest root, not the pinned one.
	if len(pinnedThread.Items) == 0 || pinnedThread.Items[0].Comment.ID != rootID {
		t.Fatalf("pinning reordered the page: %#v", pinnedThread.Items)
	}
}

// The page's reply previews are cut per root inside one query. The loop it
// replaced put a query behind every row on the page, which at the maximum page
// size is a hundred round trips for a single read. What must not change is the
// answer: the same oldest replies, in the same order, with the same extra row
// that tells the caller more remain.
func TestReplyPreviewsMatchPerRootPaging(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	base := time.UnixMilli(1_700_000_000_000)
	// Crockford base32: I, L, O and U are not in the ULID alphabet.
	const alphabet = "0123456789ABCDEFGHJKMNPQ"
	times := make([]time.Time, 0, 12)
	ids := make([]string, 0, 12)
	for index := 0; index < 12; index++ {
		times = append(times, base.Add(time.Duration(index)*time.Second))
		ids = append(ids, "01ARZ3NDEKTSV4RRFFQ69G5G"+string(alphabet[index])+"0")
	}
	service := newTestCommentService(t, db, runtimeState, newSequenceClock(times...).Now, commentIDs(ids...))
	author := testCommentAuthor(t, db, runtimeState)
	postID, _ := domain.ParseID(entryOne)

	busy := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "busy"})
	busyID := busy.Comment.Comment.ID
	for index := 0; index < 6; index++ {
		mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &busyID, ContentMD: "busy reply"})
	}
	quiet := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "quiet"})
	quietID := quiet.Comment.Comment.ID
	mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ParentID: &quietID, ContentMD: "only reply"})
	bare := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "bare"})
	bareID := bare.Comment.Comment.ID

	if err := db.ReadTx(context.Background(), func(queries Queries) error {
		store := newCommentStore(queries)
		roots := []domain.ID{busyID, quietID, bareID}
		limit := app.InlineReplyPreviewCount + 1
		batched, err := store.ReplyPreviews(context.Background(), roots, limit)
		if err != nil {
			return err
		}
		if len(batched) != len(roots) {
			t.Fatalf("previews cover %d roots, want %d", len(batched), len(roots))
		}
		for _, rootID := range roots {
			individual, listErr := store.ListCommentReplies(context.Background(), rootID, nil, limit)
			if listErr != nil {
				return listErr
			}
			if len(batched[rootID]) != len(individual) {
				t.Fatalf("root %s batched=%d individual=%d", rootID, len(batched[rootID]), len(individual))
			}
			for index := range individual {
				if batched[rootID][index].ID != individual[index].ID {
					t.Fatalf("root %s position %d batched=%s individual=%s", rootID, index, batched[rootID][index].ID, individual[index].ID)
				}
			}
		}
		// A root with no replies still has an entry, so a caller reading the
		// map by key renders nothing rather than reading a missing key.
		if len(batched[bareID]) != 0 {
			t.Fatalf("root without replies = %#v", batched[bareID])
		}
		// The extra row is what tells the caller more remain.
		if len(batched[busyID]) != limit {
			t.Fatalf("busy root preview = %d, want the probe row too", len(batched[busyID]))
		}
		empty, err := store.ReplyPreviews(context.Background(), nil, limit)
		if err != nil || len(empty) != 0 {
			t.Fatalf("empty batch = %#v, %v", empty, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// A root leaves with every reply under it -- parent_id cascades in SQL, and
// nothing above it counted whose replies those were. That cascade was the
// owner's moderation for as long as removing a comment was an owner-only route.
// The day an approved member could reach it, "delete my own comment" became a
// way to delete anybody who had answered it.
func TestAMemberDeletingTheirOwnCommentCannotTakeAnotherAccountsReply(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	base := time.UnixMilli(1_700_000_000_000)
	clock := newSequenceClock(base, base.Add(time.Second))
	service := newTestCommentService(t, db, runtimeState, clock.Now, commentIDs(commentOne, commentTwo))
	owner := testCommentAuthor(t, db, runtimeState)
	member := testCommentMember(t, db, runtimeState, "member-with-a-guest", "Lin Lin")
	postID, _ := domain.ParseID(entryOne)

	root := mustOwnerComment(t, service, member, app.CreateCommentInput{EntryID: postID, ContentMD: "member root"})
	rootID := root.Comment.Comment.ID
	ownerReply := mustOwnerComment(t, service, owner, app.CreateCommentInput{EntryID: postID, ParentID: &rootID, ContentMD: "owner reply"})

	// The preview is refused too. It reports the reach of a delete that cannot
	// happen, which leaves it reporting how many replies somebody else wrote.
	for name, dryRun := range map[string]bool{"preview": true, "removal": false} {
		t.Run(name, func(t *testing.T) {
			if _, err := service.DeleteBy(context.Background(), rootID, member, dryRun); !domain.HasCode(err, domain.CodeUnauthorized) {
				t.Fatalf("member %s = %v, want a refusal", name, err)
			}
		})
	}
	assertCount(t, db.read, `SELECT count(*) FROM comments WHERE id = ?`, 1, ownerReply.Comment.Comment.ID.String())
	assertCount(t, db.read, `SELECT count(*) FROM comments WHERE id = ?`, 1, rootID.String())

	// The owner keeps the cascade, because moderating the site is what it is
	// for. Nothing about the refusal above narrows that.
	deleted, err := service.DeleteBy(context.Background(), rootID, owner, false)
	if err != nil || !deleted.Deleted || deleted.RepliesAffected != 1 {
		t.Fatalf("owner delete = %+v, %v", deleted, err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM comments`, 0)
}

// The refusal is about whose words the cascade reaches, not about the cascade.
// A conversation a member held with themselves is theirs to withdraw whole.
func TestAMemberRemovesTheirOwnCommentTogetherWithTheirOwnReplies(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	base := time.UnixMilli(1_700_000_000_000)
	clock := newSequenceClock(base, base.Add(time.Second))
	service := newTestCommentService(t, db, runtimeState, clock.Now, commentIDs(commentOne, commentTwo))
	member := testCommentMember(t, db, runtimeState, "member-alone", "Lin Lin")
	postID, _ := domain.ParseID(entryOne)

	root := mustOwnerComment(t, service, member, app.CreateCommentInput{EntryID: postID, ContentMD: "member root"})
	rootID := root.Comment.Comment.ID
	mustOwnerComment(t, service, member, app.CreateCommentInput{EntryID: postID, ParentID: &rootID, ContentMD: "member reply"})

	preview, err := service.DeleteBy(context.Background(), rootID, member, true)
	if err != nil || preview.Deleted || preview.RepliesAffected != 1 {
		t.Fatalf("member preview = %+v, %v", preview, err)
	}
	deleted, err := service.DeleteBy(context.Background(), rootID, member, false)
	if err != nil || !deleted.Deleted || deleted.RepliesAffected != 1 {
		t.Fatalf("member delete = %+v, %v", deleted, err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM comments`, 0)
}

// A legacy guest reply has no account behind it, so it is nobody's to withdraw
// but the owner's. Counting it as the member's own -- which a NULL author_user_id
// compared for equality would do -- would put the anonymous visitors an archive
// still holds inside a member's cascade.
func TestAMemberCannotTakeALegacyGuestReplyWithTheirOwnComment(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	base := time.UnixMilli(1_700_000_000_000)
	service := newTestCommentService(t, db, runtimeState, func() time.Time { return base }, commentIDs(commentOne))
	member := testCommentMember(t, db, runtimeState, "member-with-a-ghost", "Lin Lin")
	postID, _ := domain.ParseID(entryOne)

	root := mustOwnerComment(t, service, member, app.CreateCommentInput{EntryID: postID, ContentMD: "member root"})
	rootID := root.Comment.Comment.ID
	if err := db.WriteTx(context.Background(), func(queries Queries) error {
		_, execErr := queries.ExecContext(context.Background(), `
INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms)
VALUES (?, ?, ?, 'guest', 'Somebody passing', 'guest reply', ?)`, commentTwo, entryOne, rootID.String(), base.UnixMilli())
		return execErr
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := service.DeleteBy(context.Background(), rootID, member, false); !domain.HasCode(err, domain.CodeUnauthorized) {
		t.Fatalf("member delete over a guest reply = %v, want a refusal", err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM comments WHERE id = ?`, 1, commentTwo)
}

// An idempotency key is minted by one caller and means nothing to another. It
// was looked up in a single site-wide namespace, so a key one account chose
// answered for every account: a second account offering the same key was told
// its own first attempt had already been spent.
func TestAnIdempotencyKeyIsOneAccountsAndNotTheSites(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	base := time.UnixMilli(1_700_000_000_000)
	service := newTestCommentService(t, db, runtimeState, func() time.Time { return base }, commentIDs(commentOne, commentTwo))
	owner := testCommentAuthor(t, db, runtimeState)
	// The same display name on both, so the two requests differ in nothing the
	// digest can see. This is the case that used to hand the second account the
	// first account's comment rather than merely refusing it.
	member := testCommentMember(t, db, runtimeState, "twin", owner.DisplayName)
	postID, _ := domain.ParseID(entryOne)

	first := mustOwnerComment(t, service, owner, app.CreateCommentInput{EntryID: postID, ContentMD: "same words", IdempotencyKey: "shared"})
	second := mustOwnerComment(t, service, member, app.CreateCommentInput{EntryID: postID, ContentMD: "same words", IdempotencyKey: "shared"})

	if second.Replayed {
		t.Fatal("the second account was told its first attempt had already been made")
	}
	if second.Comment.Comment.ID == first.Comment.Comment.ID {
		t.Fatalf("both accounts hold comment %s", first.Comment.Comment.ID)
	}
	if second.Comment.Comment.AuthorUserID == nil || *second.Comment.Comment.AuthorUserID != member.ID {
		t.Fatalf("second comment author = %v, want %s", second.Comment.Comment.AuthorUserID, member.ID)
	}
	// Each account still replays its own key, which is the property the scope
	// has to keep while it narrows who the key answers for.
	replay, err := service.Create(context.Background(), app.CreateCommentInput{Author: member, EntryID: postID, ContentMD: "same words", IdempotencyKey: "shared"})
	if err != nil || !replay.Replayed || replay.Comment.Comment.ID != second.Comment.Comment.ID {
		t.Fatalf("member replay = %+v, %v", replay, err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM comments`, 2)
}

// The digest describes the request, and a display name is not part of one:
// Create stamps it from the account after the caller has stopped speaking. A
// person who renamed themselves between a write and its retry sent identical
// bytes and was answered IDEMPOTENCY_KEY_REUSED.
func TestARenameDoesNotTurnARetryIntoAConflict(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "post")
	base := time.UnixMilli(1_700_000_000_000)
	service := newTestCommentService(t, db, runtimeState, func() time.Time { return base }, commentIDs(commentOne))
	accounts := testAccountService(t, db, runtimeState)
	author := testCommentAuthor(t, db, runtimeState)
	postID, _ := domain.ParseID(entryOne)

	written := mustOwnerComment(t, service, author, app.CreateCommentInput{EntryID: postID, ContentMD: "unchanged words", IdempotencyKey: "retried-across-a-rename"})

	renamed, err := accounts.UpdateProfile(context.Background(), author.ID, app.ProfileUpdate{DisplayName: strptr("A Different Name")})
	if err != nil {
		t.Fatal(err)
	}
	replay, err := service.Create(context.Background(), app.CreateCommentInput{Author: renamed, EntryID: postID, ContentMD: "unchanged words", IdempotencyKey: "retried-across-a-rename"})
	if err != nil || !replay.Replayed || replay.Comment.Comment.ID != written.Comment.Comment.ID {
		t.Fatalf("replay after a rename = %+v, %v", replay, err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM comments`, 1)
}

// testAccountService builds the account authority these fixtures need alongside
// the comment service, against the same database.
func testAccountService(t *testing.T, database *Database, runtimeState *runtimefs.Runtime) *app.AccountService {
	t.Helper()
	repository, err := NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewAccountService(repository)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

// testCommentMember signs a second account in and approves it. Signing in is
// the only way an account that is not the owner comes to exist.
func testCommentMember(t *testing.T, database *Database, runtimeState *runtimefs.Runtime, providerUserID, displayName string) domain.User {
	t.Helper()
	service := testAccountService(t, database, runtimeState)
	outcome, err := service.SignIn(context.Background(), app.ExternalIdentity{
		Provider: "google", ProviderUserID: providerUserID, DisplayName: displayName,
	})
	if err != nil {
		t.Fatal(err)
	}
	member, err := service.SetStatus(context.Background(), outcome.User.ID, domain.AccountActive)
	if err != nil {
		t.Fatal(err)
	}
	return member
}

// testCommentAuthor is the account fixture comment writes are attributed to.
// A comment names its author now, so a write without one is refused.
func testCommentAuthor(t *testing.T, database *Database, runtimeState *runtimefs.Runtime) domain.User {
	t.Helper()
	repository, err := NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewAccountService(repository)
	if err != nil {
		t.Fatal(err)
	}
	owner, err := service.OwnerAccount(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return owner
}
