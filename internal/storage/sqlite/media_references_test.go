package sqlite

import (
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
)

func TestAvatarReferenceClockIsContinuousAndTransactional(t *testing.T) {
	ctx := testAccountContext()
	database, runtimeState := openTestDatabase(t, Options{})
	created := time.UnixMilli(1_700_000_000_000)
	firstRemoval := created.Add(2 * time.Hour)
	clock := created.Add(time.Hour)
	insertReferenceMedia(t, database, mediaOne, "a", created)
	repository, err := NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewAccountService(repository, app.WithAccountClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatal(err)
	}

	avatar := mediaOne
	if _, err := service.UpdateProfile(ctx, testAccountID, app.ProfileUpdate{AvatarMediaID: &app.NullableString{Value: &avatar}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, nil)
	clock = clock.Add(30 * time.Minute)
	if _, err := service.UpdateProfile(ctx, testAccountID, app.ProfileUpdate{AvatarMediaID: &app.NullableString{Value: &avatar}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, nil)

	clock = firstRemoval
	if _, err := service.UpdateProfile(ctx, testAccountID, app.ProfileUpdate{AvatarMediaID: &app.NullableString{}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, &firstRemoval)

	clock = firstRemoval.Add(6 * time.Hour)
	if _, err := service.UpdateProfile(ctx, testAccountID, app.ProfileUpdate{AvatarMediaID: &app.NullableString{}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, &firstRemoval)

	if _, err := service.UpdateProfile(ctx, testAccountID, app.ProfileUpdate{AvatarMediaID: &app.NullableString{Value: &avatar}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, nil)

	clock = firstRemoval.Add(7 * time.Hour)
	if _, err := service.UpdateProfile(ctx, testAccountID, app.ProfileUpdate{AvatarMediaID: &app.NullableString{}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, &clock)
}

func TestCoverReferenceUsesCentralResolver(t *testing.T) {
	ctx := testAccountContext()
	database, runtimeState := openTestDatabase(t, Options{})
	created := time.UnixMilli(1_700_000_000_000)
	removed := created.Add(time.Hour)
	insertReferenceMedia(t, database, mediaOne, "d", created)
	repository, err := NewAccountRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	service, err := app.NewAccountService(repository, app.WithAccountClock(func() time.Time { return removed }))
	if err != nil {
		t.Fatal(err)
	}
	cover := mediaOne
	if _, err := service.UpdateProfile(ctx, testAccountID, app.ProfileUpdate{CoverMediaID: &app.NullableString{Value: &cover}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, nil)
	if _, err := service.UpdateProfile(ctx, testAccountID, app.ProfileUpdate{CoverMediaID: &app.NullableString{}}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, &removed)
}

func TestPostDeleteReconcilesCascadedMediaInSameTransaction(t *testing.T) {
	ctx := testAccountContext()
	database, runtimeState := openTestDatabase(t, Options{})
	created := time.UnixMilli(1_700_000_000_000)
	removed := created.Add(time.Hour)
	insertReferenceMedia(t, database, mediaOne, "b", created)
	insertReferenceMedia(t, database, mediaTwo, "c", created)
	mustWrite(t, database, `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, author_user_id)
VALUES (?, 'moment', 'with media', ?, ?, ?, '0123456789abcdef0123456789abcdef')`, entryOne, created.UnixMilli(), created.UnixMilli(), created.UnixMilli())
	mustWrite(t, database, `INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 0, NULL)`, entryOne, mediaOne)
	mustWrite(t, database, `
INSERT INTO comments (id, entry_id, parent_id, author_name, content_md, created_at_ms)
VALUES (?, ?, NULL, 'Guest', 'comment image', ?)`, commentRoot, entryOne, created.UnixMilli())
	mustWrite(t, database, `INSERT INTO comment_media (comment_id, media_id, position, alt_text) VALUES (?, ?, 0, '')`, commentRoot, mediaTwo)
	mustWrite(t, database, `UPDATE users SET avatar_media_id = ? WHERE id = (SELECT profile_user_id FROM site_settings WHERE id = 1)`, mediaOne)
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
	deleted, err := service.Delete(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	if len(deleted.Media) != 1 || deleted.Media[0].MediaID.String() != mediaOne {
		t.Fatalf("deleted post media snapshot = %#v", deleted.Media)
	}
	assertCount(t, database.read, `SELECT count(*) FROM entry_media WHERE entry_id = ?`, 0, entryOne)
	assertCount(t, database.read, `SELECT count(*) FROM comment_media`, 0)
	assertCount(t, database.read, `SELECT count(*) FROM media WHERE id IN (?, ?)`, 2, mediaOne, mediaTwo)
	assertMediaClock(t, database, mediaOne, nil)
	assertMediaClock(t, database, mediaTwo, &removed)
}

// TestLinkCardThumbnailUsesCentralResolver proves the link card thumbnail is a
// first-class reference source: publishing one clears the unreferenced clock,
// replacing the card restarts it, and the resolver -- not a feature-local
// query -- is what decides. Spec section 7.6 requires this coverage whenever a
// media reference source is added.
func TestLinkCardThumbnailUsesCentralResolver(t *testing.T) {
	ctx := testAccountContext()
	database, runtimeState := openTestDatabase(t, Options{})
	created := time.UnixMilli(1_700_000_000_000)
	published := created.Add(time.Hour)
	cleared := published.Add(time.Hour)
	insertReferenceMedia(t, database, mediaOne, "e", created)
	repository, err := NewPostRepository(database, runtimeState.Locks)
	if err != nil {
		t.Fatal(err)
	}
	clock := published
	service, err := app.NewPostService(repository, app.WithPostClock(func() time.Time { return clock }))
	if err != nil {
		t.Fatal(err)
	}
	publishedPost, err := service.Create(ctx, app.CreatePostInput{
		ContentMD: "watch this",
		LinkCards: []domain.LinkCard{{
			SourceURL:        "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			CanonicalURL:     "https://www.youtube.com/watch?v=dQw4w9WgXcQ",
			Provider:         domain.LinkProviderYouTube,
			Kind:             domain.LinkKindVideo,
			Title:            "a video",
			SiteName:         "YouTube",
			ThumbnailMediaID: mediaOne,
			EmbedURL:         "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ",
			CapturedAt:       published.UnixMilli(),
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(publishedPost.Post.LinkCards) != 1 {
		t.Fatalf("expected the created post to carry one card, got %d", len(publishedPost.Post.LinkCards))
	}
	assertMediaClock(t, database, mediaOne, nil)

	// Clearing the card must restart the clock in the same transaction.
	clock = cleared
	empty := []domain.LinkCard{}
	if _, err := service.Update(ctx, app.UpdatePostInput{
		ID:              publishedPost.Post.Entry.ID,
		PreserveContent: true,
		LinkCards:       &empty,
	}); err != nil {
		t.Fatal(err)
	}
	assertMediaClock(t, database, mediaOne, &cleared)
}

// TestLinkCardThumbnailBlocksMediaDeletion proves the ON DELETE RESTRICT
// reference keeps a live thumbnail from being pruned out from under a card.
func TestLinkCardThumbnailBlocksMediaDeletion(t *testing.T) {
	database, runtimeState := openTestDatabase(t, Options{})
	created := time.UnixMilli(1_700_000_000_000)
	insertReferenceMedia(t, database, mediaOne, "f", created)
	mustWrite(t, database, `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, author_user_id)
VALUES (?, 'moment', 'carded', ?, ?, ?, '0123456789abcdef0123456789abcdef')`, entryOne, created.UnixMilli(), created.UnixMilli(), created.UnixMilli())
	mustWrite(t, database, `
INSERT INTO entry_link_card (
    entry_id, position, source_url, canonical_url, provider, card_kind, title,
    description, site_name, author_name, duration_seconds, thumbnail_media_id,
    embed_url, captured_at_ms
) VALUES (?, 0, 'https://example.com/a', 'https://example.com/a', 'generic', 'link', 't', '', 'example.com', '', NULL, ?, '', ?)`,
		entryOne, mediaOne, created.UnixMilli())
	_ = runtimeState
	err := database.WriteTx(testAccountContext(), func(queries Queries) error {
		_, execErr := queries.ExecContext(testAccountContext(), `DELETE FROM media WHERE id = ?`, mediaOne)
		return execErr
	})
	if err == nil {
		t.Fatal("expected a referenced link card thumbnail to block media deletion")
	}
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
	mustWrite(t, database, `INSERT INTO media_grants(user_id,media_id,created_at_ms) VALUES(?,?,1)`, testAccountID, id)
}

func assertMediaClock(t *testing.T, database *Database, id string, want *time.Time) {
	t.Helper()
	var value *int64
	if err := database.ReadTx(testAccountContext(), func(queries Queries) error {
		return queries.QueryRowContext(testAccountContext(), `SELECT unreferenced_since_ms FROM media WHERE id = ?`, id).Scan(&value)
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
