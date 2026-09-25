-- Articles are entries. This relaxes the two table checks that stood in their
-- way and adds the three tables that hold them.
--
-- entries and entry_media are rebuilt rather than altered because SQLite has no
-- way to drop a CHECK. Migrations run with foreign key enforcement off (see
-- applyMigrations), which is what makes the rebuild local: with enforcement on,
-- dropping the superseded copy of entries would carry out ON DELETE actions and
-- empty comments, entry_media, entry_location, entry_quotes, entry_link_card
-- and comment_pins, while renaming it instead would rewrite the REFERENCES
-- clause of all six and drag them into the rebuild. Dropping the table also
-- takes its indexes and triggers with it, and the implicit delete does not fire
-- them, so entry_fts keeps its rows and the objects are recreated below. The
-- catalog check at open is what proves none of them was forgotten.
--
-- Any future rebuild of entries must include entry_article and article_drafts,
-- which reference it from here on.

CREATE TABLE entries_next (
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
        CHECK (visibility IN ('everyone', 'only_me')),
    -- A moment carries Markdown and an article does not. Tying the two together
    -- here is what stops "this column does not apply to this row" from becoming
    -- a convention nobody can check.
    CHECK ((kind = 'moment') = (content_md IS NOT NULL))
) STRICT;

INSERT INTO entries_next (
    id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms,
    content_text, reposted_at_ms, edited_at_ms, visibility
)
SELECT
    id, kind, content_md, created_at_ms, updated_at_ms, published_at_ms,
    content_text, reposted_at_ms, edited_at_ms, visibility
FROM entries;

DROP TABLE entries;
ALTER TABLE entries_next RENAME TO entries;

CREATE INDEX entries_timeline_idx
    ON entries (kind, COALESCE(reposted_at_ms, published_at_ms) DESC, id DESC);

CREATE TRIGGER entries_search_insert
AFTER INSERT ON entries
BEGIN
    INSERT INTO entry_fts (entry_id, content_text)
    VALUES (NEW.id, NEW.content_text);
END;

CREATE TRIGGER entries_search_update
AFTER UPDATE OF content_text ON entries
BEGIN
    DELETE FROM entry_fts WHERE entry_id = OLD.id;
    INSERT INTO entry_fts (entry_id, content_text)
    VALUES (NEW.id, NEW.content_text);
END;

CREATE TRIGGER entries_search_delete
AFTER DELETE ON entries
BEGIN
    DELETE FROM entry_fts WHERE entry_id = OLD.id;
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

CREATE TRIGGER entries_search_revision_delete
AFTER DELETE ON entries
BEGIN
    UPDATE search_index_state SET revision = revision + 1 WHERE id = 1;
END;

-- An article body places its own pictures, so the nine-picture grid limit no
-- longer belongs in the column. Moments keep it; the application layer is what
-- holds them to nine from here on.
CREATE TABLE entry_media_next (
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

INSERT INTO entry_media_next (entry_id, media_id, position, alt_text)
SELECT entry_id, media_id, position, alt_text FROM entry_media;

DROP TABLE entry_media;
ALTER TABLE entry_media_next RENAME TO entry_media;

CREATE INDEX entry_media_media_idx ON entry_media (media_id);

-- The published article. content_md on the entry is NULL for these rows; the
-- body lives here and nowhere else.
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

CREATE INDEX entry_article_cover_idx ON entry_article (cover_media_id);

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

-- Drafts deliberately live outside entries. Eighteen read paths select FROM
-- entries and fifteen of them name a kind; a status column there would need
-- every one of them to filter correctly, and one miss would publish an
-- unfinished article. A separate table cannot leak that way.
--
-- published_entry_id is NULL for a draft that was never published, and names
-- the entry when the draft is the working copy of one -- editing a published
-- article must not change what readers see with every keystroke.
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
) STRICT;

CREATE UNIQUE INDEX article_drafts_published_entry_idx
    ON article_drafts (published_entry_id)
    WHERE published_entry_id IS NOT NULL;

CREATE INDEX article_drafts_cover_idx ON article_drafts (cover_media_id);

CREATE TABLE article_draft_media (
    draft_id TEXT NOT NULL REFERENCES article_drafts(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id) ON DELETE RESTRICT,
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 49),
    alt_text TEXT CHECK (alt_text IS NULL OR length(alt_text) <= 1000),
    PRIMARY KEY (draft_id, position)
) STRICT;

CREATE INDEX article_draft_media_media_idx ON article_draft_media (media_id);
