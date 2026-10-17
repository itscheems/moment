package sqlite

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/transport/postdto"
)

func TestTimelineRefreshCursorSurvivesEmptyHeadAndSameMillisecondPublications(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	now := time.UnixMilli(1_780_000_000_000)
	service := newTestPostService(t, db, func() time.Time { return now }, sequenceIDs(entryTwo, entryOne))
	ctx := testAccountContext()
	empty, err := service.List(ctx, app.ListPostsInput{})
	if err != nil || empty.RefreshCursor == "" || len(empty.Items) != 0 {
		t.Fatalf("empty head: %+v %v", empty, err)
	}
	idle, err := service.List(ctx, app.ListPostsInput{Cursor: empty.RefreshCursor})
	if err != nil || idle.RefreshCursor != empty.RefreshCursor || idle.PrevCursor != "" {
		t.Fatalf("idle: %+v %v", idle, err)
	}
	first, err := service.Create(ctx, app.CreatePostInput{ContentMD: "first"})
	if err != nil {
		t.Fatal(err)
	}
	loaded, err := service.List(ctx, app.ListPostsInput{Cursor: empty.RefreshCursor})
	if err != nil || len(loaded.Items) != 1 || loaded.Items[0].Entry.ID != first.Post.Entry.ID || loaded.RefreshCursor == "" || loaded.PrevCursor != "" {
		t.Fatalf("first refresh: %+v %v", loaded, err)
	}
	head, err := service.List(ctx, app.ListPostsInput{})
	if err != nil {
		t.Fatal(err)
	}
	exhausted, err := service.List(ctx, app.ListPostsInput{Cursor: head.PrevCursor})
	if err != nil || len(exhausted.Items) != 0 || exhausted.RefreshCursor != loaded.RefreshCursor {
		t.Fatalf("exhausted row continuation: %+v %v", exhausted, err)
	}
	// Smaller random ULID sorts before the previously observed row at the same time.
	second, err := service.Create(ctx, app.CreatePostInput{ContentMD: "second"})
	if err != nil {
		t.Fatal(err)
	}
	refreshed, err := service.List(ctx, app.ListPostsInput{Cursor: exhausted.RefreshCursor})
	if err != nil || len(refreshed.Items) != 2 || refreshed.Items[1].Entry.ID != second.Post.Entry.ID {
		t.Fatalf("same ms refresh: %+v %v", refreshed, err)
	}
	hidden := domain.VisibilityOnlyMe
	_, err = service.Update(ctx, app.UpdatePostInput{ID: second.Post.Entry.ID, PreserveContent: true, Visibility: &hidden})
	if err != nil {
		t.Fatal(err)
	}
	visible, err := service.List(ctx, app.ListPostsInput{Cursor: loaded.RefreshCursor})
	if err != nil || len(visible.Items) != 1 {
		t.Fatalf("visibility: %+v %v", visible, err)
	}
	primaryAccount, err := service.List(ctx, app.ListPostsInput{Cursor: loaded.RefreshCursor, Viewer: app.AccountViewer(testAccountID)})
	if err != nil || len(primaryAccount.Items) != 2 {
		t.Fatalf("owner visibility: %+v %v", primaryAccount, err)
	}
}

func TestTimelineRefreshSeeksTenThousandEntriesWithBoundedProjectionMerge(t *testing.T) {
	t.Parallel()
	db, _ := openTestDatabase(t, Options{})
	ctx := testAccountContext()
	base := time.UnixMilli(1_780_000_000_000)
	service := newTestPostService(t, db, func() time.Time { return base.Add(9960 * time.Millisecond) }, sequenceIDs(entryOne))
	empty, err := service.List(ctx, app.ListPostsInput{})
	if err != nil || empty.RefreshCursor == "" {
		t.Fatalf("initial refresh watermark: %+v %v", empty, err)
	}
	repository, err := newPostRepository(db)
	if err != nil {
		t.Fatal(err)
	}
	body, err := domain.ParseArticleDocument([]byte(`{"schema_version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"body"}]}]}`))
	if err != nil {
		t.Fatal(err)
	}
	article, err := domain.NewArticle("Article", body, nil)
	if err != nil {
		t.Fatal(err)
	}
	err = repository.Write(ctx, func(store app.PostStore) error {
		for i := 0; i < 10000; i++ {
			instant := base.Add(time.Duration(i) * time.Millisecond)
			id, idErr := domain.NewID(instant, bytes.NewReader(make([]byte, 10)))
			if idErr != nil {
				return idErr
			}
			entry, entryErr := domain.NewEntry(id, fmt.Sprintf("post %d", i), instant)
			if entryErr != nil {
				return entryErr
			}
			if i%3 == 0 {
				entry, entryErr = domain.NewArticleEntry(id, instant)
				if entryErr != nil {
					return entryErr
				}
			}
			if i%5 == 0 {
				entry.SetReposted(true, instant.Add(10*time.Millisecond))
			}
			entry.AuthorUserID = testAccountID
			if err := store.InsertEntry(ctx, entry, "body"); err != nil {
				return err
			}
			if entry.RepostedAt != nil {
				if _, err := store.SetEntryRepostedAt(ctx, id, entry.RepostedAt, entry.UpdatedAt); err != nil {
					return err
				}
			}
			if entry.Kind == domain.KindArticle {
				if err := store.InsertArticle(ctx, id, article); err != nil {
					return err
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range []domain.EntryKind{"", domain.KindMoment, domain.KindArticle} {
		query := app.TimelineQuery{Kind: kind, Direction: app.TimelineNewer, Position: &app.TimelinePosition{TimelineAtMS: base.UnixMilli() + 9990, Boundary: true}}
		statement, args := timelineEntriesQuery(query, 21)
		rows, err := db.read.QueryContext(ctx, "EXPLAIN QUERY PLAN "+statement, args...)
		if err != nil {
			t.Fatal(err)
		}
		var plan []string
		for rows.Next() {
			var a, b, c int
			var detail string
			if err := rows.Scan(&a, &b, &c, &detail); err != nil {
				t.Fatal(err)
			}
			plan = append(plan, detail)
		}
		if err := rows.Close(); err != nil {
			t.Fatal(err)
		}
		explain := strings.Join(plan, "\n")
		if strings.Contains(explain, "SCAN entries") || strings.Count(explain, "SEARCH entries USING INDEX") < 2 {
			t.Fatalf("unbounded kind=%q plan:\n%s", kind, explain)
		}
		t.Logf("kind=%q query plan:\n%s", kind, explain)
		var got []app.TimelineEntry
		err = repository.Read(ctx, func(store app.PostStore) error {
			var loadErr error
			got, loadErr = store.ListEntries(ctx, query, 21)
			return loadErr
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(got) == 0 || len(got) > 21 {
			t.Fatalf("kind=%q count=%d", kind, len(got))
		}
		for _, row := range got {
			if row.TimelineAt().UnixMilli() < query.Position.TimelineAtMS || (kind != "" && row.Entry.Kind != kind) {
				t.Fatalf("unexpected row: %+v", row)
			}
		}
		// Drain new publications in real service pages while the 10,000-row
		// history stays behind the server-issued watermark.
		cursor := empty.RefreshCursor
		seen := make(map[string]bool)
		reads, totalBytes := 0, 0
		for {
			page, err := service.List(ctx, app.ListPostsInput{Kind: kind, Cursor: cursor})
			if err != nil || len(page.Items) > 20 || page.RefreshCursor == "" {
				t.Fatalf("kind=%q refresh: %+v %v", kind, page, err)
			}
			reads++
			encoded, err := json.Marshal(postdto.FromPage(page))
			if err != nil {
				t.Fatal(err)
			}
			totalBytes += len(encoded)
			for _, post := range page.Items {
				key := post.Entry.ID.String() + ":" + string(post.TimelineKind)
				if seen[key] || post.Article != nil {
					t.Fatalf("duplicate projection or article body loaded: %s", key)
				}
				seen[key] = true
			}
			if page.PrevCursor == "" {
				break
			}
			if reads > 3 {
				t.Fatal("refresh repeatedly read historical pages")
			}
			cursor = page.PrevCursor
		}
		want := map[domain.EntryKind]int{"": 50, domain.KindMoment: 33, domain.KindArticle: 17}[kind]
		if len(seen) != want || reads != (want+19)/20 || totalBytes > 64*1024 {
			t.Fatalf("kind=%q rows=%d requests=%d bytes=%d", kind, len(seen), reads, totalBytes)
		}
		t.Logf("kind=%q incremental rows=%d requests=%d response bytes=%d", kind, len(seen), reads, totalBytes)
	}
}
