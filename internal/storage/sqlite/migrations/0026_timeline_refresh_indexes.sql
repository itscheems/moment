-- Both timeline projections support bounded seeks, with and without kind.
CREATE INDEX entries_published_seek_idx ON entries (published_at_ms, id);
CREATE INDEX entries_kind_published_seek_idx ON entries (kind, published_at_ms, id);
CREATE INDEX entries_reposted_seek_idx ON entries (reposted_at_ms, id) WHERE reposted_at_ms IS NOT NULL;
CREATE INDEX entries_kind_reposted_seek_idx ON entries (kind, reposted_at_ms, id) WHERE reposted_at_ms IS NOT NULL;
