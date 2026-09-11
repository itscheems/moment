-- Historical updates include repost activity, so unknown edit times stay NULL.
ALTER TABLE entries ADD COLUMN edited_at_ms INTEGER
    CHECK (edited_at_ms IS NULL OR (edited_at_ms >= created_at_ms AND edited_at_ms <= updated_at_ms));
