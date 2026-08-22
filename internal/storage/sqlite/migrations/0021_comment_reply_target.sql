-- Who a reply is answering, as an annotation rather than a tree edge.
--
-- parent_id already carries the structure, and the one-level rule that governs
-- it is enforced in four independent places: the two triggers below it in
-- 0001_core.sql, again in the comment service, and once more in the archive
-- import preflight. Widening parent_id to express "answering a sibling" would
-- mean changing all four in step, and any one of them missed becomes a defect
-- that only appears on a single path.
--
-- reply_to_comment_id sidesteps that entirely. It takes no part in ordering,
-- depth or cascade, so none of those four sites has to know it exists. Cycles
-- are structurally impossible — a target must live in the same conversation,
-- depth is capped at one, and the column is only ever written on insert — and
-- a cycle would be harmless anyway, because nothing traverses this edge. If
-- comment editing is ever added that last premise stops holding and the
-- invariant needs rechecking on update.
--
-- ON DELETE SET NULL keeps one property that makes every reader simpler: a
-- non-null value always points at a row that exists. The prefix is a
-- navigation hint, and once the target is gone there is nothing left to
-- navigate to; rendering "replying to [deleted]" would be noise. CASCADE would
-- be far worse — deleting one reply would silently take unrelated replies with
-- it — and RESTRICT would block the owner from deleting their own comments.
ALTER TABLE comments
ADD COLUMN reply_to_comment_id TEXT REFERENCES comments(id) ON DELETE SET NULL;

CREATE INDEX comments_reply_target_idx ON comments (reply_to_comment_id);

CREATE TRIGGER comments_reply_target_insert_guard
BEFORE INSERT ON comments
WHEN NEW.reply_to_comment_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'comment reply target must be in the same conversation')
    WHERE NEW.parent_id IS NULL
       OR NEW.reply_to_comment_id = NEW.id
       OR NOT EXISTS (
            SELECT 1
            FROM comments AS target
            WHERE target.id = NEW.reply_to_comment_id
              AND target.entry_id = NEW.entry_id
              AND (target.id = NEW.parent_id OR target.parent_id = NEW.parent_id)
       );
END;

CREATE TRIGGER comments_reply_target_update_guard
BEFORE UPDATE OF entry_id, parent_id, reply_to_comment_id ON comments
WHEN NEW.reply_to_comment_id IS NOT NULL
BEGIN
    SELECT RAISE(ABORT, 'comment reply target must be in the same conversation')
    WHERE NEW.parent_id IS NULL
       OR NEW.reply_to_comment_id = NEW.id
       OR NOT EXISTS (
            SELECT 1
            FROM comments AS target
            WHERE target.id = NEW.reply_to_comment_id
              AND target.entry_id = NEW.entry_id
              AND (target.id = NEW.parent_id OR target.parent_id = NEW.parent_id)
       );
END;
