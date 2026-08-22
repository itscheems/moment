-- Comment authorship becomes a reference instead of a name.
--
-- author_name has always been a snapshot, and for owner comments it is not
-- even that: the service stamps the literal "Owner" and every reader replaces
-- it with the current site identity. The web did that replacement and the CLI
-- did not, so the same comment reported two different authors depending on
-- where you looked.
--
-- author_user_id fixes the direction of the lookup. It is nullable and carries
-- no foreign key because the table it will eventually point at does not exist
-- yet; when accounts arrive, the resolver reads that table instead of the
-- singleton and nothing above it changes. Legacy guest rows keep NULL — they
-- never had an identity to reference, only the name their author typed.
ALTER TABLE comments
ADD COLUMN author_user_id TEXT
CHECK (
    author_user_id IS NULL
    OR (
        length(author_user_id) = 32
        AND author_user_id NOT GLOB '*[^0-9a-f]*'
    )
);

UPDATE comments
SET author_user_id = (SELECT owner_id FROM site_settings WHERE id = 1)
WHERE author_kind = 'owner';

CREATE INDEX comments_author_user_idx ON comments (author_user_id);
