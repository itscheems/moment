package sqlite

import (
	"context"
	"strings"
	"testing"
	"time"
)

func TestMigration0029BackfillsDraftExcerptsWithoutChangingBodiesOrRevisions(t *testing.T) {
	ctx := context.Background()
	pool := openRawMigrationPool(t)
	all, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, pool, all[:28], time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	body := `{"schema_version":1,"type":"doc","content":[{"type":"paragraph","content":[{"type":"text","text":"English 中文 preview"}]}]}`
	if _, err := pool.ExecContext(ctx, `INSERT INTO article_drafts(id,title,body_json,body_schema_version,created_at_ms,updated_at_ms) VALUES(?, 'title', ?,1,1000,2000)`, fixtureEntryOne, body); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, pool, all, time.Unix(2, 0)); err != nil {
		t.Fatal(err)
	}
	var raw, excerpt string
	var created, updated int64
	if err := pool.QueryRowContext(ctx, `SELECT body_json,excerpt,created_at_ms,updated_at_ms FROM article_drafts WHERE id=?`, fixtureEntryOne).Scan(&raw, &excerpt, &created, &updated); err != nil {
		t.Fatal(err)
	}
	if raw != body || created != 1000 || updated != 2000 || excerpt != "English 中文 preview" {
		t.Fatalf("backfill changed authority: %s,%s,%d,%d", raw, excerpt, created, updated)
	}
	// The summary projection is independent of decoding body JSON at list time.
	if _, err := pool.ExecContext(ctx, `UPDATE article_drafts SET body_json='{"schema_version":1,"type":"doc","content":[{"type":"unknown"}]}' WHERE id=?`, fixtureEntryOne); err != nil {
		t.Fatal(err)
	}
	var author string
	if err := pool.QueryRowContext(ctx, `SELECT author_user_id FROM article_drafts WHERE id=?`, fixtureEntryOne).Scan(&author); err != nil {
		t.Fatal(err)
	}
	summaries, err := newPostStore(pool).ArticleDraftSummaries(ctx, author)
	if err != nil || len(summaries) != 1 || summaries[0].Excerpt != "English 中文 preview" {
		t.Fatalf("body-free summary=%+v,%v", summaries, err)
	}
}

func TestMigration0029RollsBackAnUnparseableDraft(t *testing.T) {
	ctx := context.Background()
	pool := openRawMigrationPool(t)
	all, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, pool, all[:28], time.Unix(1, 0)); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.ExecContext(ctx, `INSERT INTO article_drafts(id,title,body_json,body_schema_version,created_at_ms,updated_at_ms) VALUES(?, 'title','{"schema_version":1,"type":"doc","content":[{"type":"unknown"}]}',1,1000,2000)`, fixtureEntryOne); err != nil {
		t.Fatal(err)
	}
	if err := applyMigrations(ctx, pool, all, time.Unix(2, 0)); err == nil || !strings.Contains(err.Error(), "invalid stored draft") {
		t.Fatalf("migration should roll back: %v", err)
	}
	var version int
	if err := pool.QueryRowContext(ctx, `SELECT max(version) FROM schema_migrations`).Scan(&version); err != nil {
		t.Fatal(err)
	}
	if version != 28 {
		t.Fatalf("committed failed migration: %d", version)
	}
}
