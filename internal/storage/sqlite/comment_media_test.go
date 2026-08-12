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

	root, err := service.CreateGuest(ctx, app.CreateCommentInput{
		EntryID: postID, AuthorName: "Guest", ContentMD: "root with images",
		Media: rootMedia, IdempotencyKey: "comment-media-root",
	})
	if err != nil || !reflect.DeepEqual(root.Comment.Media, rootMedia) {
		t.Fatalf("create root = %#v, %v", root, err)
	}
	reply, err := service.CreateOwner(ctx, app.CreateCommentInput{
		EntryID: postID, ParentID: &root.Comment.Comment.ID, ContentMD: "reply with image",
		Media: replyMedia, IdempotencyKey: "comment-media-reply",
	})
	if err != nil || !reflect.DeepEqual(reply.Comment.Media, replyMedia) {
		t.Fatalf("create reply = %#v, %v", reply, err)
	}
	assertMediaClock(t, database, mediaOne, nil)
	assertMediaClock(t, database, mediaTwo, nil)

	thread, err := service.List(ctx, postID)
	if err != nil || len(thread.Items) != 2 ||
		!reflect.DeepEqual(thread.Items[0].Media, rootMedia) ||
		!reflect.DeepEqual(thread.Items[1].Media, replyMedia) {
		t.Fatalf("thread = %#v, %v", thread, err)
	}
	replayed, err := service.CreateGuest(ctx, app.CreateCommentInput{
		EntryID: postID, AuthorName: "Guest", ContentMD: "root with images",
		Media: rootMedia, IdempotencyKey: "comment-media-root",
	})
	if err != nil || !replayed.Replayed || !reflect.DeepEqual(replayed.Comment, root.Comment) {
		t.Fatalf("replay = %#v, %v", replayed, err)
	}

	preview, err := service.Delete(ctx, root.Comment.Comment.ID, true)
	if err != nil || preview.RepliesAffected != 1 || !reflect.DeepEqual(preview.Comment.Media, rootMedia) {
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
