CREATE TABLE comment_pins (
    entry_id TEXT PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    comment_id TEXT NOT NULL UNIQUE REFERENCES comments(id) ON DELETE CASCADE
) STRICT;

CREATE TRIGGER comment_pins_root_insert_guard
BEFORE INSERT ON comment_pins
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1
    FROM comments
    WHERE id = NEW.comment_id
      AND entry_id = NEW.entry_id
      AND parent_id IS NULL
)
BEGIN
    SELECT RAISE(ABORT, 'pinned comment must be a root in the same entry');
END;

CREATE TRIGGER comment_pins_root_update_guard
BEFORE UPDATE ON comment_pins
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1
    FROM comments
    WHERE id = NEW.comment_id
      AND entry_id = NEW.entry_id
      AND parent_id IS NULL
)
BEGIN
    SELECT RAISE(ABORT, 'pinned comment must be a root in the same entry');
END;
