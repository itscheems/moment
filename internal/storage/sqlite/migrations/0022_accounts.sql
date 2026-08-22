-- Accounts: the layer that answers "who is this" instead of "is this the owner".
--
-- Until now identity lived in two places that never referenced each other. The
-- credential lived in secrets.json, a single 0600 file with room for exactly
-- one owner. The profile lived in site_settings row 1, behind CHECK (id = 1).
-- Neither knew the other existed, and nothing in the system could name a second
-- person. 0020 wrote down where this was heading:
--
--     "author_user_id fixes the direction of the lookup. It is nullable and
--      carries no foreign key because the table it will eventually point at
--      does not exist yet; when accounts arrive, the resolver reads that table
--      instead of the singleton and nothing above it changes."
--
-- This is that table.
--
-- Three tables, three questions. identities answers "what did they log in
-- with", users answers "who are they", sessions answers "where are they signed
-- in right now". Keeping them apart is what lets a person attach a second
-- provider without becoming a second person.
--
--
-- Why user ids stay 32 lowercase hex instead of becoming ULIDs
--
-- Every other entity here is a ULID, so hex looks like an inconsistency. It is
-- not: the account identifier already exists and is already load-bearing in six
-- places -- site_settings.owner_id, comments.author_user_id, the domain
-- validator, the archive's owner_id check, and two assertions in the browser
-- client. Switching would mean changing all six, rewriting every stored
-- author_user_id, and making existing archives unreadable, to buy nothing but
-- visual consistency with a different kind of object.
--
--
-- Why comments is not rebuilt here
--
-- The obvious move is to give comments.author_user_id a real foreign key and
-- widen author_kind to name a third sort of author. Both are refused, for one
-- mechanical reason and one design reason.
--
-- Mechanical: migrations run inside a single transaction with foreign keys ON,
-- and PRAGMA foreign_keys cannot be changed mid-transaction, so the standard
-- table rebuild is unavailable. Six places reference comments(id) -- parent_id,
-- reply_to_comment_id, comment_media, comment_location, comment_pins, and the
-- extended-media variants. DROP TABLE with foreign keys enabled performs an
-- implicit DELETE, and that DELETE fires ON DELETE CASCADE on every one of
-- them. Rebuilding comments here would silently take comment media, locations
-- and pins with it. That is not a risk worth accepting for a constraint the
-- resolver already degrades around: an author_user_id pointing at nothing
-- renders as plain text, exactly like a legacy guest row, and doctor reports
-- the orphan.
--
-- Design: author_kind does not need a third value. The browser client already
-- says so at CommentItem.tsx, describing it as "the axis being retired: its one
-- remaining job is marking rows a legacy guest wrote". That job is unchanged by
-- accounts. 'owner' now reads as "written from an account" and 'guest' as "no
-- identity to resolve"; which account, and what role it holds, is answered by
-- author_user_id against users -- the single source that stays correct when a
-- member is later promoted, where a stored copy of the role would not.
--
--
-- Why site_settings IS rebuilt
--
-- Nothing references site_settings, so none of the above applies to it. It is
-- the child in every relationship it participates in, and deleting a child row
-- fires no foreign key action. That makes the rebuild the safe, complete way to
-- do three things at once: drop the four profile columns that now belong to the
-- account, point owner_id at users with a real foreign key, and add the
-- registration policy.
--
-- The profile columns move rather than being copied. Leaving username or avatar
-- behind would create a second answer to "what is the owner called", and the
-- first time the two disagree the bug is unfalsifiable from either side alone.
--
--
-- Why new accounts land pending
--
-- registration_mode defaults to 'approval' rather than 'open' because approval
-- keeps a choice open that 'open' throws away. A site that hands write access
-- to anyone with a Google account cannot later decide to look at applicants
-- without first taking that access back; a site that reviews them can stop
-- reviewing at any time, or automate the decision, without changing what the
-- state means. The prior round removed the only rate limiter in the system on
-- the grounds that commenting required an account -- a premise that holds only
-- while accounts are not free to create.

CREATE TABLE users (
    id TEXT PRIMARY KEY CHECK (
        length(id) = 32
        AND id NOT GLOB '*[^0-9a-f]*'
        -- The legacy identity fallback synthesises this value when every
        -- identity field is blank. It is a well-formed hex string, so it would
        -- pass every other check here and collide across instances.
        AND id <> '00000000000000000000000000000000'
    ),
    username TEXT NOT NULL COLLATE NOCASE CHECK (
        length(username) BETWEEN 3 AND 30
        AND username = lower(username)
        AND substr(username, 1, 1) GLOB '[a-z]'
        AND username NOT GLOB '*[^a-z0-9_]*'
    ),
    display_name TEXT NOT NULL CHECK (
        display_name <> ''
        AND display_name = trim(display_name)
        AND length(display_name) <= 80
    ),
    bio TEXT NOT NULL DEFAULT '',
    avatar_media_id TEXT REFERENCES media(id) ON DELETE RESTRICT,
    cover_media_id TEXT REFERENCES media(id) ON DELETE RESTRICT,
    role TEXT NOT NULL CHECK (role IN ('owner', 'member')),
    -- pending is the landing state under 'approval': signed in, able to read
    -- and to edit its own profile, unable to write anything anyone else sees.
    status TEXT NOT NULL CHECK (status IN ('pending', 'active', 'suspended')),
    -- NULL means the username was generated from a provider display name and
    -- the person has not confirmed or replaced it yet. It is what the one-time
    -- "pick a username" prompt keys off, and why skipping that prompt still
    -- writes a value.
    username_set_at_ms INTEGER CHECK (username_set_at_ms IS NULL OR username_set_at_ms > 0),
    created_at_ms INTEGER NOT NULL CHECK (created_at_ms > 0),
    updated_at_ms INTEGER NOT NULL CHECK (updated_at_ms > 0)
) STRICT;

CREATE UNIQUE INDEX users_username_unique ON users (username COLLATE NOCASE);

-- One owner, enforced by the database rather than by every write path
-- remembering to check.
CREATE UNIQUE INDEX users_single_owner ON users (role) WHERE role = 'owner';

CREATE INDEX users_status_idx ON users (status);

-- An external identity: which provider, and which account there.
--
-- provider is deliberately an open set with no CHECK listing known values.
-- Narrowing it later is the mistake 'guest' already taught this schema once:
-- the archive importer writes stored values back verbatim, so a value domain
-- that rejects anything a previous version wrote makes those backups
-- unimportable. Legality is decided in Go, where it can change without
-- invalidating history.
CREATE TABLE identities (
    provider TEXT NOT NULL CHECK (provider <> '' AND provider = lower(provider)),
    provider_user_id TEXT NOT NULL CHECK (provider_user_id <> ''),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Nullable on purpose. A GitHub account can decline to expose any address,
    -- and an address that cannot be delivered to is worth less than none at
    -- all if it is mistaken for a way to reach someone. It never participates
    -- in a unique constraint: matching accounts by email is the account
    -- takeover path this design refuses to open.
    email TEXT,
    email_verified INTEGER NOT NULL CHECK (email_verified IN (0, 1)),
    email_deliverable INTEGER NOT NULL CHECK (email_deliverable IN (0, 1)),
    display_name TEXT NOT NULL DEFAULT '',
    -- Kept as a retry hint for avatar capture, never as a render source. The
    -- browser client accepts only managed media ids for avatars.
    avatar_url TEXT,
    created_at_ms INTEGER NOT NULL CHECK (created_at_ms > 0),
    last_login_at_ms INTEGER NOT NULL CHECK (last_login_at_ms > 0),
    PRIMARY KEY (provider, provider_user_id)
) STRICT;

CREATE INDEX identities_user_idx ON identities (user_id);

-- A live sign-in. This is what makes "log out this one device" expressible.
--
-- Before this table the only revocation was the global session epoch in
-- secrets.json: rotating it signed everyone out at once, and DELETE /session
-- merely unset the cookie, leaving a copied cookie value valid for its whole
-- twelve hours. Neither is acceptable once sessions belong to different people.
--
-- Deliberately excluded from the portable archive. A backup that carries
-- sessions revives dead ones on restore, and a restore is exactly when that is
-- least wanted. Restoring therefore ends every session, which is the correct
-- outcome rather than a limitation.
CREATE TABLE sessions (
    id TEXT PRIMARY KEY CHECK (
        length(id) = 32 AND id NOT GLOB '*[^0-9a-f]*'
    ),
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    issued_at_ms INTEGER NOT NULL CHECK (issued_at_ms > 0),
    expires_at_ms INTEGER NOT NULL CHECK (expires_at_ms > 0),
    last_seen_at_ms INTEGER NOT NULL CHECK (last_seen_at_ms > 0),
    revoked_at_ms INTEGER CHECK (revoked_at_ms IS NULL OR revoked_at_ms > 0),
    surface TEXT NOT NULL CHECK (surface <> ''),
    label TEXT
) STRICT;

CREATE INDEX sessions_user_idx ON sessions (user_id, issued_at_ms);
CREATE INDEX sessions_expiry_idx ON sessions (expires_at_ms);

-- Promote the existing singleton into the first account. Its identity fields
-- are carried across unchanged, including a username that may collide with the
-- reserved list introduced alongside this migration: reserved names are
-- enforced on write, never on read, so an existing account is never
-- invalidated by a list that grows later. doctor reports the collision instead.
--
-- username_set_at_ms is stamped rather than left NULL. This account's name was
-- chosen by a person, not generated from a provider profile, so the one-time
-- "pick a username" prompt must not appear for it.
INSERT INTO users (
    id, username, display_name, bio, avatar_media_id, cover_media_id,
    role, status, username_set_at_ms, created_at_ms, updated_at_ms
)
SELECT
    owner_id,
    username,
    display_name,
    bio,
    avatar_media_id,
    cover_media_id,
    'owner',
    'active',
    CAST(strftime('%s', 'now') AS INTEGER) * 1000,
    CAST(strftime('%s', 'now') AS INTEGER) * 1000,
    CAST(strftime('%s', 'now') AS INTEGER) * 1000
FROM site_settings
WHERE id = 1;

-- Rebuild site_settings without the four columns that moved, with owner_id
-- pointing at a real row, and with the registration policy.
CREATE TABLE site_settings_next (
    id INTEGER PRIMARY KEY CHECK (id = 1),
    title TEXT NOT NULL,
    timezone TEXT NOT NULL CHECK (timezone <> ''),
    public_base_url TEXT CHECK (
        public_base_url IS NULL
        OR (public_base_url <> '' AND public_base_url = trim(public_base_url))
    ),
    comments_enabled INTEGER NOT NULL CHECK (comments_enabled IN (0, 1)),
    pinned_entry_id TEXT NULL REFERENCES entries(id) ON DELETE SET NULL,
    owner_id TEXT NOT NULL REFERENCES users(id) ON DELETE RESTRICT CHECK (
        length(owner_id) = 32 AND owner_id NOT GLOB '*[^0-9a-f]*'
    ),
    registration_mode TEXT NOT NULL DEFAULT 'approval'
        CHECK (registration_mode IN ('open', 'approval', 'closed'))
) STRICT;

INSERT INTO site_settings_next (
    id, title, timezone, public_base_url, comments_enabled,
    pinned_entry_id, owner_id, registration_mode
)
SELECT
    id, title, timezone, public_base_url, comments_enabled,
    pinned_entry_id, owner_id, 'approval'
FROM site_settings
WHERE id = 1;

DROP TABLE site_settings;

ALTER TABLE site_settings_next RENAME TO site_settings;
