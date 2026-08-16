DROP TRIGGER media_storage_path_insert_guard;
DROP TRIGGER media_storage_path_update_guard;

ALTER TABLE entry_media RENAME TO entry_media_v1;
ALTER TABLE comment_media RENAME TO comment_media_v1;
ALTER TABLE site_settings RENAME TO site_settings_v1;
ALTER TABLE media RENAME TO media_v1;

CREATE TABLE media (
    id TEXT PRIMARY KEY CHECK (
        length(id) = 26 AND substr(id, 1, 1) BETWEEN '0' AND '7'
        AND id NOT GLOB '*[^0123456789ABCDEFGHJKMNPQRSTVWXYZ]*'
    ),
    sha256 TEXT NOT NULL UNIQUE CHECK (
        length(sha256) = 64 AND sha256 NOT GLOB '*[^0-9a-f]*'
    ),
    storage_path TEXT NOT NULL UNIQUE CHECK (
        storage_path <> '' AND substr(storage_path, 1, 1) <> '/'
        AND instr(storage_path, '\') = 0
        AND instr('/' || storage_path || '/', '/../') = 0
        AND instr(storage_path, char(0)) = 0
    ),
    original_name TEXT NOT NULL CHECK (original_name <> ''),
    mime_type TEXT NOT NULL CHECK (
        mime_type IN (
            'image/jpeg', 'image/png', 'image/webp', 'image/gif',
            'image/heic', 'video/mp4', 'video/quicktime'
        )
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
        alt_text IS NULL OR alt_text = '' OR (
            length(alt_text) <= 1000
            AND trim(alt_text, char(9) || char(10) || char(11) || char(12)
                || char(13) || char(32) || char(133) || char(160)
                || char(5760) || char(8192) || char(8193) || char(8194)
                || char(8195) || char(8196) || char(8197) || char(8198)
                || char(8199) || char(8200) || char(8201) || char(8202)
                || char(8232) || char(8233) || char(8239) || char(8287)
                || char(12288)) <> ''
        )
    ),
    PRIMARY KEY (entry_id, position)
) STRICT;

CREATE TABLE comment_media (
    comment_id TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id) ON DELETE RESTRICT,
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 3),
    alt_text TEXT CHECK (
        alt_text IS NULL OR alt_text = '' OR (
            length(alt_text) <= 1000
            AND trim(alt_text, char(9) || char(10) || char(11) || char(12)
                || char(13) || char(32) || char(133) || char(160)
                || char(5760) || char(8192) || char(8193) || char(8194)
                || char(8195) || char(8196) || char(8197) || char(8198)
                || char(8199) || char(8200) || char(8201) || char(8202)
                || char(8232) || char(8233) || char(8239) || char(8287)
                || char(12288)) <> ''
        )
    ),
    PRIMARY KEY (comment_id, position)
) STRICT;

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
    comments_enabled INTEGER NOT NULL CHECK (comments_enabled IN (0, 1)),
    cover_media_id TEXT REFERENCES media(id) ON DELETE RESTRICT,
    pinned_entry_id TEXT NULL REFERENCES entries(id) ON DELETE SET NULL,
    owner_id TEXT NOT NULL CHECK (
        length(owner_id) = 32 AND owner_id NOT GLOB '*[^0-9a-f]*'
    ),
    username TEXT NOT NULL COLLATE NOCASE CHECK (
        length(username) BETWEEN 3 AND 30 AND username = lower(username)
        AND substr(username, 1, 1) GLOB '[a-z]'
        AND username NOT GLOB '*[^a-z0-9_]*'
    ),
    display_name TEXT NOT NULL CHECK (
        display_name <> '' AND display_name = trim(display_name)
        AND length(display_name) <= 80
    )
) STRICT;

INSERT INTO media SELECT * FROM media_v1;
INSERT INTO entry_media SELECT * FROM entry_media_v1;
INSERT INTO comment_media SELECT * FROM comment_media_v1;
INSERT INTO site_settings SELECT * FROM site_settings_v1;

DROP TABLE entry_media_v1;
DROP TABLE comment_media_v1;
DROP TABLE site_settings_v1;
DROP TABLE media_v1;

CREATE INDEX entry_media_media_idx ON entry_media (media_id);
CREATE INDEX comment_media_media_idx ON comment_media (media_id);
CREATE UNIQUE INDEX site_settings_username_unique
ON site_settings(username COLLATE NOCASE);

CREATE TRIGGER media_storage_path_insert_guard
BEFORE INSERT ON media
WHEN NEW.storage_path <> (
    'media/original/' || substr(NEW.sha256, 1, 2) || '/' || NEW.sha256
    || CASE NEW.mime_type
        WHEN 'image/jpeg' THEN '.jpg' WHEN 'image/png' THEN '.png'
        WHEN 'image/webp' THEN '.webp' WHEN 'image/gif' THEN '.gif'
        WHEN 'image/heic' THEN '.heic' WHEN 'video/mp4' THEN '.mp4'
        WHEN 'video/quicktime' THEN '.mov'
       END
)
BEGIN
    SELECT RAISE(ABORT, 'media storage_path must be canonical');
END;

CREATE TRIGGER media_storage_path_update_guard
BEFORE UPDATE OF sha256, storage_path, mime_type ON media
WHEN NEW.storage_path <> (
    'media/original/' || substr(NEW.sha256, 1, 2) || '/' || NEW.sha256
    || CASE NEW.mime_type
        WHEN 'image/jpeg' THEN '.jpg' WHEN 'image/png' THEN '.png'
        WHEN 'image/webp' THEN '.webp' WHEN 'image/gif' THEN '.gif'
        WHEN 'image/heic' THEN '.heic' WHEN 'video/mp4' THEN '.mp4'
        WHEN 'video/quicktime' THEN '.mov'
       END
)
BEGIN
    SELECT RAISE(ABORT, 'media storage_path must be canonical');
END;
