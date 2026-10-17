-- Account identity, content authorship and administration are independent.
-- New instances bootstrap their first named account through the application.

CREATE TABLE account_passwords (
    user_id TEXT PRIMARY KEY REFERENCES users(id) ON DELETE CASCADE,
    password_hash BLOB NOT NULL CHECK (length(password_hash) > 0),
    updated_at_ms INTEGER NOT NULL CHECK (updated_at_ms > 0)
) STRICT;

CREATE TABLE api_tokens (
    id TEXT PRIMARY KEY CHECK (length(id) = 32 AND id NOT GLOB '*[^0-9a-f]*'),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    label TEXT NOT NULL CHECK (length(label) <= 80),
    token_hash BLOB NOT NULL UNIQUE CHECK (length(token_hash) = 32),
    created_at_ms INTEGER NOT NULL CHECK (created_at_ms > 0),
    revoked_at_ms INTEGER CHECK (revoked_at_ms IS NULL OR revoked_at_ms > 0)
) STRICT;

CREATE TABLE article_draft_media (
    draft_id TEXT NOT NULL REFERENCES article_drafts(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id) ON DELETE RESTRICT,
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 49),
    alt_text TEXT CHECK (alt_text IS NULL OR length(alt_text) <= 1000),
    PRIMARY KEY (draft_id, position)
) STRICT;

CREATE TABLE article_drafts (
    id TEXT PRIMARY KEY CHECK (
        length(id) = 26
        AND substr(id, 1, 1) BETWEEN '0' AND '7'
        AND id NOT GLOB '*[^0123456789ABCDEFGHJKMNPQRSTVWXYZ]*'
    ),
    title TEXT NOT NULL CHECK (length(title) <= 200),
    body_json TEXT NOT NULL CHECK (
        body_json <> '' AND length(CAST(body_json AS BLOB)) <= 524288
    ),
    body_schema_version INTEGER NOT NULL CHECK (body_schema_version >= 1),
    cover_media_id TEXT REFERENCES media(id) ON DELETE RESTRICT,
    published_entry_id TEXT REFERENCES entries(id) ON DELETE SET NULL,
    created_at_ms INTEGER NOT NULL CHECK (created_at_ms > 0),
    updated_at_ms INTEGER NOT NULL CHECK (updated_at_ms >= created_at_ms)
, author_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT, excerpt TEXT NOT NULL DEFAULT '') STRICT;

CREATE TABLE comment_location (
    comment_id TEXT PRIMARY KEY REFERENCES comments(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (name <> '' AND name = trim(name) AND length(name) <= 160),
    display_name TEXT NOT NULL CHECK (display_name <> '' AND display_name = trim(display_name) AND length(display_name) <= 500),
    country_code TEXT NOT NULL CHECK (
        country_code = '' OR (
            length(country_code) = 2
            AND country_code = upper(country_code)
            AND country_code NOT GLOB '*[^A-Z]*'
        )
    ),
    country TEXT NOT NULL CHECK (country = trim(country) AND length(country) <= 120),
    region TEXT NOT NULL CHECK (region = trim(region) AND length(region) <= 120),
    city TEXT NOT NULL CHECK (city = trim(city) AND length(city) <= 120),
    district TEXT NOT NULL CHECK (district = trim(district) AND length(district) <= 120),
    category TEXT NOT NULL CHECK (category = trim(category) AND length(category) <= 120),
    provider TEXT NOT NULL CHECK (provider IN ('manual', 'amap', 'geoapify')),
    provider_place_id TEXT NOT NULL CHECK (provider_place_id = trim(provider_place_id) AND length(provider_place_id) <= 512),
    latitude_e7 INTEGER,
    longitude_e7 INTEGER,
    coordinate_system TEXT CHECK (coordinate_system IN ('wgs84', 'gcj02')),
    CHECK (
        (latitude_e7 IS NULL AND longitude_e7 IS NULL AND coordinate_system IS NULL)
        OR (
            latitude_e7 BETWEEN -900000000 AND 900000000
            AND longitude_e7 BETWEEN -1800000000 AND 1800000000
            AND coordinate_system IS NOT NULL
        )
    ),
    CHECK (provider <> 'manual' OR provider_place_id = '')
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

CREATE TABLE comment_pins (
    entry_id TEXT PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    comment_id TEXT NOT NULL UNIQUE REFERENCES comments(id) ON DELETE CASCADE
) STRICT;

CREATE TABLE comments (
    id TEXT PRIMARY KEY CHECK (
        length(id) = 26
        AND substr(id, 1, 1) BETWEEN '0' AND '7'
        AND id NOT GLOB '*[^0123456789ABCDEFGHJKMNPQRSTVWXYZ]*'
    ),
    entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    parent_id TEXT REFERENCES comments(id) ON DELETE CASCADE,
    author_name TEXT NOT NULL,
    content_md TEXT NOT NULL,
    created_at_ms INTEGER NOT NULL
, author_user_id TEXT REFERENCES users(id) ON DELETE SET NULL
CHECK (
    author_user_id IS NULL
    OR (
        length(author_user_id) = 32
        AND author_user_id NOT GLOB '*[^0-9a-f]*'
    )
), reply_to_comment_id TEXT REFERENCES comments(id) ON DELETE SET NULL) STRICT;

CREATE TABLE "entries" (
    id TEXT PRIMARY KEY CHECK (
        length(id) = 26
        AND substr(id, 1, 1) BETWEEN '0' AND '7'
        AND id NOT GLOB '*[^0123456789ABCDEFGHJKMNPQRSTVWXYZ]*'
    ),
    kind TEXT NOT NULL CHECK (kind IN ('moment', 'article')),
    content_md TEXT,
    created_at_ms INTEGER NOT NULL,
    updated_at_ms INTEGER NOT NULL CHECK (updated_at_ms >= created_at_ms),
    published_at_ms INTEGER NOT NULL,
    content_text TEXT NOT NULL DEFAULT '',
    reposted_at_ms INTEGER NULL CHECK (
        reposted_at_ms IS NULL OR reposted_at_ms >= published_at_ms
    ),
    edited_at_ms INTEGER CHECK (
        edited_at_ms IS NULL
        OR (edited_at_ms >= created_at_ms AND edited_at_ms <= updated_at_ms)
    ),
    visibility TEXT NOT NULL DEFAULT 'everyone'
        CHECK (visibility IN ('everyone', 'only_me')), author_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT, publication_state TEXT NOT NULL DEFAULT 'published' CHECK(publication_state IN ('published','unpublished') AND (kind = 'article' OR publication_state = 'published')),
    CHECK ((kind = 'moment') = (content_md IS NOT NULL))
) STRICT;

CREATE TABLE entry_article (
    entry_id TEXT PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    title TEXT NOT NULL CHECK (
        title <> '' AND title = trim(title) AND length(title) <= 200
    ),
    body_json TEXT NOT NULL CHECK (
        body_json <> '' AND length(CAST(body_json AS BLOB)) <= 524288
    ),
    body_schema_version INTEGER NOT NULL CHECK (body_schema_version >= 1),
    cover_media_id TEXT REFERENCES media(id) ON DELETE RESTRICT,
    excerpt TEXT NOT NULL CHECK (length(excerpt) <= 140)
) STRICT;

CREATE VIRTUAL TABLE entry_fts USING fts5(
    entry_id UNINDEXED,
    content_text,
    tokenize = 'trigram'
);

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
    captured_at_ms INTEGER NOT NULL CHECK (captured_at_ms > 0), view_count INTEGER CHECK (view_count IS NULL OR view_count >= 0), comment_count INTEGER CHECK (comment_count IS NULL OR comment_count >= 0), like_count INTEGER CHECK (like_count IS NULL OR like_count >= 0), favicon_media_id TEXT REFERENCES media(id) ON DELETE RESTRICT,
    PRIMARY KEY (entry_id, position),
    CHECK (card_kind <> 'link' OR embed_url = '')
) STRICT;

CREATE TABLE entry_location (
    entry_id TEXT PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    name TEXT NOT NULL CHECK (name <> '' AND name = trim(name) AND length(name) <= 160),
    display_name TEXT NOT NULL CHECK (display_name <> '' AND display_name = trim(display_name) AND length(display_name) <= 500),
    country_code TEXT NOT NULL CHECK (
        country_code = '' OR (
            length(country_code) = 2
            AND country_code = upper(country_code)
            AND country_code NOT GLOB '*[^A-Z]*'
        )
    ),
    country TEXT NOT NULL CHECK (country = trim(country) AND length(country) <= 120),
    region TEXT NOT NULL CHECK (region = trim(region) AND length(region) <= 120),
    city TEXT NOT NULL CHECK (city = trim(city) AND length(city) <= 120),
    district TEXT NOT NULL CHECK (district = trim(district) AND length(district) <= 120),
    category TEXT NOT NULL CHECK (category = trim(category) AND length(category) <= 120),
    provider TEXT NOT NULL CHECK (provider IN ('manual', 'amap', 'geoapify')),
    provider_place_id TEXT NOT NULL CHECK (provider_place_id = trim(provider_place_id) AND length(provider_place_id) <= 512),
    latitude_e7 INTEGER,
    longitude_e7 INTEGER,
    coordinate_system TEXT CHECK (coordinate_system IN ('wgs84', 'gcj02')),
    CHECK (
        (latitude_e7 IS NULL AND longitude_e7 IS NULL AND coordinate_system IS NULL)
        OR (
            latitude_e7 BETWEEN -900000000 AND 900000000
            AND longitude_e7 BETWEEN -1800000000 AND 1800000000
            AND coordinate_system IS NOT NULL
        )
    ),
    CHECK (provider <> 'manual' OR provider_place_id = '')
) STRICT;

CREATE TABLE "entry_media" (
    entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id) ON DELETE RESTRICT,
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 49),
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

CREATE TABLE entry_quotes (
    entry_id TEXT PRIMARY KEY REFERENCES entries(id) ON DELETE CASCADE,
    quoted_entry_id TEXT REFERENCES entries(id) ON DELETE SET NULL,
    CHECK (quoted_entry_id IS NULL OR quoted_entry_id <> entry_id)
) STRICT;

CREATE TABLE idempotency_keys (
    scope TEXT NOT NULL CHECK (scope <> ''),
    key TEXT NOT NULL CHECK (key <> ''),
    request_hash TEXT NOT NULL CHECK (
        length(request_hash) = 64
        AND request_hash NOT GLOB '*[^0-9a-f]*'
    ),
    response_resource_id TEXT NOT NULL CHECK (response_resource_id <> ''),
    created_at_ms INTEGER NOT NULL, author_user_id TEXT,
    PRIMARY KEY (scope, key)
) STRICT;

CREATE TABLE identities (
    provider TEXT NOT NULL CHECK (provider <> '' AND provider = lower(provider)),
    provider_user_id TEXT NOT NULL CHECK (provider_user_id <> ''),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    email TEXT,
    email_verified INTEGER NOT NULL CHECK (email_verified IN (0, 1)),
    email_deliverable INTEGER NOT NULL CHECK (email_deliverable IN (0, 1)),
    display_name TEXT NOT NULL DEFAULT '',
    avatar_url TEXT,
    created_at_ms INTEGER NOT NULL CHECK (created_at_ms > 0),
    last_login_at_ms INTEGER NOT NULL CHECK (last_login_at_ms > 0),
    PRIMARY KEY (provider, provider_user_id)
) STRICT;

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

CREATE TABLE search_index_state (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    normalizer_version INTEGER NOT NULL CHECK (normalizer_version >= 0)
, revision INTEGER NOT NULL DEFAULT 1 CHECK (revision > 0)) STRICT;

CREATE TABLE sessions (
    id TEXT PRIMARY KEY CHECK (
        length(id) = 32 AND id NOT GLOB '*[^0-9a-f]*'
    ),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issued_at_ms INTEGER NOT NULL CHECK (issued_at_ms > 0),
    expires_at_ms INTEGER NOT NULL CHECK (expires_at_ms > 0),
    last_seen_at_ms INTEGER NOT NULL CHECK (last_seen_at_ms > 0),
    revoked_at_ms INTEGER CHECK (revoked_at_ms IS NULL OR revoked_at_ms > 0),
    surface TEXT NOT NULL CHECK (surface <> ''),
    label TEXT
) STRICT;

CREATE TABLE "site_settings" (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    title TEXT NOT NULL,
    timezone TEXT NOT NULL CHECK (timezone <> ''),
    public_base_url TEXT CHECK (
        public_base_url IS NULL
        OR (public_base_url <> '' AND public_base_url = trim(public_base_url))
    ),
    comments_enabled INTEGER NOT NULL CHECK (comments_enabled IN (0, 1)),
    pinned_entry_id TEXT NULL REFERENCES entries(id) ON DELETE SET NULL,
    profile_user_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT CHECK (
        length(profile_user_id) = 32 AND profile_user_id NOT GLOB '*[^0-9a-f]*'
    ),
    registration_mode TEXT NOT NULL DEFAULT 'approval'
        CHECK (registration_mode IN ('open', 'approval', 'closed'))
) STRICT;

CREATE TABLE users (
    id TEXT PRIMARY KEY CHECK (
        length(id) = 32
        AND id NOT GLOB '*[^0-9a-f]*'
        AND id <> '00000000000000000000000000000000'
    ),
    username TEXT NOT NULL COLLATE NOCASE CHECK (
        length(username) BETWEEN 3 AND 30
        AND username = lower(username)
        AND substr(username, 1, 1) GLOB '[a-z]'
        AND username NOT GLOB '*[^a-z0-9_]*'
    ),
    display_name TEXT NOT NULL CHECK (
        display_name <> ''
        AND display_name = trim(display_name)
        AND length(display_name) <= 80
    ),
    bio TEXT NOT NULL DEFAULT '',
    avatar_media_id TEXT REFERENCES media(id) ON DELETE RESTRICT,
    cover_media_id TEXT REFERENCES media(id) ON DELETE RESTRICT,
    role TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    status TEXT NOT NULL CHECK (status IN ('pending', 'active', 'suspended')),
    username_set_at_ms INTEGER CHECK (username_set_at_ms IS NULL OR username_set_at_ms > 0),
    created_at_ms INTEGER NOT NULL CHECK (created_at_ms > 0),
    updated_at_ms INTEGER NOT NULL CHECK (updated_at_ms > 0)
) STRICT;

CREATE INDEX api_tokens_user_idx ON api_tokens(user_id, created_at_ms);

CREATE INDEX article_draft_media_media_idx ON article_draft_media (media_id);

CREATE INDEX article_drafts_author_time_idx ON article_drafts(author_user_id, updated_at_ms DESC, id DESC);

CREATE INDEX article_drafts_cover_idx ON article_drafts (cover_media_id);

CREATE UNIQUE INDEX article_drafts_published_entry_idx
    ON article_drafts (published_entry_id)
    WHERE published_entry_id IS NOT NULL;

CREATE INDEX comment_media_media_idx ON comment_media (media_id);

CREATE INDEX comments_author_user_idx ON comments (author_user_id);

CREATE INDEX comments_entry_idx ON comments (entry_id, created_at_ms, id);

CREATE INDEX comments_parent_idx ON comments (parent_id, created_at_ms, id);

CREATE INDEX comments_reply_target_idx ON comments (reply_to_comment_id);

CREATE INDEX entries_author_kind_time_idx ON entries(author_user_id, kind, published_at_ms DESC, id DESC);

CREATE INDEX entries_author_published_idx ON entries(author_user_id, kind, publication_state, published_at_ms DESC, id DESC);

CREATE INDEX entries_author_time_idx ON entries(author_user_id, published_at_ms DESC, id DESC);

CREATE INDEX entries_kind_published_seek_idx ON entries (kind, published_at_ms, id);

CREATE INDEX entries_kind_reposted_seek_idx ON entries (kind, reposted_at_ms, id) WHERE reposted_at_ms IS NOT NULL;

CREATE INDEX entries_published_seek_idx ON entries (published_at_ms, id);

CREATE INDEX entries_reposted_seek_idx ON entries (reposted_at_ms, id) WHERE reposted_at_ms IS NOT NULL;

CREATE INDEX entries_timeline_idx
    ON entries (kind, COALESCE(reposted_at_ms, published_at_ms) DESC, id DESC);

CREATE INDEX entry_article_cover_idx ON entry_article (cover_media_id);

CREATE INDEX entry_link_card_favicon_idx ON entry_link_card (favicon_media_id);

CREATE INDEX entry_link_card_media_idx ON entry_link_card (thumbnail_media_id);

CREATE INDEX entry_media_media_idx ON entry_media (media_id);

CREATE INDEX entry_quotes_quoted_entry_id_idx ON entry_quotes(quoted_entry_id);

CREATE INDEX idempotency_keys_created_idx ON idempotency_keys (created_at_ms);

CREATE INDEX identities_user_idx ON identities (user_id);

CREATE INDEX sessions_expiry_idx ON sessions (expires_at_ms);

CREATE INDEX sessions_user_idx ON sessions (user_id, issued_at_ms);

CREATE INDEX users_status_idx ON users (status);

CREATE UNIQUE INDEX users_username_unique ON users (username COLLATE NOCASE);

CREATE TRIGGER comment_pins_root_insert_guard
BEFORE INSERT ON comment_pins
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1
    FROM comments
    WHERE id = NEW.comment_id
      AND entry_id = NEW.entry_id
      AND parent_id IS NULL
)
BEGIN
    SELECT RAISE(ABORT, 'pinned comment must be a root in the same entry');
END;

CREATE TRIGGER comment_pins_root_update_guard
BEFORE UPDATE ON comment_pins
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1
    FROM comments
    WHERE id = NEW.comment_id
      AND entry_id = NEW.entry_id
      AND parent_id IS NULL
)
BEGIN
    SELECT RAISE(ABORT, 'pinned comment must be a root in the same entry');
END;

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

CREATE TRIGGER comments_reply_target_insert_guard
BEFORE INSERT ON comments
WHEN NEW.reply_to_comment_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'comment reply target must be in the same conversation')
    WHERE NEW.parent_id IS NULL
       OR NEW.reply_to_comment_id = NEW.id
       OR NOT EXISTS (
            SELECT 1
            FROM comments AS target
            WHERE target.id = NEW.reply_to_comment_id
              AND target.entry_id = NEW.entry_id
              AND (target.id = NEW.parent_id OR target.parent_id = NEW.parent_id)
       );
END;

CREATE TRIGGER comments_reply_target_update_guard
BEFORE UPDATE OF entry_id, parent_id, reply_to_comment_id ON comments
WHEN NEW.reply_to_comment_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'comment reply target must be in the same conversation')
    WHERE NEW.parent_id IS NULL
       OR NEW.reply_to_comment_id = NEW.id
       OR NOT EXISTS (
            SELECT 1
            FROM comments AS target
            WHERE target.id = NEW.reply_to_comment_id
              AND target.entry_id = NEW.entry_id
              AND (target.id = NEW.parent_id OR target.parent_id = NEW.parent_id)
       );
END;

CREATE TRIGGER entries_search_delete
AFTER DELETE ON entries
BEGIN
    DELETE FROM entry_fts WHERE entry_id = OLD.id;
END;

CREATE TRIGGER entries_search_insert
AFTER INSERT ON entries
BEGIN
    INSERT INTO entry_fts (entry_id, content_text)
    VALUES (NEW.id, NEW.content_text);
END;

CREATE TRIGGER entries_search_revision_delete
AFTER DELETE ON entries
BEGIN
    UPDATE search_index_state SET revision = revision + 1 WHERE id = 1;
END;

CREATE TRIGGER entries_search_revision_insert
AFTER INSERT ON entries
BEGIN
    UPDATE search_index_state SET revision = revision + 1 WHERE id = 1;
END;

CREATE TRIGGER entries_search_revision_update
AFTER UPDATE OF content_text ON entries
BEGIN
    UPDATE search_index_state SET revision = revision + 1 WHERE id = 1;
END;

CREATE TRIGGER entries_search_update
AFTER UPDATE OF content_text ON entries
BEGIN
    DELETE FROM entry_fts WHERE entry_id = OLD.id;
    INSERT INTO entry_fts (entry_id, content_text)
    VALUES (NEW.id, NEW.content_text);
END;

CREATE TRIGGER entry_article_kind_insert_guard
BEFORE INSERT ON entry_article
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1 FROM entries WHERE id = NEW.entry_id AND kind = 'article'
)
BEGIN
    SELECT RAISE(ABORT, 'entry_article belongs to an article entry');
END;

CREATE TRIGGER entry_article_kind_update_guard
BEFORE UPDATE OF entry_id ON entry_article
FOR EACH ROW
WHEN NOT EXISTS (
    SELECT 1 FROM entries WHERE id = NEW.entry_id AND kind = 'article'
)
BEGIN
    SELECT RAISE(ABORT, 'entry_article belongs to an article entry');
END;

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

CREATE INDEX users_role_status_idx ON users(role, status);

INSERT INTO search_index_state(id, normalizer_version, revision) VALUES (1, 0, 1);

-- Grants record an account's right to reuse a deduplicated media object.
-- They are not content references and do not keep abandoned uploads alive.
CREATE TABLE media_grants (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id) ON DELETE CASCADE,
    created_at_ms INTEGER NOT NULL CHECK (created_at_ms > 0),
    PRIMARY KEY (user_id, media_id)
) STRICT;
CREATE INDEX media_grants_media_idx ON media_grants(media_id);

-- A site-wide pin can only emphasize published, public content.
CREATE TRIGGER site_pin_insert_guard BEFORE INSERT ON site_settings
WHEN NEW.pinned_entry_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM entries WHERE id=NEW.pinned_entry_id AND visibility='everyone' AND publication_state='published'
)
BEGIN SELECT RAISE(ABORT,'pinned entries must be public and published'); END;
CREATE TRIGGER site_pin_update_guard BEFORE UPDATE OF pinned_entry_id ON site_settings
WHEN NEW.pinned_entry_id IS NOT NULL AND NOT EXISTS (
 SELECT 1 FROM entries WHERE id=NEW.pinned_entry_id AND visibility='everyone' AND publication_state='published'
)
BEGIN SELECT RAISE(ABORT,'pinned entries must be public and published'); END;
CREATE TRIGGER entries_clear_unavailable_pin AFTER UPDATE OF visibility,publication_state ON entries
WHEN NEW.visibility<>'everyone' OR NEW.publication_state<>'published'
BEGIN UPDATE site_settings SET pinned_entry_id=NULL WHERE pinned_entry_id=NEW.id; END;
