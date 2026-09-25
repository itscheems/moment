import { describe, expect, it } from "vitest";

import type { PublicPost } from "../../api/public";
import { reconcileTimelineEvent, timelineItemKey } from "./timelineItems";

function original(): PublicPost {
  return {
    id: "01K2ABCDEF0123456789ABCDE0",
    kind: "moment",
    contentMd: "原帖",
    contentHtml: "<p>原帖</p>",
    createdAt: "2026-08-10T01:00:00.000Z",
    updatedAt: "2026-08-10T01:00:00.000Z",
    publishedAt: "2026-08-10T01:00:00.000Z",
    repostedAt: null,
    visibility: "everyone",
    timelineItemKind: "original",
    commentCount: 0,
    media: [],
    linkCards: [],
    location: null,
    quotedPost: null,
  };
}

describe("timeline repost projections", () => {
  it("keeps the original, adds one linked repost item, and removes only that item on undo", () => {
    const post = original();
    const reposted = {
      ...post,
      updatedAt: "2026-08-10T03:00:00.000Z",
      repostedAt: "2026-08-10T03:00:00.000Z",
    };
    const linked = reconcileTimelineEvent([post], {
      kind: "reordered",
      post: reposted,
    });

    expect(linked.map(timelineItemKey)).toEqual([
      `${post.id}:repost`,
      `${post.id}:original`,
    ]);
    expect(linked.every((item) => item.repostedAt !== null)).toBe(true);

    const undone = reconcileTimelineEvent(linked, {
      kind: "reordered",
      post: { ...reposted, repostedAt: null },
    });
    expect(undone).toEqual([
      expect.objectContaining({
        id: post.id,
        repostedAt: null,
        visibility: "everyone",
        timelineItemKind: "original",
      }),
    ]);
  });
});
