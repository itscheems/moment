package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestAvatarReferenceClockIsContinuousAndTransactional(t *testing.T) {
	ctx := context.Background()
	database, runtimeState := openTestDatabase(t, Options{})
	created := time.UnixMilli(1_700_000_000_000)
	firstRemoval := created.Add(2 * time.Hour)
	clock := created.Add(time.Hour)
	insertReferenceMedia(t, database, mediaOne, "a", created)
	repository, err := NewSiteRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewSiteService(repository, app.SiteOptions{
		AllowDevelopmentLocalHTTP: true,
		Clock:                     func() time.Time { return clock },
	})
	if err != nil {
		t.Fatal(err)
	}

	avatar := mediaOne
	if _, err := service.Update(ctx, app.SiteUpdate{AvatarMediaID: &app.NullableString{Value: &avatar}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, nil)
	clock = clock.Add(30 * time.Minute)
	if _, err := service.Update(ctx, app.SiteUpdate{AvatarMediaID: &app.NullableString{Value: &avatar}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, nil)

	clock = firstRemoval
	if _, err := service.Update(ctx, app.SiteUpdate{AvatarMediaID: &app.NullableString{}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, &firstRemoval)

	clock = firstRemoval.Add(6 * time.Hour)
	if _, err := service.Update(ctx, app.SiteUpdate{AvatarMediaID: &app.NullableString{}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, &firstRemoval)

	if _, err := service.Update(ctx, app.SiteUpdate{AvatarMediaID: &app.NullableString{Value: &avatar}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, nil)

	clock = firstRemoval.Add(7 * time.Hour)
	if _, err := service.Update(ctx, app.SiteUpdate{AvatarMediaID: &app.NullableString{}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, &clock)
}

func TestPostDeleteReconcilesCascadedMediaInSameTransaction(t *testing.T) {
	ctx := context.Background()
	database, runtimeState := openTestDatabase(t, Options{})
	created := time.UnixMilli(1_700_000_000_000)
	removed := created.Add(time.Hour)
	insertReferenceMedia(t, database, mediaOne, "b", created)
	insertReferenceMedia(t, database, mediaTwo, "c", created)
	mustWrite(t, database, `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms)
VALUES (?, 'moment', 'with media', ?, ?, ?)`, entryOne, created.UnixMilli(), created.UnixMilli(), created.UnixMilli())
	mustWrite(t, database, `INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 0, NULL)`, entryOne, mediaOne)
	mustWrite(t, database, `INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 1, '')`, entryOne, mediaTwo)
	mustWrite(t, database, `UPDATE site_settings SET avatar_media_id = ? WHERE id = 1`, mediaOne)
	mustWrite(t, database, `UPDATE media SET unreferenced_since_ms = NULL WHERE id IN (?, ?)`, mediaOne, mediaTwo)
	repository, err := NewPostRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewPostService(repository, app.WithPostClock(func() time.Time { return removed }))
	if err != nil {
		t.Fatal(err)
	}
	id, _ := domain.ParseID(entryOne)
	if _, err := service.Delete(ctx, id); err != nil {
		t.Fatal(err)
	}
	assertCount(t, database.read, `SELECT count(*) FROM entry_media WHERE entry_id = ?`, 0, entryOne)
	assertMediaClock(t, database, mediaOne, nil)
	assertMediaClock(t, database, mediaTwo, &removed)
}

func insertReferenceMedia(t *testing.T, database *Database, id, fill string, created time.Time) {
	t.Helper()
	sha := strings.Repeat(fill, 64)
	path := "media/original/" + fill + fill + "/" + sha + ".jpg"
	mustWrite(t, database, `
INSERT INTO media (
    id, sha256, storage_path, original_name, mime_type,
    byte_size, width, height, created_at_ms, unreferenced_since_ms
)
VALUES (?, ?, ?, 'reference.jpg', 'image/jpeg', 10, 32, 32, ?, ?)`, id, sha, path, created.UnixMilli(), created.UnixMilli())
}

func assertMediaClock(t *testing.T, database *Database, id string, want *time.Time) {
	t.Helper()
	var value *int64
	if err := database.ReadTx(context.Background(), func(queries Queries) error {
		return queries.QueryRowContext(context.Background(), `SELECT unreferenced_since_ms FROM media WHERE id = ?`, id).Scan(&value)
	}); err != nil {
		t.Fatal(err)
	}
	if want == nil {
		if value != nil {
			t.Fatalf("unreferenced clock = %d, want NULL", *value)
		}
		return
	}
	if value == nil || *value != want.UnixMilli() {
		t.Fatalf("unreferenced clock = %v, want %d", value, want.UnixMilli())
	}
}
