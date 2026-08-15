ALTER TABLE entries
ADD COLUMN reposted_at_ms INTEGER NULL CHECK (
    reposted_at_ms IS NULL OR reposted_at_ms >= published_at_ms
);

DROP INDEX entries_timeline_idx;

CREATE INDEX entries_timeline_idx
    ON entries (kind, COALESCE(reposted_at_ms, published_at_ms) DESC, id DESC);
