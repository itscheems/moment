-- Expand article placements while preserving ordered references and alt text.
-- Moment and comment limits remain enforced by their domain validators.

CREATE TABLE article_draft_media_expanded (
    draft_id TEXT NOT NULL REFERENCES article_drafts(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id) ON DELETE RESTRICT,
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 511),
    alt_text TEXT CHECK (alt_text IS NULL OR length(alt_text) <= 1000),
    PRIMARY KEY (draft_id, position)
) STRICT;
INSERT INTO article_draft_media_expanded (draft_id, media_id, position, alt_text)
SELECT draft_id, media_id, position, alt_text FROM article_draft_media;
DROP TABLE article_draft_media;
ALTER TABLE article_draft_media_expanded RENAME TO article_draft_media;
CREATE INDEX article_draft_media_media_idx ON article_draft_media (media_id);

CREATE TABLE entry_media_expanded (
    entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id) ON DELETE RESTRICT,
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 511),
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
INSERT INTO entry_media_expanded (entry_id, media_id, position, alt_text)
SELECT entry_id, media_id, position, alt_text FROM entry_media;
DROP TABLE entry_media;
ALTER TABLE entry_media_expanded RENAME TO entry_media;
CREATE INDEX entry_media_media_idx ON entry_media (media_id);
