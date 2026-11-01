CREATE TABLE browser_account_sets (
    id TEXT PRIMARY KEY,
    credential_hash BLOB NOT NULL UNIQUE CHECK (length(credential_hash) = 32),
    csrf_token TEXT NOT NULL,
    session_epoch INTEGER NOT NULL CHECK (session_epoch > 0),
    expires_at_ms INTEGER NOT NULL,
    revoked_at_ms INTEGER
) STRICT;

CREATE TABLE browser_account_members (
    set_id TEXT NOT NULL REFERENCES browser_account_sets(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
    last_used_at_ms INTEGER NOT NULL,
    PRIMARY KEY (set_id, user_id)
) STRICT;

CREATE INDEX browser_account_members_session ON browser_account_members(session_id);
