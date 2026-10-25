ALTER TABLE entry_quotes ADD COLUMN quoted_comment_id TEXT REFERENCES comments(id) ON DELETE SET NULL
    CHECK (quoted_comment_id IS NULL OR quoted_entry_id IS NULL);
CREATE INDEX entry_quotes_comment ON entry_quotes(quoted_comment_id) WHERE quoted_comment_id IS NOT NULL;
