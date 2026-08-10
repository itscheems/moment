ALTER TABLE entries
ADD COLUMN content_text TEXT NOT NULL DEFAULT '';

CREATE TABLE search_index_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    normalizer_version INTEGER NOT NULL CHECK (normalizer_version >= 0)
) STRICT;

INSERT INTO search_index_state (id, normalizer_version) VALUES (1, 0);

DELETE FROM entry_fts;

CREATE TRIGGER entries_search_insert
AFTER INSERT ON entries
BEGIN
    INSERT INTO entry_fts (entry_id, content_text)
    VALUES (NEW.id, NEW.content_text);
END;

CREATE TRIGGER entries_search_update
AFTER UPDATE OF content_text ON entries
BEGIN
    DELETE FROM entry_fts WHERE entry_id = OLD.id;
    INSERT INTO entry_fts (entry_id, content_text)
    VALUES (NEW.id, NEW.content_text);
END;

CREATE TRIGGER entries_search_delete
AFTER DELETE ON entries
BEGIN
    DELETE FROM entry_fts WHERE entry_id = OLD.id;
END;
