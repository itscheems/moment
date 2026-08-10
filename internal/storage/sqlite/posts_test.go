package sqlite

import (
	"context"
	"errors"
	"reflect"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestPostServiceCRUDPreservesTimelineAndRendersSafely(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	createdAt := time.Date(2026, time.August, 10, 9, 30, 0, 987654321, time.FixedZone("source", 8*60*60))
	clock := newSequenceClock(createdAt, createdAt.Add(-time.Hour))
	service := newTestPostService(t, db, clock.Now, sequenceIDs(entryOne))
	ctx := context.Background()

	created, err := service.Create(ctx, app.CreatePostInput{ContentMD: "hello **Moment** [site](https://example.test)"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Replayed || created.Post.Entry.Kind != domain.KindMoment {
		t.Fatalf("unexpected create result: %+v", created)
	}
	if !strings.Contains(created.Post.ContentHTML, "<strong>Moment</strong>") ||
		!strings.Contains(created.Post.ContentHTML, `rel="nofollow noopener noreferrer"`) {
		t.Fatalf("unsafe or incomplete rendered Markdown: %s", created.Post.ContentHTML)
	}
	if created.Post.Entry.CreatedAt != domain.NormalizeTime(createdAt) ||
		created.Post.Entry.CreatedAt != created.Post.Entry.PublishedAt {
		t.Fatalf("unexpected creation instants: %+v", created.Post.Entry)
	}

	got, err := service.Get(ctx, created.Post.Entry.ID)
	if err != nil || got.Entry != created.Post.Entry {
		t.Fatalf("get = %+v, %v", got, err)
	}
	originalCreated := got.Entry.CreatedAt
	originalPublished := got.Entry.PublishedAt
	updated, err := service.Update(ctx, app.UpdatePostInput{ID: got.Entry.ID, ContentMD: "edited"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Entry.CreatedAt != originalCreated || updated.Entry.PublishedAt != originalPublished {
		t.Fatal("update moved the Entry on the timeline")
	}
	if updated.Entry.UpdatedAt != got.Entry.UpdatedAt.Add(time.Millisecond) {
		t.Fatalf("updated time = %s, want %s", updated.Entry.UpdatedAt, got.Entry.UpdatedAt.Add(time.Millisecond))
	}

	if _, err := service.Update(ctx, app.UpdatePostInput{ID: got.Entry.ID, ContentMD: "![external](https://example.test/a.png)"}); !domain.HasCode(err, domain.CodeExternalImageNotAllowed) {
		t.Fatalf("unsafe edit error = %v", err)
	}
	afterRejected, err := service.Get(ctx, got.Entry.ID)
	if err != nil || afterRejected.Entry != updated.Entry {
		t.Fatalf("rejected edit changed the Entry: %+v, %v", afterRejected, err)
	}

	deleted, err := service.Delete(ctx, got.Entry.ID)
	if err != nil || deleted.Entry != updated.Entry {
		t.Fatalf("delete = %+v, %v", deleted, err)
	}
	if _, err := service.Get(ctx, got.Entry.ID); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("get deleted post error = %v", err)
	}
	if _, err := service.Delete(ctx, got.Entry.ID); !domain.HasCode(err, domain.CodePostNotFound) {
		t.Fatalf("second delete error = %v", err)
	}
}

func TestPostContentBoundariesLeaveNoPartialEntry(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	service := newTestPostService(t, db, func() time.Time { return time.UnixMilli(1_700_000_000_000) }, sequenceIDs(entryOne))
	ctx := context.Background()

	for _, testCase := range []struct {
		content string
		code    domain.Code
	}{
		{content: " \n\t", code: domain.CodeContentEmpty},
		{content: strings.Repeat("界", domain.MaxContentCodePoints+1), code: domain.CodeContentTooLarge},
	} {
		if _, err := service.Create(ctx, app.CreatePostInput{ContentMD: testCase.content}); !domain.HasCode(err, testCase.code) {
			t.Fatalf("content boundary error = %v, want %s", err, testCase.code)
		}
	}
	assertCount(t, db.read, `SELECT count(*) FROM entries`, 0)

	created, err := service.Create(ctx, app.CreatePostInput{ContentMD: strings.Repeat("界", domain.MaxContentCodePoints)})
	if err != nil || created.Post.Entry.ID.String() != entryOne {
		t.Fatalf("exact content limit create = %+v, %v", created, err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM entries`, 1)
}

func TestPostCreateIdempotencyIsAtomicAndConcurrent(t *testing.T) {
	db, _ := openTestDatabase(t, Options{})
	service := newTestPostService(t, db, func() time.Time { return time.UnixMilli(1_700_000_000_000) }, sequenceIDs(entryOne))
	ctx := context.Background()
	const workers = 24

	results := make(chan app.CreatePostResult, workers)
	errorsFound := make(chan error, workers)
	var group sync.WaitGroup
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			result, err := service.Create(ctx, app.CreatePostInput{ContentMD: "same payload", IdempotencyKey: "retry-1"})
			if err != nil {
				errorsFound <- err
				return
			}
			results <- result
		}()
	}
	group.Wait()
	close(results)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent idempotent create: %v", err)
	}

	var firstID domain.ID
	replayed := 0
	count := 0
	for result := range results {
		count++
		if firstID == "" {
			firstID = result.Post.Entry.ID
		}
		if result.Post.Entry.ID != firstID {
			t.Errorf("idempotent create returned %s and %s", firstID, result.Post.Entry.ID)
		}
		if result.Replayed {
			replayed++
		}
	}
	if count != workers || replayed != workers-1 {
		t.Fatalf("results=%d replayed=%d, want %d/%d", count, replayed, workers, workers-1)
	}
	assertCount(t, db.read, `SELECT count(*) FROM entries`, 1)
	assertCount(t, db.read, `SELECT count(*) FROM idempotency_keys`, 1)

	if _, err := service.Create(ctx, app.CreatePostInput{ContentMD: "different payload", IdempotencyKey: "retry-1"}); !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("idempotency conflict error = %v", err)
	}
	if _, err := service.Create(ctx, app.CreatePostInput{ContentMD: " \n\t", IdempotencyKey: "retry-1"}); !domain.HasCode(err, domain.CodeIdempotencyKeyReused) {
		t.Fatalf("invalid conflicting payload error = %v", err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM entries`, 1)
}

func TestPostCreateReplayReturnsImmutableResultAfterUpdateAndDelete(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	createdAt := time.UnixMilli(1_700_000_000_000)
	clock := newSequenceClock(createdAt, createdAt.Add(time.Hour))
	service := newTestPostService(t, db, clock.Now, sequenceIDs(entryOne))
	ctx := context.Background()
	input := app.CreatePostInput{ContentMD: "original **post**", IdempotencyKey: "immutable-replay"}

	created, err := service.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, app.UpdatePostInput{ID: created.Post.Entry.ID, ContentMD: "edited"}); err != nil {
		t.Fatal(err)
	}
	replayedAfterUpdate, err := service.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !replayedAfterUpdate.Replayed || !reflect.DeepEqual(replayedAfterUpdate.Post, created.Post) {
		t.Fatalf("replay after update = %+v, want original %+v", replayedAfterUpdate, created)
	}

	if _, err := service.Delete(ctx, created.Post.Entry.ID); err != nil {
		t.Fatal(err)
	}
	replayedAfterDelete, err := service.Create(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if !replayedAfterDelete.Replayed || !reflect.DeepEqual(replayedAfterDelete.Post, created.Post) {
		t.Fatalf("replay after delete = %+v, want original %+v", replayedAfterDelete, created)
	}
	assertCount(t, db.read, `SELECT count(*) FROM entries`, 0)
	assertCount(t, db.read, `SELECT count(*) FROM idempotency_keys`, 1)
}

func TestPostCreateReplayRejectsCorruptSnapshot(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name      string
		statement string
	}{
		{name: "resource ID", statement: `UPDATE idempotency_keys SET response_resource_id = 'not-an-id'`},
		{name: "created time", statement: `UPDATE idempotency_keys SET created_at_ms = 0`},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			db, _ := openTestDatabase(t, Options{})
			service := newTestPostService(t, db, func() time.Time { return time.UnixMilli(1_700_000_000_000) }, sequenceIDs(entryOne))
			ctx := context.Background()
			input := app.CreatePostInput{ContentMD: "original", IdempotencyKey: "corrupt-replay"}
			if _, err := service.Create(ctx, input); err != nil {
				t.Fatal(err)
			}
			if _, err := db.write.ExecContext(ctx, testCase.statement); err != nil {
				t.Fatal(err)
			}
			if _, err := service.Create(ctx, input); !domain.HasCode(err, domain.CodeDataIntegrityFailed) {
				t.Fatalf("corrupt replay error = %v", err)
			}
			assertCount(t, db.read, `SELECT count(*) FROM entries`, 1)
		})
	}
}

func TestPostCreateRollsBackWhenRetryRecordFails(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	service := newTestPostService(t, db, func() time.Time { return time.UnixMilli(1_700_000_000_000) }, sequenceIDs(entryOne))
	ctx := context.Background()

	if _, err := db.write.ExecContext(ctx, `
CREATE TRIGGER reject_test_idempotency
BEFORE INSERT ON idempotency_keys
BEGIN
    SELECT RAISE(ABORT, 'injected idempotency failure');
END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Create(ctx, app.CreatePostInput{ContentMD: "must roll back", IdempotencyKey: "rollback-1"}); !domain.HasCode(err, domain.CodeInternal) {
		t.Fatalf("injected failure error = %v", err)
	}
	assertCount(t, db.read, `SELECT count(*) FROM entries`, 0)
	assertCount(t, db.read, `SELECT count(*) FROM idempotency_keys`, 0)
}

func TestPostUpdateRollbackDoesNotAdvanceTimestamp(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	createdAt := time.UnixMilli(1_700_000_000_000)
	clock := newSequenceClock(createdAt, createdAt.Add(time.Hour))
	service := newTestPostService(t, db, clock.Now, sequenceIDs(entryOne))
	ctx := context.Background()
	created, err := service.Create(ctx, app.CreatePostInput{ContentMD: "before"})
	if err != nil {
		t.Fatal(err)
	}

	if _, err := db.write.ExecContext(ctx, `
CREATE TRIGGER reject_test_post_update
BEFORE UPDATE OF content_md ON entries
BEGIN
    SELECT RAISE(ABORT, 'injected update failure');
END`); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Update(ctx, app.UpdatePostInput{ID: created.Post.Entry.ID, ContentMD: "after"}); !domain.HasCode(err, domain.CodeInternal) {
		t.Fatalf("injected update error = %v", err)
	}
	unchanged, err := service.Get(ctx, created.Post.Entry.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Entry != created.Post.Entry {
		t.Fatalf("failed update changed persisted Entry: %+v", unchanged.Entry)
	}
}

func TestPostTimelineUsesStableDualKeyCursor(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	instant := time.UnixMilli(1_700_000_000_000)
	ids := []string{entryOne, entryTwo, entryThree, "01ARZ3NDEKTSV4RRFFQ69G5FAY", "01ARZ3NDEKTSV4RRFFQ69G5FAZ"}
	service := newTestPostService(t, db, func() time.Time { return instant }, sequenceIDs(ids...))
	ctx := context.Background()
	for index := range ids {
		if _, err := service.Create(ctx, app.CreatePostInput{ContentMD: "post " + ids[index]}); err != nil {
			t.Fatal(err)
		}
	}

	var actual []string
	cursor := ""
	for {
		page, err := service.List(ctx, app.ListPostsInput{Cursor: cursor, Limit: 2})
		if err != nil {
			t.Fatal(err)
		}
		for _, post := range page.Items {
			actual = append(actual, post.Entry.ID.String())
		}
		if page.NextCursor == "" {
			break
		}
		if page.NextCursor == cursor {
			t.Fatal("timeline cursor did not advance")
		}
		cursor = page.NextCursor
	}
	expected := append([]string(nil), ids...)
	sort.Sort(sort.Reverse(sort.StringSlice(expected)))
	if strings.Join(actual, ",") != strings.Join(expected, ",") {
		t.Fatalf("timeline IDs = %v, want %v", actual, expected)
	}

	for _, input := range []app.ListPostsInput{{Cursor: "invalid"}, {Limit: -1}, {Limit: app.MaxPostPageSize + 1}} {
		if _, err := service.List(ctx, input); !domain.HasCode(err, domain.CodeInvalidArgument) {
			t.Fatalf("invalid page input %+v returned %v", input, err)
		}
	}
}

func TestPostConcurrentCreatesAndDeletesPreserveOrdering(t *testing.T) {
	db, _ := openTestDatabase(t, Options{})
	instant := time.UnixMilli(1_700_000_000_000)
	service := newTestPostService(t, db, func() time.Time { return instant }, nil)
	ctx := context.Background()
	const workers = 32

	createdIDs := make(chan domain.ID, workers)
	errorsFound := make(chan error, workers)
	var group sync.WaitGroup
	for index := range workers {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			result, err := service.Create(ctx, app.CreatePostInput{ContentMD: "concurrent " + string(rune('A'+index))})
			if err != nil {
				errorsFound <- err
				return
			}
			createdIDs <- result.Post.Entry.ID
		}(index)
	}
	group.Wait()
	close(createdIDs)
	close(errorsFound)
	for err := range errorsFound {
		t.Errorf("concurrent create: %v", err)
	}
	ids := make([]domain.ID, 0, workers)
	for id := range createdIDs {
		ids = append(ids, id)
	}
	if len(ids) != workers {
		t.Fatalf("created %d posts, want %d", len(ids), workers)
	}

	page, err := service.List(ctx, app.ListPostsInput{Limit: app.MaxPostPageSize})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Items) != workers || page.NextCursor != "" {
		t.Fatalf("timeline page has %d items and cursor %q", len(page.Items), page.NextCursor)
	}
	if !sort.SliceIsSorted(page.Items, func(left, right int) bool {
		return page.Items[left].Entry.ID.String() > page.Items[right].Entry.ID.String()
	}) {
		t.Fatal("equal-timestamp concurrent posts are not ordered by descending ID")
	}

	target := ids[0]
	var successes atomic.Int64
	deleteErrors := make(chan error, workers)
	for range workers {
		group.Add(1)
		go func() {
			defer group.Done()
			if _, deleteErr := service.Delete(ctx, target); deleteErr == nil {
				successes.Add(1)
			} else if !domain.HasCode(deleteErr, domain.CodePostNotFound) {
				deleteErrors <- deleteErr
			}
		}()
	}
	group.Wait()
	close(deleteErrors)
	for err := range deleteErrors {
		t.Errorf("concurrent delete: %v", err)
	}
	if successes.Load() != 1 {
		t.Fatalf("concurrent delete succeeded %d times, want 1", successes.Load())
	}
	assertCount(t, db.read, `SELECT count(*) FROM entries`, workers-1)
}

func newTestPostService(t *testing.T, db *Database, clock app.PostClock, ids app.PostIDGenerator) *app.PostService {
	t.Helper()
	repository, err := newPostRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	options := []app.PostServiceOption{app.WithPostClock(clock)}
	if ids != nil {
		options = append(options, app.WithPostIDGenerator(ids))
	}
	service, err := app.NewPostService(repository, options...)
	if err != nil {
		t.Fatal(err)
	}
	return service
}

func sequenceIDs(values ...string) app.PostIDGenerator {
	var mutex sync.Mutex
	index := 0
	return func(time.Time) (domain.ID, error) {
		mutex.Lock()
		defer mutex.Unlock()
		if index >= len(values) {
			return "", errors.New("test ID sequence exhausted")
		}
		id, err := domain.ParseID(values[index])
		index++
		return id, err
	}
}

type sequenceClock struct {
	mutex sync.Mutex
	times []time.Time
	index int
}

func newSequenceClock(times ...time.Time) *sequenceClock {
	return &sequenceClock{times: times}
}

func (clock *sequenceClock) Now() time.Time {
	clock.mutex.Lock()
	defer clock.mutex.Unlock()
	if clock.index >= len(clock.times) {
		return clock.times[len(clock.times)-1]
	}
	value := clock.times[clock.index]
	clock.index++
	return value
}
