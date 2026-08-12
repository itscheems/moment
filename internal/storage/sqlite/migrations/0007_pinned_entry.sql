ALTER TABLE site_settings
ADD COLUMN pinned_entry_id TEXT NULL REFERENCES entries(id) ON DELETE SET NULL;
