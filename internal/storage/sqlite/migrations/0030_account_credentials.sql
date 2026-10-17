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

CREATE INDEX api_tokens_user_idx ON api_tokens(user_id, created_at_ms);
