-- Verification and delivery budgets are operational state, not portable credentials.
-- Verified email identities use the existing identities table and archive format.
CREATE TABLE registration_challenges (
    id TEXT PRIMARY KEY,
    email TEXT NOT NULL,
    code_hash BLOB NOT NULL CHECK(length(code_hash) = 32),
    proof_hash BLOB UNIQUE,
    requested_at_ms INTEGER NOT NULL,
    expires_at_ms INTEGER NOT NULL,
    attempts INTEGER NOT NULL DEFAULT 0,
    verified INTEGER NOT NULL DEFAULT 0 CHECK(verified IN (0, 1)),
    consumed INTEGER NOT NULL DEFAULT 0 CHECK(consumed IN (0, 1)),
    requester_hash TEXT NOT NULL
) STRICT;
CREATE INDEX registration_challenges_email ON registration_challenges(email, requested_at_ms);
CREATE INDEX registration_challenges_requester ON registration_challenges(requester_hash, requested_at_ms);
CREATE TABLE email_delivery_usage (
    provider TEXT NOT NULL,
    period TEXT NOT NULL,
    used INTEGER NOT NULL CHECK(used >= 0),
    PRIMARY KEY(provider, period)
) STRICT;
