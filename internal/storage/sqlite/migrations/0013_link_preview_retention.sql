CREATE TABLE link_preview_media (
    media_id TEXT PRIMARY KEY REFERENCES media(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE compose_draft_link_media (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    draft_key TEXT NOT NULL,
    media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    expires_at_ms INTEGER NOT NULL,
    PRIMARY KEY (user_id, draft_key, media_id)
) STRICT;
CREATE INDEX compose_draft_link_media_media_idx ON compose_draft_link_media(media_id);
