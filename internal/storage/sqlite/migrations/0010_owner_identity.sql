ALTER TABLE site_settings
ADD COLUMN owner_id TEXT NOT NULL DEFAULT '00000000000000000000000000000000'
CHECK (
    length(owner_id) = 32
    AND owner_id NOT GLOB '*[^0-9a-f]*'
);

ALTER TABLE site_settings
ADD COLUMN username TEXT NOT NULL DEFAULT 'owner' COLLATE NOCASE
CHECK (
    length(username) BETWEEN 3 AND 30
    AND username = lower(username)
    AND substr(username, 1, 1) GLOB '[a-z]'
    AND username NOT GLOB '*[^a-z0-9_]*'
);

ALTER TABLE site_settings
ADD COLUMN display_name TEXT NOT NULL DEFAULT 'Moment'
CHECK (
    display_name <> ''
    AND display_name = trim(display_name)
    AND length(display_name) <= 80
);

UPDATE site_settings
SET owner_id = lower(hex(randomblob(16))),
    display_name = substr(trim(title), 1, 80)
WHERE id = 1;

CREATE UNIQUE INDEX site_settings_username_unique
ON site_settings(username COLLATE NOCASE);
