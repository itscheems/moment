-- Account identity is stable across administrator and profile changes.
ALTER TABLE entries ADD COLUMN author_user_id TEXT REFERENCES users(id) ON DELETE RESTRICT;
ALTER TABLE article_drafts ADD COLUMN author_user_id TEXT REFERENCES users(id) ON DELETE RESTRICT;

UPDATE entries SET author_user_id = (SELECT owner_id FROM site_settings WHERE id = 1);
UPDATE article_drafts SET author_user_id = (SELECT owner_id FROM site_settings WHERE id = 1);

CREATE INDEX entries_author_time_idx ON entries(author_user_id, published_at_ms DESC, id DESC);
CREATE INDEX entries_author_kind_time_idx ON entries(author_user_id, kind, published_at_ms DESC, id DESC);
CREATE INDEX article_drafts_author_time_idx ON article_drafts(author_user_id, updated_at_ms DESC, id DESC);

-- Legacy importers omit this additive field. Their original author is resolved
-- at import time; subsequent updates never reassign it.
CREATE TRIGGER entries_legacy_author AFTER INSERT ON entries
WHEN NEW.author_user_id IS NULL BEGIN
    UPDATE entries SET author_user_id = (SELECT owner_id FROM site_settings WHERE id = 1) WHERE id = NEW.id;
END;
CREATE TRIGGER article_drafts_legacy_author AFTER INSERT ON article_drafts
WHEN NEW.author_user_id IS NULL BEGIN
    UPDATE article_drafts SET author_user_id = (SELECT owner_id FROM site_settings WHERE id = 1) WHERE id = NEW.id;
END;

ALTER TABLE idempotency_keys ADD COLUMN author_user_id TEXT;
UPDATE idempotency_keys SET author_user_id = (SELECT owner_id FROM site_settings WHERE id = 1);
