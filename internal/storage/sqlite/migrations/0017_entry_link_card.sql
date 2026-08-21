CREATE TABLE entry_link_card (
    entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    position INTEGER NOT NULL CHECK (position >= 0 AND position <= 8),
    source_url TEXT NOT NULL CHECK (source_url <> '' AND source_url = trim(source_url) AND length(source_url) <= 2048),
    canonical_url TEXT NOT NULL CHECK (canonical_url <> '' AND canonical_url = trim(canonical_url) AND length(canonical_url) <= 2048),
    provider TEXT NOT NULL CHECK (provider IN ('generic', 'youtube', 'bilibili')),
    card_kind TEXT NOT NULL CHECK (card_kind IN ('link', 'video')),
    title TEXT NOT NULL CHECK (title = trim(title) AND length(title) <= 300),
    description TEXT NOT NULL CHECK (description = trim(description) AND length(description) <= 1000),
    site_name TEXT NOT NULL CHECK (site_name = trim(site_name) AND length(site_name) <= 160),
    author_name TEXT NOT NULL CHECK (author_name = trim(author_name) AND length(author_name) <= 160),
    duration_seconds INTEGER CHECK (duration_seconds IS NULL OR (duration_seconds > 0 AND duration_seconds <= 86400)),
    thumbnail_media_id TEXT REFERENCES media(id) ON DELETE RESTRICT,
    embed_url TEXT NOT NULL CHECK (embed_url = trim(embed_url) AND length(embed_url) <= 2048),
    captured_at_ms INTEGER NOT NULL CHECK (captured_at_ms > 0),
    PRIMARY KEY (entry_id, position),
    CHECK (card_kind <> 'link' OR embed_url = '')
) STRICT;

CREATE INDEX entry_link_card_media_idx ON entry_link_card (thumbnail_media_id);
