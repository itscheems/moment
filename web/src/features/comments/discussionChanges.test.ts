import { describe, expect, it } from "vitest";
import type { PublicComment } from "../../api/public";
import {
  clearReplyTarget,
  isDirectReply,
  mergeCommentReplies,
  reconcileConversationComment,
} from "./discussionChanges";
import {
  reconcileCreatedComment,
  reconcileDeletedComment,
} from "./commentOrdering";

function comment(
  id: string,
  parentId: string | null,
  targetId: string | null,
  hour: number,
): PublicComment {
  return {
    id,
    postId: "post",
    parentId,
    depth: parentId ? 1 : 0,
    authorName: "Alice",
    author: {
      userId: null,
      username: null,
      displayName: "Alice",
      avatarMediaId: null,
      profileUrl: null,
    },
    replyTo: targetId
      ? {
          commentId: targetId,
          author: {
            userId: null,
            username: null,
            displayName: "Bob",
            avatarMediaId: null,
            profileUrl: null,
          },
        }
      : null,
    contentMd: id,
    contentHtml: "<p>" + id + "</p>",
    createdAt: "2026-08-10T0" + hour + ":00:00Z",
    media: [],
    location: null,
    replyCount: parentId ? 0 : 2,
    repliesNextCursor: null,
  };
}
describe("discussion updates", () => {
  it("updates a visible child's count without appending its indirect answer", () => {
    const target = {
      ...comment("target", "root", "selected", 1),
      replyCount: 2,
    };
    const sibling = comment("sibling", "root", "selected", 2);
    const answer = comment("answer", "root", "target", 3);
    const after = reconcileConversationComment(
      [target, sibling],
      { kind: "created", comment: answer },
      "selected",
    );
    expect(after.map((item) => item.id)).toEqual(["target", "sibling"]);
    expect(after[0].replyCount).toBe(3);
    expect(after[1].replyCount).toBe(0);
    const removed = reconcileConversationComment(
      after,
      { kind: "deleted", comment: answer },
      "selected",
    );
    expect(removed[0].replyCount).toBe(2);
  });

  it("updates floor and child counters once when adding and removing an answer", () => {
    const root = comment("root", null, null, 0);
    const target = { ...comment("target", "root", "root", 1), replyCount: 3 };
    const answer = comment("answer", "root", "target", 2);
    const created = reconcileCreatedComment([root, target], answer);
    expect(created[0].replyCount).toBe(3);
    expect(created[1].replyCount).toBe(4);
    expect(reconcileCreatedComment(created, answer)[1].replyCount).toBe(4);
    const deleted = reconcileDeletedComment(created, answer);
    expect(deleted[0].replyCount).toBe(2);
    expect(deleted[1].replyCount).toBe(3);
  });
  it("distinguishes direct answers from other replies in the same floor", () => {
    const child = comment("child", "root", "target", 2);
    expect(isDirectReply(child, "target")).toBe(true);
    expect(isDirectReply(child, "root")).toBe(false);
    expect(isDirectReply(comment("old", "root", null, 1), "root")).toBe(true);
    expect(isDirectReply(comment("root", null, null, 0), "root")).toBe(false);
  });
  it("merges fetched pages ahead of a newer local answer without duplicates", () => {
    const old = comment("old", "root", "target", 1),
      newer = comment("new", "root", "target", 3),
      middle = comment("middle", "root", "target", 2);
    expect(
      mergeCommentReplies([old, newer], [middle, old]).map((c) => c.id),
    ).toEqual(["old", "middle", "new"]);
  });
  it("clears the deleted target while retaining its answers in the floor", () => {
    const root = comment("root", null, null, 0),
      target = comment("target", "root", null, 1),
      answer = comment("answer", "root", "target", 2);
    const after = reconcileDeletedComment([root, target, answer], target);
    expect(after.map((c) => c.id)).toEqual(["root", "answer"]);
    expect(after[1].replyTo).toBeNull();
    expect(after[0].replyCount).toBe(1);
    expect(isDirectReply(clearReplyTarget(answer, target.id), "root")).toBe(
      true,
    );
  });
});
