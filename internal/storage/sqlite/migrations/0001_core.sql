CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY CHECK (version > 0),
    name TEXT NOT NULL UNIQUE CHECK (name <> ''),
    applied_at_ms INTEGER NOT NULL CHECK (applied_at_ms > 0),
    checksum TEXT NOT NULL CHECK (
        length(checksum) = 64
        AND checksum NOT GLOB '*[^0-9a-f]*'
    )
) STRICT;

CREATE TABLE entries (
    id TEXT PRIMARY KEY CHECK (
        length(id) = 26
        AND substr(id, 1, 1) BETWEEN '0' AND '7'
        AND id NOT GLOB '*[^0123456789ABCDEFGHJKMNPQRSTVWXYZ]*'
    ),
    kind TEXT NOT NULL CHECK (kind = 'moment'),
    content_md TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL CHECK (updated_at_ms >= created_at_ms),
    published_at_ms INTEGER NOT NULL
) STRICT;

CREATE INDEX entries_timeline_idx
    ON entries (kind, published_at_ms DESC, id DESC);

CREATE TABLE media (
    id TEXT PRIMARY KEY CHECK (
        length(id) = 26
        AND substr(id, 1, 1) BETWEEN '0' AND '7'
        AND id NOT GLOB '*[^0123456789ABCDEFGHJKMNPQRSTVWXYZ]*'
    ),
    sha256 TEXT NOT NULL UNIQUE CHECK (
        length(sha256) = 64
        AND sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    storage_path TEXT NOT NULL UNIQUE CHECK (
        storage_path <> ''
        AND substr(storage_path, 1, 1) <> '/'
        AND instr(storage_path, '\') = 0
        AND instr('/' || storage_path || '/', '/../') = 0
        AND instr(storage_path, char(0)) = 0
    ),
    original_name TEXT NOT NULL CHECK (original_name <> ''),
    mime_type TEXT NOT NULL CHECK (
        mime_type IN ('image/jpeg', 'image/png', 'image/webp', 'image/gif')
    ),
    byte_size INTEGER NOT NULL CHECK (byte_size > 0),
    width INTEGER NOT NULL CHECK (width > 0),
    height INTEGER NOT NULL CHECK (height > 0),
    created_at_ms INTEGER NOT NULL,
    unreferenced_since_ms INTEGER CHECK (
        unreferenced_since_ms IS NULL OR unreferenced_since_ms >= created_at_ms
    )
) STRICT;

CREATE TABLE entry_media (
    entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id) ON DELETE RESTRICT,
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 8),
    alt_text TEXT CHECK (
        alt_text IS NULL
        OR alt_text = ''
        OR (
            length(alt_text) <= 1000
            AND trim(
                alt_text,
                char(9) || char(10) || char(11) || char(12) || char(13)
                || char(32) || char(133) || char(160) || char(5760)
                || char(8192) || char(8193) || char(8194) || char(8195)
                || char(8196) || char(8197) || char(8198) || char(8199)
                || char(8200) || char(8201) || char(8202) || char(8232)
                || char(8233) || char(8239) || char(8287) || char(12288)
            ) <> ''
        )
    ),
    PRIMARY KEY (entry_id, position)
) STRICT;

CREATE INDEX entry_media_media_idx ON entry_media (media_id);

CREATE TABLE comments (
    id TEXT PRIMARY KEY CHECK (
        length(id) = 26
        AND substr(id, 1, 1) BETWEEN '0' AND '7'
        AND id NOT GLOB '*[^0123456789ABCDEFGHJKMNPQRSTVWXYZ]*'
    ),
    entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    parent_id TEXT REFERENCES comments(id) ON DELETE CASCADE,
    author_kind TEXT NOT NULL CHECK (author_kind IN ('owner', 'guest')),
    author_name TEXT NOT NULL,
    content_md TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL
) STRICT;

CREATE INDEX comments_entry_idx ON comments (entry_id, created_at_ms, id);
CREATE INDEX comments_parent_idx ON comments (parent_id, created_at_ms, id);

CREATE TRIGGER comments_parent_insert_guard
BEFORE INSERT ON comments
WHEN NEW.parent_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'comment parent must be a root in the same entry')
    WHERE NEW.parent_id = NEW.id
       OR NOT EXISTS (
            SELECT 1
            FROM comments AS parent
            WHERE parent.id = NEW.parent_id
              AND parent.entry_id = NEW.entry_id
              AND parent.parent_id IS NULL
       );
END;

CREATE TRIGGER comments_parent_update_guard
BEFORE UPDATE OF entry_id, parent_id ON comments
BEGIN
    SELECT RAISE(ABORT, 'comment parent must be a root in the same entry')
    WHERE NEW.parent_id IS NOT NULL
      AND (
          NEW.parent_id = NEW.id
          OR NOT EXISTS (
                SELECT 1
                FROM comments AS parent
                WHERE parent.id = NEW.parent_id
                  AND parent.entry_id = NEW.entry_id
                  AND parent.parent_id IS NULL
          )
      );

    SELECT RAISE(ABORT, 'a comment with replies cannot become a reply')
    WHERE NEW.parent_id IS NOT NULL
      AND EXISTS (SELECT 1 FROM comments AS child WHERE child.parent_id = NEW.id);

    SELECT RAISE(ABORT, 'comment replies must remain in the same entry')
    WHERE EXISTS (
        SELECT 1
        FROM comments AS child
        WHERE child.parent_id = NEW.id
          AND child.entry_id <> NEW.entry_id
    );
END;

CREATE TABLE site_settings (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    title TEXT NOT NULL,
    bio TEXT NOT NULL,
    timezone TEXT NOT NULL CHECK (timezone <> ''),
    public_base_url TEXT CHECK (
        public_base_url IS NULL
        OR (public_base_url <> '' AND public_base_url = trim(public_base_url))
    ),
    avatar_media_id TEXT REFERENCES media(id) ON DELETE RESTRICT,
    comments_enabled INTEGER NOT NULL CHECK (comments_enabled IN (0, 1))
) STRICT;

CREATE TABLE idempotency_keys (
    scope TEXT NOT NULL CHECK (scope <> ''),
    key TEXT NOT NULL CHECK (key <> ''),
    request_hash TEXT NOT NULL CHECK (
        length(request_hash) = 64
        AND request_hash NOT GLOB '*[^0-9a-f]*'
    ),
    response_resource_id TEXT NOT NULL CHECK (response_resource_id <> ''),
    created_at_ms INTEGER NOT NULL,
    PRIMARY KEY (scope, key)
) STRICT;

CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at_ms);

CREATE VIRTUAL TABLE entry_fts USING fts5(
    entry_id UNINDEXED,
    content_text,
    tokenize = 'trigram'
);
