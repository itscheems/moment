CREATE TABLE post_likes (
    post_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at_ms INTEGER NOT NULL CHECK(created_at_ms > 0),
    PRIMARY KEY(post_id,user_id)
) STRICT;
CREATE INDEX post_likes_user ON post_likes(user_id,post_id);

CREATE TABLE comment_likes (
    comment_id TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at_ms INTEGER NOT NULL CHECK(created_at_ms > 0),
    PRIMARY KEY(comment_id,user_id)
) STRICT;
CREATE INDEX comment_likes_user ON comment_likes(user_id,comment_id);

CREATE TABLE entry_mentions (
    entry_id TEXT NOT NULL REFERENCES entries(id) ON DELETE CASCADE,
    handle TEXT NOT NULL,
    user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    notified INTEGER NOT NULL CHECK(notified IN (0,1)),
    PRIMARY KEY(entry_id,handle)
) STRICT;
CREATE TABLE comment_mentions (
    comment_id TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    handle TEXT NOT NULL,
    user_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    notified INTEGER NOT NULL CHECK(notified IN (0,1)),
    PRIMARY KEY(comment_id,handle)
) STRICT;
CREATE TABLE comment_reply_recipients (
    comment_id TEXT NOT NULL REFERENCES comments(id) ON DELETE CASCADE,
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    PRIMARY KEY(comment_id,user_id)
) STRICT;

CREATE TABLE notifications (
    sequence INTEGER PRIMARY KEY AUTOINCREMENT,
    id TEXT NOT NULL UNIQUE CHECK(length(id)=26),
    recipient_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    actor_id TEXT REFERENCES users(id) ON DELETE SET NULL,
    actor_name TEXT NOT NULL,
    event_type TEXT NOT NULL CHECK(event_type IN ('post.created','post.updated','post.published','comment.created','post.liked','comment.liked')),
    subject_kind TEXT NOT NULL CHECK(subject_kind IN ('post','comment')),
    subject_id TEXT NOT NULL,
    post_id TEXT NOT NULL,
    post_kind TEXT NOT NULL CHECK(post_kind IN ('moment','article')),
    reply_to_comment_id TEXT,
    reasons INTEGER NOT NULL CHECK(reasons BETWEEN 1 AND 15),
    occurred_at_ms INTEGER NOT NULL CHECK(occurred_at_ms > 0)
) STRICT;
CREATE INDEX notifications_recipient_sequence ON notifications(recipient_id,sequence DESC);
CREATE INDEX notifications_subject ON notifications(subject_kind,subject_id);

CREATE TABLE notification_seen (
    user_id TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    scope TEXT NOT NULL CHECK(scope IN ('all','mentions')),
    through_sequence INTEGER NOT NULL CHECK(through_sequence >= 0),
    PRIMARY KEY(user_id,scope)
) STRICT;
