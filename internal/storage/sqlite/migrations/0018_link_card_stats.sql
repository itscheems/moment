ALTER TABLE entry_link_card
ADD COLUMN view_count INTEGER CHECK (view_count IS NULL OR view_count >= 0);

ALTER TABLE entry_link_card
ADD COLUMN comment_count INTEGER CHECK (comment_count IS NULL OR comment_count >= 0);

ALTER TABLE entry_link_card
ADD COLUMN like_count INTEGER CHECK (like_count IS NULL OR like_count >= 0);
