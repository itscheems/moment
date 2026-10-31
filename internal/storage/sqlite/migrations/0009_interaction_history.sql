-- Imported identities are display data and never acquire local account authority.
CREATE TABLE post_likes_history (
    id INTEGER PRIMARY KEY,
    post_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
    snapshot_key TEXT,
    snapshot_name TEXT,
    snapshot_username TEXT,
    created_at_ms INTEGER NOT NULL CHECK(created_at_ms > 0),
    CHECK ((user_id IS NOT NULL AND snapshot_key IS NULL AND snapshot_name IS NULL AND snapshot_username IS NULL)
        OR (user_id IS NULL AND snapshot_key IS NOT NULL AND snapshot_name IS NOT NULL))
) STRICT;
INSERT INTO post_likes_history(post_id,user_id,created_at_ms) SELECT post_id,user_id,created_at_ms FROM post_likes;
DROP TABLE post_likes;
ALTER TABLE post_likes_history RENAME TO post_likes;
CREATE INDEX post_likes_user ON post_likes(user_id,post_id);
CREATE UNIQUE INDEX post_likes_account ON post_likes(post_id,user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX post_likes_snapshot ON post_likes(post_id,snapshot_key) WHERE snapshot_key IS NOT NULL;

CREATE TABLE comment_likes_history (
    id INTEGER PRIMARY KEY,
    comment_id TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
    snapshot_key TEXT,
    snapshot_name TEXT,
    snapshot_username TEXT,
    created_at_ms INTEGER NOT NULL CHECK(created_at_ms > 0),
    CHECK ((user_id IS NOT NULL AND snapshot_key IS NULL AND snapshot_name IS NULL AND snapshot_username IS NULL)
        OR (user_id IS NULL AND snapshot_key IS NOT NULL AND snapshot_name IS NOT NULL))
) STRICT;
INSERT INTO comment_likes_history(comment_id,user_id,created_at_ms) SELECT comment_id,user_id,created_at_ms FROM comment_likes;
DROP TABLE comment_likes;
ALTER TABLE comment_likes_history RENAME TO comment_likes;
CREATE INDEX comment_likes_user ON comment_likes(user_id,comment_id);
CREATE UNIQUE INDEX comment_likes_account ON comment_likes(comment_id,user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX comment_likes_snapshot ON comment_likes(comment_id,snapshot_key) WHERE snapshot_key IS NOT NULL;

ALTER TABLE entry_mentions ADD COLUMN snapshot_key TEXT;
ALTER TABLE entry_mentions ADD COLUMN snapshot_name TEXT;
ALTER TABLE entry_mentions ADD COLUMN snapshot_username TEXT;
ALTER TABLE comment_mentions ADD COLUMN snapshot_key TEXT;
ALTER TABLE comment_mentions ADD COLUMN snapshot_name TEXT;
ALTER TABLE comment_mentions ADD COLUMN snapshot_username TEXT;

CREATE TABLE comment_reply_recipients_history (
    id INTEGER PRIMARY KEY,
    comment_id TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    user_id TEXT REFERENCES users(id) ON DELETE CASCADE,
    snapshot_key TEXT,
    snapshot_name TEXT,
    snapshot_username TEXT,
    CHECK ((user_id IS NOT NULL AND snapshot_key IS NULL AND snapshot_name IS NULL AND snapshot_username IS NULL)
        OR (user_id IS NULL AND snapshot_key IS NOT NULL AND snapshot_name IS NOT NULL))
) STRICT;
INSERT INTO comment_reply_recipients_history(comment_id,user_id) SELECT comment_id,user_id FROM comment_reply_recipients;
DROP TABLE comment_reply_recipients;
ALTER TABLE comment_reply_recipients_history RENAME TO comment_reply_recipients;
CREATE UNIQUE INDEX comment_reply_recipients_account ON comment_reply_recipients(comment_id,user_id) WHERE user_id IS NOT NULL;
CREATE UNIQUE INDEX comment_reply_recipients_snapshot ON comment_reply_recipients(comment_id,snapshot_key) WHERE snapshot_key IS NOT NULL;

ALTER TABLE notifications ADD COLUMN historical INTEGER NOT NULL DEFAULT 0 CHECK(historical IN (0,1));
ALTER TABLE notifications ADD COLUMN force_unavailable INTEGER NOT NULL DEFAULT 0 CHECK(force_unavailable IN (0,1));
CREATE INDEX notifications_recipient_time ON notifications(recipient_id,occurred_at_ms DESC,sequence DESC);
CREATE INDEX notifications_live_sequence ON notifications(recipient_id,sequence) WHERE historical=0;
