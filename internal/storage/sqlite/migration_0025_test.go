package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Migration 0025 rebuilds entries and entry_media. Rebuilding a table other
// tables point at is the one migration shape that can lose rows without saying
// anything, so this fixture puts a row in every table that hangs off entries
// and every table that hangs off those, and the assertions below are about
// nothing surviving less than intact.
const (
	fixtureEntryOne   = "01ARZ3NDEKTSV4RRFFQ69G5FAV"
	fixtureEntryTwo   = "01ARZ3NDEKTSV4RRFFQ69G5FAW"
	fixtureMediaOne   = "01ARZ3NDEKTSV4RRFFQ69G5FB1"
	fixtureMediaTwo   = "01ARZ3NDEKTSV4RRFFQ69G5FB2"
	fixtureCommentOne = "01ARZ3NDEKTSV4RRFFQ69G5FC1"
	fixtureCommentTwo = "01ARZ3NDEKTSV4RRFFQ69G5FC2"
)

var migrationFixtureTables = []string{
	"entries", "entry_media", "entry_location", "entry_quotes", "entry_link_card",
	"comments", "comment_media", "comment_location", "comment_pins",
	"site_settings", "users", "media", "entry_fts",
}

func TestMigration0025KeepsEveryRowAndRelaxesTheChecks(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	pool := openRawMigrationPool(t)

	all, err := embeddedMigrations()
	if err != nil {
		t.Fatal(err)
	}
	var before []migration
	for _, item := range all {
		if item.version < 25 {
			before = append(before, item)
		}
	}
	if len(before) == len(all) {
		t.Fatal("expected migration 0025 to exist")
	}
	if err := applyMigrations(ctx, pool, before, time.Unix(1, 0)); err != nil {
		t.Fatalf("apply migrations up to 0024: %v", err)
	}

	seedMigrationFixture(t, pool)
	countsBefore := tableCounts(t, pool)
	ftsBefore := searchIndexRows(t, pool)
	referencedBefore := referencedMediaIDs(t, pool)
	if len(ftsBefore) == 0 || len(referencedBefore) == 0 {
		t.Fatalf("the fixture proves nothing: %d index rows, %d referenced media", len(ftsBefore), len(referencedBefore))
	}

	if err := applyMigrations(ctx, pool, all, time.Unix(2, 0)); err != nil {
		t.Fatalf("apply 0025: %v", err)
	}

	for _, table := range migrationFixtureTables {
		if countsBefore[table] != tableCounts(t, pool)[table] {
			t.Fatalf("%s went from %d rows to %d", table, countsBefore[table], tableCounts(t, pool)[table])
		}
		if countsBefore[table] == 0 {
			t.Fatalf("%s had no rows before the migration, so surviving it proves nothing", table)
		}
	}

	if after := searchIndexRows(t, pool); !equalRows(ftsBefore, after) {
		t.Fatalf("the search index changed across the migration:\nbefore %v\nafter  %v", ftsBefore, after)
	}
	if after := referencedMediaIDs(t, pool); !equalRows(referencedBefore, after) {
		t.Fatalf("media reference resolution changed:\nbefore %v\nafter  %v", referencedBefore, after)
	}

	assertNoOrphans(t, pool)
	assertRelaxedChecks(t, pool)
	assertArticleTablesUsable(t, pool)
}

func assertRelaxedChecks(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()

	if _, err := pool.ExecContext(ctx, `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FD1', 'article', NULL, 1, 1, 1, 'written')`); err != nil {
		t.Fatalf("an article entry with no Markdown must be accepted: %v", err)
	}

	for name, statement := range map[string]string{
		"a moment without Markdown": `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FD2', 'moment', NULL, 1, 1, 1)`,
		"an article carrying Markdown": `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FD3', 'article', 'body', 1, 1, 1)`,
		"an unknown kind": `
INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FD4', 'note', 'body', 1, 1, 1)`,
	} {
		if _, err := pool.ExecContext(ctx, statement); err == nil {
			t.Fatalf("%s must be refused", name)
		}
	}

	if _, err := pool.ExecContext(ctx,
		`INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 49, NULL)`,
		fixtureEntryOne, fixtureMediaOne); err != nil {
		t.Fatalf("an article body may place more than nine pictures: %v", err)
	}
	if _, err := pool.ExecContext(ctx,
		`INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 50, NULL)`,
		fixtureEntryOne, fixtureMediaOne); err == nil {
		t.Fatal("position 50 must still be refused")
	}
}

func assertArticleTablesUsable(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	const articleID = "01ARZ3NDEKTSV4RRFFQ69G5FD1"

	if _, err := pool.ExecContext(ctx, `
INSERT INTO entry_article (entry_id, title, body_json, body_schema_version, cover_media_id, excerpt)
VALUES (?, '标题', '{"schema_version":1,"type":"doc","content":[]}', 1, ?, '摘要')`,
		articleID, fixtureMediaTwo); err != nil {
		t.Fatalf("insert article: %v", err)
	}
	// The satellite belongs to an article, and the trigger is what says so.
	if _, err := pool.ExecContext(ctx, `
INSERT INTO entry_article (entry_id, title, body_json, body_schema_version, cover_media_id, excerpt)
VALUES (?, '标题', '{"schema_version":1,"type":"doc","content":[]}', 1, NULL, '')`,
		fixtureEntryOne); err == nil {
		t.Fatal("a moment must not carry an article body")
	}

	const draftID = "01ARZ3NDEKTSV4RRFFQ69G5FE1"
	if _, err := pool.ExecContext(ctx, `
INSERT INTO article_drafts (id, title, body_json, body_schema_version, cover_media_id, published_entry_id, created_at_ms, updated_at_ms)
VALUES (?, '', '{"schema_version":1,"type":"doc","content":[]}', 1, NULL, ?, 1, 1)`,
		draftID, articleID); err != nil {
		t.Fatalf("a draft may have no title yet: %v", err)
	}
	if _, err := pool.ExecContext(ctx, `
INSERT INTO article_drafts (id, title, body_json, body_schema_version, cover_media_id, published_entry_id, created_at_ms, updated_at_ms)
VALUES ('01ARZ3NDEKTSV4RRFFQ69G5FE2', '', '{"schema_version":1,"type":"doc","content":[]}', 1, NULL, ?, 1, 1)`,
		articleID); err == nil {
		t.Fatal("one published article may have at most one working copy")
	}

	if _, err := pool.ExecContext(ctx,
		`INSERT INTO article_draft_media (draft_id, media_id, position, alt_text) VALUES (?, ?, 0, NULL)`,
		draftID, fixtureMediaOne); err != nil {
		t.Fatalf("insert draft media: %v", err)
	}
	if _, err := pool.ExecContext(ctx, `DELETE FROM article_drafts WHERE id = ?`, draftID); err != nil {
		t.Fatalf("delete draft: %v", err)
	}
	var remaining int
	if err := pool.QueryRowContext(ctx,
		`SELECT count(*) FROM article_draft_media WHERE draft_id = ?`, draftID).Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("draft media should follow its draft, %d rows left", remaining)
	}
	assertNoOrphans(t, pool)
}

func assertNoOrphans(t *testing.T, pool *sql.DB) {
	t.Helper()
	rows, err := pool.QueryContext(context.Background(), `PRAGMA foreign_key_check`)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rows.Close() }()
	var violations []string
	for rows.Next() {
		var table, parent sql.NullString
		var rowID, fkID sql.NullInt64
		if err := rows.Scan(&table, &rowID, &parent, &fkID); err != nil {
			t.Fatal(err)
		}
		violations = append(violations, fmt.Sprintf("%s -> %s", table.String, parent.String))
	}
	if len(violations) > 0 {
		t.Fatalf("foreign key violations: %s", strings.Join(violations, ", "))
	}
}

func tableCounts(t *testing.T, pool *sql.DB) map[string]int {
	t.Helper()
	counts := make(map[string]int, len(migrationFixtureTables))
	for _, table := range migrationFixtureTables {
		var count int
		if err := pool.QueryRowContext(context.Background(),
			`SELECT count(*) FROM `+table).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", table, err)
		}
		counts[table] = count
	}
	return counts
}

func searchIndexRows(t *testing.T, pool *sql.DB) []string {
	t.Helper()
	return queryStrings(t, pool, `SELECT entry_id || '\t' || content_text FROM entry_fts ORDER BY entry_id`)
}

// The live predicate names tables 0025 creates, so it cannot be asked the same
// question on both sides of the migration. This is the predicate as it stood
// before, spelled out: what it resolves must not change, whatever else does.
const mediaReferencedBefore0025SQL = `(
    EXISTS (SELECT 1 FROM entry_media AS usage WHERE usage.media_id = media.id)
    OR EXISTS (SELECT 1 FROM comment_media AS usage WHERE usage.media_id = media.id)
    OR EXISTS (SELECT 1 FROM users AS account WHERE account.avatar_media_id = media.id)
    OR EXISTS (SELECT 1 FROM users AS account WHERE account.cover_media_id = media.id)
    OR EXISTS (SELECT 1 FROM entry_link_card AS card WHERE card.thumbnail_media_id = media.id)
    OR EXISTS (SELECT 1 FROM entry_link_card AS card WHERE card.favicon_media_id = media.id)
)`

func referencedMediaIDs(t *testing.T, pool *sql.DB) []string {
	t.Helper()
	return queryStrings(t, pool, `SELECT id FROM media WHERE `+mediaReferencedBefore0025SQL+` ORDER BY id`)
}

func queryStrings(t *testing.T, pool *sql.DB, statement string) []string {
	t.Helper()
	rows, err := pool.QueryContext(context.Background(), statement)
	if err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
	defer func() { _ = rows.Close() }()
	var values []string
	for rows.Next() {
		var value string
		if err := rows.Scan(&value); err != nil {
			t.Fatal(err)
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return values
}

func equalRows(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func seedMigrationFixture(t *testing.T, pool *sql.DB) {
	t.Helper()
	ctx := context.Background()
	statements := []struct {
		sql       string
		arguments []any
	}{
		{`INSERT INTO media (id, sha256, storage_path, original_name, mime_type, byte_size, width, height, created_at_ms, unreferenced_since_ms)
VALUES (?, ?, ?, 'a.jpg', 'image/jpeg', 1, 1, 1, 1, NULL)`,
			[]any{fixtureMediaOne, strings.Repeat("a", 64), canonicalMediaPath(strings.Repeat("a", 64))}},
		{`INSERT INTO media (id, sha256, storage_path, original_name, mime_type, byte_size, width, height, created_at_ms, unreferenced_since_ms)
VALUES (?, ?, ?, 'b.jpg', 'image/jpeg', 1, 1, 1, 1, NULL)`,
			[]any{fixtureMediaTwo, strings.Repeat("b", 64), canonicalMediaPath(strings.Repeat("b", 64))}},
		// Migration 0010 already installed the single owner this schema allows,
		// so the fixture adopts it and gives it an avatar to reference.
		{`UPDATE users SET avatar_media_id = ? WHERE role = 'owner'`, []any{fixtureMediaTwo}},
		{`INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text)
VALUES (?, 'moment', '第一条 **正文**', 1, 1, 1, '第一条 正文')`, []any{fixtureEntryOne}},
		{`INSERT INTO entries (id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms, content_text)
VALUES (?, 'moment', '被引用的那条', 2, 2, 2, '被引用的那条')`, []any{fixtureEntryTwo}},
		{`INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 0, '一张图')`,
			[]any{fixtureEntryOne, fixtureMediaOne}},
		{`INSERT INTO entry_media (entry_id, media_id, position, alt_text) VALUES (?, ?, 8, NULL)`,
			[]any{fixtureEntryOne, fixtureMediaTwo}},
		{`INSERT INTO entry_location (entry_id, name, display_name, country_code, country, region, city, district, category, provider, provider_place_id, latitude_e7, longitude_e7, coordinate_system)
VALUES (?, '静安寺', '上海市静安区南京西路1686号', 'CN', '中国', '上海市', '上海市', '静安区', '风景名胜', 'amap', 'B0FFF', 312231860, 1214452600, 'gcj02')`,
			[]any{fixtureEntryOne}},
		{`INSERT INTO entry_quotes (entry_id, quoted_entry_id) VALUES (?, ?)`,
			[]any{fixtureEntryOne, fixtureEntryTwo}},
		{`INSERT INTO entry_link_card (entry_id, position, source_url, canonical_url, provider, card_kind, title, description, site_name, author_name, duration_seconds, thumbnail_media_id, embed_url, captured_at_ms)
VALUES (?, 0, 'https://example.com/a', 'https://example.com/a', 'generic', 'link', 't', '', 'example.com', '', NULL, ?, '', 1)`,
			[]any{fixtureEntryTwo, fixtureMediaTwo}},
		{`INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms, author_user_id)
VALUES (?, ?, NULL, 'owner', 'Owner', '根评论', 1, (SELECT id FROM users WHERE role = 'owner'))`,
			[]any{fixtureCommentOne, fixtureEntryOne}},
		{`INSERT INTO comments (id, entry_id, parent_id, author_kind, author_name, content_md, created_at_ms, reply_to_comment_id)
VALUES (?, ?, ?, 'guest', 'Guest', '回复', 2, ?)`,
			[]any{fixtureCommentTwo, fixtureEntryOne, fixtureCommentOne, fixtureCommentOne}},
		{`INSERT INTO comment_media (comment_id, media_id, position, alt_text) VALUES (?, ?, 0, NULL)`,
			[]any{fixtureCommentTwo, fixtureMediaOne}},
		{`INSERT INTO comment_location (comment_id, name, display_name, country_code, country, region, city, district, category, provider, provider_place_id, latitude_e7, longitude_e7, coordinate_system)
VALUES (?, '外滩', '上海市黄浦区中山东一路', 'CN', '中国', '上海市', '上海市', '黄浦区', '风景名胜', 'amap', 'B0GGG', 312333000, 1214900000, 'gcj02')`,
			[]any{fixtureCommentOne}},
		{`INSERT INTO comment_pins (entry_id, comment_id) VALUES (?, ?)`,
			[]any{fixtureEntryOne, fixtureCommentOne}},
		// The single settings row is installed by migration too; pinning an
		// entry is what makes it reference the table being rebuilt.
		{`UPDATE site_settings SET pinned_entry_id = ? WHERE id = 1`, []any{fixtureEntryOne}},
	}
	for _, statement := range statements {
		if _, err := pool.ExecContext(ctx, statement.sql, statement.arguments...); err != nil {
			t.Fatalf("seed failed: %v\n%s", err, statement.sql)
		}
	}
}

// The media table carries a trigger that insists the stored path is derived
// from the digest, so the fixture derives it the same way.
func canonicalMediaPath(sha string) string {
	return "media/original/" + sha[:2] + "/" + sha + ".jpg"
}
