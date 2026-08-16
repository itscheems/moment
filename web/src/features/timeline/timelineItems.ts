import type { PublicPost } from "../../api/public";
import type { OwnerPostEvent } from "../owner/postEvents";

export function timelineItemKey(post: PublicPost): string {
  return `${post.id}:${post.timelineItemKind}`;
}

export function compareTimelineItems(
  left: PublicPost,
  right: PublicPost,
): number {
  const leftTime = Date.parse(
    left.timelineItemKind === "repost" && left.repostedAt
      ? left.repostedAt
      : left.publishedAt,
  );
  const rightTime = Date.parse(
    right.timelineItemKind === "repost" && right.repostedAt
      ? right.repostedAt
      : right.publishedAt,
  );
  return (
    rightTime - leftTime ||
    Number(right.timelineItemKind === "repost") -
      Number(left.timelineItemKind === "repost") ||
    right.id.localeCompare(left.id)
  );
}

function asTimelineItem(
  post: PublicPost,
  timelineItemKind: PublicPost["timelineItemKind"],
): PublicPost {
  return { ...post, timelineItemKind };
}

// Repost events replace presentation surfaces, not the authoritative post.
// The original stays at its publication position when it is already loaded;
// the repost projection is inserted or removed at the activity position.
export function reconcileTimelineEvent(
  current: PublicPost[],
  event: OwnerPostEvent,
): PublicPost[] {
  const matching = current.filter((post) => post.id === event.post.id);
  const others = current.filter((post) => post.id !== event.post.id);

  if (event.kind === "deleted") return others;
  if (event.kind === "created") {
    return [asTimelineItem(event.post, "original"), ...others].sort(
      compareTimelineItems,
    );
  }
  if (event.kind === "updated") {
    return [
      ...others,
      ...matching.map((post) =>
        asTimelineItem(event.post, post.timelineItemKind),
      ),
    ].sort(compareTimelineItems);
  }

  const hadOriginal = matching.some(
    (post) => post.timelineItemKind === "original",
  );
  const next = [...others];
  if (event.post.repostedAt !== null) {
    next.push(asTimelineItem(event.post, "repost"));
  }
  if (hadOriginal) {
    next.push(asTimelineItem(event.post, "original"));
  }
  return next.sort(compareTimelineItems);
}
