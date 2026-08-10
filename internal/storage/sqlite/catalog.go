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
	{name: "schema_migrations", kind: "table", table: "schema_migrations"},
	{name: "entries", kind: "table", table: "entries"},
	{name: "media", kind: "table", table: "media"},
	{name: "entry_media", kind: "table", table: "entry_media"},
	{name: "comments", kind: "table", table: "comments"},
	{name: "site_settings", kind: "table", table: "site_settings"},
	{name: "idempotency_keys", kind: "table", table: "idempotency_keys"},
	{name: "entries_timeline_idx", kind: "index", table: "entries"},
	{name: "entry_media_media_idx", kind: "index", table: "entry_media"},
	{name: "comments_entry_idx", kind: "index", table: "comments"},
	{name: "comments_parent_idx", kind: "index", table: "comments"},
	{name: "idempotency_keys_created_idx", kind: "index", table: "idempotency_keys"},
	{name: "comments_parent_insert_guard", kind: "trigger", table: "comments"},
	{name: "comments_parent_update_guard", kind: "trigger", table: "comments"},
	{name: "media_storage_path_insert_guard", kind: "trigger", table: "media"},
	{name: "media_storage_path_update_guard", kind: "trigger", table: "media"},
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
