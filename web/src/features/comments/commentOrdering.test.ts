import { describe, expect, it } from "vitest";

import type { PublicComment } from "../../api/public";
import {
  appendRepliesToRoot,
  appendRoots,
  reconcileCreatedComment,
  reconcileDeletedComment,
} from "./commentOrdering";

const postId = "01K2ABCDEF0123456789ABCDE0";

function at(hour: number): string {
  return `2026-08-10T0${hour}:00:00.000Z`;
}

function comment(
  id: string,
  createdAt: string,
  parentId: string | null = null,
  replyCount = 0,
): PublicComment {
  return {
    id,
    postId,
    parentId,
    depth: parentId ? 1 : 0,
    authorKind: "owner",
    authorName: "Owner",
    author: {
      userId: "0123456789abcdef0123456789abcdef",
      username: "owner",
      displayName: "Moment",
      avatarMediaId: null,
      profileUrl: "/@owner",
    },
    replyTo: null,
    contentMd: id,
    contentHtml: `<p>${id}</p>`,
    createdAt,
    media: [],
    location: null,
    replyCount,
    repliesNextCursor: null,
  };
}

const ids = (items: PublicComment[]) => items.map((item) => item.id);

describe("comment ordering under pagination", () => {
  // A continuation page is older than anything the reader just wrote, so
  // dropping it at the tail would render their newest comment above comments
  // older than it — and every other helper here locates rows by walking the
  // creation order this would break.
  it("merges a continuation page of roots by creation time, not at the tail", () => {
    const loaded = [comment("r1", at(1)), comment("r2", at(2))];
    const mine = comment("r9", at(9));
    const withMine = reconcileCreatedComment(loaded, mine);
    expect(ids(withMine)).toEqual(["r1", "r2", "r9"]);

    const nextPage = [comment("r3", at(3)), comment("r4", at(4))];
    expect(ids(appendRoots(withMine, nextPage))).toEqual([
      "r1",
      "r2",
      "r3",
      "r4",
      "r9",
    ]);
  });

  // A root and the replies travelling with it are one unit: splitting the run
  // would separate a reply from its root, and a reply that arrived with its
  // root is already counted in that root's total.
  it("keeps each root's replies with it while merging and leaves totals alone", () => {
    const loaded = [comment("r5", at(5), null, 1), comment("r5a", at(5), "r5")];
    const incoming = [
      comment("r2", at(2), null, 1),
      comment("r2a", at(2), "r2"),
    ];
    const merged = appendRoots(loaded, incoming);

    expect(ids(merged)).toEqual(["r2", "r2a", "r5", "r5a"]);
    expect(merged[0].replyCount).toBe(1);
    expect(merged[2].replyCount).toBe(1);
  });

  // A fetched reply page resumes from the cursor, so it is older than a reply
  // the reader posted moments ago. Appending it would put an hour-old reply
  // below one written seconds ago, and would then misplace the next insert.
  it("merges a fetched reply page ahead of a newer optimistic reply", () => {
    const loaded = [
      comment("root", at(1), null, 4),
      comment("a1", at(2), "root"),
      comment("a2", at(3), "root"),
    ];
    const withMine = reconcileCreatedComment(
      loaded,
      comment("mine", at(9), "root"),
    );
    expect(ids(withMine)).toEqual(["root", "a1", "a2", "mine"]);

    const fetched = [comment("a3", at(4), "root")];
    expect(ids(appendRepliesToRoot(withMine, "root", fetched))).toEqual([
      "root",
      "a1",
      "a2",
      "a3",
      "mine",
    ]);
  });

  // replyCount drives both the "展开其余 N 条回复" control and the delete
  // confirmation. Leaving the server snapshot untouched makes the control
  // vanish while replies are still unfetched, and makes the dialog understate
  // an irreversible cascade.
  it("moves a root's reply total with optimistic inserts and removals", () => {
    const loaded = [
      comment("root", at(1), null, 4),
      comment("a1", at(2), "root"),
    ];

    const added = reconcileCreatedComment(
      loaded,
      comment("mine", at(9), "root"),
    );
    expect(added[0].replyCount).toBe(5);

    const removed = reconcileDeletedComment(
      added,
      comment("a1", at(2), "root"),
    );
    expect(removed[0].replyCount).toBe(4);
    expect(ids(removed)).toEqual(["root", "mine"]);

    // Removing a reply that is not on the page must not move the total.
    const absent = reconcileDeletedComment(
      removed,
      comment("ghost", at(3), "root"),
    );
    expect(absent[0].replyCount).toBe(4);
  });
});
