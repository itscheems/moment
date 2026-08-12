ALTER TABLE site_settings
ADD COLUMN cover_media_id TEXT REFERENCES media(id) ON DELETE RESTRICT;
