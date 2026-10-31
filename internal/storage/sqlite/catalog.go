package sqlite

import (
	"context"
	"database/sql"
	"fmt"
)

type expectedCatalogObject struct {
	name  string
	kind  string
	table string
}

var requiredCatalogObjects = []expectedCatalogObject{
	{name: "post_likes", kind: "table", table: "post_likes"},
	{name: "post_likes_user", kind: "index", table: "post_likes"},
	{name: "post_likes_account", kind: "index", table: "post_likes"},
	{name: "post_likes_snapshot", kind: "index", table: "post_likes"},
	{name: "comment_likes", kind: "table", table: "comment_likes"},
	{name: "comment_likes_user", kind: "index", table: "comment_likes"},
	{name: "comment_likes_account", kind: "index", table: "comment_likes"},
	{name: "comment_likes_snapshot", kind: "index", table: "comment_likes"},
	{name: "entry_mentions", kind: "table", table: "entry_mentions"},
	{name: "comment_mentions", kind: "table", table: "comment_mentions"},
	{name: "comment_reply_recipients", kind: "table", table: "comment_reply_recipients"},
	{name: "comment_reply_recipients_account", kind: "index", table: "comment_reply_recipients"},
	{name: "comment_reply_recipients_snapshot", kind: "index", table: "comment_reply_recipients"},
	{name: "notifications", kind: "table", table: "notifications"},
	{name: "notifications_recipient_sequence", kind: "index", table: "notifications"},
	{name: "notifications_subject", kind: "index", table: "notifications"},
	{name: "notifications_recipient_time", kind: "index", table: "notifications"},
	{name: "notifications_live_sequence", kind: "index", table: "notifications"},
	{name: "notification_seen", kind: "table", table: "notification_seen"},
	{name: "site_pin_insert_guard", kind: "trigger", table: "site_settings"},
	{name: "site_pin_update_guard", kind: "trigger", table: "site_settings"},
	{name: "entries_clear_unavailable_pin", kind: "trigger", table: "entries"},
	{name: "media_grants", kind: "table", table: "media_grants"},
	{name: "media_grants_media_idx", kind: "index", table: "media_grants"},
	{name: "schema_migrations", kind: "table", table: "schema_migrations"},
	{name: "entries", kind: "table", table: "entries"},
	{name: "media", kind: "table", table: "media"},
	{name: "entry_media", kind: "table", table: "entry_media"},
	{name: "comment_media", kind: "table", table: "comment_media"},
	{name: "entry_link_card", kind: "table", table: "entry_link_card"},
	{name: "comments", kind: "table", table: "comments"},
	{name: "site_settings", kind: "table", table: "site_settings"},
	{name: "users", kind: "table", table: "users"},
	{name: "identities", kind: "table", table: "identities"},
	{name: "sessions", kind: "table", table: "sessions"},
	{name: "account_passwords", kind: "table", table: "account_passwords"},
	{name: "api_tokens", kind: "table", table: "api_tokens"},
	{name: "api_tokens_user_idx", kind: "index", table: "api_tokens"},
	{name: "idempotency_keys", kind: "table", table: "idempotency_keys"},
	{name: "search_index_state", kind: "table", table: "search_index_state"},
	{name: "entries_timeline_idx", kind: "index", table: "entries"},
	{name: "entries_published_seek_idx", kind: "index", table: "entries"},
	{name: "entries_kind_published_seek_idx", kind: "index", table: "entries"},
	{name: "entries_reposted_seek_idx", kind: "index", table: "entries"},
	{name: "entries_kind_reposted_seek_idx", kind: "index", table: "entries"},

	{name: "entry_media_media_idx", kind: "index", table: "entry_media"},
	{name: "comment_media_media_idx", kind: "index", table: "comment_media"},
	{name: "entry_link_card_media_idx", kind: "index", table: "entry_link_card"},
	{name: "entry_link_card_favicon_idx", kind: "index", table: "entry_link_card"},
	{name: "comments_entry_idx", kind: "index", table: "comments"},
	{name: "comments_parent_idx", kind: "index", table: "comments"},
	{name: "users_role_status_idx", kind: "index", table: "users"},
	{name: "users_username_unique", kind: "index", table: "users"},
	{name: "identities_user_idx", kind: "index", table: "identities"},
	{name: "sessions_user_idx", kind: "index", table: "sessions"},
	{name: "idempotency_keys_created_idx", kind: "index", table: "idempotency_keys"},
	{name: "comments_parent_insert_guard", kind: "trigger", table: "comments"},
	{name: "comments_parent_update_guard", kind: "trigger", table: "comments"},
	{name: "media_storage_path_insert_guard", kind: "trigger", table: "media"},
	{name: "media_storage_path_update_guard", kind: "trigger", table: "media"},
	{name: "entries_search_insert", kind: "trigger", table: "entries"},
	{name: "entries_search_update", kind: "trigger", table: "entries"},
	{name: "entries_search_delete", kind: "trigger", table: "entries"},
	{name: "entries_search_revision_insert", kind: "trigger", table: "entries"},
	{name: "entries_search_revision_update", kind: "trigger", table: "entries"},
	{name: "entries_search_revision_delete", kind: "trigger", table: "entries"},
	{name: "entry_article", kind: "table", table: "entry_article"},
	{name: "article_drafts", kind: "table", table: "article_drafts"},
	{name: "article_draft_media", kind: "table", table: "article_draft_media"},
	{name: "entry_article_cover_idx", kind: "index", table: "entry_article"},
	{name: "article_drafts_published_entry_idx", kind: "index", table: "article_drafts"},
	{name: "article_drafts_cover_idx", kind: "index", table: "article_drafts"},
	{name: "article_draft_media_media_idx", kind: "index", table: "article_draft_media"},
	{name: "entry_article_kind_insert_guard", kind: "trigger", table: "entry_article"},
	{name: "entry_article_kind_update_guard", kind: "trigger", table: "entry_article"},
}

type catalogQueryer interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

// verifySchemaCatalog checks stable object identities instead of comparing
// SQLite's formatting of complete CREATE statements. Extra objects remain
// valid so future migrations can extend the schema independently.
func verifySchemaCatalog(ctx context.Context, queryer catalogQueryer) error {
	rows, err := queryer.QueryContext(ctx, `
SELECT type, name, tbl_name
FROM sqlite_schema
WHERE name NOT LIKE 'sqlite_%'`)
	if err != nil {
		return fmt.Errorf("%w: read sqlite_schema: %w", ErrSchemaIncomplete, err)
	}
	defer func() { _ = rows.Close() }()

	actual := make(map[string]expectedCatalogObject)
	for rows.Next() {
		var kind, name, table string
		if err := rows.Scan(&kind, &name, &table); err != nil {
			return fmt.Errorf("%w: scan sqlite_schema: %w", ErrSchemaIncomplete, err)
		}
		actual[name] = expectedCatalogObject{kind: kind, table: table}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("%w: iterate sqlite_schema: %w", ErrSchemaIncomplete, err)
	}
	for _, expected := range requiredCatalogObjects {
		object, exists := actual[expected.name]
		if !exists {
			return fmt.Errorf("%w: required %s %q is missing", ErrSchemaIncomplete, expected.kind, expected.name)
		}
		if object.kind != expected.kind || object.table != expected.table {
			return fmt.Errorf("%w: object %q is %s on %q, expected %s on %q", ErrSchemaIncomplete, expected.name, object.kind, object.table, expected.kind, expected.table)
		}
	}

	var ftsType string
	if err := queryer.QueryRowContext(ctx, `
SELECT type
FROM pragma_table_list
WHERE schema = 'main' AND name = 'entry_fts'`).Scan(&ftsType); err != nil {
		return fmt.Errorf("%w: required FTS table entry_fts is missing: %w", ErrSchemaIncomplete, err)
	}
	if ftsType != "virtual" {
		return fmt.Errorf("%w: entry_fts has type %q, expected virtual", ErrSchemaIncomplete, ftsType)
	}
	return nil
}
