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

export function countReplies(items: PublicComment[]): Map<string, number> {
  const counts = new Map<string, number>();
  for (const comment of items) {
    if (comment.parentId) {
      counts.set(comment.parentId, (counts.get(comment.parentId) ?? 0) + 1);
    }
  }
  return counts;
}
