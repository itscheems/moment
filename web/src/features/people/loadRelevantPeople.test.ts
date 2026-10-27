import { describe, expect, it, vi } from "vitest";
import {
  PublicApiError,
  type CommentContext,
  type PublicAccount,
  type PublicApi,
  type PublicComment,
  type PublicCommentAuthor,
  type PublicPost,
} from "../../api/public";
import { loadRelevantPeople } from "./loadRelevantPeople";

function author(username: string, userId = username): PublicCommentAuthor {
  return {
    userId,
    username,
    displayName: "Same display name",
    avatarMediaId: null,
    profileUrl: `/${username}`,
  };
}

function profile(identity: PublicCommentAuthor): PublicAccount {
  if (!identity.userId || !identity.username || !identity.profileUrl)
    throw new Error("A fixture profile requires an account");
  return {
    userId: identity.userId,
    username: identity.username,
    displayName: identity.displayName,
    profileUrl: identity.profileUrl,
    avatarMediaId: null,
    coverMediaId: null,
    bio: `${identity.username}'s biography`,
  };
}

const alice = author("alice");
const bob = author("bobby");
const carol = author("carol");
const dana = author("dana");
const profiles = [alice, bob, carol, dana].map(profile);
const post: PublicPost = {
  id: "post",
  author: alice,
  kind: "moment",
  contentMd: "A post",
  contentHtml: "<p>A post</p>",
  article: null,
  createdAt: "2026-10-27T01:00:00Z",
  updatedAt: "2026-10-27T01:00:00Z",
  publishedAt: "2026-10-27T01:00:00Z",
  repostedAt: null,
  visibility: "everyone",
  timelineItemKind: "original",
  commentCount: 100,
  media: [],
  linkCards: [],
};

function comment(
  id: string,
  identity: PublicCommentAuthor,
  target?: PublicComment,
): PublicComment {
  return {
    id,
    postId: post.id,
    parentId: id === "root" ? null : "root",
    depth: id === "root" ? 0 : 1,
    authorName: identity.displayName,
    author: identity,
    replyTo: target ? { commentId: target.id, author: target.author } : null,
    contentMd: "A comment",
    contentHtml: "<p>A comment</p>",
    createdAt: post.createdAt,
    media: [],
    location: null,
    replyCount: 20,
    repliesNextCursor: "unloaded-replies",
  };
}

function context(
  selected: PublicComment,
  target: PublicComment | null = null,
): CommentContext {
  return {
    comment: selected,
    replyTarget: target,
    rootCommentId: "root",
    pinnedCommentId: null,
    directReplyCount: 20,
  };
}

function api() {
  return {
    user: vi.fn<PublicApi["user"]>(async (username) => {
      const found = profiles.find((value) => value.username === username);
      if (!found) throw new Error(`Unexpected profile lookup: ${username}`);
      return found;
    }),
    commentContext: vi.fn<PublicApi["commentContext"]>(),
  };
}

function quote(identity: PublicCommentAuthor) {
  return {
    unavailable: false as const,
    id: "quote",
    kind: "moment" as const,
    author: identity,
    article: null,
    contentMd: "Quoted content",
    contentHtml: "<p>Quoted content</p>",
    publishedAt: post.publishedAt,
    media: [],
    location: null,
  };
}

describe("loadRelevantPeople", () => {
  it("includes the current author and direct quote author, deduplicating by account", async () => {
    const service = api();
    const people = await loadRelevantPeople(
      service,
      { ...post, quotedPost: quote(bob) },
      null,
    );
    expect(people.map((person) => person.username)).toEqual(["alice", "bobby"]);
    expect(people[1].bio).toBe("bobby's biography");
    expect(service.commentContext).not.toHaveBeenCalled();
    service.user.mockClear();
    await loadRelevantPeople(
      service,
      { ...post, quotedPost: quote(alice) },
      null,
    );
    expect(service.user).toHaveBeenCalledTimes(1);
  });

  it("walks the selected reply chain through earlier replies without collecting descendants", async () => {
    const root = comment("root", bob);
    const middle = comment("middle", carol, root);
    const selected = comment("selected", dana, middle);
    const service = api();
    const signal = new AbortController().signal;
    service.commentContext.mockResolvedValueOnce(context(middle, root));

    const people = await loadRelevantPeople(
      service,
      { ...post, quotedPost: quote(dana) },
      context(selected, middle),
      signal,
    );

    expect(people.map((person) => person.username)).toEqual([
      "dana",
      "carol",
      "bobby",
      "alice",
    ]);
    expect(service.commentContext).toHaveBeenCalledExactlyOnceWith(
      post.id,
      middle.id,
      signal,
    );
    expect(service.user.mock.calls.every((call) => call[1] === signal)).toBe(
      true,
    );
  });

  it("does not carry the original post's quote into a comment detail", async () => {
    const service = api();
    const people = await loadRelevantPeople(
      service,
      { ...post, quotedPost: quote(dana) },
      context(comment("root", bob)),
    );
    expect(people.map((person) => person.username)).toEqual(["bobby", "alice"]);
    expect(service.commentContext).not.toHaveBeenCalled();
  });

  it("includes the root author for an unaddressed historical reply", async () => {
    const root = comment("root", bob);
    const selected = comment("selected", carol);
    const service = api();
    service.commentContext.mockResolvedValueOnce(context(root));

    const people = await loadRelevantPeople(service, post, context(selected));

    expect(people.map((person) => person.username)).toEqual([
      "carol",
      "bobby",
      "alice",
    ]);
    expect(service.commentContext).toHaveBeenCalledExactlyOnceWith(
      post.id,
      root.id,
      undefined,
    );
  });

  it("completes an intermediate unaddressed reply with its root and deduplicates authors", async () => {
    const root = comment("root", carol);
    const middle = comment("middle", bob);
    const selected = comment("selected", carol, middle);
    const service = api();
    service.commentContext.mockResolvedValueOnce(context(root));

    const people = await loadRelevantPeople(
      service,
      { ...post, author: bob },
      context(selected, middle),
    );

    expect(people.map((person) => person.username)).toEqual(["carol", "bobby"]);
    expect(service.user).toHaveBeenCalledTimes(2);
    expect(service.commentContext).toHaveBeenCalledExactlyOnceWith(
      post.id,
      root.id,
      undefined,
    );
  });

  it("does not substitute a root for a missing explicit reply target", async () => {
    const root = comment("root", bob);
    const selected = comment("selected", carol, root);
    const service = api();

    const people = await loadRelevantPeople(service, post, context(selected));

    expect(people.map((person) => person.username)).toEqual(["carol", "alice"]);
    expect(service.commentContext).not.toHaveBeenCalled();
  });

  it("stops when an intermediate explicit target disappears instead of adding the root", async () => {
    const root = comment("root", bob);
    const middle = comment("middle", carol, root);
    const selected = comment("selected", dana, middle);
    const service = api();
    service.commentContext.mockResolvedValueOnce(context(middle));

    const people = await loadRelevantPeople(
      service,
      post,
      context(selected, middle),
    );

    expect(people.map((person) => person.username)).toEqual([
      "dana",
      "carol",
      "alice",
    ]);
    expect(service.commentContext).toHaveBeenCalledExactlyOnceWith(
      post.id,
      middle.id,
      undefined,
    );
  });

  it("terminates malformed cycles and lists each account only once", async () => {
    const root = comment("root", bob);
    const selected = comment("selected", bob, root);
    root.replyTo = { commentId: selected.id, author: selected.author };
    const service = api();
    service.commentContext.mockResolvedValue(context(root, selected));
    const people = await loadRelevantPeople(
      service,
      post,
      context(selected, root),
    );
    expect(people.map((person) => person.username)).toEqual(["bobby", "alice"]);
    expect(service.commentContext).toHaveBeenCalledTimes(1);
  });

  it("skips legacy guests, unavailable quotes and handles now owned by another account", async () => {
    const guest = { ...bob, userId: null, username: null, profileUrl: null };
    const service = api();
    service.user.mockResolvedValue(profile(author("alice", "replacement")));
    expect(
      await loadRelevantPeople(
        service,
        { ...post, quotedPost: { unavailable: true } },
        context(comment("root", guest)),
      ),
    ).toEqual([]);
    expect(service.user).toHaveBeenCalledExactlyOnceWith("alice", undefined);
  });

  it("omits missing public profiles but propagates retryable lookup failures", async () => {
    const service = api();
    service.user.mockRejectedValueOnce(
      new PublicApiError("Missing", { status: 404, code: "ACCOUNT_NOT_FOUND" }),
    );
    expect(await loadRelevantPeople(service, post, null)).toEqual([]);
    const error = new PublicApiError("Offline", {
      status: 503,
      code: "UNAVAILABLE",
    });
    service.user.mockRejectedValueOnce(error);
    await expect(loadRelevantPeople(service, post, null)).rejects.toBe(error);
  });

  it("keeps the readable chain when an ancestor disappears", async () => {
    const root = comment("root", bob);
    const middle = comment("middle", carol, root);
    const selected = comment("selected", dana, middle);
    const service = api();
    service.commentContext.mockRejectedValueOnce(
      new PublicApiError("Missing", { status: 404, code: "COMMENT_NOT_FOUND" }),
    );
    expect(
      (await loadRelevantPeople(service, post, context(selected, middle))).map(
        (person) => person.username,
      ),
    ).toEqual(["dana", "carol", "alice"]);
  });

  it("propagates ancestor failures and stops aborted loads before requesting data", async () => {
    const root = comment("root", bob);
    const middle = comment("middle", carol, root);
    const selected = comment("selected", dana, middle);
    const service = api();
    const error = new Error("Connection lost");
    service.commentContext.mockRejectedValueOnce(error);
    await expect(
      loadRelevantPeople(service, post, context(selected, middle)),
    ).rejects.toBe(error);
    const controller = new AbortController();
    controller.abort();
    await expect(
      loadRelevantPeople(service, post, null, controller.signal),
    ).rejects.toMatchObject({ name: "AbortError" });
    expect(service.user).not.toHaveBeenCalled();
  });
});
