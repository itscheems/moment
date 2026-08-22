package sqlite

import (
	"context"
	"reflect"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestCommentMediaLifecycleOrderingReplayAndCascade(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, database, entryOne, "comment media host")
	seededAt := time.UnixMilli(1_700_000_000_000)
	rootAt := seededAt.Add(time.Hour)
	replyAt := seededAt.Add(2 * time.Hour)
	deletedAt := seededAt.Add(3 * time.Hour)
	insertReferenceMedia(t, database, mediaOne, "a", seededAt)
	insertReferenceMedia(t, database, mediaTwo, "b", seededAt)
	service := newTestCommentService(
		t,
		database,
		runtimeState,
		newSequenceClock(rootAt, replyAt, deletedAt).Now,
		commentIDs(commentOne, commentTwo),
	)
	postID, _ := domain.ParseID(entryOne)
	one, _ := domain.ParseID(mediaOne)
	two, _ := domain.ParseID(mediaTwo)
	decorative := ""
	description := "street at night"
	rootMedia := []domain.EntryMediaUsage{
		{MediaID: one, Position: 0},
		{MediaID: one, Position: 1, AltText: &decorative},
	}
	replyMedia := []domain.EntryMediaUsage{{MediaID: two, Position: 0, AltText: &description}}

	root, err := service.CreateOwner(ctx, app.CreateCommentInput{
		EntryID: postID, ContentMD: "root with images",
		Media: rootMedia, IdempotencyKey: "comment-media-root",
	})
	if err != nil || !reflect.DeepEqual(root.Comment.Media, expectedMediaProjections(rootMedia)) {
		t.Fatalf("create root = %#v, %v", root, err)
	}
	reply, err := service.CreateOwner(ctx, app.CreateCommentInput{
		EntryID: postID, ParentID: &root.Comment.Comment.ID, ContentMD: "reply with image",
		Media: replyMedia, IdempotencyKey: "comment-media-reply",
	})
	if err != nil || !reflect.DeepEqual(reply.Comment.Media, expectedMediaProjections(replyMedia)) {
		t.Fatalf("create reply = %#v, %v", reply, err)
	}
	assertMediaClock(t, database, mediaOne, nil)
	assertMediaClock(t, database, mediaTwo, nil)

	thread, err := service.List(ctx, postID)
	if err != nil || len(thread.Items) != 2 ||
		!reflect.DeepEqual(thread.Items[0].Media, expectedMediaProjections(rootMedia)) ||
		!reflect.DeepEqual(thread.Items[1].Media, expectedMediaProjections(replyMedia)) {
		t.Fatalf("thread = %#v, %v", thread, err)
	}
	replayed, err := service.CreateOwner(ctx, app.CreateCommentInput{
		EntryID: postID, ContentMD: "root with images",
		Media: rootMedia, IdempotencyKey: "comment-media-root",
	})
	if err != nil || !replayed.Replayed || !reflect.DeepEqual(replayed.Comment, root.Comment) {
		t.Fatalf("replay = %#v, want %#v, err = %v", replayed.Comment, root.Comment, err)
	}

	preview, err := service.Delete(ctx, root.Comment.Comment.ID, true)
	if err != nil || preview.RepliesAffected != 1 || !reflect.DeepEqual(preview.Comment.Media, expectedMediaProjections(rootMedia)) {
		t.Fatalf("delete preview = %#v, %v", preview, err)
	}
	assertMediaClock(t, database, mediaOne, nil)
	assertMediaClock(t, database, mediaTwo, nil)
	deleted, err := service.Delete(ctx, root.Comment.Comment.ID, false)
	if err != nil || !deleted.Deleted || deleted.RepliesAffected != 1 {
		t.Fatalf("delete = %#v, %v", deleted, err)
	}
	assertCount(t, database.read, `SELECT count(*) FROM comment_media`, 0)
	assertMediaClock(t, database, mediaOne, &deletedAt)
	assertMediaClock(t, database, mediaTwo, &deletedAt)
}

func TestCommentMediaRejectsMissingOverLimitAndOversizeAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, database, entryOne, "comment media host")
	createdAt := time.UnixMilli(1_700_000_000_000)
	insertReferenceMedia(t, database, mediaOne, "c", createdAt)
	postID, _ := domain.ParseID(entryOne)
	one, _ := domain.ParseID(mediaOne)
	missing, _ := domain.ParseID(mediaTwo)
	service := newTestCommentService(t, database, runtimeState, func() time.Time { return createdAt.Add(time.Hour) }, commentIDs(commentOne, commentTwo, commentThree))

	if _, err := service.CreateOwner(ctx, app.CreateCommentInput{EntryID: postID, ContentMD: "missing", Media: []domain.EntryMediaUsage{{MediaID: missing, Position: 0}}}); !domain.HasCode(err, domain.CodeMediaNotFound) {
		t.Fatalf("missing media = %v", err)
	}
	overLimit := make([]domain.EntryMediaUsage, domain.MaxCommentMediaUsages+1)
	for index := range overLimit {
		overLimit[index] = domain.EntryMediaUsage{MediaID: one, Position: index}
	}
	if _, err := service.CreateOwner(ctx, app.CreateCommentInput{EntryID: postID, ContentMD: "too many", Media: overLimit}); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("over limit = %v", err)
	}
	if _, err := database.write.ExecContext(ctx, `UPDATE media SET byte_size = ? WHERE id = ?`, 21<<20, mediaOne); err != nil {
		t.Fatal(err)
	}
	tooLarge := []domain.EntryMediaUsage{{MediaID: one, Position: 0}, {MediaID: one, Position: 1}}
	if _, err := service.CreateOwner(ctx, app.CreateCommentInput{EntryID: postID, ContentMD: "too large", Media: tooLarge}); !domain.HasCode(err, domain.CodeMediaTooLarge) {
		t.Fatalf("too large = %v", err)
	}
	assertCount(t, database.read, `SELECT count(*) FROM comments`, 0)
	assertCount(t, database.read, `SELECT count(*) FROM comment_media`, 0)
	assertMediaClock(t, database, mediaOne, &createdAt)
}

// Loading a page's attachments in one query has to give exactly what asking
// per comment gave. The batched form exists because the per-comment loop was
// about to become a fixed cost on every page; a batch that quietly disagreed
// with it would change what readers see, not just how fast they see it.
func TestBatchedCommentMediaMatchesPerCommentLoading(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, runtimeState := openTestDatabase(t, Options{})
	insertEntry(t, database, entryOne, "comment media host")
	seededAt := time.UnixMilli(1_700_000_000_000)
	insertReferenceMedia(t, database, mediaOne, "a", seededAt)
	insertReferenceMedia(t, database, mediaTwo, "b", seededAt)
	service := newTestCommentService(t, database, runtimeState,
		newSequenceClock(seededAt.Add(time.Hour), seededAt.Add(2*time.Hour), seededAt.Add(3*time.Hour)).Now,
		commentIDs(commentOne, commentTwo, commentThree))
	postID, _ := domain.ParseID(entryOne)
	one, _ := domain.ParseID(mediaOne)
	two, _ := domain.ParseID(mediaTwo)
	described := "street at night"

	withMedia := mustOwnerComment(t, service, app.CreateCommentInput{
		EntryID: postID, ContentMD: "two images",
		Media: []domain.EntryMediaUsage{
			{MediaID: one, Position: 0},
			{MediaID: two, Position: 1, AltText: &described},
		},
	})
	rootID := withMedia.Comment.Comment.ID
	// A comment with no attachments at all has to come back as an empty slice
	// rather than be absent from the batch, or the caller reading the map by
	// key would silently render nothing for it.
	bare := mustOwnerComment(t, service, app.CreateCommentInput{
		EntryID: postID, ParentID: &rootID, ContentMD: "no images",
	})
	bareID := bare.Comment.Comment.ID

	if err := database.ReadTx(ctx, func(queries Queries) error {
		store := newCommentStore(queries)
		batched, err := store.CommentMediaUsagesByComment(ctx, []domain.ID{rootID, bareID})
		if err != nil {
			return err
		}
		if len(batched) != 2 {
			t.Fatalf("batched comments = %d, want both", len(batched))
		}
		for _, id := range []domain.ID{rootID, bareID} {
			individual, err := store.CommentMediaUsages(ctx, id)
			if err != nil {
				return err
			}
			if !reflect.DeepEqual(batched[id], individual) {
				t.Fatalf("comment %s batched=%#v individual=%#v", id, batched[id], individual)
			}
		}
		if len(batched[bareID]) != 0 {
			t.Fatalf("comment without media = %#v, want an empty slice", batched[bareID])
		}
		empty, err := store.CommentMediaUsagesByComment(ctx, nil)
		if err != nil || len(empty) != 0 {
			t.Fatalf("empty batch = %#v, %v", empty, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
