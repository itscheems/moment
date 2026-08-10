INSERT INTO site_settings (
    id,
    title,
    bio,
    timezone,
    public_base_url,
    avatar_media_id,
    comments_enabled
)
VALUES (
    1,
    'Moment',
    '',
    '__moment_host_timezone__',
    NULL,
    NULL,
    1
)
ON CONFLICT(id) DO NOTHING;
