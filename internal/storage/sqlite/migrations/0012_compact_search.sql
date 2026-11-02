-- Keep visible text for snippets and a second indexed representation for
-- whitespace-tolerant matching. Authoritative bodies are never rewritten.
DROP TRIGGER entries_search_insert;
DROP TRIGGER entries_search_update;
DROP TABLE entry_fts;
CREATE VIRTUAL TABLE entry_fts USING fts5(
    entry_id UNINDEXED,
    content_text,
    compact_text,
    tokenize = 'trigram'
);
INSERT INTO entry_fts(entry_id, content_text, compact_text)
SELECT id, content_text, replace(content_text, ' ', '') FROM entries;
CREATE TRIGGER entries_search_insert
AFTER INSERT ON entries
BEGIN
    INSERT INTO entry_fts(entry_id, content_text, compact_text)
    VALUES (NEW.id, NEW.content_text, replace(NEW.content_text, ' ', ''));
END;
CREATE TRIGGER entries_search_update
AFTER UPDATE OF content_text ON entries
BEGIN
    DELETE FROM entry_fts WHERE entry_id = OLD.id;
    INSERT INTO entry_fts(entry_id, content_text, compact_text)
    VALUES (NEW.id, NEW.content_text, replace(NEW.content_text, ' ', ''));
END;
-- Relevance scores changed; previously issued search cursors must be refreshed.
UPDATE search_index_state SET revision = revision + 1 WHERE id = 1;
