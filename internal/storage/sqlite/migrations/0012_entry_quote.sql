CREATE TABLE entry_quotes (
    entry_id TEXT PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    quoted_entry_id TEXT REFERENCES entries(id) ON DELETE SET NULL,
    CHECK (quoted_entry_id IS NULL OR quoted_entry_id <> entry_id)
) STRICT;

CREATE INDEX entry_quotes_quoted_entry_id_idx ON entry_quotes(quoted_entry_id);
