package sqlite

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestPostMediaLifecycleOrderingClockAndImmutableReplay(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, _ := openTestDatabase(t, Options{})
	createdAt := time.UnixMilli(1_700_000_000_000)
	reorderedAt := createdAt.Add(time.Hour)
	removedAt := createdAt.Add(2 * time.Hour)
	clearedAt := createdAt.Add(3 * time.Hour)
	deletedAt := createdAt.Add(4 * time.Hour)
	insertReferenceMedia(t, database, mediaOne, "a", createdAt)
	insertReferenceMedia(t, database, mediaTwo, "b", createdAt)
	service := newTestPostService(t, database,
		newSequenceClock(createdAt, reorderedAt, removedAt, clearedAt, deletedAt).Now,
		sequenceIDs(entryOne),
	)
	one, _ := domain.ParseID(mediaOne)
	two, _ := domain.ParseID(mediaTwo)
	decorative := ""
	provided := "first description"
	initial := []domain.EntryMediaUsage{
		{MediaID: one, Position: 0},
		{MediaID: one, Position: 1, AltText: &decorative},
		{MediaID: two, Position: 2, AltText: &provided},
	}
	input := app.CreatePostInput{ContentMD: "post with media", Media: initial, IdempotencyKey: "media-replay"}
	created, err := service.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(created.Post.Media, expectedMediaProjections(initial)) {
		t.Fatalf("created media = %#v", created.Post.Media)
	}
	assertMediaClock(t, database, mediaOne, nil)
	assertMediaClock(t, database, mediaTwo, nil)

	got, err := service.Get(ctx, created.Post.Entry.ID, app.OwnerViewer())
	if err != nil || !reflect.DeepEqual(got.Media, expectedMediaProjections(initial)) {
		t.Fatalf("get media = %#v, %v", got.Media, err)
	}
	page, err := service.List(ctx, app.ListPostsInput{})
	if err != nil || len(page.Items) != 1 || !reflect.DeepEqual(page.Items[0].Media, expectedMediaProjections(initial)) {
		t.Fatalf("list = %#v, %v", page, err)
	}

	reorderedAlt := "same blob, new usage"
	reordered := []domain.EntryMediaUsage{
		{MediaID: two, Position: 0, AltText: &provided},
		{MediaID: one, Position: 1, AltText: &reorderedAlt},
	}
	updated, err := service.Update(ctx, app.UpdatePostInput{ID: created.Post.Entry.ID, PreserveContent: true, Media: &reordered})
	if err != nil {
		t.Fatal(err)
	}
	updatedBody, updatedHasBody := updated.Entry.Markdown()
	createdBody, createdHasBody := created.Post.Entry.Markdown()
	if updatedHasBody != createdHasBody || updatedBody != createdBody || !reflect.DeepEqual(updated.Media, expectedMediaProjections(reordered)) {
		t.Fatalf("media-only reorder = %#v", updated)
	}
	assertMediaClock(t, database, mediaOne, nil)
	assertMediaClock(t, database, mediaTwo, nil)

	onlyTwo := []domain.EntryMediaUsage{{MediaID: two, Position: 0}}
	if _, err := service.Update(ctx, app.UpdatePostInput{ID: created.Post.Entry.ID, PreserveContent: true, Media: &onlyTwo}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, &removedAt)
	assertMediaClock(t, database, mediaTwo, nil)

	empty := []domain.EntryMediaUsage{}
	cleared, err := service.Update(ctx, app.UpdatePostInput{ID: created.Post.Entry.ID, PreserveContent: true, Media: &empty})
	if err != nil || cleared.Media == nil || len(cleared.Media) != 0 {
		t.Fatalf("clear = %#v, %v", cleared, err)
	}
	assertMediaClock(t, database, mediaOne, &removedAt)
	assertMediaClock(t, database, mediaTwo, &clearedAt)

	replayed, err := service.Create(ctx, input)
	if err != nil || !replayed.Replayed || !reflect.DeepEqual(replayed.Post, created.Post) {
		t.Fatalf("replay after replacement = %#v, %v", replayed, err)
	}
	changedAlt := "different"
	conflict := input
	conflict.Media = domain.CloneEntryMediaUsages(initial)
	conflict.Media[2].AltText = &changedAlt
	if _, err := service.Create(ctx, conflict); !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("usage-sensitive replay conflict = %v", err)
	}

	deleted, err := service.Delete(ctx, created.Post.Entry.ID)
	if err != nil || deleted.Media == nil || len(deleted.Media) != 0 {
		t.Fatalf("delete cleared post = %#v, %v", deleted, err)
	}
	replayed, err = service.Create(ctx, input)
	if err != nil || !replayed.Replayed || !reflect.DeepEqual(replayed.Post, created.Post) {
		t.Fatalf("replay after delete = %#v, %v", replayed, err)
	}
	assertCount(t, database.read, `SELECT count(*) FROM entries`, 0)
	assertCount(t, database.read, `SELECT count(*) FROM entry_media`, 0)
}

func TestPostMediaCreateAndUpdateRollbackAtomically(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, _ := openTestDatabase(t, Options{})
	createdAt := time.UnixMilli(1_700_000_000_000)
	insertReferenceMedia(t, database, mediaOne, "c", createdAt)
	insertReferenceMedia(t, database, mediaTwo, "d", createdAt)
	one, _ := domain.ParseID(mediaOne)
	two, _ := domain.ParseID(mediaTwo)
	service := newTestPostService(t, database, func() time.Time { return createdAt.Add(time.Hour) }, sequenceIDs(entryOne, entryTwo))

	if _, err := database.write.ExecContext(ctx, `
CREATE TRIGGER reject_post_media_create
BEFORE INSERT ON entry_media
BEGIN
    SELECT RAISE(ABORT, 'injected media usage failure');
END`); err != nil {
		t.Fatal(err)
	}
	failedInput := app.CreatePostInput{
		ContentMD:      "must roll back",
		Media:          []domain.EntryMediaUsage{{MediaID: one, Position: 0}},
		IdempotencyKey: "media-rollback",
	}
	if _, err := service.Create(ctx, failedInput); !domain.HasCode(err, domain.CodeInternal) {
		t.Fatalf("create rollback error = %v", err)
	}
	assertCount(t, database.read, `SELECT count(*) FROM entries`, 0)
	assertCount(t, database.read, `SELECT count(*) FROM entry_media`, 0)
	assertCount(t, database.read, `SELECT count(*) FROM idempotency_keys`, 0)
	assertMediaClock(t, database, mediaOne, &createdAt)
	if _, err := database.write.ExecContext(ctx, `DROP TRIGGER reject_post_media_create`); err != nil {
		t.Fatal(err)
	}

	created, err := service.Create(ctx, app.CreatePostInput{ContentMD: "before", Media: []domain.EntryMediaUsage{{MediaID: one, Position: 0}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := database.write.ExecContext(ctx, `
CREATE TRIGGER reject_post_media_update
BEFORE INSERT ON entry_media
WHEN NEW.media_id = '`+mediaTwo+`'
BEGIN
    SELECT RAISE(ABORT, 'injected replacement failure');
END`); err != nil {
		t.Fatal(err)
	}
	replacement := []domain.EntryMediaUsage{{MediaID: two, Position: 0}}
	if _, err := service.Update(ctx, app.UpdatePostInput{ID: created.Post.Entry.ID, ContentMD: "after", Media: &replacement}); !domain.HasCode(err, domain.CodeInternal) {
		t.Fatalf("update rollback error = %v", err)
	}
	unchanged, err := service.Get(ctx, created.Post.Entry.ID, app.OwnerViewer())
	unchangedBody, unchangedHasBody := unchanged.Entry.Markdown()
	if err != nil || !unchangedHasBody || unchangedBody != "before" || !reflect.DeepEqual(unchanged.Media, created.Post.Media) {
		t.Fatalf("failed replacement changed authority: %#v, %v", unchanged, err)
	}
	assertMediaClock(t, database, mediaOne, nil)
	assertMediaClock(t, database, mediaTwo, &createdAt)
}

func TestPostMediaRejectsMissingOverLimitAndOversizeWithoutPartialUsage(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, _ := openTestDatabase(t, Options{})
	createdAt := time.UnixMilli(1_700_000_000_000)
	insertReferenceMedia(t, database, mediaOne, "e", createdAt)
	one, _ := domain.ParseID(mediaOne)
	missing, _ := domain.ParseID(mediaTwo)
	service := newTestPostService(t, database, func() time.Time { return createdAt.Add(time.Hour) }, sequenceIDs(entryOne, entryTwo, entryThree))

	if _, err := service.Create(ctx, app.CreatePostInput{ContentMD: "missing", Media: []domain.EntryMediaUsage{{MediaID: missing, Position: 0}}}); !domain.HasCode(err, domain.CodeMediaNotFound) {
		t.Fatalf("missing media error = %v", err)
	}
	overLimit := make([]domain.EntryMediaUsage, domain.MaxEntryMediaUsages+1)
	for index := range overLimit {
		overLimit[index] = domain.EntryMediaUsage{MediaID: one, Position: index}
	}
	if _, err := service.Create(ctx, app.CreatePostInput{ContentMD: "too many", Media: overLimit}); !domain.HasCode(err, domain.CodeInvalidArgument) {
		t.Fatalf("over-limit error = %v", err)
	}
	if _, err := database.write.ExecContext(ctx, `UPDATE media SET byte_size = ? WHERE id = ?`, 26<<20, mediaOne); err != nil {
		t.Fatal(err)
	}
	oversize := []domain.EntryMediaUsage{{MediaID: one, Position: 0}, {MediaID: one, Position: 1}}
	if _, err := service.Create(ctx, app.CreatePostInput{ContentMD: "too large", Media: oversize}); !domain.HasCode(err, domain.CodeMediaTooLarge) {
		t.Fatalf("aggregate size error = %v", err)
	}
	assertCount(t, database.read, `SELECT count(*) FROM entries`, 0)
	assertCount(t, database.read, `SELECT count(*) FROM entry_media`, 0)
	assertMediaClock(t, database, mediaOne, &createdAt)
}

func TestPostMediaIdempotentCreateIsConcurrent(t *testing.T) {
	database, _ := openTestDatabase(t, Options{})
	createdAt := time.UnixMilli(1_700_000_000_000)
	insertReferenceMedia(t, database, mediaOne, "f", createdAt)
	one, _ := domain.ParseID(mediaOne)
	service := newTestPostService(t, database, func() time.Time { return createdAt.Add(time.Hour) }, sequenceIDs(entryOne))
	input := app.CreatePostInput{
		ContentMD:      "concurrent media",
		Media:          []domain.EntryMediaUsage{{MediaID: one, Position: 0}, {MediaID: one, Position: 1}},
		IdempotencyKey: "concurrent-media",
	}
	const workers = 16
	var group sync.WaitGroup
	errorsFound := make(chan error, workers)
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := service.Create(context.Background(), input)
			if err != nil {
				errorsFound <- err
				return
			}
			if !reflect.DeepEqual(result.Post.Media, expectedMediaProjections(input.Media)) {
				errorsFound <- domain.NewError(domain.CodeInternal, "media replay drifted", nil)
			}
		}()
	}
	group.Wait()
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent create: %v", err)
	}
	assertCount(t, database.read, `SELECT count(*) FROM entries`, 1)
	assertCount(t, database.read, `SELECT count(*) FROM entry_media`, 2)
	assertMediaClock(t, database, mediaOne, nil)
}

func expectedMediaProjections(inputs []domain.EntryMediaUsage) []app.MediaUsageProjection {
	projections := make([]app.MediaUsageProjection, len(inputs))
	for index, input := range inputs {
		projections[index] = app.MediaUsageProjection{
			MediaID: input.MediaID, Position: input.Position, AltText: input.AltText,
			MIMEType: "image/jpeg", Width: 32, Height: 32,
		}
	}
	return projections
}

func TestSearchResultsCarryCanonicalPostMedia(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	database, runtimeState := openTestDatabase(t, Options{})
	createdAt := time.UnixMilli(1_700_000_000_000)
	insertReferenceMedia(t, database, mediaOne, "9", createdAt)
	one, _ := domain.ParseID(mediaOne)
	alt := "search result image"
	posts := newTestPostService(t, database, func() time.Time { return createdAt.Add(time.Hour) }, sequenceIDs(entryOne))
	created, err := posts.Create(ctx, app.CreatePostInput{
		ContentMD: "searchable media moment",
		Media:     []domain.EntryMediaUsage{{MediaID: one, Position: 0, AltText: &alt}},
	})
	if err != nil {
		t.Fatal(err)
	}
	repository, err := NewSearchRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	search, err := app.NewSearchService(repository)
	if err != nil {
		t.Fatal(err)
	}
	result, err := search.Search(ctx, app.SearchInput{Query: "searchable"})
	if err != nil || len(result.Items) != 1 || !reflect.DeepEqual(result.Items[0].Post.Media, created.Post.Media) {
		t.Fatalf("search result = %#v, %v", result, err)
	}
}
