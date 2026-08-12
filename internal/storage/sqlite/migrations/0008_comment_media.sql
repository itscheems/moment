CREATE TABLE comment_media (
    comment_id TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    media_id TEXT NOT NULL REFERENCES media(id) ON DELETE RESTRICT,
    position INTEGER NOT NULL CHECK (position BETWEEN 0 AND 3),
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
    PRIMARY KEY (comment_id, position)
) STRICT;

CREATE INDEX comment_media_media_idx ON comment_media (media_id);
