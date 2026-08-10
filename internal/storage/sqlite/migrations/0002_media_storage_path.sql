CREATE TRIGGER media_storage_path_insert_guard
BEFORE INSERT ON media
WHEN NEW.storage_path <> (
    'media/original/'
    || substr(NEW.sha256, 1, 2)
    || '/'
    || NEW.sha256
    || CASE NEW.mime_type
        WHEN 'image/jpeg' THEN '.jpg'
        WHEN 'image/png' THEN '.png'
        WHEN 'image/webp' THEN '.webp'
        WHEN 'image/gif' THEN '.gif'
       END
)
BEGIN
    SELECT RAISE(ABORT, 'media storage_path must be canonical');
END;

CREATE TRIGGER media_storage_path_update_guard
BEFORE UPDATE OF sha256, storage_path, mime_type ON media
WHEN NEW.storage_path <> (
    'media/original/'
    || substr(NEW.sha256, 1, 2)
    || '/'
    || NEW.sha256
    || CASE NEW.mime_type
        WHEN 'image/jpeg' THEN '.jpg'
        WHEN 'image/png' THEN '.png'
        WHEN 'image/webp' THEN '.webp'
        WHEN 'image/gif' THEN '.gif'
       END
)
BEGIN
    SELECT RAISE(ABORT, 'media storage_path must be canonical');
END;
