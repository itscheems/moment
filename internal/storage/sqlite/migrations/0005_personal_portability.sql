ALTER TABLE comments ADD COLUMN author_username TEXT;
ALTER TABLE comments ADD COLUMN author_display_name TEXT;
ALTER TABLE comments ADD COLUMN author_avatar_media_id TEXT REFERENCES media(id) ON DELETE RESTRICT;

CREATE TRIGGER comment_snapshot_insert BEFORE INSERT ON comments
WHEN (NEW.author_username IS NOT NULL OR NEW.author_avatar_media_id IS NOT NULL) AND NEW.author_display_name IS NULL
BEGIN SELECT RAISE(ABORT, 'comment author snapshot requires a display name'); END;
CREATE TRIGGER comment_snapshot_update BEFORE UPDATE OF author_username,author_display_name,author_avatar_media_id ON comments
WHEN (NEW.author_username IS NOT NULL OR NEW.author_avatar_media_id IS NOT NULL) AND NEW.author_display_name IS NULL
BEGIN SELECT RAISE(ABORT, 'comment author snapshot requires a display name'); END;
