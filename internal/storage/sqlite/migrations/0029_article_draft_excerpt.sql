-- Derived through the document grammar when a draft is written or imported.
-- The migration runner backfills existing rows using the same excerpt function.
ALTER TABLE article_drafts ADD COLUMN excerpt TEXT NOT NULL DEFAULT '';
