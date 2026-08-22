ALTER TABLE entry_link_card
ADD COLUMN favicon_media_id TEXT REFERENCES media(id) ON DELETE RESTRICT;

CREATE INDEX entry_link_card_favicon_idx ON entry_link_card (favicon_media_id);
