ALTER TABLE entry_link_card ADD COLUMN display_mode TEXT NOT NULL DEFAULT 'cover'
    CHECK (display_mode IN ('cover', 'compact'));
