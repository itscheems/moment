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
	postID, _ := domain.ParseID(entryOne)

	rootOne := mustGuestComment(t, service, app.CreateCommentInput{EntryID: postID, AuthorName: "  Alice  ", ContentMD: "first **root**"})
	replyOne := mustOwnerComment(t, service, app.CreateCommentInput{EntryID: postID, ParentID: &rootOne.Comment.Comment.ID, ContentMD: "latest reply"})
	rootTwo := mustGuestComment(t, service, app.CreateCommentInput{EntryID: postID, AuthorName: "Bob", ContentMD: "second root"})
	replyTwo := mustGuestComment(t, service, app.CreateCommentInput{EntryID: postID, ParentID: &rootTwo.Comment.Comment.ID, AuthorName: "陈", ContentMD: "earlier reply"})

	thread, err := service.List(context.Background(), postID)
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
	if rootOne.Comment.Comment.AuthorName != "Alice" || replyOne.Comment.Comment.AuthorKind != domain.CommentAuthorOwner || replyOne.Comment.Comment.AuthorName != "Owner" || !strings.Contains(rootOne.Comment.ContentHTML, "<strong>root</strong>") {
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
	if _, err := service.List(context.Background(), postID); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("deleted post list = %v", err)
	}
}

func TestCommentPinIsSingletonRootScopedAndCascadesWithDeletion(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "one")
	insertEntry(t, db, entryTwo, "two")
	base := time.UnixMilli(1_700_000_000_000)
	service := newTestCommentService(t, db, runtimeState, func() time.Time { return base }, commentIDs(commentOne, commentTwo, commentThree, commentFour))
	one, _ := domain.ParseID(entryOne)
	two, _ := domain.ParseID(entryTwo)
	rootOne := mustOwnerComment(t, service, app.CreateCommentInput{EntryID: one, ContentMD: "first"})
	reply := mustGuestComment(t, service, app.CreateCommentInput{EntryID: one, ParentID: &rootOne.Comment.Comment.ID, AuthorName: "Guest", ContentMD: "reply"})
	rootTwo := mustGuestComment(t, service, app.CreateCommentInput{EntryID: one, AuthorName: "Guest", ContentMD: "second"})
	otherPostRoot := mustOwnerComment(t, service, app.CreateCommentInput{EntryID: two, ContentMD: "other"})

	pinned, err := service.Pin(context.Background(), one, rootOne.Comment.Comment.ID)
	if err != nil || pinned.PinnedCommentID == nil || *pinned.PinnedCommentID != rootOne.Comment.Comment.ID {
		t.Fatalf("pin first root = %+v, %v", pinned, err)
	}
	pinned, err = service.Pin(context.Background(), one, rootTwo.Comment.Comment.ID)
	if err != nil || pinned.PinnedCommentID == nil || *pinned.PinnedCommentID != rootTwo.Comment.Comment.ID {
		t.Fatalf("replace pinned root = %+v, %v", pinned, err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM comment_pins WHERE entry_id = ? AND comment_id = ?`, 1, entryOne, commentThree)
	thread, err := service.List(context.Background(), one)
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
	thread, err = service.List(context.Background(), one)
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
	location := &domain.Location{
		Name: "BANANA+香蕉攀岩", DisplayName: "天目西路218号嘉里合集B1B103B104",
		CountryCode: "CN", Country: "中国", Region: "上海市", City: "上海市",
		District: "静安区", Category: "体育场馆", Provider: "amap", ProviderPlaceID: "B0TEST",
		Point: &domain.GeoPoint{LatitudeE7: 312231860, LongitudeE7: 1214452600, System: domain.CoordinateGCJ02},
	}

	created, err := service.CreateOwner(context.Background(), app.CreateCommentInput{
		EntryID: postID, Location: location, IdempotencyKey: "located-comment",
	})
	if err != nil || created.Comment.Comment.ContentMD != "" || created.Comment.ContentHTML != "" ||
		created.Comment.Comment.Location == nil || created.Comment.Comment.Location.DisplayName != location.DisplayName {
		t.Fatalf("created location comment = %+v, %v", created, err)
	}
	location.Name = "caller mutation"
	thread, err := service.List(context.Background(), postID)
	if err != nil || len(thread.Items) != 1 || thread.Items[0].Comment.Location == nil || thread.Items[0].Comment.Location.Name != "BANANA+香蕉攀岩" {
		t.Fatalf("stored location comment = %+v, %v", thread, err)
	}
	replayLocation := domain.CloneLocation(thread.Items[0].Comment.Location)
	replayed, err := service.CreateOwner(context.Background(), app.CreateCommentInput{EntryID: postID, Location: replayLocation, IdempotencyKey: "located-comment"})
	if err != nil || !replayed.Replayed || replayed.Comment.Comment.Location == nil {
		t.Fatalf("location replay = %+v, %v", replayed, err)
	}
	replayLocation.DisplayName = "different"
	if _, err := service.CreateOwner(context.Background(), app.CreateCommentInput{EntryID: postID, Location: replayLocation, IdempotencyKey: "located-comment"}); !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
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
	one, _ := domain.ParseID(entryOne)
	two, _ := domain.ParseID(entryTwo)
	root := mustGuestComment(t, service, app.CreateCommentInput{EntryID: one, AuthorName: "Guest", ContentMD: "root"})
	reply := mustGuestComment(t, service, app.CreateCommentInput{EntryID: one, ParentID: &root.Comment.Comment.ID, AuthorName: "Guest", ContentMD: "reply"})
	if _, err := service.CreateGuest(context.Background(), app.CreateCommentInput{EntryID: one, ParentID: &reply.Comment.Comment.ID, AuthorName: "Guest", ContentMD: "deep"}); !domain.HasCode(err, domain.CodeCommentDepthExceeded) {
		t.Fatalf("deep = %v", err)
	}
	if _, err := service.CreateGuest(context.Background(), app.CreateCommentInput{EntryID: two, ParentID: &root.Comment.Comment.ID, AuthorName: "Guest", ContentMD: "cross"}); !domain.HasCode(err, domain.CodeCommentDepthExceeded) {
		t.Fatalf("cross = %v", err)
	}
	missing, _ := domain.ParseID(commentFour)
	if _, err := service.CreateGuest(context.Background(), app.CreateCommentInput{EntryID: one, ParentID: &missing, AuthorName: "Guest", ContentMD: "missing"}); !domain.HasCode(err, domain.CodeCommentNotFound) {
		t.Fatalf("missing = %v", err)
	}
	for _, test := range []struct {
		name, content string
		code          domain.Code
	}{
		{name: "", content: "valid", code: domain.CodeInvalidArgument},
		{name: "Guest", content: "<b>raw</b>", code: domain.CodeInvalidArgument},
		{name: "Guest", content: "![external](https://example.test/a.png)", code: domain.CodeExternalImageNotAllowed},
		{name: "Guest", content: strings.Repeat("界", domain.MaxCommentContentCodePoints+1), code: domain.CodeContentTooLarge},
	} {
		if _, err := service.CreateGuest(context.Background(), app.CreateCommentInput{EntryID: one, AuthorName: test.name, ContentMD: test.content}); !domain.HasCode(err, test.code) {
			t.Fatalf("unsafe input = %v, want %s", err, test.code)
		}
	}
	mustWrite(t, db, `UPDATE site_settings SET comments_enabled = 0 WHERE id = 1`)
	if _, err := service.CreateOwner(context.Background(), app.CreateCommentInput{EntryID: one, ContentMD: "closed"}); !domain.HasCode(err, domain.CodeCommentsDisabled) {
		t.Fatalf("disabled = %v", err)
	}
	thread, err := service.List(context.Background(), one)
	if err != nil || len(thread.Items) != 2 {
		t.Fatalf("closed read = %+v, %v", thread, err)
	}

	assertWriteRejected(t, db, `INSERT INTO comments VALUES (?, ?, ?, 'guest', 'Bypass', 'deep', ?)`, commentThree, entryOne, commentTwo, base.Add(time.Second).UnixMilli())
	assertWriteRejected(t, db, `INSERT INTO comments VALUES (?, ?, ?, 'guest', 'Bypass', 'cross', ?)`, commentThree, entryTwo, commentOne, base.Add(time.Second).UnixMilli())
}

func TestCommentIdempotencyConcurrentReplayAndCancellation(t *testing.T) {
	db, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, db, entryOne, "one")
	postID, _ := domain.ParseID(entryOne)
	service := newTestCommentService(t, db, runtimeState, func() time.Time { return time.UnixMilli(1_700_000_000_000) }, commentIDs(commentOne, commentTwo))
	const workers = 16
	results := make(chan app.CreateCommentResult, workers)
	errs := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := service.CreateOwner(context.Background(), app.CreateCommentInput{EntryID: postID, ContentMD: "retry", IdempotencyKey: "comment-retry"})
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
	if _, err := service.CreateOwner(context.Background(), app.CreateCommentInput{EntryID: postID, ContentMD: "changed", IdempotencyKey: "comment-retry"}); !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("conflict = %v", err)
	}
	if _, err := service.CreateOwner(context.Background(), app.CreateCommentInput{EntryID: postID, ContentMD: " ", IdempotencyKey: "comment-retry"}); !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("invalid conflicting replay = %v", err)
	}
	originalID, _ := domain.ParseID(commentOne)
	if _, err := service.Delete(context.Background(), originalID, false); err != nil {
		t.Fatal(err)
	}
	replayedAfterDelete, err := service.CreateOwner(context.Background(), app.CreateCommentInput{EntryID: postID, ContentMD: "retry", IdempotencyKey: "comment-retry"})
	if err != nil || !replayedAfterDelete.Replayed || replayedAfterDelete.Comment.Comment.ID != originalID {
		t.Fatalf("replay after delete = %+v, %v", replayedAfterDelete, err)
	}
	guest, err := service.CreateGuest(context.Background(), app.CreateCommentInput{EntryID: postID, AuthorName: "Guest", ContentMD: "retry", IdempotencyKey: "comment-retry"})
	if err != nil || guest.Replayed || guest.Comment.Comment.ID.String() != commentTwo {
		t.Fatalf("guest scope = %+v, %v", guest, err)
	}
	cancelled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := service.CreateOwner(cancelled, app.CreateCommentInput{EntryID: postID, ContentMD: "cancelled"}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled create = %v", err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM comments`, 1)
	if _, err := service.List(cancelled, postID); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancelled = %v", err)
	}
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

func mustGuestComment(t *testing.T, service *app.CommentService, input app.CreateCommentInput) app.CreateCommentResult {
	t.Helper()
	result, err := service.CreateGuest(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
func mustOwnerComment(t *testing.T, service *app.CommentService, input app.CreateCommentInput) app.CreateCommentResult {
	t.Helper()
	result, err := service.CreateOwner(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	return result
}
