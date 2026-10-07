ALTER TABLE entries ADD COLUMN publication_state TEXT NOT NULL DEFAULT 'published' CHECK(publication_state IN ('published','unpublished') AND (kind = 'article' OR publication_state = 'published'));
CREATE INDEX entries_author_published_idx ON entries(author_user_id, kind, publication_state, published_at_ms DESC, id DESC);
UPDATE idempotency_keys SET scope = scope || '.' || author_user_id WHERE scope = 'articles.publish.v1' AND author_user_id IS NOT NULL;
