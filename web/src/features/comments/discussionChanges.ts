import type { PublicComment } from "../../api/public";

export type CommentMutation =
  | { kind: "created"; comment: PublicComment }
  | { kind: "deleted"; comment: PublicComment; repliesAffected: number }
  | { kind: "pin"; pinnedCommentId: string | null };

export type DiscussionChange = CommentMutation & {
  source: string;
  revision: number;
};

export function isDirectReply(
  comment: PublicComment,
  targetId: string,
): boolean {
  return (
    comment.parentId !== null &&
    (comment.replyTo?.commentId === targetId ||
      (comment.replyTo === null && comment.parentId === targetId))
  );
}

export function mergeCommentReplies(
  current: PublicComment[],
  incoming: PublicComment[],
): PublicComment[] {
  const byId = new Map(current.map((comment) => [comment.id, comment]));
  for (const comment of incoming)
    if (!byId.has(comment.id)) byId.set(comment.id, comment);
  return [...byId.values()].sort(
    (a, b) =>
      Date.parse(a.createdAt) - Date.parse(b.createdAt) ||
      a.id.localeCompare(b.id),
  );
}

export function clearReplyTarget(
  comment: PublicComment,
  deletedId: string,
): PublicComment {
  return comment.replyTo?.commentId === deletedId
    ? { ...comment, replyTo: null }
    : comment;
}

/** Roots count their whole floor; child rows count answers addressed to them. */
export function shiftReplyCounts(
  items: PublicComment[],
  reply: PublicComment,
  delta: number,
): PublicComment[] {
  return items.map((item) =>
    (
      item.parentId === null
        ? item.id === reply.parentId
        : isDirectReply(reply, item.id)
    )
      ? { ...item, replyCount: Math.max(0, item.replyCount + delta) }
      : item,
  );
}

export function reconcileConversationComment(
  current: PublicComment[],
  change: { kind: "created" | "deleted"; comment: PublicComment },
  conversationId: string,
): PublicComment[] {
  const { comment } = change;
  if (change.kind === "created") {
    if (current.some((item) => item.id === comment.id)) return current;
    const updated = shiftReplyCounts(current, comment, 1);
    return isDirectReply(comment, conversationId)
      ? mergeCommentReplies(updated, [comment])
      : updated;
  }
  return shiftReplyCounts(current, comment, -1)
    .filter((item) => item.id !== comment.id)
    .map((item) => clearReplyTarget(item, comment.id));
}
