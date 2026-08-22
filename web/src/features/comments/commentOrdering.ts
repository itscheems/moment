import { type PublicComment } from "../../api/public";

function orderComments(left: PublicComment, right: PublicComment): number {
  const time = Date.parse(left.createdAt) - Date.parse(right.createdAt);
  return time || left.id.localeCompare(right.id);
}

export function reconcileCreatedComment(
  current: PublicComment[],
  created: PublicComment,
): PublicComment[] {
  if (current.some((comment) => comment.id === created.id)) return current;
  if (created.parentId === null) {
    const index = current.findIndex(
      (comment) =>
        comment.parentId === null && orderComments(created, comment) < 0,
    );
    return index < 0
      ? [...current, created]
      : [...current.slice(0, index), created, ...current.slice(index)];
  }
  const rootIndex = current.findIndex(
    (comment) => comment.id === created.parentId && comment.parentId === null,
  );
  if (rootIndex < 0) {
    throw new Error("created reply has no visible top-level parent");
  }
  let index = rootIndex + 1;
  while (
    index < current.length &&
    current[index].parentId === created.parentId &&
    orderComments(current[index], created) < 0
  ) {
    index += 1;
  }
  return [...current.slice(0, index), created, ...current.slice(index)];
}

export function reconcileDeletedComment(
  current: PublicComment[],
  deleted: PublicComment,
): PublicComment[] {
  if (deleted.parentId !== null) {
    return current.filter((comment) => comment.id !== deleted.id);
  }
  const rootIndex = current.findIndex((comment) => comment.id === deleted.id);
  if (rootIndex < 0) return current;
  let end = rootIndex + 1;
  while (end < current.length && current[end].parentId === deleted.id) end += 1;
  return [...current.slice(0, rootIndex), ...current.slice(end)];
}

export function orderPinnedConversation(
  current: PublicComment[],
  pinnedCommentId: string | null,
): PublicComment[] {
  if (!pinnedCommentId) return current;
  const start = current.findIndex(
    (comment) => comment.id === pinnedCommentId && comment.parentId === null,
  );
  if (start <= 0) return current;
  let end = start + 1;
  while (end < current.length && current[end].parentId === pinnedCommentId) {
    end += 1;
  }
  return [
    ...current.slice(start, end),
    ...current.slice(0, start),
    ...current.slice(end),
  ];
}

/**
 * Splice a freshly loaded page of replies in behind the ones already shown.
 *
 * Every other helper here relies on a root being immediately followed by its
 * own replies, and pagination does not change that — it only means fewer of
 * them are present. Appending to the end of a root's existing run keeps the
 * invariant and keeps the replies themselves in creation order.
 *
 * Replies already on screen are skipped rather than replaced. A reply the
 * reader just wrote can arrive optimistically and then again in a later page;
 * dropping the duplicate is what stops it appearing twice.
 */
export function appendRepliesToRoot(
  current: PublicComment[],
  rootId: string,
  replies: PublicComment[],
): PublicComment[] {
  const rootIndex = current.findIndex(
    (comment) => comment.id === rootId && comment.parentId === null,
  );
  if (rootIndex < 0) return current;
  let end = rootIndex + 1;
  while (end < current.length && current[end].parentId === rootId) end += 1;
  const present = new Set(current.map((comment) => comment.id));
  const added = replies.filter(
    (reply) => reply.parentId === rootId && !present.has(reply.id),
  );
  if (added.length === 0) return current;
  return [...current.slice(0, end), ...added, ...current.slice(end)];
}

/**
 * Append a page of roots, skipping any already present.
 *
 * A root can already be on screen for two reasons: it is the pinned
 * conversation, which travels beside the stream and therefore appears in both,
 * or the reader wrote it moments ago and it was inserted optimistically.
 */
export function appendRoots(
  current: PublicComment[],
  incoming: PublicComment[],
): PublicComment[] {
  const present = new Set(current.map((comment) => comment.id));
  const added = incoming.filter((comment) => !present.has(comment.id));
  return added.length === 0 ? current : [...current, ...added];
}

/**
 * Fold the pinned conversation into the stream at the point in time it belongs.
 *
 * The pinned root is delivered separately so that pinning never reorders the
 * page a cursor addresses, but the array underneath still has to hold roots in
 * creation order: every other helper here — inserting a new comment, removing
 * a deleted one, hoisting the pinned run for display — locates things by
 * walking that order. Dropping the pinned run at the front instead would put a
 * root out of sequence and send a newly written comment to the wrong place.
 *
 * When the pinned root is already in the page, the stream is left alone.
 */
export function mergePinnedConversation(
  items: PublicComment[],
  pinned: PublicComment[],
): PublicComment[] {
  const root = pinned[0];
  if (!root || items.some((comment) => comment.id === root.id)) return items;
  const index = items.findIndex(
    (comment) => comment.parentId === null && orderComments(root, comment) < 0,
  );
  return index < 0
    ? [...items, ...pinned]
    : [...items.slice(0, index), ...pinned, ...items.slice(index)];
}

export function countReplies(items: PublicComment[]): Map<string, number> {
  const counts = new Map<string, number>();
  for (const comment of items) {
    if (comment.parentId) {
      counts.set(comment.parentId, (counts.get(comment.parentId) ?? 0) + 1);
    }
  }
  return counts;
}
