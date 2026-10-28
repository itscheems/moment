ALTER TABLE sessions ADD COLUMN source_token_id TEXT REFERENCES api_tokens(id);
-- Existing sessions have no reliable provenance. Require a new sign-in rather
-- than letting sessions issued before provenance tracking outlive revocation.
UPDATE sessions SET revoked_at_ms = max(issued_at_ms, CAST(strftime('%s', 'now') AS INTEGER) * 1000) WHERE revoked_at_ms IS NULL;
