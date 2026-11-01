-- Collections issued before credential rotation must not adopt future sign-ins.
-- Ordinary sessions stay valid and can bootstrap a replacement collection.
UPDATE browser_account_sets
SET revoked_at_ms = CAST(strftime('%s', 'now') AS INTEGER) * 1000
WHERE revoked_at_ms IS NULL;
