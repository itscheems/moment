CREATE TABLE entry_map_card (
    entry_id TEXT PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    map_card_json TEXT NOT NULL CHECK (
        json_valid(map_card_json)
        AND json_type(map_card_json) = 'object'
        AND json_type(map_card_json, '$.places') = 'array'
        AND json_array_length(map_card_json, '$.places') BETWEEN 1 AND 20
    )
) STRICT;

CREATE TRIGGER entry_map_card_moment_only
BEFORE INSERT ON entry_map_card
WHEN (SELECT kind FROM entries WHERE id = NEW.entry_id) <> 'moment'
BEGIN
    SELECT RAISE(ABORT, 'only moments may have a top-level map');
END;
