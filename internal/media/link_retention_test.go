package media

import (
	"bytes"
	"context"
	"crypto/rand"
	"strings"
	"testing"
	"time"

	"github.com/moment/moment/internal/app"
	"github.com/moment/moment/internal/domain"
	"github.com/moment/moment/internal/storage/sqlite"
)

func TestAutomaticLinkRetentionProtectsDraftsAndOrdinaryUploads(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	store, _, db := newTestStoreWithOptions(t, Options{Clock: func() time.Time { return now }, Entropy: rand.Reader})
	ctx := app.WithContentActor(context.Background(), domain.User{ID: "0123456789abcdef0123456789abcdef"})
	adapter := NewLinkThumbnailAdapter(store)
	abandoned, _, _, _, err := adapter.Ingest(ctx, "link-preview", bytes.NewReader(encodeTransparentPNG(t, 12, 10)))
	if err != nil {
		t.Fatal(err)
	}
	kept, _, _, _, err := adapter.Ingest(ctx, "link-preview", bytes.NewReader(encodeTransparentPNG(t, 13, 10)))
	if err != nil {
		t.Fatal(err)
	}
	ordinary, _, err := store.Ingest(ctx, "upload.png", bytes.NewReader(encodeTransparentPNG(t, 14, 10)))
	if err != nil {
		t.Fatal(err)
	}
	sharedBytes := encodeTransparentPNG(t, 15, 10)
	shared, _, _, _, err := adapter.Ingest(ctx, "link-preview", bytes.NewReader(sharedBytes))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.Ingest(ctx, "explicit-upload.png", bytes.NewReader(sharedBytes)); err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("a", 64)
	if err := store.RetainLinkDraft(ctx, key, []string{kept.String()}); err != nil {
		t.Fatal(err)
	}
	saved, err := store.getLocked(ctx, kept)
	if err != nil || saved.UnreferencedSince != nil {
		t.Fatalf("draft reference clock: %v, %v", saved, err)
	}
	result, err := store.PruneLinkPreviews(ctx, now.Add(23*time.Hour), false)
	if err != nil || result.Deleted != 0 {
		t.Fatalf("grace period: %+v, %v", result, err)
	}
	now = now.Add(25 * time.Hour)
	result, err = store.PruneLinkPreviews(ctx, now, false)
	if err != nil || result.Deleted != 1 {
		t.Fatalf("automatic prune: %+v, %v", result, err)
	}
	if _, found, err := store.findByID(ctx, abandoned); err != nil || found {
		t.Fatalf("abandoned preview survived: %v", err)
	}
	for _, id := range []domain.ID{kept, ordinary.ID, shared} {
		if _, found, err := store.findByID(ctx, id); err != nil || !found {
			t.Fatalf("protected media missing: %s, %v", id, err)
		}
	}
	// Refuse another account's media and keep the original draft intact.
	other := app.WithContentActor(context.Background(), domain.User{ID: strings.Repeat("f", 32)})
	if err := store.RetainLinkDraft(other, key, []string{kept.String()}); err == nil {
		t.Fatal("accepted foreign media")
	}
	var references int
	if err := db.ReadTx(ctx, func(q sqlite.Queries) error {
		return q.QueryRowContext(ctx, `SELECT count(*) FROM compose_draft_link_media`).Scan(&references)
	}); err != nil || references != 1 {
		t.Fatalf("reference changed after refusal: %d, %v", references, err)
	}
	if err := store.RetainLinkDraft(ctx, key, nil); err != nil {
		t.Fatal(err)
	}
	result, err = store.PruneLinkPreviews(ctx, now.Add(23*time.Hour), false)
	if err != nil || result.Deleted != 0 {
		t.Fatalf("released draft grace: %+v, %v", result, err)
	}
	result, err = store.PruneLinkPreviews(ctx, now.Add(25*time.Hour), false)
	if err != nil || result.Deleted != 1 {
		t.Fatalf("released draft cleanup: %+v, %v", result, err)
	}
}

func TestLinkDraftExpiryAndPublishedReferences(t *testing.T) {
	now := time.Date(2026, 11, 1, 0, 0, 0, 0, time.UTC)
	store, _, db := newTestStoreWithOptions(t, Options{Clock: func() time.Time { return now }, Entropy: rand.Reader})
	ctx := app.WithContentActor(context.Background(), domain.User{ID: "0123456789abcdef0123456789abcdef"})
	id, _, _, _, err := NewLinkThumbnailAdapter(store).Ingest(ctx, "link-favicon", bytes.NewReader(encodeTransparentPNG(t, 10, 10)))
	if err != nil {
		t.Fatal(err)
	}
	key := strings.Repeat("b", 64)
	if err := store.RetainLinkDraft(ctx, key, []string{id.String()}); err != nil {
		t.Fatal(err)
	}
	// Expiry releases the reference and starts a fresh grace period, rather
	// than deleting media at the same instant the browser draft expires.
	now = now.Add(31 * 24 * time.Hour)
	result, err := store.PruneLinkPreviews(ctx, now, false)
	if err != nil || result.Deleted != 0 {
		t.Fatalf("expiry: %+v, %v", result, err)
	}
	value, err := store.getLocked(ctx, id)
	if err != nil || value.UnreferencedSince == nil || !value.UnreferencedSince.Equal(now) {
		t.Fatalf("expiry clock: %+v, %v", value, err)
	}
	// A published card is protected by the same central reference resolver.
	err = db.WriteTx(ctx, func(q sqlite.Queries) error {
		_, err := q.ExecContext(ctx, `INSERT INTO entries(id,kind,content_md,created_at_ms,updated_at_ms,published_at_ms,author_user_id) VALUES(?,'moment','caption',?,?,?,?)`, "01ARZ3NDEKTSV4RRFFQ69G5FAV", now.UnixMilli(), now.UnixMilli(), now.UnixMilli(), "0123456789abcdef0123456789abcdef")
		if err != nil {
			return err
		}
		_, err = q.ExecContext(ctx, `INSERT INTO entry_link_card(entry_id,position,source_url,canonical_url,provider,card_kind,title,description,site_name,author_name,favicon_media_id,embed_url,captured_at_ms) VALUES(?,0,'https://example.com/','https://example.com/','generic','link','','','','',?,'',?)`, "01ARZ3NDEKTSV4RRFFQ69G5FAV", id.String(), now.UnixMilli())
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err = store.PruneLinkPreviews(ctx, now.Add(48*time.Hour), false)
	if err != nil || result.Deleted != 0 {
		t.Fatalf("published card removed: %+v, %v", result, err)
	}
}
