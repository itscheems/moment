ALTER TABLE search_index_state
ADD COLUMN revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0);

CREATE TRIGGER entries_search_revision_insert
AFTER INSERT ON entries
BEGIN
    UPDATE search_index_state SET revision = revision + 1 WHERE id = 1;
END;

CREATE TRIGGER entries_search_revision_update
AFTER UPDATE OF content_text ON entries
BEGIN
    UPDATE search_index_state SET revision = revision + 1 WHERE id = 1;
END;

CREATE TRIGGER entries_search_revision_delete
AFTER DELETE ON entries
BEGIN
    UPDATE search_index_state SET revision = revision + 1 WHERE id = 1;
END;
