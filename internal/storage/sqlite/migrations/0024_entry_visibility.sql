-- Who may read an entry. 'everyone' is what every entry has meant until now,
-- so existing rows keep their meaning; 'only_me' narrows readers to the owner.
--
-- No index: the timeline walks entries_timeline_idx in time order and drops
-- hidden rows as it goes, and a personal site hides few of them.
ALTER TABLE entries ADD COLUMN visibility TEXT NOT NULL DEFAULT 'everyone'
    CHECK (visibility IN ('everyone', 'only_me'));
